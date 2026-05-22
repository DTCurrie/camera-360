package camera360

import (
	"image"
	"image/color"
	"math"
	"sync"
)

// FisheyeLens calibrates one hemispherical lens within the camera's
// dual-fisheye frame. All pixel-space coordinates are in the GLOBAL frame
// (the full 1920×960 image), not the half-rectangle.
//
// FOVDeg is the total angular extent of the image circle (>180° for cameras
// with seam overlap — the AKASO 360's lenses are typically ~200°).
// RotationDeg rotates around the optical axis, applied to (px, py) sampling
// coordinates; useful when a lens is mounted at a non-standard angle.
type FisheyeLens struct {
	CenterX     float64 `json:"center_x"`
	CenterY     float64 `json:"center_y"`
	Radius      float64 `json:"radius"`
	FOVDeg      float64 `json:"fov_deg"`
	RotationDeg float64 `json:"rotation_deg,omitempty"`
}

// erpStitchSample is one ERP pixel's bilinear-sampled candidate from each of
// the two lenses, plus the blend weight between them. Pre-computed once per
// (lens calibration, ERP size) pair.
type erpStitchSample struct {
	frontX, frontY   int32
	frontDX, frontDY float32

	backX, backY   int32
	backDX, backDY float32

	// 1.0 = pure front, 0.0 = pure back, in-between = blended.
	frontWeight float32
}

// StitcherOpts controls optional behaviour of the stitcher. Zero values give
// reasonable defaults (full overlap-based smoothstep blend, lenses assumed
// to be perfectly back-to-back).
type StitcherOpts struct {
	// SeamFeatherDeg controls the half-width of the seam-blend region, in
	// degrees of arc on the unit sphere. Smaller = sharper seam (less
	// ghosting from misaligned lenses but more visible color/brightness
	// transition). Larger = smoother blend (more ghosting from disagreement
	// between lenses but seam becomes invisible).
	//
	// If zero, defaults to the lens's natural overlap (FOV/2 − 90°).
	SeamFeatherDeg float64

	// HardSeam, if true, disables blending entirely and always picks
	// whichever lens has the pixel closer to its optical centre (i.e.
	// smaller θ). Useful as a diagnostic to see exactly where the seam
	// would be without blending.
	HardSeam bool

	// BackExtrinsic{Yaw,Pitch,Roll}Deg corrects for the back lens not being
	// perfectly 180° opposed to the front. Most consumer 360 cameras have
	// a few degrees of manufacturing error that shows up as a wavy
	// horizon, mismatched verticals, or vertical seam offset. These three
	// values are applied as an intrinsic Y-X-Z rotation (yaw, then pitch,
	// then roll) on top of the canonical 180° back-lens flip.
	BackExtrinsicYawDeg   float64
	BackExtrinsicPitchDeg float64
	BackExtrinsicRollDeg  float64

	// LensModel selects which fisheye projection function maps sphere
	// directions to pixel radii. Defaults to LensEquidistant. Try
	// LensEquisolid if the equidistant default produces a doubled image
	// along the seam at large theta — many consumer 360 cameras use
	// equisolid optics.
	LensModel LensModel
}

// LensModel enumerates supported fisheye projection models. The default
// (zero value, LensEquisolid) matches the AKASO 360's lens family; switch to
// LensEquidistant only if you have evidence the camera uses that model.
type LensModel int

const (
	// LensEquisolid: r = 2·f·sin(θ/2). Common on consumer action cameras
	// (GoPro Max, Insta360, many SJCAM/AKASO variants). Empirically the
	// best match for the AKASO 360 — visibly reduces seam ghosting at
	// large θ compared to equidistant.
	LensEquisolid LensModel = iota
	// LensEquidistant: r = f·θ. Common on technical fisheye lenses and
	// rendering tools. Available as a fallback.
	LensEquidistant
)

// StitchParams is the full set of values that determine the stitch LUT.
// Use FisheyeStitcher.Params() to read and Reconfigure() to write.
type StitchParams struct {
	Front FisheyeLens
	Back  FisheyeLens
	Opts  StitcherOpts
}

