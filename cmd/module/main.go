package main

import (
	"camera360"
	// Blank imports register each device model's component in its own
	// subpackage's init(); the model identifiers live in the root camera360
	// package (models.go), which also registers the discovery service.
	_ "camera360/akaso_360"
	_ "camera360/jvcu360"

	"go.viam.com/rdk/components/audioin"
	camera "go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/module"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/discovery"
)

func main() {
	// ModularMain can take multiple APIModel arguments, if your module implements multiple models.
	module.ModularMain(
		resource.APIModel{API: camera.API, Model: camera360.AKASO360Camera}, // AKASO 360 RTSP/Ambarella camera
		resource.APIModel{API: camera.API, Model: camera360.JVCU360Camera},  // j5create JVCU360 UVC 360 webcam
		resource.APIModel{API: audioin.API, Model: camera360.JVCU360Mic},    // JVCU360 UAC microphone
		resource.APIModel{API: discovery.API, Model: camera360.Discovery},   // UVC webcam discovery
	)
}
