// Discovery + capture tool for USB (UVC/UAC) webcams. It does three things,
// selected by flags:
//
//	go run ./cmd/uvc -list                          # enumerate capture devices
//	go run ./cmd/uvc -capture -frames 30 -out out   # grab JPEG frames to disk
//	go run ./cmd/uvc -audio -seconds 3 -out out     # grab a WAV clip to disk
//
// The point of -capture is to see, over the wire, exactly what the device
// outputs (resolution + pixel layout) before we build any dewarping on top. For
// a multi-mode 360 camera like the j5create JVCU360, switch the mode on the
// device between runs to capture each one (see jvcu360/README.md). Prerequisite:
// ffmpeg on PATH, and on Linux a V4L2 node (/dev/videoN); pass -video-device /
// -audio-device to target a specific device (see -list output).
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg" // register the JPEG decoder for image.DecodeConfig
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"camera360"
	"camera360/jvcu360"
	"go.viam.com/rdk/components/audioin"
	camera "go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
)

func main() {
	var (
		list        = flag.Bool("list", false, "enumerate capture devices and exit")
		doCapture   = flag.Bool("capture", false, "capture video frames to -out")
		doAudio     = flag.Bool("audio", false, "capture an audio clip to -out")
		frames      = flag.Int("frames", 10, "number of video frames to capture")
		seconds     = flag.Float64("seconds", 3, "seconds of audio to capture")
		outDir      = flag.String("out", "out", "output directory")
		videoDevice = flag.String("video-device", "", "video device (default per-OS)")
		audioDevice = flag.String("audio-device", "", "audio device (default per-OS)")
		width       = flag.Int("width", 0, "capture width (0 = per-OS default)")
		height      = flag.Int("height", 0, "capture height (0 = per-OS default)")
		fps         = flag.Int("fps", 30, "capture frame rate")
	)
	flag.Parse()

	logger := logging.NewLogger("uvc")
	var err error
	switch {
	case *list:
		err = listDevices(logger)
	case *doAudio:
		err = captureAudio(logger, *outDir, *audioDevice, *seconds)
	case *doCapture:
		err = captureFrames(logger, *outDir, *videoDevice, *frames, *width, *height, *fps)
	default:
		fmt.Fprintln(os.Stderr, "specify one of -list, -capture, or -audio")
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// listDevices prints the host OS's raw capture-device enumeration, then the
// module's own structured view: which of those are confirmed UVC webcams, the
// /dev/videoN and ALSA handles to configure, and any 360/fisheye classification.
// The structured pass is the same code the discovery service uses (Linux-only).
//
// ffmpeg's avfoundation lister and v4l2-ctl both exit non-zero and/or write to
// stderr, so we capture combined output and print it regardless of exit status.
func listDevices(logger logging.Logger) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "ffmpeg", []string{"-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", ""}
	default: // linux
		if _, err := exec.LookPath("v4l2-ctl"); err == nil {
			name, args = "v4l2-ctl", []string{"--list-devices"}
		} else {
			name, args = "sh", []string{"-c", "ls -l /dev/video* 2>/dev/null || echo 'no /dev/video* devices found'"}
		}
	}
	out, _ := exec.Command(name, args...).CombinedOutput()
	fmt.Print(string(out))

	webcams, err := camera360.EnumerateUVCWebcams(context.Background(), logger)
	if err != nil {
		return err
	}
	if len(webcams) == 0 {
		fmt.Println("\nNo UVC webcams identified (sysfs detection is Linux-only).")
		return nil
	}
	fmt.Println("\nIdentified UVC webcams:")
	for _, w := range webcams {
		lens := w.LensHint
		if lens == "" {
			lens = "uvc"
		}
		audio := w.AudioDevice
		if audio == "" {
			audio = "(none)"
		}
		fmt.Printf("  %-24s video=%s  audio=%s  usb=%s  lens=%s\n", w.Label, w.VideoDevice, audio, w.USBID, lens)
	}
	return nil
}

func captureFrames(logger logging.Logger, outDir, device string, frames, width, height, fps int) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	ctx := context.Background()
	cfg := &jvcu360.CameraConfig{VideoDevice: device, Width: width, Height: height, FrameRate: fps}
	cam, err := jvcu360.NewCamera(ctx, camera.Named("uvc-cli"), cfg, logger)
	if err != nil {
		return fmt.Errorf("open camera: %w", err)
	}
	defer cam.Close(context.Background())

	logger.Info("waiting for first frame…")
	for i := 0; i < frames; i++ {
		images, _, err := cam.Images(ctx, nil, nil)
		if err != nil {
			return fmt.Errorf("get images: %w", err)
		}
		for _, ni := range images {
			b, err := ni.Bytes(ctx)
			if err != nil {
				return fmt.Errorf("encode %s: %w", ni.SourceName, err)
			}
			path := filepath.Join(outDir, fmt.Sprintf("frame_%03d.jpg", i))
			if err := os.WriteFile(path, b, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			w, h := "?", "?"
			if cfg, _, derr := image.DecodeConfig(bytes.NewReader(b)); derr == nil {
				w, h = fmt.Sprint(cfg.Width), fmt.Sprint(cfg.Height)
			}
			logger.Infow("wrote frame", "path", path, "width", w, "height", h, "bytes", len(b))
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func captureAudio(logger logging.Logger, outDir, device string, seconds float64) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mic, err := jvcu360.NewMic(ctx, audioin.Named("uvc-cli-mic"), &jvcu360.MicConfig{AudioDevice: device}, logger)
	if err != nil {
		return fmt.Errorf("open mic: %w", err)
	}
	defer mic.Close(context.Background())

	props, err := mic.Properties(ctx, nil)
	if err != nil {
		return fmt.Errorf("mic properties: %w", err)
	}
	logger.Infow("recording", "seconds", seconds, "sample_rate", props.SampleRateHz, "channels", props.NumChannels)
	ch, err := mic.GetAudio(ctx, "pcm16", float32(seconds), 0, nil)
	if err != nil {
		return fmt.Errorf("get audio: %w", err)
	}
	var pcm []byte
	for chunk := range ch {
		pcm = append(pcm, chunk.AudioData...)
	}
	path := filepath.Join(outDir, "mic.wav")
	if err := writeWAV(path, pcm, int(props.SampleRateHz), int(props.NumChannels)); err != nil {
		return fmt.Errorf("write wav: %w", err)
	}
	logger.Infow("wrote audio", "path", path, "pcm_bytes", len(pcm))
	return nil
}

// writeWAV wraps raw s16le PCM in a minimal 44-byte WAV header so the clip is
// directly playable for a quick "does the mic work" check.
func writeWAV(path string, pcm []byte, sampleRate, numChannels int) error {
	const bitsPerSample = 16
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))            // fmt chunk size
	binary.Write(&buf, binary.LittleEndian, uint16(1))             // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(numChannels))   //nolint:gosec
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))    //nolint:gosec
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))      //nolint:gosec
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))    //nolint:gosec
	binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample)) // bits per sample
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