// FisheyeStitcher precomputes the ERP↔fisheye sampling table for a fixed
// (front lens, back lens, ERP size) triple. Apply it to any dual-fisheye
// frame of the matching size to get a stitched equirectangular panorama.
//
// All public methods are safe for concurrent use. Reconfigure() blocks any
// in-flight StitchToERP / HalfFrame calls for the duration of the LUT
// rebuild (~1 s for a 1920×960 ERP on a 2024 laptop); read methods do not
// block each other.
type FisheyeStitcher struct {
	frameW   int
	frameH   int
	erpW     int
	erpH     int
	leftRect image.Rectangle // back lens lives in the left half by default
	rightOff image.Rectangle // front lens lives in the right half

	mu    sync.RWMutex
	front FisheyeLens
	back  FisheyeLens
	opts  StitcherOpts
	table []erpStitchSample
}

// BuildFisheyeStitcher precomputes the warp table with default blend options.
// See BuildFisheyeStitcherOpts for the full-control variant.
//
// Convention: the front lens (the one the camera body labels +Z) lives in
// the right half of the frame, the back lens in the left half. This matches
// what we observed on the AKASO 360 reference frame; for cameras that swap
// halves, exchange the front/back lens configs.
func BuildFisheyeStitcher(front, back FisheyeLens, frameW, frameH, erpW, erpH int) *FisheyeStitcher {
	return BuildFisheyeStitcherOpts(front, back, frameW, frameH, erpW, erpH, StitcherOpts{})
}

// BuildFisheyeStitcherOpts is the full-control variant.
func BuildFisheyeStitcherOpts(front, back FisheyeLens, frameW, frameH, erpW, erpH int, opts StitcherOpts) *FisheyeStitcher {
	s := &FisheyeStitcher{
		front:    front,
		back:     back,
		frameW:   frameW,
		frameH:   frameH,
		erpW:     erpW,
		erpH:     erpH,
		opts:     opts,
		leftRect: image.Rect(0, 0, frameW/2, frameH),
		rightOff: image.Rect(frameW/2, 0, frameW, frameH),
	}
	s.build()
	return s
}

