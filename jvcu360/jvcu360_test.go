package jvcu360

import (
	"image"
	"image/color"
	"slices"
	"testing"

	"camera360"
)

func TestCameraConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     CameraConfig
		wantErr bool
	}{
		{"empty defaults ok", CameraConfig{}, false},
		{"explicit values ok", CameraConfig{VideoDevice: "/dev/video0", Width: 1920, Height: 720, FrameRate: 30, InputFormat: "mjpeg"}, false},
		{"negative width", CameraConfig{Width: -1}, true},
		{"negative height", CameraConfig{Height: -1}, true},
		{"negative frame rate", CameraConfig{FrameRate: -1}, true},
		{"crop ok", CameraConfig{CropTop: 60, CropBottom: 60}, false},
		{"negative crop top", CameraConfig{CropTop: -1}, true},
		{"negative crop bottom", CameraConfig{CropBottom: -1}, true},
		{"fov ok", CameraConfig{HFOVDeg: 360, VFOVDeg: 53}, false},
		{"h_fov too big", CameraConfig{HFOVDeg: 400}, true},
		{"v_fov too big", CameraConfig{VFOVDeg: 200}, true},
		{"negative h_fov", CameraConfig{HFOVDeg: -1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, opt, err := tc.cfg.Validate("test")
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req != nil || opt != nil {
				t.Errorf("expected no dependencies, got required=%v optional=%v", req, opt)
			}
		})
	}
}

func TestVideoInputArgs(t *testing.T) {
	args := camera360.VideoInputArgs("/dev/video0", 1920, 1080, 30, "mjpeg")
	if len(args) < 2 || args[len(args)-2] != "-i" || args[len(args)-1] != "/dev/video0" {
		t.Fatalf("expected args to end with -i /dev/video0, got %v", args)
	}
	if !slices.Contains(args, "1920x1080") {
		t.Errorf("expected video_size 1920x1080 in args, got %v", args)
	}
	if !slices.Contains(args, "30") {
		t.Errorf("expected framerate 30 in args, got %v", args)
	}
}

// letterboxImage builds a w×h image with `top` black rows, `bottom` black rows,
// and a mid-gray band between them.
func letterboxImage(w, h, top, bottom int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	band := color.RGBA{120, 120, 120, 255}
	for y := top; y < h-bottom; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, band)
		}
	}
	return img
}

func TestDetectLetterbox(t *testing.T) {
	cases := []struct {
		name                string
		w, h, top, bottom   int
		wantTop, wantBottom int
	}{
		{"symmetric bars", 1920, 1080, 60, 60, 60, 60},
		{"asymmetric bars", 1920, 1080, 80, 40, 80, 40},
		{"no bars", 1920, 960, 0, 0, 0, 0},
		{"top only", 100, 100, 25, 0, 25, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			top, bottom := detectLetterbox(letterboxImage(tc.w, tc.h, tc.top, tc.bottom), 16)
			if top != tc.wantTop || bottom != tc.wantBottom {
				t.Fatalf("got top=%d bottom=%d, want top=%d bottom=%d", top, bottom, tc.wantTop, tc.wantBottom)
			}
		})
	}
}

func TestDetectLetterboxAllDark(t *testing.T) {
	// A fully black frame has no content band; report the whole height as top.
	top, bottom := detectLetterbox(image.NewRGBA(image.Rect(0, 0, 64, 48)), 16)
	if top != 48 || bottom != 0 {
		t.Fatalf("got top=%d bottom=%d, want top=48 bottom=0", top, bottom)
	}
}

func TestCropFilter(t *testing.T) {
	cases := []struct {
		name       string
		top, bot   int
		wantExpr   string
		wantWanted bool
	}{
		{"none", 0, 0, "", false},
		{"top and bottom", 60, 60, "crop=iw:ih-120:0:60", true},
		{"top only", 48, 0, "crop=iw:ih-48:0:48", true},
		{"bottom only", 0, 48, "crop=iw:ih-48:0:0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, ok := cropFilter(tc.top, tc.bot)
			if ok != tc.wantWanted {
				t.Fatalf("ok = %v, want %v", ok, tc.wantWanted)
			}
			if expr != tc.wantExpr {
				t.Errorf("expr = %q, want %q", expr, tc.wantExpr)
			}
		})
	}
}
