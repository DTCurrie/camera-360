package camera360

import (
	"image"
	"image/color"
	"math"
	"sync"
)

// PinholeView is the steerable virtual pinhole view derived from an equi-
// rectangular source. (Yaw, Pitch) are in degrees: yaw rotates left/right
// around the up axis, pitch rotates up/down. FOVDeg is the horizontal field
// of view of the synthesized pinhole.
type PinholeView struct {
	Yaw    float64
	Pitch  float64
	FOVDeg float64
	Width  int
	Height int
}

// pinholeSample is one bilinear sample into an ERP source: four neighbour
// pixels and their fractional weights. Pre-computed once per (view, ERP-size)
// pair so per-frame projection is just a tight loop of integer fetches.
type pinholeSample struct {
	x0, y0 int32
	dx, dy float32 // fractional offsets into the (x0, y0) cell
}

// PinholeLUT is a precomputed sampling table from a fixed (view, ERP-size)
// configuration. Apply it to any matching-sized ERP frame to get the flat
// rectilinear view.
type PinholeLUT struct {
	view  PinholeView
	erpW  int
	erpH  int
	table []pinholeSample // length == view.Width * view.Height
}

// BuildPinholeLUT precomputes the ERP→pinhole sampling table. The math: for
// each output pixel (u, v) build a ray in camera coordinates, rotate by yaw
// and pitch, convert to spherical (lon, lat), then look up the corresponding
// ERP pixel coordinate.
func BuildPinholeLUT(view PinholeView, erpW, erpH int) *PinholeLUT {
	w, h := view.Width, view.Height
	if w <= 0 || h <= 0 || erpW <= 0 || erpH <= 0 {
		return &PinholeLUT{view: view, erpW: erpW, erpH: erpH}
	}
	fovRad := view.FOVDeg * math.Pi / 180
	// focal length in pixels for the horizontal field of view
	f := float64(w) / 2 / math.Tan(fovRad/2)
	cx := float64(w) / 2
	cy := float64(h) / 2

	yaw := view.Yaw * math.Pi / 180
	pitch := view.Pitch * math.Pi / 180
	sinY, cosY := math.Sin(yaw), math.Cos(yaw)
	sinP, cosP := math.Sin(pitch), math.Cos(pitch)

	table := make([]pinholeSample, w*h)
	for v := 0; v < h; v++ {
		for u := 0; u < w; u++ {
			// Camera-frame ray: forward is +Z, right is +X, down is +Y
			x := (float64(u) - cx)
			y := (float64(v) - cy)
			z := f
			// rotate around X axis by pitch (look up/down)
			y2 := y*cosP - z*sinP
			z2 := y*sinP + z*cosP
			// rotate around Y axis by yaw (look left/right)
			x3 := x*cosY + z2*sinY
			y3 := y2
			z3 := -x*sinY + z2*cosY
			// to unit sphere
			r := math.Sqrt(x3*x3 + y3*y3 + z3*z3)
			x3 /= r
			y3 /= r
			z3 /= r
			// spherical: lon in [-pi, pi], lat in [-pi/2, pi/2]
			lon := math.Atan2(x3, z3)
			lat := math.Asin(y3)
			// ERP pixel coords
			erpX := (lon/math.Pi + 1) * 0.5 * float64(erpW) // [0, erpW)
			erpY := (lat/(math.Pi/2) + 1) * 0.5 * float64(erpH)
			// wrap lon, clamp lat
			erpX = math.Mod(erpX, float64(erpW))
			if erpX < 0 {
				erpX += float64(erpW)
			}
			if erpY < 0 {
				erpY = 0
			} else if erpY > float64(erpH-1) {
				erpY = float64(erpH - 1)
			}
			x0 := int32(erpX)
			y0 := int32(erpY)
			table[v*w+u] = pinholeSample{
				x0: x0,
				y0: y0,
				dx: float32(erpX - float64(x0)),
				dy: float32(erpY - float64(y0)),
			}
		}
	}
	return &PinholeLUT{view: view, erpW: erpW, erpH: erpH, table: table}
}