func (s *FisheyeStitcher) build() {
	s.table = make([]erpStitchSample, s.erpW*s.erpH)

	frontFOV := s.front.FOVDeg * math.Pi / 180
	backFOV := s.back.FOVDeg * math.Pi / 180
	frontRot := s.front.RotationDeg * math.Pi / 180
	backRot := s.back.RotationDeg * math.Pi / 180

	// Overlap (one-sided) in radians: e.g. with a 200° lens, each lens
	// covers 100° from the optical axis, 10° past the equator.
	overlap := math.Max(frontFOV/2-math.Pi/2, 0)
	if bo := math.Max(backFOV/2-math.Pi/2, 0); bo < overlap {
		overlap = bo
	}
	// Feather is the z-space half-width of the blend band. Allow the caller
	// to override via SeamFeatherDeg; otherwise default to the lens's
	// natural overlap.
	featherRad := overlap
	if s.opts.SeamFeatherDeg > 0 {
		featherRad = s.opts.SeamFeatherDeg * math.Pi / 180
	}
	feather := math.Sin(featherRad)
	if feather < 0.005 {
		feather = 0.005 // floor; below this the blend is effectively a hard seam
	}

	for v := 0; v < s.erpH; v++ {
		// lat in [+π/2, -π/2] as v goes 0..erpH-1 (image y increases downward)
		lat := (0.5 - (float64(v)+0.5)/float64(s.erpH)) * math.Pi
		sinLat := math.Sin(lat)
		cosLat := math.Cos(lat)
		for u := 0; u < s.erpW; u++ {
			// lon in [-π, +π] left-to-right
			lon := ((float64(u)+0.5)/float64(s.erpW) - 0.5) * 2 * math.Pi

			// Unit sphere point. Convention: +Z front, +X right, +Y up.
			x := cosLat * math.Sin(lon)
			y := sinLat
			z := cosLat * math.Cos(lon)

			frontSample := projectFisheye(x, y, z, &s.front, frontRot, frontFOV, s.opts.LensModel)
			// Back lens looks at -Z; mirror x to keep handedness.
			bx, by, bz := -x, y, -z
			// Apply extrinsic correction in the back lens's local frame.
			if s.opts.BackExtrinsicYawDeg != 0 || s.opts.BackExtrinsicPitchDeg != 0 || s.opts.BackExtrinsicRollDeg != 0 {
				bx, by, bz = applyYPR(bx, by, bz, s.opts.BackExtrinsicYawDeg, s.opts.BackExtrinsicPitchDeg, s.opts.BackExtrinsicRollDeg)
			}
			backSample := projectFisheye(bx, by, bz, &s.back, backRot, backFOV, s.opts.LensModel)

			var weight float32
			switch {
			case s.opts.HardSeam:
				// Pick whichever lens is closer to its optical centre.
				// θ_front = acos(z); θ_back = acos(-z). Smaller θ means
				// the pixel is more axial → less distortion, more reliable.
				if z >= 0 {
					weight = 1
				} else {
					weight = 0
				}
			default:
				// Smoothstep blend around z=0 over ±feather.
				t := (z + feather) / (2 * feather)
				if t < 0 {
					t = 0
				} else if t > 1 {
					t = 1
				}
				weight = float32(t * t * (3 - 2*t))
			}
			// If a lens's projection landed outside its image circle,
			// force the other lens to take over.
			if !frontSample.valid {
				weight = 0
			} else if !backSample.valid {
				weight = 1
			}

			// Add the half-frame offsets: front lens is in the right half.
			fx := frontSample.px + float64(s.rightOff.Min.X)
			fy := frontSample.py
			bxPix := backSample.px
			byPix := backSample.py

			s.table[v*s.erpW+u] = erpStitchSample{
				frontX:      int32(fx),
				frontY:      int32(fy),
				frontDX:     float32(fx - math.Floor(fx)),
				frontDY:     float32(fy - math.Floor(fy)),
				backX:       int32(bxPix),
				backY:       int32(byPix),
				backDX:      float32(bxPix - math.Floor(bxPix)),
				backDY:      float32(byPix - math.Floor(byPix)),
				frontWeight: weight,
			}
		}
	}
}

type fisheyeProj struct {
	px, py float64
	valid  bool // false if outside the image circle
}

// applyYPR rotates a direction vector by yaw (around Y), then pitch (around
// X), then roll (around Z), all in degrees. Convention matches the typical
// camera-orientation YPR.
func applyYPR(x, y, z, yawDeg, pitchDeg, rollDeg float64) (float64, float64, float64) {
	yaw := yawDeg * math.Pi / 180
	pitch := pitchDeg * math.Pi / 180
	roll := rollDeg * math.Pi / 180
	// Yaw around Y: rotates X and Z.
	if yaw != 0 {
		sy, cy := math.Sin(yaw), math.Cos(yaw)
		x, z = x*cy+z*sy, -x*sy+z*cy
	}
	// Pitch around X: rotates Y and Z.
	if pitch != 0 {
		sp, cp := math.Sin(pitch), math.Cos(pitch)
		y, z = y*cp-z*sp, y*sp+z*cp
	}
	// Roll around Z: rotates X and Y.
	if roll != 0 {
		sr, cr := math.Sin(roll), math.Cos(roll)
		x, y = x*cr-y*sr, x*sr+y*cr
	}
	return x, y, z
}

