// Smoke test for the camera-360 module's RTSP camera: connects to a live 360
// camera over RTSP, pulls one set of frames, and writes them to ./out/ for
// visual inspection. The camera must be reachable first; for the AKASO 360
// that means joining its Wi-Fi hotspot (AK360_xxxx) — see akaso_360/README.md.
//
//	go run ./cmd/cli
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"camera360"
	camera "go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
)

func main() {
	host := flag.String("host", "192.168.42.1", "camera IP address")
	outDir := flag.String("out", "out", "directory to write frames into")
	yaw := flag.Float64("yaw", 0, "pinhole yaw (deg)")
	pitch := flag.Float64("pitch", 0, "pinhole pitch (deg)")
	fov := flag.Float64("fov", 90, "pinhole horizontal FOV (deg)")
	flag.Parse()

	if err := run(*host, *outDir, *yaw, *pitch, *fov); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(host, outDir string, yaw, pitch, fov float64) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger := logging.NewLogger("camera-360-cli")

	cfg := &camera360.AmbarellaConfig{
		Host:          host,
		InitialYaw:    yaw,
		InitialPitch:  pitch,
		PinholeFOVDeg: fov,
	}
	cam, err := camera360.NewAmbarellaCamera(ctx, camera.Named("cli"), cfg, logger)
	if err != nil {
		return fmt.Errorf("open camera: %w", err)
	}
	defer cam.Close(context.Background())

	logger.Info("waiting for first frame…")
	images, _, err := cam.Images(ctx, nil, nil)
	if err != nil {
		return fmt.Errorf("get images: %w", err)
	}
	for _, ni := range images {
		path := filepath.Join(outDir, ni.SourceName+".jpg")
		b, err := ni.Bytes(ctx)
		if err != nil {
			return fmt.Errorf("encode %s: %w", ni.SourceName, err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		logger.Infow("wrote frame", "source", ni.SourceName, "path", path, "bytes", len(b))
	}
	return nil
}
