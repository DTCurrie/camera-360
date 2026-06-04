package camera360

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.viam.com/rdk/logging"
)

// Capture spawns an ffmpeg subprocess to consume the camera's RTSP stream and
// expose the latest decoded frame via Latest(). Frame decoding happens on a
// background goroutine; readers always get the most recently produced frame
// (no queue, no backpressure — matches the rdk ffmpeg.go pattern).
type Capture struct {
	inputArgs []string
	label     string
	logger    logging.Logger

	cancel context.CancelFunc
	wg     sync.WaitGroup

	latest        atomic.Pointer[image.Image]
	gotFirstOnce  sync.Once
	gotFirstFrame chan struct{}

	cmdMu sync.Mutex
	cmd   *exec.Cmd

	// diag holds the most recent failure detail (ffmpeg's last stderr line and
	// the last run/exit error) so a stalled first-frame wait can report *why*
	// ffmpeg produced no frames, instead of a bare "context deadline exceeded".
	diagMu     sync.Mutex
	lastStderr string
	lastRunErr error
}

// NewCapture verifies ffmpeg is present, then spawns it with caller-supplied
// input arguments (the flags up to and including "-i <source>") and decodes the
// resulting MJPEG frames. This is the basic path, used by the USB/UVC models:
// they pass v4l2 (Linux) or avfoundation (macOS) input args. It returns once the
// subprocess has started; callers should poll Latest() (or use WaitFirstFrame)
// before assuming a frame is available. label is a human-readable source
// identifier used only in log lines.
func NewCapture(ctx context.Context, inputArgs []string, label string, logger logging.Logger) (*Capture, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found on PATH: %w", err)
	}
	innerCtx, cancel := context.WithCancel(context.Background())
	c := &Capture{
		inputArgs:     inputArgs,
		label:         label,
		logger:        logger,
		cancel:        cancel,
		gotFirstFrame: make(chan struct{}),
	}
	c.wg.Add(1)
	go c.runLoop(innerCtx)
	return c, nil
}

// NewCaptureFromRTSP is the alternate path for network/RTSP 360 cameras: it
// builds the RTSP-over-TCP input args and delegates to NewCapture.
func NewCaptureFromRTSP(ctx context.Context, rtspURL string, logger logging.Logger) (*Capture, error) {
	return NewCapture(ctx, []string{"-rtsp_transport", "tcp", "-i", rtspURL}, rtspURL, logger)
}

// runLoop spawns ffmpeg, decodes the JPEG stream from its stdout, and
// restarts it if it exits while the context is still live.
func (c *Capture) runLoop(ctx context.Context) {
	defer c.wg.Done()

	const (
		initialBackoff = 250 * time.Millisecond
		maxBackoff     = 3 * time.Second
	)
	backoff := initialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		frames, err := c.runOnce(ctx)
		if frames > 0 {
			// The run produced frames, so it was healthy; reset the backoff
			// so a later transient restart recovers quickly.
			backoff = initialBackoff
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.recordRunErr(err)
			c.logger.Warnw("ffmpeg session ended; will retry", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				// Grow the backoff so a persistently misconfigured device
				// (wrong node, unsupported format) doesn't hot-loop and bury
				// the real error in retry spam.
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
		}
	}
}

// runOnce runs one ffmpeg session to completion and returns how many frames it
// decoded along with the error that ended it (nil only on a clean EOF/exit).
func (c *Capture) runOnce(ctx context.Context) (int, error) {
	args := append([]string{"-hide_banner", "-loglevel", "warning"}, c.inputArgs...)
	args = append(args,
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-q:v", "4",
		"pipe:1",
	)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, err
	}

	c.cmdMu.Lock()
	c.cmd = cmd
	c.cmdMu.Unlock()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start ffmpeg: %w", err)
	}
	c.logger.Infow("ffmpeg started", "source", c.label, "args", args)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := stderr.Read(buf)
			if n > 0 {
				out := string(buf[:n])
				c.logger.Warnw("ffmpeg stderr", "out", out)
				c.recordStderr(out)
			}
			if rerr != nil {
				return
			}
		}
	}()

	frames, decodeErr := c.decodeStream(stdout)
	// If decode stopped for any reason other than ffmpeg having shut its
	// stdout (EOF), ffmpeg is still alive and will block on its next stdout
	// write now that nobody is reading. Kill it so Wait can return and the
	// outer loop can retry; otherwise the whole capture wedges on frame 1.
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return frames, decodeErr
	}
	return frames, waitErr
}

