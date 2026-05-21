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
	"sync"
	"sync/atomic"

	"go.viam.com/rdk/logging"
)

// Capture spawns an ffmpeg subprocess to consume the camera's RTSP stream and
// expose the latest decoded frame via Latest(). Frame decoding happens on a
// background goroutine; readers always get the most recently produced frame
// (no queue, no backpressure — matches the rdk ffmpeg.go pattern).
type Capture struct {
	rtspURL string
	logger  logging.Logger

	cancel context.CancelFunc
	wg     sync.WaitGroup

	latest        atomic.Pointer[image.Image]
	gotFirstOnce  sync.Once
	gotFirstFrame chan struct{}

	cmdMu sync.Mutex
	cmd   *exec.Cmd
}

// NewCapture verifies ffmpeg is present, then spawns it pulling rtspURL. It
// returns once the subprocess has started; callers should poll Latest() (or
// use WaitFirstFrame) before assuming a frame is available.
func NewCapture(ctx context.Context, rtspURL string, logger logging.Logger) (*Capture, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found on PATH: %w", err)
	}
	innerCtx, cancel := context.WithCancel(context.Background())
	c := &Capture{
		rtspURL:       rtspURL,
		logger:        logger,
		cancel:        cancel,
		gotFirstFrame: make(chan struct{}),
	}
	c.wg.Add(1)
	go c.runLoop(innerCtx)
	return c, nil
}

// runLoop spawns ffmpeg, decodes the JPEG stream from its stdout, and
// restarts it if it exits while the context is still live.
func (c *Capture) runLoop(ctx context.Context) {
	defer c.wg.Done()

	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.runOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Warnw("ffmpeg session ended; will retry", "err", err)
			select {
			case <-ctx.Done():
				return
			}
		}
	}
}

func (c *Capture) runOnce(ctx context.Context) error {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-rtsp_transport", "tcp",
		"-i", c.rtspURL,
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-q:v", "4",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	c.cmdMu.Lock()
	c.cmd = cmd
	c.cmdMu.Unlock()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	c.logger.Infow("ffmpeg started", "args", args)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := stderr.Read(buf)
			if n > 0 {
				c.logger.Debugw("ffmpeg stderr", "out", string(buf[:n]))
			}
			if rerr != nil {
				return
			}
		}
	}()

	decodeErr := c.decodeStream(stdout)
	// If decode stopped for any reason other than ffmpeg having shut its
	// stdout (EOF), ffmpeg is still alive and will block on its next stdout
	// write now that nobody is reading. Kill it so Wait can return and the
	// outer loop can retry; otherwise the whole capture wedges on frame 1.
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return decodeErr
	}
	return waitErr
}

func (c *Capture) decodeStream(r io.Reader) error {
	// jpeg.Decode wraps a non-ByteReader argument in a fresh bufio.Reader on
	// every call and discards whatever it had read ahead when it returns. For
	// an MJPEG-over-image2pipe stream the lookahead always crosses into the
	// next JPEG, so a new bufio per call loses the start of every subsequent
	// frame and Decode #2 fails to find an SOI marker. Holding one bufio for
	// the lifetime of the stream preserves the carry-over between frames.
	br := bufio.NewReader(r)
	for {
		img, err := jpeg.Decode(br)
		if err != nil {
			return err
		}
		c.latest.Store(&img)
		c.gotFirstOnce.Do(func() { close(c.gotFirstFrame) })
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
func (c *Capture) WaitFirstFrame(ctx context.Context) error {
	select {
	case <-c.gotFirstFrame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
