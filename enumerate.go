package camera360

// UVC webcam discovery via Linux sysfs. This is the detection engine shared by
// the discovery service (discovery.go) and the `cmd/uvc -list` tool.
//
// Why sysfs and not ffmpeg/v4l2-ctl parsing: we need to (a) tell a real USB
// webcam apart from the host's other /dev/videoN nodes — on a Raspberry Pi the
// internal ISP/codec/pispbe blocks claim the low numbers (video0..video7) and
// the USB camera lands higher, which is exactly why a hard-coded /dev/video0
// default fails there — and (b) read the USB vendor/product so we can flag known
// 360 cameras. sysfs exposes both as plain files, so this stays pure-Go with no
// cgo and no extra dependencies, matching the rest of the module.
//
// Detection is Linux-only: the node-numbering problem and the sysfs layout are
// Linux-specific, and macOS/Windows don't expose USB VID:PID this way.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"go.viam.com/rdk/logging"
)

const (
	// uvcInterfaceClass is the USB Video Class code reported in sysfs
	// (bInterfaceClass) for any UVC webcam interface.
	uvcInterfaceClass = "0e"
	defaultSysRoot    = "/sys"
	maxVideoIndex     = 1 << 30 // sort sentinel for unparpseable video node names
)

// known360 maps a lowercase "vid:pid" to a human label for cameras we know are
// 360/fisheye. This is the only authoritative 360 signal; grow it as more
// cameras are brought up. The JVCU360 (see jvcu360/README.md) is the seed.
var known360 = map[string]string{
	"0711:0360": "j5create JVCU360",
}

// name360Hints are substrings (matched case-insensitively against the device's
// product/name string) that suggest a 360 or fisheye camera. Soft signal — used
// only when there's no known360 match, and prone to both false positives and
// false negatives. VID:PID is authoritative; this just widens the net.
var name360Hints = []string{"360", "fisheye", "panoram", "theta", "insta360", "kandao"}

// DiscoveredWebcam is one confirmed UVC webcam plus the OS handles and metadata
// needed to build uvc-camera / uvc-mic configs.
type DiscoveredWebcam struct {
	// VideoDevice is the V4L2 capture node, e.g. "/dev/video8".
	VideoDevice string
	// AudioDevice is the ALSA handle for the device's mic, e.g. "plughw:2,0", or
	// "" when the device exposes no USB sound card (not every webcam has a mic).
	AudioDevice string
	// Label is the USB product string (falling back to the V4L2 device name).
	Label string
	// USBID is "vid:pid" in lowercase hex, e.g. "0711:0360".
	USBID string
	// LensHint classifies 360/fisheye capability: "known-360" (VID:PID match),
	// "name-360" (name heuristic), or "" (a plain webcam).
	LensHint string
}

// EnumerateUVCWebcams returns every confirmed UVC webcam attached to the host.
// It is Linux-only; on other platforms it logs and returns (nil, nil).
func EnumerateUVCWebcams(_ context.Context, logger logging.Logger) ([]DiscoveredWebcam, error) {
	if runtime.GOOS != "linux" {
		logger.Infow("UVC webcam discovery is only supported on Linux; returning no devices",
			"goos", runtime.GOOS)
		return nil, nil
	}
	return enumerateLinux(defaultSysRoot, logger)
}

