package camera360

import "go.viam.com/rdk/resource"

// Model identifiers registered with the RDK. The implementations live in the
// per-device subpackages (akaso_360/, jvcu360/), but the identifiers live here in
// the shared root package so the discovery service (discovery.go) can name them
// without importing those subpackages — which would create an import cycle, since
// each subpackage imports the root for shared infra (capture, xmp, projection).
var (
	// AKASO360Camera is the Ambarella/RTSP dual-fisheye 360 camera (akaso_360/).
	AKASO360Camera = resource.NewModel("dtcurrie", "camera-360", "akaso-360-camera")
	// JVCU360Camera is the j5create JVCU360 UVC 360 webcam, GPano-tagged (jvcu360/).
	JVCU360Camera = resource.NewModel("dtcurrie", "camera-360", "jvcu360-camera")
	// JVCU360Mic is the j5create JVCU360 UAC microphone (jvcu360/).
	JVCU360Mic = resource.NewModel("dtcurrie", "camera-360", "jvcu360-mic")
)
