package xu

// JVCU360 vendor Extension Units on the VideoControl interface (interface 0).
// See jvcu360/README.md. The GUIDs and selector ranges are known; which control
// selects the display mode — and what value maps to which mode — is not, and is
// what the cmd/jvcu360 probe is for.
const (
	// xuUnitA: GUID {8da31e37-c7c1-4af2-b2a5-e4aab18675f0}, control selectors 1–10.
	xuUnitA = 2
	// xuUnitB: GUID {8da31e37-c7c1-4af2-b2a5-e4aab18674f0}, control selectors 9–12.
	xuUnitB = 3
)

// Mode is one of the JVCU360's six firmware display modes, in touch-bar order.
type Mode int

const (
	Mode360AllAround Mode = iota
	ModeFullScreen
	ModeHost
	ModeDualHost
	ModeSingleView
	ModeWideAngle
)

// modeNames are the position labels in Mode order. Index by int(Mode); also the
// switch component's GetNumberOfPositions labels.
var modeNames = []string{
	"360 All-Around",
	"Full Screen",
	"Host",
	"Dual Host",
	"Single View",
	"Wide Angle",
}

// modeSize is each mode's documented frame size (width, height); {0,0} means the
// size isn't documented. Used to warn that switching mode changes the stream
// geometry (see ISSUES.md — it can desync a statically-configured jvcu360-camera).
var modeSize = map[Mode][2]int{
	Mode360AllAround: {1920, 720},
	ModeFullScreen:   {1920, 540},
	ModeHost:         {1920, 1080},
	ModeDualHost:     {1920, 1080},
	ModeSingleView:   {1920, 1080},
	ModeWideAngle:    {0, 0},
}

// Candidate is one (unit, selector) Extension-Unit control to probe.
type Candidate struct {
	Unit     uint8
	Selector uint8
}

// Candidates returns every vendor XU control worth probing — the documented
// selector ranges of the two units (unit 2: 1–10, unit 3: 9–12).
func Candidates() []Candidate {
	var c []Candidate
	for s := uint8(1); s <= 10; s++ {
		c = append(c, Candidate{Unit: xuUnitA, Selector: s})
	}
	for s := uint8(9); s <= 12; s++ {
		c = append(c, Candidate{Unit: xuUnitB, Selector: s})
	}
	return c
}

// TODO(stage B): once the probe (cmd/jvcu360) reveals which control selects the
// mode and what value maps to each Mode, encode that here — the control's
// (unit, selector, byte length) plus a Mode↔value table — and build the
// jvcu360-mode switch on top of it (SetPosition→SET_CUR, GetPosition→GET_CUR).
