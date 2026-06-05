package camera360

import (
	"bytes"
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

// Capture spawns an ffmpeg subprocess to consume a camera's stream and expose
// the latest decoded frame via Latest(). The pipeline is split into two stages
// so decode cost can never make the live frame stale:
//
//   - A reader goroutine (per ffmpeg session) drains ffmpeg's stdout, splits the
//     MJPEG byte stream into individual JPEG frames, and stores only the most
//     recent frame's *raw bytes*. Splitting is cheap (a marker scan + a copy),
//     so the OS pipe stays drained and the device-side v4l2/ffmpeg buffers stay
//     shallow — i.e. the newest raw frame is genuinely current.
//   - A single long-lived decoder goroutine wakes on each new raw frame, always
//     grabs the *latest* stored raw frame (not a queue), and decodes it. When
//     decode is slower than the capture rate it simply skips the frames that
//     arrived while it was busy, so the decoded frame trails real time by at
//     most one decode, never by an ever-growing backlog.
//
// This replaces an earlier design that decoded every frame in-order off the
// pipe: on devices where pure-Go jpeg.Decode can't sustain the capture frame
// rate (e.g. 1080p30 on ARM), that backed frames up in the pipe and Latest()
// returned images seconds behind reality.
type Capture struct {
	inputArgs []string
	label     string
	logger    logging.Logger

	cancel context.CancelFunc
	wg     sync.WaitGroup

	latest        atomic.Pointer[image.Image]
	gotFirstOnce  sync.Once
	gotFirstFrame chan struct{}

	// latestRaw holds the most recent undecoded JPEG frame; rawReady (buffered
	// to 1, coalescing) nudges the decoder that a newer one is available.
	latestRaw atomic.Pointer[[]byte]
	rawReady  chan struct{}

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
		rawReady:      make(chan struct{}, 1),
	}
	// The decoder is long-lived (it spans ffmpeg restarts); the reader is
	// (re)spawned per ffmpeg session inside runLoop.
	c.wg.Add(2)
	go c.decodeLoop(innerCtx)
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
	// nobuffer/low_delay keep ffmpeg from holding frames on the input side;
	// combined with the reader draining stdout promptly, this keeps end-to-end
	// latency to roughly one frame plus one decode.
	args := append([]string{"-hide_banner", "-loglevel", "warning", "-fflags", "nobuffer", "-flags", "low_delay"}, c.inputArgs...)
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

	frames, readErr := c.readFrames(stdout)
	// If the read stopped for any reason other than ffmpeg having shut its
	// stdout (EOF), ffmpeg is still alive and will block on its next stdout
	// write now that nobody is reading. Kill it so Wait can return and the
	// outer loop can retry; otherwise the whole capture wedges on frame 1.
	if readErr != nil && !errors.Is(readErr, io.EOF) && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return frames, readErr
	}
	return frames, waitErr
}

const (
	// readChunk is how much we pull from ffmpeg's stdout per Read. A few JPEG
	// frames' worth keeps the syscall count low without hoarding memory.
	readChunk = 64 * 1024
	// maxFrameBytes caps the in-progress frame buffer. A healthy MJPEG stream
	// has frequent SOI markers; if we somehow accumulate this much without
	// finding the next one the stream is corrupt, so we resync rather than grow
	// without bound.
	maxFrameBytes = 16 * 1024 * 1024
)

