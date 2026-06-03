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

// JVCU360 is the model for the j5create JVCU360 USB 360 webcam. Phase 1 is a
// straight UVC pass-through: it surfaces whatever frame the device is currently
// producing — i.e. the display mode selected on its capacitive touch bar — with
// no stitching or dewarping. The point of pass-through is to see, over the wire,
// exactly what each of the device's six modes looks like (resolution and pixel
// layout) before we decide how to process it.
var JVCU360 = resource.NewModel("dtcurrie", "camera-360", "jvcu360")

const (
	defaultJVCU360Width     = 1920
	defaultJVCU360Height    = 1080
	defaultJVCU360FrameRate = 30
	defaultJVCU360Format    = "mjpeg"
)

func init() {
	resource.RegisterComponent(camera.API, JVCU360,
		resource.Registration[camera.Camera, *JVCU360Config]{
			Constructor: newJVCU360Camera,
		},
	)
}

// JVCU360Config is the user-supplied JSON config. All fields are optional; the
// defaults match the JVCU360's native MJPEG 1080p30 output.
type JVCU360Config struct {
	// VideoDevice is the OS handle for the camera: a V4L2 node like
	// "/dev/video0" on Linux, or an avfoundation device index like "0" on
	// macOS. Empty uses the per-OS default.
	VideoDevice string `json:"video_device,omitempty"`
	// Width/Height/FrameRate request a capture format from the device. The
	// JVCU360 advertises up to 1920x1080@30; its non-360 modes report smaller
	// heights, which is one of the things pass-through capture reveals.
	Width     int `json:"width,omitempty"`
	Height    int `json:"height,omitempty"`
	FrameRate int `json:"frame_rate,omitempty"`
	// InputFormat is the V4L2 pixel format ("mjpeg" by default). Ignored on macOS.
	InputFormat string `json:"input_format,omitempty"`
}

// Validate applies defaults at construction; here we only reject negative
// numbers. No dependencies — this camera doesn't reference other resources.
func (cfg *JVCU360Config) Validate(path string) ([]string, []string, error) {
	if cfg.Width < 0 || cfg.Height < 0 {
		return nil, nil, fmt.Errorf("%s: width/height must be non-negative", path)
	}
	if cfg.FrameRate < 0 {
		return nil, nil, fmt.Errorf("%s: frame_rate must be non-negative", path)
	}
	return nil, nil, nil
}

type jvcu360Camera struct {
	resource.AlwaysRebuild

	name      resource.Name
	logger    logging.Logger
	frameRate int
	capture   *Capture
}

func newJVCU360Camera(ctx context.Context, _ resource.Dependencies, rawConf resource.Config, logger logging.Logger) (camera.Camera, error) {
	conf, err := resource.NativeConfig[*JVCU360Config](rawConf)
	if err != nil {
		return nil, err
	}
	return NewJVCU360Camera(ctx, rawConf.ResourceName(), conf, logger)
}

// NewJVCU360Camera is exposed for the discovery CLI in cmd/uvc/main.go; the
// regular module path goes through newJVCU360Camera.
func NewJVCU360Camera(ctx context.Context, name resource.Name, conf *JVCU360Config, logger logging.Logger) (camera.Camera, error) {
	device := conf.VideoDevice
	if device == "" {
		device = defaultVideoDevice()
	}
	width := conf.Width
	if width == 0 {
		width = defaultJVCU360Width
	}
	height := conf.Height
	if height == 0 {
		height = defaultJVCU360Height
	}
	frameRate := conf.FrameRate
	if frameRate == 0 {
		frameRate = defaultJVCU360FrameRate
	}
	inputFormat := conf.InputFormat
	if inputFormat == "" {
		inputFormat = defaultJVCU360Format
	}

	logger.Infow("opening JVCU360 over UVC",
		"device", device, "size", fmt.Sprintf("%dx%d", width, height), "fps", frameRate)
	cp, err := NewCapture(ctx, videoInputArgs(device, width, height, frameRate, inputFormat), device, logger)
	if err != nil {
		return nil, fmt.Errorf("uvc capture: %w", err)
	}
	return &jvcu360Camera{
		name:      name,
		logger:    logger,
		frameRate: frameRate,
		capture:   cp,
	}, nil
}

func (c *jvcu360Camera) Name() resource.Name { return c.name }

func (c *jvcu360Camera) Images(ctx context.Context, filterSourceNames []string, _ map[string]interface{}) ([]camera.NamedImage, resource.ResponseMetadata, error) {
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

func (c *jvcu360Camera) NextPointCloud(_ context.Context, _ map[string]interface{}) (pointcloud.PointCloud, error) {
	return nil, errNotSupported
}

func (c *jvcu360Camera) Properties(_ context.Context) (camera.Properties, error) {
	return camera.Properties{
		SupportsPCD: false,
		ImageType:   camera.ColorStream,
		MimeTypes:   []string{utils.MimeTypeJPEG},
		FrameRate:   float32(c.frameRate),
	}, nil
}

func (c *jvcu360Camera) Geometries(_ context.Context, _ map[string]interface{}) ([]spatialmath.Geometry, error) {
	return []spatialmath.Geometry{}, nil
}

func (c *jvcu360Camera) DoCommand(_ context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
	// Phase 1 is pass-through only; runtime controls (dewarping, a virtual
	// servo/switch over the digital view) land in a later phase, informed by
	// what these captures reveal about the device's output.
	return nil, errNotSupported
}

func (c *jvcu360Camera) Status(_ context.Context) (map[string]interface{}, error) {
	return map[string]interface{}{
		"source":      SourceRaw,
		"frame_rate":  c.frameRate,
		"passthrough": true,
	}, nil
}

func (c *jvcu360Camera) Close(_ context.Context) error {
	return c.capture.Close()
}
