package camera360

import (
	"slices"
	"testing"
)

func TestUVCCameraConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     UVCCameraConfig
		wantErr bool
	}{
		{"empty defaults ok", UVCCameraConfig{}, false},
		{"explicit values ok", UVCCameraConfig{VideoDevice: "/dev/video0", Width: 1920, Height: 720, FrameRate: 30, InputFormat: "mjpeg"}, false},
		{"negative width", UVCCameraConfig{Width: -1}, true},
		{"negative height", UVCCameraConfig{Height: -1}, true},
		{"negative frame rate", UVCCameraConfig{FrameRate: -1}, true},
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
	args := videoInputArgs("/dev/video0", 1920, 1080, 30, "mjpeg")
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