// Apply projects an ERP image to the configured pinhole view via bilinear
// sampling. Allocates the destination RGBA on each call.
func (l *PinholeLUT) Apply(erp image.Image) *image.RGBA {
	w, h := l.view.Width, l.view.Height
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if l.table == nil {
		return dst
	}
	erpW, erpH := l.erpW, l.erpH

	for i, s := range l.table {
		x0 := int(s.x0)
		y0 := int(s.y0)
		x1 := x0 + 1
		if x1 >= erpW {
			x1 = 0 // wrap longitude
		}
		y1 := y0 + 1
		if y1 >= erpH {
			y1 = erpH - 1
		}
		c00 := erp.At(x0, y0)
		c10 := erp.At(x1, y0)
		c01 := erp.At(x0, y1)
		c11 := erp.At(x1, y1)
		r := bilinear4(c00, c10, c01, c11, s.dx, s.dy)
		dst.Pix[i*4+0] = r.R
		dst.Pix[i*4+1] = r.G
		dst.Pix[i*4+2] = r.B
		dst.Pix[i*4+3] = 255
	}
	return dst
}

func bilinear4(c00, c10, c01, c11 color.Color, dx, dy float32) color.RGBA {
	r00, g00, b00, _ := c00.RGBA()
	r10, g10, b10, _ := c10.RGBA()
	r01, g01, b01, _ := c01.RGBA()
	r11, g11, b11, _ := c11.RGBA()
	w00 := (1 - dx) * (1 - dy)
	w10 := dx * (1 - dy)
	w01 := (1 - dx) * dy
	w11 := dx * dy
	// RGBA() returns 16-bit; shift to 8-bit
	r := w00*float32(r00>>8) + w10*float32(r10>>8) + w01*float32(r01>>8) + w11*float32(r11>>8)
	g := w00*float32(g00>>8) + w10*float32(g10>>8) + w01*float32(g01>>8) + w11*float32(g11>>8)
	b := w00*float32(b00>>8) + w10*float32(b10>>8) + w01*float32(b01>>8) + w11*float32(b11>>8)
	return color.RGBA{R: clampU8(r), G: clampU8(g), B: clampU8(b), A: 255}
}

func clampU8(v float32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// PinholeProjector wraps a LUT and lets callers reconfigure the view at
// runtime without rebuilding from scratch unless the view actually changed.
type PinholeProjector struct {
	mu   sync.Mutex
	lut  *PinholeLUT
	view PinholeView
	erpW int
	erpH int
}

func NewPinholeProjector(view PinholeView, erpW, erpH int) *PinholeProjector {
	return &PinholeProjector{
		lut:  BuildPinholeLUT(view, erpW, erpH),
		view: view,
		erpW: erpW,
		erpH: erpH,
	}
}

// SetView updates yaw/pitch/fov. Width/height are fixed at construction.
// Rebuilds the LUT only if something changed.
func (p *PinholeProjector) SetView(yaw, pitch, fov float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if yaw == p.view.Yaw && pitch == p.view.Pitch && fov == p.view.FOVDeg {
		return
	}
	p.view.Yaw = yaw
	p.view.Pitch = pitch
	p.view.FOVDeg = fov
	p.lut = BuildPinholeLUT(p.view, p.erpW, p.erpH)
}

// View returns the current yaw/pitch/fov.
func (p *PinholeProjector) View() (yaw, pitch, fov float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.view.Yaw, p.view.Pitch, p.view.FOVDeg
}

// Project applies the current LUT to an ERP frame, resizing internal state
// if the frame size doesn't match what was used to build the LUT.
func (p *PinholeProjector) Project(erp image.Image) *image.RGBA {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := erp.Bounds()
	if b.Dx() != p.erpW || b.Dy() != p.erpH {
		p.erpW = b.Dx()
		p.erpH = b.Dy()
		p.lut = BuildPinholeLUT(p.view, p.erpW, p.erpH)
	}
	return p.lut.Apply(erp)
}
