package camera360

import (
	"testing"
)

func TestApplyStitchUpdate_PartialFields(t *testing.T) {
	front, back := DefaultLenses()
	p := StitchParams{Front: front, Back: back, Opts: StitcherOpts{}}

	if err := applyStitchUpdate(&p, map[string]interface{}{
		"seam_feather_deg":        5.0,
		"back_extrinsic_roll_deg": -2.0,
		"lens_model":              "equidistant",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if p.Opts.SeamFeatherDeg != 5 {
		t.Errorf("seam_feather_deg: want 5, got %v", p.Opts.SeamFeatherDeg)
	}
	if p.Opts.BackExtrinsicRollDeg != -2 {
		t.Errorf("back_extrinsic_roll_deg: want -2, got %v", p.Opts.BackExtrinsicRollDeg)
	}
	if p.Opts.LensModel != LensEquidistant {
		t.Errorf("lens_model: want LensEquidistant, got %v", p.Opts.LensModel)
	}
	// Untouched fields should still equal defaults.
	if p.Front.Radius != front.Radius {
		t.Errorf("front.radius mutated unexpectedly: want %v, got %v", front.Radius, p.Front.Radius)
	}
}

func TestApplyStitchUpdate_NestedLens(t *testing.T) {
	front, back := DefaultLenses()
	p := StitchParams{Front: front, Back: back, Opts: StitcherOpts{}}

	if err := applyStitchUpdate(&p, map[string]interface{}{
		"front_lens": map[string]interface{}{
			"radius":  478.0,
			"fov_deg": 195.0,
		},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if p.Front.Radius != 478 {
		t.Errorf("front.radius: want 478, got %v", p.Front.Radius)
	}
	if p.Front.FOVDeg != 195 {
		t.Errorf("front.fov_deg: want 195, got %v", p.Front.FOVDeg)
	}
	// Other front fields should be untouched.
	if p.Front.CenterX != front.CenterX {
		t.Errorf("front.center_x mutated: want %v, got %v", front.CenterX, p.Front.CenterX)
	}
	// Back lens should be untouched.
	if p.Back != back {
		t.Errorf("back lens mutated when only front specified")
	}
}

func TestApplyStitchUpdate_TypeErrors(t *testing.T) {
	front, back := DefaultLenses()
	p := StitchParams{Front: front, Back: back}

	if err := applyStitchUpdate(&p, map[string]interface{}{"seam_feather_deg": "five"}); err == nil {
		t.Error("expected type error for string in seam_feather_deg")
	}
	if err := applyStitchUpdate(&p, map[string]interface{}{"lens_model": "perspective"}); err == nil {
		t.Error("expected error for unknown lens_model value")
	}
	if err := applyStitchUpdate(&p, map[string]interface{}{"front_lens": "not an object"}); err == nil {
		t.Error("expected error for non-object front_lens")
	}
}

func TestStitchParamsRoundTrip(t *testing.T) {
	front, back := DefaultLenses()
	original := StitchParams{
		Front: front,
		Back:  back,
		Opts: StitcherOpts{
			SeamFeatherDeg:        5,
			BackExtrinsicRollDeg:  -2,
			BackExtrinsicYawDeg:   1,
			BackExtrinsicPitchDeg: -0.5,
			LensModel:             LensEquidistant,
		},
	}
	m := stitchParamsToMap(original)
	// Round-trip through the apply path.
	recovered := StitchParams{Front: front, Back: back}
	if err := applyStitchUpdate(&recovered, m); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if recovered.Opts != original.Opts {
		t.Errorf("opts mismatch after round-trip\nwant: %+v\ngot:  %+v", original.Opts, recovered.Opts)
	}
	if recovered.Front != original.Front {
		t.Errorf("front mismatch after round-trip")
	}
}
