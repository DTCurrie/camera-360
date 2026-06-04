package camera360

import (
	"context"
	"testing"

	"go.viam.com/rdk/components/audioin"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/discovery"
)

func sampleWebcams() []DiscoveredWebcam {
	return []DiscoveredWebcam{
		{VideoDevice: "/dev/video8", AudioDevice: "plughw:2,0", Label: "JVCU360", USBID: "0711:0360", LensHint: "known-360"},
		{VideoDevice: "/dev/video10", AudioDevice: "", Label: "Acme Fisheye Cam", USBID: "2222:3333", LensHint: "name-360"},
		{VideoDevice: "/dev/video12", AudioDevice: "plughw:3,0", Label: "Logi Webcam", USBID: "046d:0825", LensHint: ""},
	}
}

func newTestDiscovery(t *testing.T, webcams []DiscoveredWebcam, includeMic, includeAllUVC bool) *uvcDiscovery {
	t.Helper()
	return &uvcDiscovery{
		Named:         discovery.Named("disco").AsNamed(),
		logger:        logging.NewTestLogger(t),
		includeMic:    includeMic,
		includeAllUVC: includeAllUVC,
		enumerate: func(context.Context, logging.Logger) ([]DiscoveredWebcam, error) {
			return webcams, nil
		},
	}
}

func TestDiscoverResourcesDefault360Only(t *testing.T) {
	d := newTestDiscovery(t, sampleWebcams(), true, false)
	got, err := d.DiscoverResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("DiscoverResources: %v", err)
	}
	// JVCU360 (cam+mic) and Acme Fisheye (cam, no mic); the plain Logi webcam is
	// filtered out by the 360-only default.
	if len(got) != 3 {
		t.Fatalf("want 3 configs, got %d: %+v", len(got), got)
	}
	if got[0].Name != "jvcu360" || got[0].API != camera.API || got[0].Model != UVCCamera {
		t.Errorf("config 0 = %q %v %v, want jvcu360 camera UVCCamera", got[0].Name, got[0].API, got[0].Model)
	}
	if got[1].Name != "jvcu360-mic" || got[1].API != audioin.API || got[1].Model != UVCMic {
		t.Errorf("config 1 = %q %v %v, want jvcu360-mic audio_in UVCMic", got[1].Name, got[1].API, got[1].Model)
	}
	if got[2].Name != "acme-fisheye-cam" || got[2].API != camera.API {
		t.Errorf("config 2 = %q %v, want acme-fisheye-cam camera", got[2].Name, got[2].API)
	}
	// The camera config's typed attributes carry the discovered node.
	cfg, ok := got[0].ConvertedAttributes.(*UVCCameraConfig)
	if !ok {
		t.Fatalf("config 0 ConvertedAttributes = %T, want *UVCCameraConfig", got[0].ConvertedAttributes)
	}
	if cfg.VideoDevice != "/dev/video8" {
		t.Errorf("config 0 video_device = %q, want /dev/video8", cfg.VideoDevice)
	}
	if got[0].Attributes["lens_hint"] != "known-360" || got[0].Attributes["usb_id"] != "0711:0360" {
		t.Errorf("config 0 attributes missing lens_hint/usb_id: %+v", got[0].Attributes)
	}
}

func TestDiscoverResourcesIncludeAllUVC(t *testing.T) {
	d := newTestDiscovery(t, sampleWebcams(), true, true)
	got, err := d.DiscoverResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("DiscoverResources: %v", err)
	}
	// All three cams; two have mics => 3 + 2 = 5 configs.
	if len(got) != 5 {
		t.Fatalf("want 5 configs, got %d: %+v", len(got), names(got))
	}
}

func TestDiscoverResourcesNoMic(t *testing.T) {
	d := newTestDiscovery(t, sampleWebcams(), false, true)
	got, err := d.DiscoverResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("DiscoverResources: %v", err)
	}
	// include_mic=false => only the three camera configs, no mics.
	if len(got) != 3 {
		t.Fatalf("want 3 configs, got %d: %+v", len(got), names(got))
	}
	for _, c := range got {
		if c.API == audioin.API {
			t.Errorf("unexpected mic config %q with include_mic=false", c.Name)
		}
	}
}

func TestDiscoverResourcesEmpty(t *testing.T) {
	d := newTestDiscovery(t, nil, true, false)
	got, err := d.DiscoverResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("DiscoverResources: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want no configs, got %+v", names(got))
	}
}

func TestToAttributeMapOmitsZeroFields(t *testing.T) {
	m, err := toAttributeMap(&UVCCameraConfig{VideoDevice: "/dev/video8"})
	if err != nil {
		t.Fatalf("toAttributeMap: %v", err)
	}
	if len(m) != 1 || m["video_device"] != "/dev/video8" {
		t.Errorf("want {video_device:/dev/video8}, got %+v", m)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"JVCU360":           "jvcu360",
		"Acme Fisheye Cam":  "acme-fisheye-cam",
		"  Logi__Webcam!! ": "logi-webcam",
		"360°":              "360",
		"":                  "",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q)=%q want %q", in, got, want)
		}
	}
}

func names(cfgs []resource.Config) []string {
	out := make([]string, len(cfgs))
	for i, c := range cfgs {
		out[i] = c.Name
	}
	return out
}
