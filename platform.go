package camera360

import (
	"fmt"
	"runtime"
	"strconv"
)

// Platform-specific ffmpeg input construction for USB cameras and microphones.
//
// On Linux the JVCU360 enumerates as a V4L2 video node (/dev/videoN) plus an
// ALSA capture device; on macOS both the camera and mic are reached through
// avfoundation. We shell out to ffmpeg in both cases (see capture.go and
// audiocapture.go), so the only platform difference is the set of input flags
// assembled here. Windows (dshow) is intentionally not handled in this phase.

const (
	defaultLinuxVideoDevice  = "/dev/video0"
	defaultDarwinVideoDevice = "0"       // avfoundation video device index
	defaultLinuxAudioDevice  = "default" // ALSA; usually wants overriding with the webcam's card
	defaultDarwinAudioDevice = ":0"      // avfoundation audio-only index (":N" = audio, "N" = video)
)

// defaultVideoDevice returns the conventional video device for the host OS.
func defaultVideoDevice() string {
	if runtime.GOOS == "darwin" {
		return defaultDarwinVideoDevice
	}
	return defaultLinuxVideoDevice
}

// defaultAudioDevice returns the conventional audio capture device for the host OS.
func defaultAudioDevice() string {
	if runtime.GOOS == "darwin" {
		return defaultDarwinAudioDevice
	}
	return defaultLinuxAudioDevice
}

// videoInputArgs builds the ffmpeg input flags (up to and including "-i") for a
// UVC camera at the given device, frame size and rate. inputFormat is the V4L2
// pixel format requested from the device ("mjpeg" for the JVCU360); it is
// ignored on macOS, where avfoundation negotiates format itself.
func videoInputArgs(device string, width, height, frameRate int, inputFormat string) []string {
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
func audioInputArgs(device string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"-f", "avfoundation", "-i", device}
	default: // linux (and others, best-effort)
		return []string{"-f", "alsa", "-i", device}
	}
}
