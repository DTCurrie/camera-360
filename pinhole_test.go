package camera360

import (
	"image"
	"image/color"
	"testing"
)

// makeERP builds a synthetic equirectangular test image: a solid red dot at
// the centre (lon=0, lat=0 → ERP coords (W/2, H/2)), blue everywhere else.
// Yaw=0/pitch=0 should produce a pinhole view with the red dot in the centre;
// yaw=180 should push it off-frame (the camera now looks at the back of the
// panorama where everything is blue).
func makeERP(w, h, dotRadius int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	blue := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	red := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, blue)
		}
	}
	cx, cy := w/2, h/2
	for dy := -dotRadius; dy <= dotRadius; dy++ {
		for dx := -dotRadius; dx <= dotRadius; dx++ {
			if dx*dx+dy*dy <= dotRadius*dotRadius {
				img.Set(cx+dx, cy+dy, red)
			}
		}
	}
	return img
}

func centerColor(img *image.RGBA) color.RGBA {
	b := img.Bounds()
	c := img.RGBAAt(b.Dx()/2, b.Dy()/2)
	return c
}

func dominant(img *image.RGBA) string {
	b := img.Bounds()
	var rSum, gSum, bSum int
	n := 0
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := img.RGBAAt(x, y)
			rSum += int(c.R)
			gSum += int(c.G)
			bSum += int(c.B)
			n++
		}
	}
	r, g, bl := rSum/n, gSum/n, bSum/n
	switch {
	case r > g && r > bl:
		return "red"
	case bl > r && bl > g:
		return "blue"
	default:
		return "other"
	}
}

func TestPinholeForwardLooksAtDot(t *testing.T) {
	erp := makeERP(800, 400, 20)
	view := PinholeView{Yaw: 0, Pitch: 0, FOVDeg: 60, Width: 200, Height: 200}
	p := NewPinholeProjector(view, 800, 400)
	out := p.Project(erp)
	c := centerColor(out)
	if c.R < 200 || c.B > 50 {
		t.Errorf("yaw=0 expected red dot at centre, got RGBA{%d,%d,%d}", c.R, c.G, c.B)
	}
}

func TestPinholeBehindIsBlue(t *testing.T) {
	erp := makeERP(800, 400, 20)
	view := PinholeView{Yaw: 180, Pitch: 0, FOVDeg: 60, Width: 200, Height: 200}
	p := NewPinholeProjector(view, 800, 400)
	out := p.Project(erp)
	if got := dominant(out); got != "blue" {
		t.Errorf("yaw=180 expected blue-dominant view (red dot is behind us), got %s", got)
	}
}

func TestPinholeYaw90PutsDotOffCenterHorizontally(t *testing.T) {
	erp := makeERP(800, 400, 20)
	view := PinholeView{Yaw: 90, Pitch: 0, FOVDeg: 60, Width: 200, Height: 200}
	p := NewPinholeProjector(view, 800, 400)
	out := p.Project(erp)
	// Looking 90° to the right; the red dot was at lon=0, so it should now
	// be at lon=-90 in the rotated frame — outside our 60° FOV. Frame should
	// be predominantly blue.
	if got := dominant(out); got != "blue" {
		t.Errorf("yaw=90 with fov=60 should not see the dot, got %s", got)
	}
}

func TestPinholeProjectorUpdateRebuildsLUT(t *testing.T) {
	erp := makeERP(800, 400, 20)
	view := PinholeView{Yaw: 0, Pitch: 0, FOVDeg: 60, Width: 100, Height: 100}
	p := NewPinholeProjector(view, 800, 400)

	out1 := p.Project(erp)
	if c := centerColor(out1); c.R < 200 {
		t.Fatalf("baseline yaw=0 expected red centre, got %v", c)
	}

	p.SetView(180, 0, 60)
	out2 := p.Project(erp)
	if got := dominant(out2); got != "blue" {
		t.Fatalf("after SetView(180), expected blue-dominant; got %s", got)
	}
}
