package main

import (
	"camera360"
	"go.viam.com/rdk/components/audioin"
	camera "go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/module"
	"go.viam.com/rdk/resource"
)

func main() {
	// ModularMain can take multiple APIModel arguments, if your module implements multiple models.
	module.ModularMain(
		resource.APIModel{API: camera.API, Model: camera360.Camera},      // RTSP 360 camera (Ambarella)
		resource.APIModel{API: camera.API, Model: camera360.JVCU360},     // j5create JVCU360 (UVC)
		resource.APIModel{API: audioin.API, Model: camera360.JVCU360Mic}, // JVCU360 mic (UAC)
	)
}
