// Standalone, dependency-light XU probe for the JVCU360 — a stripped sibling of
// cmd/jvcu360 that imports ONLY the pure-Go jvcu360 package (UVC XU ioctl), so it
// cross-compiles to a static binary with CGO_ENABLED=0 and runs on the Pi with no
// repo, no pyusb, no rdk/audio deps:
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o jvcu360-probe ./cmd/jvcu360probe
//	scp jvcu360-probe PI:/tmp/ && ssh PI /tmp/jvcu360-probe -dev /dev/video0 -dump
//
// It goes through uvcvideo (UVCIOC_CTRL_QUERY), so it needs no driver detach and
// runs while the camera streams. Modes:
//
//	-dump                                 read INFO/LEN/CUR/MIN/MAX/DEF for all candidates
//	-watch                                poll GET_CUR, print on change (cycle the touch bar)
//	-set -unit 2 -selector 1 -value 0x05  issue SET_CUR
//	-sweep -unit 2 -selector 1 -from 0 -to 31   walk SET_CUR across a value range
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"camera360/jvcu360/xu"
)

func main() {
	var (
		dev      = flag.String("dev", "/dev/video0", "V4L2 node of the JVCU360")
		dump     = flag.Bool("dump", false, "read INFO/LEN/CUR/MIN/MAX/DEF for all candidate XU controls")
		watch    = flag.Bool("watch", false, "poll GET_CUR and print on change")
		set      = flag.Bool("set", false, "issue SET_CUR (needs -unit -selector -value)")
		sweep    = flag.Bool("sweep", false, "walk SET_CUR across [-from,-to] on one control (needs -unit -selector)")
		unit     = flag.Int("unit", 0, "XU unit ID")
		selector = flag.Int("selector", 0, "XU control selector")
		value    = flag.String("value", "", "comma-separated bytes for -set, e.g. \"0x05\" or \"1,0\"")
		from     = flag.Int("from", 0, "sweep start value (inclusive)")
		to       = flag.Int("to", 31, "sweep end value (inclusive)")
		hold     = flag.Duration("hold", 1500*time.Millisecond, "sweep: pause after each SET_CUR so you can watch the stream")
		interval = flag.Duration("interval", 250*time.Millisecond, "watch poll interval")
	)
	flag.Parse()

	d, err := xu.Open(*dev)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer d.Close()
	fmt.Printf("opened %s for XU control\n", *dev)

	switch {
	case dump != nil && *dump:
		err = runDump(d)
	case *watch:
		err = runWatch(d, *unit, *selector, *interval)
	case *set:
		err = runSet(d, *unit, *selector, *value)
	case *sweep:
		err = runSweep(d, *unit, *selector, *from, *to, *hold)
	default:
		flag.Usage()
		err = fmt.Errorf("specify one of -dump, -watch, -set, -sweep")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runDump(d *xu.Device) error {
	fmt.Println("unit sel  len  info                cur / min / max / def")
	for _, c := range xu.Candidates() {
		info, err := d.GetInfo(c.Unit, c.Selector)
		if err != nil {
			fmt.Printf("%-4d %-3d  (GET_INFO failed: %v)\n", c.Unit, c.Selector, err)
			continue
		}
		n, err := d.GetLen(c.Unit, c.Selector)
		if err != nil {
			fmt.Printf("%-4d %-3d  -    %-19s (GET_LEN failed: %v)\n", c.Unit, c.Selector, decodeInfo(info), err)
			continue
		}
		fmt.Printf("%-4d %-3d  %-3d  %-19s cur=[%s] min=[%s] max=[%s] def=[%s]\n",
			c.Unit, c.Selector, n, decodeInfo(info),
			readReq(d.GetCur, c, n), readReq(d.GetMin, c, n),
			readReq(d.GetMax, c, n), readReq(d.GetDef, c, n))
	}
	return nil
}

func runWatch(d *xu.Device, unit, selector int, interval time.Duration) error {
	var targets []xu.Candidate
	if unit != 0 && selector != 0 {
		targets = []xu.Candidate{{Unit: uint8(unit), Selector: uint8(selector)}}
	} else {
		for _, c := range xu.Candidates() {
			if info, err := d.GetInfo(c.Unit, c.Selector); err == nil && info&0x01 != 0 {
				targets = append(targets, c)
			}
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("no readable XU controls to watch")
	}
	lens := make(map[xu.Candidate]int, len(targets))
	for _, c := range targets {
		n, err := d.GetLen(c.Unit, c.Selector)
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
				if err := d.GetCur(c.Unit, c.Selector, b); err != nil {
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

func runSet(d *xu.Device, unit, selector int, value string) error {
	if unit == 0 || selector == 0 || value == "" {
		return fmt.Errorf("-set requires -unit, -selector and -value")
	}
	val, err := parseBytes(value)
	if err != nil {
		return err
	}
	u, s := uint8(unit), uint8(selector)
	before := make([]byte, len(val))
	if err := d.GetCur(u, s, before); err == nil {
		fmt.Printf("before: cur=[%s]\n", hx(before))
	}
	if err := d.SetCur(u, s, val); err != nil {
		return fmt.Errorf("SET_CUR: %w", err)
	}
	after := make([]byte, len(val))
	if err := d.GetCur(u, s, after); err == nil {
		fmt.Printf("after:  cur=[%s]\n", hx(after))
	}
	fmt.Printf("set unit=%d sel=%d value=[%s] ok\n", unit, selector, hx(val))
	return nil
}

// runSweep walks SET_CUR over [from,to] on one control, restoring the original
// value at the end. Watch the live stream (ffplay/v4l2) during the hold window:
// a value that flips the geometry to a square/circular frame is the raw-fisheye
// tell prong 2 is hunting for. The control's byte length is read via GetLen and
// the swept value is written little-endian into that buffer.
func runSweep(d *xu.Device, unit, selector, from, to int, hold time.Duration) error {
	if unit == 0 || selector == 0 {
		return fmt.Errorf("-sweep requires -unit and -selector")
	}
	u, s := uint8(unit), uint8(selector)
	n, err := d.GetLen(u, s)
	if err != nil || n <= 0 {
		return fmt.Errorf("GET_LEN unit=%d sel=%d: %v", unit, selector, err)
	}
	orig := make([]byte, n)
	haveOrig := d.GetCur(u, s, orig) == nil
	if haveOrig {
		fmt.Printf("original cur=[%s] (will restore)\n", hx(orig))
	}
	fmt.Printf("sweeping unit=%d sel=%d len=%d from %d..%d, %s each — WATCH THE STREAM\n", unit, selector, n, from, to, hold)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	for v := from; v <= to; v++ {
		buf := make([]byte, n)
		for i := 0; i < n && i < 8; i++ {
			buf[i] = byte(uint(v) >> (8 * i))
		}
		err := d.SetCur(u, s, buf)
		got := make([]byte, n)
		readErr := d.GetCur(u, s, got)
		stat := "ok"
		if err != nil {
			stat = "SET err: " + err.Error()
		} else if readErr == nil && hx(got) != hx(buf) {
			stat = "clamped/readback=[" + hx(got) + "]"
		}
		fmt.Printf("%s  value=%-3d set=[%s]  %s\n", time.Now().Format("15:04:05.000"), v, hx(buf), stat)
		select {
		case <-sig:
			fmt.Println("interrupted")
			v = to // fall through to restore
		case <-time.After(hold):
		}
	}
	if haveOrig {
		if err := d.SetCur(u, s, orig); err != nil {
			fmt.Printf("WARNING: failed to restore original [%s]: %v\n", hx(orig), err)
		} else {
			fmt.Printf("restored cur=[%s]\n", hx(orig))
		}
	}
	return nil
}

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

func decodeInfo(b byte) string {
	var f []string
	for _, bit := range []struct {
		m byte
		n string
	}{{0x01, "GET"}, {0x02, "SET"}, {0x04, "DISABLED"}, {0x08, "AUTOUPDATE"}, {0x10, "ASYNC"}} {
		if b&bit.m != 0 {
			f = append(f, bit.n)
		}
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
