// Offline tester: loads a dual-fisheye reference frame from disk, runs it
// through the stitcher + pinhole projection, and writes outputs to disk.
// Lets us iterate on calibration without the camera being reachable.
//
//	go run ./cmd/offline -in out/dual_fisheye_reference.jpg
package main

import (
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"

	"camera360"
)

func main() {
	in := flag.String("in", "out/dual_fisheye_reference.jpg", "input dual-fisheye JPEG")
	outDir := flag.String("out", "out", "output directory")
	yaw := flag.Float64("yaw", 0, "pinhole yaw (deg)")
	pitch := flag.Float64("pitch", 0, "pinhole pitch (deg)")
	fov := flag.Float64("fov", 90, "pinhole FOV (deg)")
	rotFront := flag.Float64("rot_front", 0, "front lens rotation (deg)")
	rotBack := flag.Float64("rot_back", 0, "back lens rotation (deg)")
	cxFront := flag.Float64("cx_front", 480, "front lens centre X in right half (px)")
	cyFront := flag.Float64("cy_front", 480, "front lens centre Y (px)")
	rFront := flag.Float64("r_front", 440, "front lens image-circle radius (px)")
	cxBack := flag.Float64("cx_back", 480, "back lens centre X in left half (px)")
	cyBack := flag.Float64("cy_back", 480, "back lens centre Y (px)")
	rBack := flag.Float64("r_back", 440, "back lens image-circle radius (px)")
	fovLens := flag.Float64("fov_lens", 200, "physical lens FOV (deg)")
	seamFeather := flag.Float64("seam_feather", 0, "seam blend half-width (deg); 0 = use natural overlap")
	hardSeam := flag.Bool("hard_seam", false, "disable blending; pick whichever lens is closer to optical axis")
	backYaw := flag.Float64("back_yaw", 0, "back lens extrinsic yaw correction (deg)")
	backPitch := flag.Float64("back_pitch", 0, "back lens extrinsic pitch correction (deg)")
	backRoll := flag.Float64("back_roll", 0, "back lens extrinsic roll correction (deg)")
	lensModel := flag.String("lens", "equisolid", "lens projection: equisolid or equidistant")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		panic(err)
	}
	src := loadJPEG(*in)
	b := src.Bounds()
	fmt.Printf("loaded %s (%dx%d)\n", *in, b.Dx(), b.Dy())

	front := camera360.FisheyeLens{CenterX: *cxFront, CenterY: *cyFront, Radius: *rFront, FOVDeg: *fovLens, RotationDeg: *rotFront}
	back := camera360.FisheyeLens{CenterX: *cxBack, CenterY: *cyBack, Radius: *rBack, FOVDeg: *fovLens, RotationDeg: *rotBack}
	model := camera360.LensEquisolid
	if *lensModel == "equidistant" {
		model = camera360.LensEquidistant
	}
	stitcher := camera360.BuildFisheyeStitcherOpts(front, back, b.Dx(), b.Dy(), 1920, 960,
		camera360.StitcherOpts{
			SeamFeatherDeg:        *seamFeather,
			HardSeam:              *hardSeam,
			BackExtrinsicYawDeg:   *backYaw,
			BackExtrinsicPitchDeg: *backPitch,
			BackExtrinsicRollDeg:  *backRoll,
			LensModel:             model,
		})
	erp := stitcher.StitchToERP(src)

	view := camera360.PinholeView{Yaw: *yaw, Pitch: *pitch, FOVDeg: *fov, Width: 1280, Height: 720}
	projector := camera360.NewPinholeProjector(view, 1920, 960)
	flat := projector.Project(erp)

	writeJPEG(filepath.Join(*outDir, "stitched.jpg"), erp)
	writeJPEG(filepath.Join(*outDir, "stitched_pinhole.jpg"), flat)
	fmt.Println("wrote stitched.jpg, stitched_pinhole.jpg")
}

func loadJPEG(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		panic(err)
	}
	return img
}

func writeJPEG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		panic(err)
	}
}
