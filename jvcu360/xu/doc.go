// Package jvcu360 holds device-specific support for the j5create JVCU360 360
// camera: pure-Go control of its vendor UVC Extension Units — used to switch the
// six firmware display modes that are otherwise only reachable via the device's
// capacitive touch bar — and (later) the jvcu360-mode switch component.
//
// Extension-Unit access is Linux-only: it goes through the in-kernel uvcvideo
// driver via the UVCIOC_CTRL_QUERY ioctl, so it needs no driver detach and
// coexists with a live ffmpeg capture. On other platforms the calls return
// ErrUnsupported so the rest of the module still builds and runs (matching the
// Linux-only convention of camera360.EnumerateUVCWebcams).
package xu

import "errors"

// ErrUnsupported is returned by Extension-Unit operations on non-Linux builds.
var ErrUnsupported = errors.New("jvcu360: UVC extension-unit control is only supported on Linux")
