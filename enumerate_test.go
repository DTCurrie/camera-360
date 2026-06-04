package camera360

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.viam.com/rdk/logging"
)

// mkAttr writes a sysfs attribute file (creating parent dirs) under root.
func mkAttr(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkDir creates an (empty) directory under root.
func mkDir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		t.Fatal(err)
	}
}

// mkLink creates linkRel as a symlink to an absolute path (root/target).
func mkLink(t *testing.T, root, target, linkRel string) {
	t.Helper()
	link := filepath.Join(root, linkRel)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, target), link); err != nil {
		t.Fatal(err)
	}
}

// fakeSysfs builds a sysfs fixture tree exercising every detection branch:
//   - a JVCU360 (known-360) with a paired mic and a metadata node to skip;
//   - an Acme "Fisheye" cam (name-360) with no mic;
//   - a vendor-class USB device that is not a webcam (excluded);
//   - a Raspberry-Pi-style platform ISP node with no USB ancestry (excluded).
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// JVCU360: USB device 1-1, video interface :1.0 (UVC), audio interface :1.2.
	mkAttr(t, root, "devices/usb1/1-1/idVendor", "0711")
	mkAttr(t, root, "devices/usb1/1-1/idProduct", "0360")
	mkAttr(t, root, "devices/usb1/1-1/product", "JVCU360")
	mkAttr(t, root, "devices/usb1/1-1/1-1:1.0/bInterfaceClass", "0e")
	mkAttr(t, root, "devices/usb1/1-1/1-1:1.2/bInterfaceClass", "01")
	// video7 is the metadata node (skipped despite the lower number), video8 the
	// capture node we want, video9 a second node of the same device (collapsed).
	mkAttr(t, root, "class/video4linux/video7/name", "JVCU360 Metadata")
	mkLink(t, root, "devices/usb1/1-1/1-1:1.0", "class/video4linux/video7/device")
	mkAttr(t, root, "class/video4linux/video8/name", "JVCU360")
	mkLink(t, root, "devices/usb1/1-1/1-1:1.0", "class/video4linux/video8/device")
	mkAttr(t, root, "class/video4linux/video9/name", "JVCU360")
	mkLink(t, root, "devices/usb1/1-1/1-1:1.0", "class/video4linux/video9/device")
	mkLink(t, root, "devices/usb1/1-1/1-1:1.2", "class/sound/card2/device")
	mkDir(t, root, "class/sound/controlC2") // non-numeric: ignored

	// Acme Fisheye (name heuristic), no sound card.
	mkAttr(t, root, "devices/usb3/3-1/idVendor", "2222")
	mkAttr(t, root, "devices/usb3/3-1/idProduct", "3333")
	mkAttr(t, root, "devices/usb3/3-1/product", "Acme Fisheye Cam")
	mkAttr(t, root, "devices/usb3/3-1/3-1:1.0/bInterfaceClass", "0e")
	mkAttr(t, root, "class/video4linux/video10/name", "Acme Fisheye Cam")
	mkLink(t, root, "devices/usb3/3-1/3-1:1.0", "class/video4linux/video10/device")

	// Vendor-class USB device that is not a UVC webcam.
	mkAttr(t, root, "devices/usb2/2-1/idVendor", "1234")
	mkAttr(t, root, "devices/usb2/2-1/idProduct", "5678")
	mkAttr(t, root, "devices/usb2/2-1/2-1:1.0/bInterfaceClass", "ff")
	mkAttr(t, root, "class/video4linux/video1/name", "Vendor Thing")
	mkLink(t, root, "devices/usb2/2-1/2-1:1.0", "class/video4linux/video1/device")

	// Raspberry Pi internal ISP block: a platform device, no USB ancestry.
	mkDir(t, root, "devices/platform/isp")
	mkAttr(t, root, "class/video4linux/video0/name", "bcm2835-isp")
	mkLink(t, root, "devices/platform/isp", "class/video4linux/video0/device")

	return root
}

func TestEnumerateLinux(t *testing.T) {
	root := fakeSysfs(t)

	got, err := enumerateLinux(root, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("enumerateLinux: %v", err)
	}
	want := []DiscoveredWebcam{
		{VideoDevice: "/dev/video8", AudioDevice: "plughw:2,0", Label: "JVCU360", USBID: "0711:0360", LensHint: "known-360"},
		{VideoDevice: "/dev/video10", AudioDevice: "", Label: "Acme Fisheye Cam", USBID: "2222:3333", LensHint: "name-360"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("enumerateLinux mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestEnumerateLinuxNoSysfs(t *testing.T) {
	// A root with no class/video4linux dir yields no devices and no error.
	got, err := enumerateLinux(t.TempDir(), logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no webcams, got %+v", got)
	}
}

func TestClassifyLens(t *testing.T) {
	cases := []struct {
		usbID, label, want string
	}{
		{"0711:0360", "anything", "known-360"},        // VID:PID wins
		{"9999:9999", "Acme Fisheye Cam", "name-360"}, // name heuristic
		{"9999:9999", "Insta360 Link", "name-360"},
		{"9999:9999", "Logitech HD Webcam", ""}, // plain webcam
		{"9999:9999", "", ""},
	}
	for _, tc := range cases {
		if got := classifyLens(tc.usbID, tc.label); got != tc.want {
			t.Errorf("classifyLens(%q,%q)=%q want %q", tc.usbID, tc.label, got, tc.want)
		}
	}
}

func TestVideoIndex(t *testing.T) {
	if got := videoIndex("video8"); got != 8 {
		t.Errorf("videoIndex(video8)=%d want 8", got)
	}
	if got := videoIndex("video10"); got != 10 {
		t.Errorf("videoIndex(video10)=%d want 10", got)
	}
	if got := videoIndex("controlC0"); got != maxVideoIndex {
		t.Errorf("videoIndex(controlC0)=%d want sentinel", got)
	}
}
