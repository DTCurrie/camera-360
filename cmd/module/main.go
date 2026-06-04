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
		resource.APIModel{API: camera.API, Model: camera360.AmbarellaCamera}, // Ambarella RTSP 360 camera
		resource.APIModel{API: camera.API, Model: camera360.UVCCamera},       // USB (UVC) webcam
		resource.APIModel{API: audioin.API, Model: camera360.UVCMic},         // USB (UAC) microphone
	)
}
