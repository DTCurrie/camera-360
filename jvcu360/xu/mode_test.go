package xu

import "testing"

func TestModeTableConsistent(t *testing.T) {
	if len(modeNames) != 6 {
		t.Fatalf("modeNames has %d entries, want 6", len(modeNames))
	}
	// Every Mode constant must have a label and a size entry, and the enum must
	// run contiguously from 0 so int(Mode) indexes modeNames.
	for m := Mode360AllAround; m <= ModeWideAngle; m++ {
		if int(m) >= len(modeNames) {
			t.Fatalf("Mode %d has no label", m)
		}
		if modeNames[m] == "" {
			t.Errorf("Mode %d has an empty label", m)
		}
		if _, ok := modeSize[m]; !ok {
			t.Errorf("Mode %q has no size entry", modeNames[m])
		}
	}
	if int(ModeWideAngle) != len(modeNames)-1 {
		t.Errorf("ModeWideAngle = %d, want %d (last index)", ModeWideAngle, len(modeNames)-1)
	}
}

func TestCandidates(t *testing.T) {
	c := Candidates()
	// unit 2 selectors 1–10 (10) + unit 3 selectors 9–12 (4) = 14.
	if len(c) != 14 {
		t.Fatalf("Candidates() = %d, want 14", len(c))
	}
	for _, cand := range c {
		switch cand.Unit {
		case xuUnitA:
			if cand.Selector < 1 || cand.Selector > 10 {
				t.Errorf("unit %d selector %d out of range 1–10", cand.Unit, cand.Selector)
			}
		case xuUnitB:
			if cand.Selector < 9 || cand.Selector > 12 {
				t.Errorf("unit %d selector %d out of range 9–12", cand.Unit, cand.Selector)
			}
		default:
			t.Errorf("unexpected unit %d", cand.Unit)
		}
	}
}