// projectFisheye maps a unit-sphere direction (x, y, z) with the lens facing
// +Z (and world-up = +Y) to a pixel coordinate within the lens's source
// rectangle. The selected LensModel chooses between equidistant
// (r = R · θ / (FOV/2)) and equisolid (r = R · sin(θ/2) / sin(FOV/4))
// normalized so that r(FOV/2) = R in both models.
//
// Two coordinate-system details are easy to get wrong:
//   - For lenses with FOV > 180°, the lens still sees some pixels with z<0
//     (just past the horizon). We reject only when theta exceeds FOV/2.
//   - Image-Y goes DOWN; world-Y goes UP. The sin(phi) component must be
//     subtracted, not added, so world-up lands at the top of the image.
func projectFisheye(x, y, z float64, lens *FisheyeLens, rotRad, fovRad float64, model LensModel) fisheyeProj {
	theta := math.Acos(z)
	if theta > fovRad/2 {
		return fisheyeProj{}
	}
	phi := math.Atan2(y, x) + rotRad
	var r float64
	switch model {
	case LensEquidistant:
		r = lens.Radius * theta / (fovRad / 2)
	default: // LensEquisolid (zero value, default)
		r = lens.Radius * math.Sin(theta/2) / math.Sin(fovRad/4)
	}
	px := lens.CenterX + r*math.Cos(phi)
	py := lens.CenterY - r*math.Sin(phi)
	return fisheyeProj{px: px, py: py, valid: true}
}

// Params returns the current lens calibration and stitcher options.
func (s *FisheyeStitcher) Params() StitchParams {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StitchParams{Front: s.front, Back: s.back, Opts: s.opts}
}

// Reconfigure swaps in a new (front, back, opts) triple and rebuilds the LUT.
// Blocks concurrent StitchToERP / HalfFrame calls for the duration of the
// rebuild. Intended for low-frequency operations like calibration tuning,
// not per-frame use.
func (s *FisheyeStitcher) Reconfigure(p StitchParams) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.front = p.Front
	s.back = p.Back
	s.opts = p.Opts
	s.build()
}

// StitchToERP applies the precomputed table to a dual-fisheye frame and
// returns a stitched equirectangular RGBA image.
func (s *FisheyeStitcher) StitchToERP(frame image.Image) *image.RGBA {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dst := image.NewRGBA(image.Rect(0, 0, s.erpW, s.erpH))
	if s.table == nil {
		return dst
	}
	// Fast path: jpeg.Decode returns *image.YCbCr for color JPEGs. Going
	// through the image.Image interface (At + RGBA per sample) costs ~7M
	// interface calls per 1920×960 ERP on a 4-corner bilinear stitch — on a
	// Pi that's the difference between sub-second and many-seconds per frame.
	if ycc, ok := frame.(*image.YCbCr); ok {
		s.stitchToERPYCbCr(ycc, dst)
		return dst
	}
	for i, smp := range s.table {
		var r, g, b uint8
		if smp.frontWeight >= 1 {
			r, g, b = bilinearRGB(frame, int(smp.frontX), int(smp.frontY), smp.frontDX, smp.frontDY)
		} else if smp.frontWeight <= 0 {
			r, g, b = bilinearRGB(frame, int(smp.backX), int(smp.backY), smp.backDX, smp.backDY)
		} else {
			fr, fg, fb := bilinearRGB(frame, int(smp.frontX), int(smp.frontY), smp.frontDX, smp.frontDY)
			br, bg, bb := bilinearRGB(frame, int(smp.backX), int(smp.backY), smp.backDX, smp.backDY)
			w := smp.frontWeight
			r = uint8(float32(fr)*w + float32(br)*(1-w))
			g = uint8(float32(fg)*w + float32(bg)*(1-w))
			b = uint8(float32(fb)*w + float32(bb)*(1-w))
		}
		dst.Pix[i*4+0] = r
		dst.Pix[i*4+1] = g
		dst.Pix[i*4+2] = b
		dst.Pix[i*4+3] = 255
	}
	return dst
}

