package camera360

import (
	"context"
	"fmt"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/data"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
	"go.viam.com/rdk/utils"
)

// UVCCamera is the model for any USB Video Class (UVC) webcam. Phase 1 is a
// straight pass-through: it surfaces whatever frame the device is currently
// producing — for a 360 camera, the display mode it is currently in — with no
// stitching or dewarping. The point of pass-through is to see, over the wire,
// exactly what the device outputs (resolution and pixel layout) before we
// decide how to process it. Tested on the j5create JVCU360 (see jvcu360/README.md
// for device-specific notes such as its six touch-bar modes).
var UVCCamera = resource.NewModel("dtcurrie", "camera-360", "uvc-camera")

const (
	defaultUVCFrameRate = 30
	defaultUVCFormat    = "mjpeg"
)

func init() {
	resource.RegisterComponent(camera.API, UVCCamera,
		resource.Registration[camera.Camera, *UVCCameraConfig]{
			Constructor: newUVCCamera,
		},
	)
}

// UVCCameraConfig is the user-supplied JSON config. All fields are optional. The
// frame-rate/format defaults match a typical UVC webcam's MJPEG 30fps output; the
// default capture size is OS-specific (1080p on Linux, 720p on macOS — see
// defaultVideoSize and ISSUES.md).
type UVCCameraConfig struct {
	// VideoDevice is the OS handle for the camera: a V4L2 node like
	// "/dev/video0" on Linux, or an avfoundation device index like "0" on
	// macOS. Empty uses the per-OS default.
	VideoDevice string `json:"video_device,omitempty"`
	// Width/Height/FrameRate request a capture format from the device. A device
	// may advertise several formats (e.g. the JVCU360 reports different heights
	// per 360/non-360 mode), which is one of the things pass-through reveals.
	Width     int `json:"width,omitempty"`
	Height    int `json:"height,omitempty"`
	FrameRate int `json:"frame_rate,omitempty"`
	// InputFormat is the V4L2 pixel format ("mjpeg" by default). Ignored on macOS.
	InputFormat string `json:"input_format,omitempty"`
}

// Validate applies defaults at construction; here we only reject negative
// numbers. No dependencies — this camera doesn't reference other resources.
func (cfg *UVCCameraConfig) Validate(path string) ([]string, []string, error) {
	if cfg.Width < 0 || cfg.Height < 0 {
		return nil, nil, fmt.Errorf("%s: width/height must be non-negative", path)
	}
	if cfg.FrameRate < 0 {
		return nil, nil, fmt.Errorf("%s: frame_rate must be non-negative", path)
	}
	return nil, nil, nil
}

type uvcCamera struct {
	resource.AlwaysRebuild

	name      resource.Name
	logger    logging.Logger
	frameRate int
	capture   *Capture
}

func newUVCCamera(ctx context.Context, _ resource.Dependencies, rawConf resource.Config, logger logging.Logger) (camera.Camera, error) {
	conf, err := resource.NativeConfig[*UVCCameraConfig](rawConf)
	if err != nil {
		return nil, err
	}
	return NewUVCCamera(ctx, rawConf.ResourceName(), conf, logger)
}

// NewUVCCamera is exposed for the discovery CLI in cmd/uvc/main.go; the
// regular module path goes through newUVCCamera.
func NewUVCCamera(ctx context.Context, name resource.Name, conf *UVCCameraConfig, logger logging.Logger) (camera.Camera, error) {
	device := conf.VideoDevice
	if device == "" {
		device = defaultVideoDevice()
	}
	defaultWidth, defaultHeight := defaultVideoSize()
	width := conf.Width
	if width == 0 {
		width = defaultWidth
	}
	height := conf.Height
	if height == 0 {
		height = defaultHeight
	}
	// Enforce the per-OS capture ceiling (logs once on macOS if it caps).
	width, height = clampVideoSize(width, height, logger)
	frameRate := conf.FrameRate
	if frameRate == 0 {
		frameRate = defaultUVCFrameRate
	}
	inputFormat := conf.InputFormat
	if inputFormat == "" {
		inputFormat = defaultUVCFormat
	}

	logger.Infow("opening UVC camera",
		"device", device, "size", fmt.Sprintf("%dx%d", width, height), "fps", frameRate)
	cp, err := NewCapture(ctx, videoInputArgs(device, width, height, frameRate, inputFormat), device, logger)
	if err != nil {
		return nil, fmt.Errorf("uvc capture: %w", err)
	}
	return &uvcCamera{
		name:      name,
		logger:    logger,
		frameRate: frameRate,
		capture:   cp,
	}, nil
}

func (c *uvcCamera) Name() resource.Name { return c.name }

func (c *uvcCamera) Images(ctx context.Context, filterSourceNames []string, _ map[string]interface{}) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	// Pass-through exposes exactly one source; if a filter is given, it must
	// ask for that source.
	if len(filterSourceNames) > 0 {
		wanted := false
		for _, n := range filterSourceNames {
			if n == SourceRaw {
				wanted = true
			}
		}
		if !wanted {
			return nil, resource.ResponseMetadata{}, fmt.Errorf("unknown source(s) %v; only %q is available", filterSourceNames, SourceRaw)
		}
	}

	// Block briefly for the first frame on a cold start; after that Latest()
	// returns the most recent frame immediately.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.capture.WaitFirstFrame(waitCtx); err != nil {
		return nil, resource.ResponseMetadata{}, fmt.Errorf("waiting for first frame: %w", err)
	}
	raw, err := c.capture.Latest()
	if err != nil {
		return nil, resource.ResponseMetadata{}, err
	}
	ni, err := camera.NamedImageFromImage(raw, SourceRaw, utils.MimeTypeJPEG, data.Annotations{})
	if err != nil {
		return nil, resource.ResponseMetadata{}, err
	}
	return []camera.NamedImage{ni}, resource.ResponseMetadata{CapturedAt: time.Now()}, nil
}

func (c *uvcCamera) NextPointCloud(_ context.Context, _ map[string]interface{}) (pointcloud.PointCloud, error) {
	return nil, errNotSupported
}

func (c *uvcCamera) Properties(_ context.Context) (camera.Properties, error) {
	return camera.Properties{
		SupportsPCD: false,
		ImageType:   camera.ColorStream,
		MimeTypes:   []string{utils.MimeTypeJPEG},
		FrameRate:   float32(c.frameRate),
	}, nil
}

func (c *uvcCamera) Geometries(_ context.Context, _ map[string]interface{}) ([]spatialmath.Geometry, error) {
	return []spatialmath.Geometry{}, nil
}

func (c *uvcCamera) DoCommand(_ context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
	// Phase 1 is pass-through only; runtime controls (dewarping, a virtual
	// servo/switch over the digital view) land in a later phase, informed by
	// what these captures reveal about the device's output.
	return nil, errNotSupported
}

func (c *uvcCamera) Status(_ context.Context) (map[string]interface{}, error) {
	return map[string]interface{}{
		"source":      SourceRaw,
		"frame_rate":  c.frameRate,
		"passthrough": true,
	}, nil
}

func (c *uvcCamera) Close(_ context.Context) error {
	return c.capture.Close()
}