// enumerateLinux is the Linux implementation, parameterized on sysRoot so tests
// can point it at a fixture tree instead of the real /sys.
func enumerateLinux(sysRoot string, logger logging.Logger) ([]DiscoveredWebcam, error) {
	realSysRoot, err := filepath.EvalSymlinks(sysRoot)
	if err != nil {
		logger.Debugw("sysfs root unavailable; no UVC webcams", "sys_root", sysRoot, "err", err)
		return nil, nil
	}

	v4lDir := filepath.Join(sysRoot, "class", "video4linux")
	entries, err := os.ReadDir(v4lDir)
	if err != nil {
		logger.Debugw("no video4linux class dir; no UVC webcams", "dir", v4lDir, "err", err)
		return nil, nil
	}

	// A single camera exposes several /dev/videoN nodes (capture + metadata), so
	// we group nodes by their USB device directory and keep the lowest-numbered
	// capture node per device.
	type group struct {
		usbDir   string
		usbID    string
		label    string
		bestNode string
		bestIdx  int
	}
	groups := map[string]*group{}

	for _, e := range entries {
		node := e.Name()
		if !strings.HasPrefix(node, "video") {
			continue
		}
		ifaceDir, err := filepath.EvalSymlinks(filepath.Join(v4lDir, node, "device"))
		if err != nil || !strings.HasPrefix(ifaceDir, realSysRoot) {
			continue
		}
		// Gate on the USB Video interface class — this is what tells a real webcam
		// apart from the Pi's platform ISP/codec nodes (which have no such file).
		if readSysAttr(filepath.Join(ifaceDir, "bInterfaceClass")) != uvcInterfaceClass {
			continue
		}
		usbDir := findUSBDeviceDir(ifaceDir, realSysRoot)
		if usbDir == "" {
			continue // not under a USB device
		}
		v4lName := readSysAttr(filepath.Join(v4lDir, node, "name"))
		if strings.Contains(strings.ToLower(v4lName), "metadata") {
			continue // metadata node, not the capture node
		}
		idx := videoIndex(node)
		if g := groups[usbDir]; g != nil {
			if idx < g.bestIdx {
				g.bestNode, g.bestIdx = node, idx
			}
			continue
		}
		vid := readSysAttr(filepath.Join(usbDir, "idVendor"))
		pid := readSysAttr(filepath.Join(usbDir, "idProduct"))
		label := readSysAttr(filepath.Join(usbDir, "product"))
		if label == "" {
			label = v4lName
		}
		groups[usbDir] = &group{
			usbDir:   usbDir,
			usbID:    strings.ToLower(vid + ":" + pid),
			label:    label,
			bestNode: node,
			bestIdx:  idx,
		}
	}

	if len(groups) == 0 {
		return nil, nil
	}

	usbToCard := soundCardsByUSB(sysRoot, realSysRoot)

	out := make([]DiscoveredWebcam, 0, len(groups))
	for _, g := range groups {
		w := DiscoveredWebcam{
			VideoDevice: filepath.Join("/dev", g.bestNode),
			Label:       g.label,
			USBID:       g.usbID,
			LensHint:    classifyLens(g.usbID, g.label),
		}
		if card, ok := usbToCard[g.usbDir]; ok {
			w.AudioDevice = fmt.Sprintf("plughw:%d,0", card)
		} else {
			logger.Debugw("no USB sound card paired with webcam; mic left to default",
				"video_device", w.VideoDevice, "usb_id", w.USBID)
		}
		out = append(out, w)
	}
	// Stable, human-friendly order by capture-node number.
	sort.Slice(out, func(i, j int) bool {
		return videoIndex(filepath.Base(out[i].VideoDevice)) < videoIndex(filepath.Base(out[j].VideoDevice))
	})
	return out, nil
}

// soundCardsByUSB maps a USB device directory to the lowest ALSA card index
// hosted by that device, so a webcam's mic can be paired to its camera.
func soundCardsByUSB(sysRoot, realSysRoot string) map[string]int {
	result := map[string]int{}
	soundDir := filepath.Join(sysRoot, "class", "sound")
	entries, err := os.ReadDir(soundDir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "card") {
			continue
		}
		card, err := strconv.Atoi(strings.TrimPrefix(name, "card"))
		if err != nil {
			continue // e.g. "controlC0"
		}
		ifaceDir, err := filepath.EvalSymlinks(filepath.Join(soundDir, name, "device"))
		if err != nil || !strings.HasPrefix(ifaceDir, realSysRoot) {
			continue
		}
		usbDir := findUSBDeviceDir(ifaceDir, realSysRoot)
		if usbDir == "" {
			continue
		}
		if existing, ok := result[usbDir]; !ok || card < existing {
			result[usbDir] = card
		}
	}
	return result
}

// findUSBDeviceDir walks up from a USB interface directory to the USB device
// directory (the one holding idVendor/idProduct), stopping at sysRoot. Returns
// "" if no USB device ancestor is found (e.g. a platform/ISP node).
func findUSBDeviceDir(start, sysRoot string) string {
	for dir := start; dir != "" && dir != "/" && dir != "." && dir != sysRoot; {
		if readSysAttr(filepath.Join(dir, "idVendor")) != "" &&
			readSysAttr(filepath.Join(dir, "idProduct")) != "" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// classifyLens returns the 360/fisheye classification for a device.
func classifyLens(usbID, label string) string {
	if _, ok := known360[usbID]; ok {
		return "known-360"
	}
	lower := strings.ToLower(label)
	for _, hint := range name360Hints {
		if strings.Contains(lower, hint) {
			return "name-360"
		}
	}
	return ""
}

// readSysAttr reads a sysfs attribute file and trims trailing whitespace,
// returning "" if it can't be read.
func readSysAttr(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// videoIndex parses the integer N from a "videoN" node name, returning a large
// sentinel for anything unparseable so it sorts last.
func videoIndex(node string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(node, "video"))
	if err != nil {
		return maxVideoIndex
	}
	return n
}