// stitchToERPYCbCr is the YCbCr fast path. It reads Y/Cb/Cr planes directly
// (skipping image.Image dispatch) and bilinear-interpolates in YCbCr space,
// converting to RGB once per output pixel instead of once per sampled corner.
func (s *FisheyeStitcher) stitchToERPYCbCr(src *image.YCbCr, dst *image.RGBA) {
	b := src.Rect
	minX, minY := b.Min.X, b.Min.Y
	maxX, maxY := b.Max.X-1, b.Max.Y-1
	yStride := src.YStride
	cStride := src.CStride
	// COffset for an arbitrary x is computed as
	// (y-Rect.Min.Y)/yRatio*CStride + (x-Rect.Min.X)/xRatio. Encoding the
	// ratio as a shift keeps the inner loop branchless for the common 4:2:0
	// case but still handles every subsampling mode YCbCr supports.
	var xShift, yShift uint
	switch src.SubsampleRatio {
	case image.YCbCrSubsampleRatio444:
		xShift, yShift = 0, 0
	case image.YCbCrSubsampleRatio422:
		xShift, yShift = 1, 0
	case image.YCbCrSubsampleRatio420:
		xShift, yShift = 1, 1
	case image.YCbCrSubsampleRatio440:
		xShift, yShift = 0, 1
	case image.YCbCrSubsampleRatio411:
		xShift, yShift = 2, 0
	case image.YCbCrSubsampleRatio410:
		xShift, yShift = 2, 1
	default:
		xShift, yShift = 0, 0
	}

	sampleYCbCr := func(x, y int, dx, dy float32) (yOut, cbOut, crOut float32) {
		x0 := x
		if x0 < minX {
			x0 = minX
		} else if x0 > maxX {
			x0 = maxX
		}
		x1 := x0 + 1
		if x1 > maxX {
			x1 = maxX
		}
		y0 := y
		if y0 < minY {
			y0 = minY
		} else if y0 > maxY {
			y0 = maxY
		}
		y1 := y0 + 1
		if y1 > maxY {
			y1 = maxY
		}
		yo00 := (y0-minY)*yStride + (x0 - minX)
		yo10 := (y0-minY)*yStride + (x1 - minX)
		yo01 := (y1-minY)*yStride + (x0 - minX)
		yo11 := (y1-minY)*yStride + (x1 - minX)
		co00 := ((y0 - minY) >> yShift) * cStride
		co01 := ((y1 - minY) >> yShift) * cStride
		cx0 := (x0 - minX) >> xShift
		cx1 := (x1 - minX) >> xShift
		w00 := (1 - dx) * (1 - dy)
		w10 := dx * (1 - dy)
		w01 := (1 - dx) * dy
		w11 := dx * dy
		yOut = w00*float32(src.Y[yo00]) + w10*float32(src.Y[yo10]) +
			w01*float32(src.Y[yo01]) + w11*float32(src.Y[yo11])
		cbOut = w00*float32(src.Cb[co00+cx0]) + w10*float32(src.Cb[co00+cx1]) +
			w01*float32(src.Cb[co01+cx0]) + w11*float32(src.Cb[co01+cx1])
		crOut = w00*float32(src.Cr[co00+cx0]) + w10*float32(src.Cr[co00+cx1]) +
			w01*float32(src.Cr[co01+cx0]) + w11*float32(src.Cr[co01+cx1])
		return
	}

	for i, smp := range s.table {
		var yF, cbF, crF float32
		switch {
		case smp.frontWeight >= 1:
			yF, cbF, crF = sampleYCbCr(int(smp.frontX), int(smp.frontY), smp.frontDX, smp.frontDY)
		case smp.frontWeight <= 0:
			yF, cbF, crF = sampleYCbCr(int(smp.backX), int(smp.backY), smp.backDX, smp.backDY)
		default:
			yA, cbA, crA := sampleYCbCr(int(smp.frontX), int(smp.frontY), smp.frontDX, smp.frontDY)
			yB, cbB, crB := sampleYCbCr(int(smp.backX), int(smp.backY), smp.backDX, smp.backDY)
			w := smp.frontWeight
			yF = yA*w + yB*(1-w)
			cbF = cbA*w + cbB*(1-w)
			crF = crA*w + crB*(1-w)
		}
		r, g, bb := color.YCbCrToRGB(uint8(yF), uint8(cbF), uint8(crF))
		dst.Pix[i*4+0] = r
		dst.Pix[i*4+1] = g
		dst.Pix[i*4+2] = bb
		dst.Pix[i*4+3] = 255
	}
}

