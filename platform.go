package camera360

import (
	"fmt"
	"runtime"
	"strconv"
	"sync"

	"go.viam.com/rdk/logging"
)

// Platform-specific ffmpeg input construction for USB cameras and microphones.
//
// On Linux a UVC camera enumerates as a V4L2 video node (/dev/videoN) plus an
// ALSA capture device; on macOS both the camera and mic are reached through
// avfoundation. We shell out to ffmpeg in both cases (see capture.go and
// audiocapture.go), so the only platform difference is the set of input flags
// assembled here. Windows (dshow) is intentionally not handled in this phase.

const (
	defaultLinuxVideoDevice  = "/dev/video0"
	defaultDarwinVideoDevice = "0"       // avfoundation video device index
	defaultLinuxAudioDevice  = "default" // ALSA; usually wants overriding with the webcam's card
	defaultDarwinAudioDevice = ":0"      // avfoundation audio-only index (":N" = audio, "N" = video)

	// Capture size differs by OS because the two ingress paths differ. On Linux
	// we pull the device's native MJPEG over V4L2, which is compressed and fits
	// comfortably over USB at full resolution. On macOS, avfoundation tends to
	// expose only RAW formats (uyvy422), whose uncompressed 1080p mode many
	// webcams cannot actually sustain over USB — the device advertises it but
	// emits a frozen frame, expecting 1080p to be pulled as MJPEG (which
	// avfoundation cannot request). 1280x720 raw streams live, so on macOS it is
	// both the default and the hard ceiling (anything larger is clamped down to
	// it). Observed on the j5create JVCU360; see jvcu360/README.md and ISSUES.md.
	defaultLinuxVideoWidth  = 1920
	defaultLinuxVideoHeight = 1080
	maxDarwinVideoWidth     = 1280
	maxDarwinVideoHeight    = 720
)

// darwinCapLogOnce keeps the "capping resolution on macOS" notice to one line
// per process, no matter how many times the camera is (re)built.
var darwinCapLogOnce sync.Once

// DefaultVideoDevice returns the conventional video device for the host OS.
func DefaultVideoDevice() string {
	if runtime.GOOS == "darwin" {
		return defaultDarwinVideoDevice
	}
	return defaultLinuxVideoDevice
}

// DefaultVideoSize returns the default capture width and height for the host OS.
func DefaultVideoSize() (int, int) {
	if runtime.GOOS == "darwin" {
		return maxDarwinVideoWidth, maxDarwinVideoHeight
	}
	return defaultLinuxVideoWidth, defaultLinuxVideoHeight
}

// clampVideoSize enforces the per-OS capture ceiling. On macOS a UVC webcam's
// uncompressed modes above 720p often do not stream over avfoundation (see
// defaultVideoSize), so a larger requested size is clamped to 1280x720 and a
// one-time notice is logged so it's obvious why the request wasn't honored. On
// other OSes the size is returned unchanged.
func ClampVideoSize(width, height int, logger logging.Logger) (int, int) {
	if runtime.GOOS != "darwin" {
		return width, height
	}
	if width <= maxDarwinVideoWidth && height <= maxDarwinVideoHeight {
		return width, height
	}
	darwinCapLogOnce.Do(func() {
		logger.Infow(
			"capping capture resolution on macOS: this device streams above 720p only as MJPEG over V4L2 (Linux); macOS avfoundation exposes raw video, which it sustains only up to 720p",
			"requested", fmt.Sprintf("%dx%d", width, height),
			"using", fmt.Sprintf("%dx%d", maxDarwinVideoWidth, maxDarwinVideoHeight),
		)
	})
	return maxDarwinVideoWidth, maxDarwinVideoHeight
}

// DefaultAudioDevice returns the conventional audio capture device for the host OS.
func DefaultAudioDevice() string {
	if runtime.GOOS == "darwin" {
		return defaultDarwinAudioDevice
	}
	return defaultLinuxAudioDevice
}

// videoInputArgs builds the ffmpeg input flags (up to and including "-i") for a
// UVC camera at the given device, frame size and rate. inputFormat is the V4L2
// pixel format requested from the device (e.g. "mjpeg"); it is
// ignored on macOS, where avfoundation negotiates format itself.
func VideoInputArgs(device string, width, height, frameRate int, inputFormat string) []string {
	size := fmt.Sprintf("%dx%d", width, height)
	fps := strconv.Itoa(frameRate)
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"-f", "avfoundation",
			"-framerate", fps,
			"-video_size", size,
			"-i", device,
		}
	default: // linux (and others, best-effort)
		args := []string{"-f", "v4l2"}
		if inputFormat != "" {
			args = append(args, "-input_format", inputFormat)
		}
		return append(args,
			"-video_size", size,
			"-framerate", fps,
			"-i", device,
		)
	}
}

// audioInputArgs builds the ffmpeg input flags (up to and including "-i") for a
// UAC microphone at the given device.
func AudioInputArgs(device string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"-f", "avfoundation", "-i", device}
	default: // linux (and others, best-effort)
		return []string{"-f", "alsa", "-i", device}
	}
}
