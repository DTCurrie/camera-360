package camera360

import "errors"

// Source name constants for the named-image views the camera models return from
// Images(). The AKASO model exposes all five; the JVCU360 exposes only SourceRaw.
const (
	SourceRaw             = "raw"
	SourceFront           = "front"
	SourceBack            = "back"
	SourceEquirectangular = "equirectangular"
	SourcePinhole         = "pinhole"
)

// ErrNotSupported is returned by component methods this module does not implement.
var ErrNotSupported = errors.New("not supported by camera-360")