// HalfFrame extracts one half of the dual-fisheye frame as a new RGBA image.
// half=="front" returns the right half, "back" the left half.
func (s *FisheyeStitcher) HalfFrame(frame image.Image, half string) *image.RGBA {
	// No lock needed — rightOff/leftRect are immutable after construction.
	var r image.Rectangle
	switch half {
	case "front":
		r = s.rightOff
	case "back":
		r = s.leftRect
	default:
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	if ycc, ok := frame.(*image.YCbCr); ok {
		halfFrameYCbCr(ycc, r, out)
		return out
	}
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			c := frame.At(r.Min.X+x, r.Min.Y+y)
			rr, gg, bb, _ := c.RGBA()
			i := (y*r.Dx() + x) * 4
			out.Pix[i+0] = uint8(rr >> 8)
			out.Pix[i+1] = uint8(gg >> 8)
			out.Pix[i+2] = uint8(bb >> 8)
			out.Pix[i+3] = 255
		}
	}
	return out
}

// halfFrameYCbCr copies a rectangular region of a YCbCr image into an RGBA
// destination, converting each pixel via the JPEG-standard YCbCr→RGB formula
// without going through image.Image dispatch.
func halfFrameYCbCr(src *image.YCbCr, region image.Rectangle, dst *image.RGBA) {
	w, h := region.Dx(), region.Dy()
	dstStride := dst.Stride
	for y := 0; y < h; y++ {
		srcY := region.Min.Y + y
		for x := 0; x < w; x++ {
			srcX := region.Min.X + x
			yi := src.YOffset(srcX, srcY)
			ci := src.COffset(srcX, srcY)
			r, g, b := color.YCbCrToRGB(src.Y[yi], src.Cb[ci], src.Cr[ci])
			o := y*dstStride + x*4
			dst.Pix[o+0] = r
			dst.Pix[o+1] = g
			dst.Pix[o+2] = b
			dst.Pix[o+3] = 255
		}
	}
}

// bilinearRGB does a clamped bilinear sample of an arbitrary image.Image.
// Out-of-bounds reads are clamped to the nearest edge.
func bilinearRGB(img image.Image, x0, y0 int, dx, dy float32) (uint8, uint8, uint8) {
	b := img.Bounds()
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	x0 = clamp(x0, b.Min.X, b.Max.X-1)
	y0 = clamp(y0, b.Min.Y, b.Max.Y-1)
	x1 := clamp(x0+1, b.Min.X, b.Max.X-1)
	y1 := clamp(y0+1, b.Min.Y, b.Max.Y-1)
	c00 := img.At(x0, y0)
	c10 := img.At(x1, y0)
	c01 := img.At(x0, y1)
	c11 := img.At(x1, y1)
	r := bilinear4(c00, c10, c01, c11, dx, dy)
	return r.R, r.G, r.B
}

// DefaultLenses returns calibration values measured from the AKASO 360
// 1920×960 dual-fisheye preview stream. The image circle is inscribed in
// each 960×960 half (radius ≈ 480, center at the half's geometric centre).
// The lens FOV is 200° (each lens covers a ~10° overlap past the equator
// which the stitcher uses for seam blending).
//
// These defaults produce a usable stitch with minor parallax artifacts on
// close objects; per-camera tuning of the centre or rotation may further
// reduce seam-region distortion.
func DefaultLenses() (front, back FisheyeLens) {
	front = FisheyeLens{
		CenterX:     480,
		CenterY:     480,
		Radius:      480,
		FOVDeg:      200,
		RotationDeg: 0,
	}
	back = FisheyeLens{
		CenterX:     480,
		CenterY:     480,
		Radius:      480,
		FOVDeg:      200,
		RotationDeg: 0,
	}
	return front, back
}
