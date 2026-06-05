// Probe/control tool for the j5create JVCU360's vendor UVC Extension Units,
// used to reverse-engineer and exercise its display-mode control over USB on
// Linux. It goes through the uvcvideo driver (UVCIOC_CTRL_QUERY), so it needs no
// driver detach and can run while the live stream is up — unlike the pyusb
// scripts in jvcu360/.
//
//	go run ./cmd/jvcu360 -dump                         # read all candidate XU controls
//	go run ./cmd/jvcu360 -watch                        # poll GET_CUR, print on change
//	go run ./cmd/jvcu360 -set -unit 2 -selector 1 -value 0x02
//
// Workflow to find the mode control: in one shell stream the camera
// (`go run ./cmd/uvc -capture` or ffplay); in another run `-watch` and cycle the
// touch bar by hand, recording which (unit,selector) changes and its value per
// mode. Confirm with `-set`. Then encode the mapping in jvcu360/mode.go.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"camera360"
	"camera360/jvcu360/xu"
	"go.viam.com/rdk/logging"
)

func main() {
	var (
		videoDevice = flag.String("video-device", "", "V4L2 node (default: auto-detect the JVCU360)")
		dump        = flag.Bool("dump", false, "read INFO/LEN/CUR/MIN/MAX/DEF for all candidate XU controls")
		watch       = flag.Bool("watch", false, "poll GET_CUR and print on change (cycle the touch bar to map values to modes)")
		set         = flag.Bool("set", false, "issue SET_CUR (requires -unit, -selector, -value)")
		unit        = flag.Int("unit", 0, "XU unit ID (for -set, or to -watch a single control)")
		selector    = flag.Int("selector", 0, "XU control selector (for -set, or to -watch a single control)")
		value       = flag.String("value", "", "comma-separated bytes for -set, e.g. \"0x02\" or \"1,0\"")
		interval    = flag.Duration("interval", 250*time.Millisecond, "poll interval for -watch")
	)
	flag.Parse()

	logger := logging.NewLogger("jvcu360")
	if err := run(logger, *videoDevice, *dump, *watch, *set, *unit, *selector, *value, *interval); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(logger logging.Logger, videoDevice string, dump, watch, set bool, unit, selector int, value string, interval time.Duration) error {
	path, err := resolveDevice(logger, videoDevice)
	if err != nil {
		return err
	}
	dev, err := xu.Open(path)
	if err != nil {
		return err
	}
	defer dev.Close()
	logger.Infow("opened device for XU control", "device", path)

	switch {
	case dump:
		return runDump(dev)
	case watch:
		return runWatch(dev, unit, selector, interval)
	case set:
		return runSet(dev, unit, selector, value)
	default:
		flag.Usage()
		return fmt.Errorf("specify one of -dump, -watch, or -set")
	}
}

// resolveDevice returns the V4L2 node to operate on: the override if given, else
// the auto-detected JVCU360 (USB 0711:0360, or any known-360 webcam).
func resolveDevice(logger logging.Logger, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	cams, err := camera360.EnumerateUVCWebcams(context.Background(), logger)
	if err != nil {
		return "", err
	}
	for _, c := range cams {
		if c.USBID == "0711:0360" {
			return c.VideoDevice, nil
		}
	}
	for _, c := range cams {
		if c.LensHint == "known-360" {
			return c.VideoDevice, nil
		}
	}
	return "", fmt.Errorf("no JVCU360 (USB 0711:0360) found; pass -video-device")
}

func runDump(dev *xu.Device) error {
	fmt.Println("unit sel  len  info                cur / min / max / def")
	for _, c := range xu.Candidates() {
		info, err := dev.GetInfo(c.Unit, c.Selector)
		if err != nil {
			fmt.Printf("%-4d %-3d  (GET_INFO failed: %v)\n", c.Unit, c.Selector, err)
			continue
		}
		n, err := dev.GetLen(c.Unit, c.Selector)
		if err != nil {
			fmt.Printf("%-4d %-3d  -    %-19s (GET_LEN failed: %v)\n", c.Unit, c.Selector, decodeInfo(info), err)
			continue
		}
		fmt.Printf("%-4d %-3d  %-3d  %-19s cur=[%s] min=[%s] max=[%s] def=[%s]\n",
			c.Unit, c.Selector, n, decodeInfo(info),
			readReq(dev.GetCur, c, n), readReq(dev.GetMin, c, n),
			readReq(dev.GetMax, c, n), readReq(dev.GetDef, c, n))
	}
	return nil
}

func runWatch(dev *xu.Device, unit, selector int, interval time.Duration) error {
	var targets []xu.Candidate
	if unit != 0 && selector != 0 {
		targets = []xu.Candidate{{Unit: uint8(unit), Selector: uint8(selector)}}
	} else {
		// Watch every candidate that reports GET support.
		for _, c := range xu.Candidates() {
			if info, err := dev.GetInfo(c.Unit, c.Selector); err == nil && info&0x01 != 0 {
				targets = append(targets, c)
			}
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("no readable XU controls to watch")
	}

	lens := make(map[xu.Candidate]int, len(targets))
	for _, c := range targets {
		n, err := dev.GetLen(c.Unit, c.Selector)
		if err != nil || n <= 0 {
			n = 1
		}
		lens[c] = n
	}

	fmt.Printf("watching %d control(s) every %s; cycle the touch bar, Ctrl-C to stop\n", len(targets), interval)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	last := make(map[xu.Candidate]string, len(targets))
	for {
		select {
		case <-sig:
			fmt.Println("stopped")
			return nil
		case <-ticker.C:
			for _, c := range targets {
				b := make([]byte, lens[c])
				if err := dev.GetCur(c.Unit, c.Selector, b); err != nil {
					continue
				}
				v := hx(b)
				if prev, ok := last[c]; !ok || prev != v {
					last[c] = v
					fmt.Printf("%s  unit=%d sel=%d  cur=[%s]\n", time.Now().Format("15:04:05.000"), c.Unit, c.Selector, v)
				}
			}
		}
	}
}

func runSet(dev *xu.Device, unit, selector int, value string) error {
	if unit == 0 || selector == 0 || value == "" {
		return fmt.Errorf("-set requires -unit, -selector and -value")
	}
	val, err := parseBytes(value)
	if err != nil {
		return err
	}
	u, s := uint8(unit), uint8(selector)

	before := make([]byte, len(val))
	if err := dev.GetCur(u, s, before); err == nil {
		fmt.Printf("before: cur=[%s]\n", hx(before))
	}
	if err := dev.SetCur(u, s, val); err != nil {
		return fmt.Errorf("SET_CUR: %w", err)
	}
	after := make([]byte, len(val))
	if err := dev.GetCur(u, s, after); err == nil {
		fmt.Printf("after:  cur=[%s]\n", hx(after))
	}
	fmt.Printf("set unit=%d sel=%d value=[%s] ok\n", unit, selector, hx(val))
	return nil
}

// readReq runs one GET_* method into an n-byte buffer and returns it as hex, or
// a short error marker so a failing control doesn't abort the whole dump.
func readReq(fn func(uint8, uint8, []byte) error, c xu.Candidate, n int) string {
	if n <= 0 {
		return "-"
	}
	b := make([]byte, n)
	if err := fn(c.Unit, c.Selector, b); err != nil {
		return "err"
	}
	return hx(b)
}

// decodeInfo renders the UVC GET_INFO capability bitmap.
func decodeInfo(b byte) string {
	var f []string
	if b&0x01 != 0 {
		f = append(f, "GET")
	}
	if b&0x02 != 0 {
		f = append(f, "SET")
	}
	if b&0x04 != 0 {
		f = append(f, "DISABLED")
	}
	if b&0x08 != 0 {
		f = append(f, "AUTOUPDATE")
	}
	if b&0x10 != 0 {
		f = append(f, "ASYNC")
	}
	return fmt.Sprintf("%#02x[%s]", b, strings.Join(f, ","))
}

func hx(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, " ")
}

// parseBytes parses a comma-separated list of byte literals (each in any base
// strconv understands, e.g. "0x02", "2", "0b10").
func parseBytes(s string) ([]byte, error) {
	parts := strings.Split(s, ",")
	out := make([]byte, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(p), 0, 8)
		if err != nil {
			return nil, fmt.Errorf("bad byte %q: %w", p, err)
		}
		out = append(out, byte(n))
	}
	return out, nil
}
