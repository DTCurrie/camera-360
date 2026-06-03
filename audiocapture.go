package camera360

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"

	"go.viam.com/rdk/logging"
)

// AudioCapture spawns an ffmpeg subprocess that decodes a UAC microphone to
// signed 16-bit little-endian PCM on stdout. Unlike the video Capture it runs
// no restart loop: each GetAudio call owns one short-lived AudioCapture whose
// lifetime is bounded by the request context, so a transient device error ends
// that stream cleanly rather than silently respawning mid-recording.
type AudioCapture struct {
	stdout io.ReadCloser
	logger logging.Logger
	cmd    *exec.Cmd
}

// NewAudioCapture starts ffmpeg reading from inputArgs (the flags up to and
// including "-i <device>") and emitting interleaved s16le PCM at sampleRate /
// numChannels. The subprocess is killed when ctx is cancelled or Close is called.
func NewAudioCapture(ctx context.Context, inputArgs []string, sampleRate, numChannels int, logger logging.Logger) (*AudioCapture, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found on PATH: %w", err)
	}
	args := append([]string{"-hide_banner", "-loglevel", "warning"}, inputArgs...)
	args = append(args,
		"-ac", strconv.Itoa(numChannels),
		"-ar", strconv.Itoa(sampleRate),
		"-f", "s16le",
		"pipe:1",
	)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	logger.Infow("ffmpeg audio capture started", "args", args)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := stderr.Read(buf)
			if n > 0 {
				logger.Warnw("ffmpeg stderr", "out", string(buf[:n]))
			}
			if rerr != nil {
				return
			}
		}
	}()

	return &AudioCapture{stdout: stdout, logger: logger, cmd: cmd}, nil
}

// Read fills p with PCM bytes from ffmpeg's stdout. It is a thin pass-through to
// the subprocess pipe; callers typically wrap it with io.ReadFull to assemble
// fixed-size chunks.
func (a *AudioCapture) Read(p []byte) (int, error) {
	return a.stdout.Read(p)
}

// Close kills the ffmpeg subprocess and reaps it.
func (a *AudioCapture) Close() error {
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
		_ = a.cmd.Wait()
	}
	return nil
}