func (c *Capture) decodeStream(r io.Reader) (int, error) {
	// jpeg.Decode wraps a non-ByteReader argument in a fresh bufio.Reader on
	// every call and discards whatever it had read ahead when it returns. For
	// an MJPEG-over-image2pipe stream the lookahead always crosses into the
	// next JPEG, so a new bufio per call loses the start of every subsequent
	// frame and Decode #2 fails to find an SOI marker. Holding one bufio for
	// the lifetime of the stream preserves the carry-over between frames.
	br := bufio.NewReader(r)
	frames := 0
	for {
		// Resync to the next FF D8. Even with a stable bufio across calls,
		// jpeg.Decode can leave the reader positioned past EOI when an
		// MJPEG-over-image2pipe encoder writes padding between frames — the
		// next Decode then starts inside scan data and errors with
		// "missing SOI marker". Discarding up to the next SOI is cheap and
		// keeps us aligned regardless of what ffmpeg emits.
		if err := scanToSOI(br); err != nil {
			return frames, err
		}
		img, err := jpeg.Decode(br)
		if err != nil {
			peek, _ := br.Peek(32)
			c.logger.Warnw("jpeg decode failed",
				"err", err, "frames_decoded", frames, "peek_hex", fmt.Sprintf("%x", peek))
			return frames, err
		}
		frames++
		c.latest.Store(&img)
		c.gotFirstOnce.Do(func() { close(c.gotFirstFrame) })
	}
}

// scanToSOI advances br until the next two buffered bytes are the JPEG SOI
// marker (FF D8), then returns with those bytes still unread so jpeg.Decode
// will see them. We use Peek+Discard rather than ReadByte so we never have
// to un-read more than zero bytes (bufio.Reader can only UnreadByte the
// single most recently read byte).
func scanToSOI(br *bufio.Reader) error {
	for {
		head, err := br.Peek(2)
		if err != nil {
			return err
		}
		if head[0] == 0xFF && head[1] == 0xD8 {
			return nil
		}
		if _, err := br.Discard(1); err != nil {
			return err
		}
	}
}

// Latest returns the most recently decoded frame, or an error if no frame has
// been produced yet.
func (c *Capture) Latest() (image.Image, error) {
	p := c.latest.Load()
	if p == nil {
		return nil, errors.New("no frame available yet")
	}
	return *p, nil
}

// WaitFirstFrame blocks until the first frame has been decoded or ctx expires.
// On timeout it folds in the most recent ffmpeg failure (see recordStderr /
// recordRunErr) so the caller sees *why* no frame arrived rather than a bare
// "context deadline exceeded".
func (c *Capture) WaitFirstFrame(ctx context.Context) error {
	select {
	case <-c.gotFirstFrame:
		return nil
	case <-ctx.Done():
		if diag := c.lastDiagnostic(); diag != "" {
			return fmt.Errorf("%w: no frame from %q yet — last ffmpeg failure: %s", ctx.Err(), c.label, diag)
		}
		return fmt.Errorf("%w: no frame from %q yet and ffmpeg reported no error "+
			"(is it a video-capture device that supports the configured format/size/frame-rate?)", ctx.Err(), c.label)
	}
}

// recordStderr remembers ffmpeg's most recent (non-blank) stderr output. The
// fatal error ffmpeg prints before exiting is the line that matters, and it is
// the last thing written, so keeping the latest chunk captures it.
func (c *Capture) recordStderr(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	c.diagMu.Lock()
	c.lastStderr = s
	c.diagMu.Unlock()
}

// recordRunErr remembers the error that ended the most recent ffmpeg session.
func (c *Capture) recordRunErr(err error) {
	c.diagMu.Lock()
	c.lastRunErr = err
	c.diagMu.Unlock()
}

// lastDiagnostic returns a human-readable summary of the most recent ffmpeg
// failure (its exit/decode error and/or its last stderr line), or "" if none.
func (c *Capture) lastDiagnostic() string {
	c.diagMu.Lock()
	defer c.diagMu.Unlock()
	switch {
	case c.lastRunErr != nil && c.lastStderr != "":
		return fmt.Sprintf("%v (%s)", c.lastRunErr, c.lastStderr)
	case c.lastRunErr != nil:
		return c.lastRunErr.Error()
	default:
		return c.lastStderr
	}
}

// Close terminates the ffmpeg subprocess and waits for the worker to exit.
func (c *Capture) Close() error {
	c.cancel()
	c.cmdMu.Lock()
	cmd := c.cmd
	c.cmdMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	c.wg.Wait()
	return nil
}