// readFrames drains r (ffmpeg's stdout), splits the MJPEG stream into individual
// JPEG frames on SOI (FF D8) boundaries, and hands each completed frame to
// storeRaw. It does no decoding, so it keeps up with the full capture rate and
// keeps the OS pipe drained. It returns the number of frames seen and the error
// that ended the stream. A frame is "complete" once the *next* frame's SOI is
// seen, which is what lets us delimit frames without parsing JPEG structure.
func (c *Capture) readFrames(r io.Reader) (int, error) {
	acc := make([]byte, 0, readChunk*2)
	buf := make([]byte, readChunk)
	frames := 0
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			acc = append(acc, buf[:n]...)
			for {
				start := indexSOI(acc, 0)
				if start < 0 {
					// No frame start yet; drop junk but keep a trailing 0xFF
					// that might be the first half of an SOI split across reads.
					acc = keepTrailingByte(acc)
					break
				}
				next := indexSOI(acc, start+2)
				if next < 0 {
					// Incomplete trailing frame; keep it (from its SOI) to
					// finish on the next read, unless it's grown implausibly
					// large, in which case resync.
					if start > 0 {
						acc = append(acc[:0], acc[start:]...)
					}
					if len(acc) > maxFrameBytes {
						acc = acc[:0]
					}
					break
				}
				c.storeRaw(acc[start:next])
				frames++
				acc = append(acc[:0], acc[next:]...)
			}
		}
		if rerr != nil {
			// The stream ended. A complete trailing frame has no following SOI
			// to delimit it, so flush it best-effort: a clean EOF leaves a whole
			// final JPEG, and a mid-write kill leaves a truncated one the decoder
			// will simply reject. Either way we don't silently drop the last
			// frame of a session.
			if start := indexSOI(acc, 0); start >= 0 {
				c.storeRaw(acc[start:])
				frames++
			}
			return frames, rerr
		}
	}
}

// storeRaw copies one JPEG frame out of the read accumulator (which gets
// overwritten) and publishes it as the latest raw frame, nudging the decoder.
func (c *Capture) storeRaw(frame []byte) {
	f := make([]byte, len(frame))
	copy(f, frame)
	c.latestRaw.Store(&f)
	// Coalescing notify: if a signal is already pending the decoder will pick
	// up whatever the latest frame is when it wakes, so dropping this one is
	// exactly the stale-frame skipping we want.
	select {
	case c.rawReady <- struct{}{}:
	default:
	}
}

// decodeLoop is the single long-lived decoder. On each wake it grabs the
// *latest* raw frame (skipping any that piled up while it was decoding) and
// decodes it, so the published image trails real time by at most one decode.
func (c *Capture) decodeLoop(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.rawReady:
		}
		p := c.latestRaw.Load()
		if p == nil {
			continue
		}
		img, err := jpeg.Decode(bytes.NewReader(*p))
		if err != nil {
			head := *p
			if len(head) > 32 {
				head = head[:32]
			}
			c.logger.Warnw("jpeg decode failed",
				"err", err, "frame_bytes", len(*p), "head_hex", fmt.Sprintf("%x", head))
			continue
		}
		c.latest.Store(&img)
		c.gotFirstOnce.Do(func() { close(c.gotFirstFrame) })
	}
}

// indexSOI returns the index of the next JPEG SOI marker (FF D8) in b at or
// after from, or -1 if none (a trailing lone 0xFF counts as "not found" since
// its companion byte hasn't arrived yet).
func indexSOI(b []byte, from int) int {
	if from < 0 {
		from = 0
	}
	for i := from; i < len(b); {
		j := bytes.IndexByte(b[i:], 0xFF)
		if j < 0 {
			return -1
		}
		i += j
		if i+1 >= len(b) {
			return -1 // 0xFF at the very end; can't confirm the 0xD8 yet
		}
		if b[i+1] == 0xD8 {
			return i
		}
		i++
	}
	return -1
}

// keepTrailingByte discards b but preserves a trailing 0xFF (the possible first
// byte of an SOI marker straddling two reads), reusing b's backing array.
func keepTrailingByte(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == 0xFF {
		b[0] = 0xFF
		return b[:1]
	}
	return b[:0]
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

// LatestRaw returns the most recently captured frame's undecoded JPEG bytes, or
// an error if no frame has been produced yet. The slice is owned by the Capture
// and must not be mutated (storeRaw publishes a fresh copy per frame, so the
// returned bytes stay valid). Useful when a caller wants the device's original
// JPEG without a decode/re-encode round-trip.
func (c *Capture) LatestRaw() ([]byte, error) {
	p := c.latestRaw.Load()
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
