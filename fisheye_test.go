package camera360

import (
	"image"
	"image/color"
	"testing"
)

// makeDualFisheye builds a synthetic dual-fisheye frame: solid-red back half
// (left) and solid-blue front half (right), each with a centered circular
// image area. Useful for verifying the stitcher's lens selection and seam
// blending.
func makeDualFisheye(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	half := w / 2
	cx0, cy0 := half/2, h/2      // back lens centre (left half)
	cx1, cy1 := half+half/2, h/2 // front lens centre (right half)
	radius := h/2 - 20

	back := color.RGBA{R: 255, A: 255}
	front := color.RGBA{B: 255, A: 255}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.RGBA
			if x < half {
				dx, dy := x-cx0, y-cy0
				if dx*dx+dy*dy <= radius*radius {
					c = back
				}
			} else {
				dx, dy := x-cx1, y-cy1
				if dx*dx+dy*dy <= radius*radius {
					c = front
				}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func dominantRGBA(img *image.RGBA, x0, y0, x1, y1 int) string {
	var rs, gs, bs, n int
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			c := img.RGBAAt(x, y)
			rs += int(c.R)
			gs += int(c.G)
			bs += int(c.B)
			n++
		}
	}
	r, g, b := rs/n, gs/n, bs/n
	switch {
	case r > g && r > b:
		return "red"
	case b > r && b > g:
		return "blue"
	default:
		return "other"
	}
}

func TestStitcherForwardIsFront(t *testing.T) {
	// 1920x960 dual fisheye, blue front (right half) / red back (left half).
	frame := makeDualFisheye(1920, 960)
	front, back := DefaultLenses()
	s := BuildFisheyeStitcher(front, back, 1920, 960, 800, 400)
	erp := s.StitchToERP(frame)

	// Centre of the ERP corresponds to lon=0 (looking forward), which should
	// sample the front (blue) lens.
	c := erp.RGBAAt(400, 200)
	if c.B < 200 || c.R > 50 {
		t.Errorf("expected blue (front) at lon=0; got RGBA{%d,%d,%d}", c.R, c.G, c.B)
	}
}

func TestStitcherBackwardIsBack(t *testing.T) {
	frame := makeDualFisheye(1920, 960)
	front, back := DefaultLenses()
	s := BuildFisheyeStitcher(front, back, 1920, 960, 800, 400)
	erp := s.StitchToERP(frame)

	// Far-left edge of the ERP corresponds to lon = -π (looking behind),
	// which should sample the back (red) lens.
	c := erp.RGBAAt(5, 200)
	if c.R < 200 || c.B > 50 {
		t.Errorf("expected red (back) at lon=-π; got RGBA{%d,%d,%d}", c.R, c.G, c.B)
	}
}

// rgbaToYCbCr420 converts an RGBA test image to YCbCr 4:2:0, the format
// jpeg.Decode returns for color JPEGs. Exercises the stitcher/HalfFrame
// YCbCr fast paths.
func rgbaToYCbCr420(src *image.RGBA) *image.YCbCr {
	b := src.Bounds()
	dst := image.NewYCbCr(b, image.YCbCrSubsampleRatio420)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := src.RGBAAt(x, y)
			yy, cb, cr := color.RGBToYCbCr(c.R, c.G, c.B)
			dst.Y[dst.YOffset(x, y)] = yy
			dst.Cb[dst.COffset(x, y)] = cb
			dst.Cr[dst.COffset(x, y)] = cr
		}
	}
	return dst
}

func TestStitcherYCbCrFastPathMatchesRGBA(t *testing.T) {
	rgba := makeDualFisheye(1920, 960)
	ycc := rgbaToYCbCr420(rgba)
	front, back := DefaultLenses()
	s := BuildFisheyeStitcher(front, back, 1920, 960, 800, 400)

	// Forward should sample the front (blue) lens.
	erp := s.StitchToERP(ycc)
	c := erp.RGBAAt(400, 200)
	if c.B < 200 || c.R > 50 {
		t.Errorf("YCbCr fast path: expected blue (front) at lon=0; got RGBA{%d,%d,%d}", c.R, c.G, c.B)
	}
	// Behind should sample the back (red) lens.
	c = erp.RGBAAt(5, 200)
	if c.R < 200 || c.B > 50 {
		t.Errorf("YCbCr fast path: expected red (back) at lon=-π; got RGBA{%d,%d,%d}", c.R, c.G, c.B)
	}
}

func BenchmarkStitchToERP_RGBA(b *testing.B) {
	frame := makeDualFisheye(1920, 960)
	front, back := DefaultLenses()
	s := BuildFisheyeStitcher(front, back, 1920, 960, 1920, 960)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.StitchToERP(frame)
	}
}

func BenchmarkStitchToERP_YCbCr(b *testing.B) {
	rgba := makeDualFisheye(1920, 960)
	frame := rgbaToYCbCr420(rgba)
	front, back := DefaultLenses()
	s := BuildFisheyeStitcher(front, back, 1920, 960, 1920, 960)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.StitchToERP(frame)
	}
}

func TestHalfFrameYCbCrFastPath(t *testing.T) {
	rgba := makeDualFisheye(1920, 960)
	ycc := rgbaToYCbCr420(rgba)
	front, back := DefaultLenses()
	s := BuildFisheyeStitcher(front, back, 1920, 960, 800, 400)

	fh := s.HalfFrame(ycc, "front")
	bh := s.HalfFrame(ycc, "back")
	if got := dominantRGBA(fh, 200, 200, 700, 700); got != "blue" {
		t.Errorf("YCbCr fast path: front half should be blue-dominant; got %s", got)
	}
	if got := dominantRGBA(bh, 200, 200, 700, 700); got != "red" {
		t.Errorf("YCbCr fast path: back half should be red-dominant; got %s", got)
	}
}

func TestHalfFrameExtraction(t *testing.T) {
	frame := makeDualFisheye(1920, 960)
	front, back := DefaultLenses()
	s := BuildFisheyeStitcher(front, back, 1920, 960, 800, 400)

	fh := s.HalfFrame(frame, "front")
	bh := s.HalfFrame(frame, "back")
	if got := dominantRGBA(fh, 200, 200, 700, 700); got != "blue" {
		t.Errorf("front half should be blue-dominant; got %s", got)
	}
	if got := dominantRGBA(bh, 200, 200, 700, 700); got != "red" {
		t.Errorf("back half should be red-dominant; got %s", got)
	}
}
