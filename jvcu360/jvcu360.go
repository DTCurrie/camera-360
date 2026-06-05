// Package jvcu360 implements the dtcurrie:camera-360:jvcu360-camera and
// dtcurrie:camera-360:jvcu360-mic models for the j5create JVCU360 USB 360 webcam.
// The camera captures the device's already-dewarped UVC output and tags every
// frame with GPano cropped-area XMP describing its partial equatorial band, so a
// capable 360 viewer maps the band at its true latitudes instead of stretching it
// pole-to-pole. The device must be set MANUALLY to its "360 All Around" mode: it
// can't report its mode over USB and we don't drive it (see jvcu360/xu and the
// deep probe), so the model assumes that mode and always emits GPano.
//
// Shared capture/XMP/platform infra lives in the root camera360 package; the XU
// mode-control probe is the sibling jvcu360/xu package.
package jvcu360

import (
	"context"
	"fmt"
	"image"
	"time"

	"camera360"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/data"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
	"go.viam.com/rdk/utils"
)

const (
	defaultUVCFrameRate = 30
	defaultUVCFormat    = "mjpeg"
	// defaultHFOVDeg/defaultVFOVDeg are the JVCU360 "360 All Around" band's
	// coverage, used for the GPano cropped-area math: full 360° around, ~53°
	// vertical (j5create spec; see the jvcu360-hardware notes).
	defaultHFOVDeg = 360.0
	defaultVFOVDeg = 53.0
)

func init() {
	resource.RegisterComponent(camera.API, camera360.JVCU360Camera,
		resource.Registration[camera.Camera, *CameraConfig]{
			Constructor: newCamera,
		},
	)
}

// CameraConfig is the user-supplied JSON config. All fields are optional. The
// frame-rate/format defaults match the JVCU360's MJPEG 30fps output; the default
// capture size is OS-specific (1080p on Linux, 720p on macOS — see
// camera360.DefaultVideoSize and ISSUES.md).
type CameraConfig struct {
	// VideoDevice is the OS handle for the camera: a V4L2 node like
	// "/dev/video0" on Linux, or an avfoundation device index like "0" on
	// macOS. Empty uses the per-OS default.
	VideoDevice string `json:"video_device,omitempty"`
	// Width/Height/FrameRate request a capture format from the device.
	Width     int `json:"width,omitempty"`
	Height    int `json:"height,omitempty"`
	FrameRate int `json:"frame_rate,omitempty"`
	// InputFormat is the V4L2 pixel format ("mjpeg" by default). Ignored on macOS.
	InputFormat string `json:"input_format,omitempty"`
	// CropTop/CropBottom trim that many pixels off the top and bottom of every
	// frame before it leaves the module. The JVCU360's 360 All-Around output is
	// an equatorial band letterboxed with black bars at the poles; trimming them
	// yields a tight content band. Only top/bottom are exposed: trimming
	// left/right would break the 360° longitude wrap. The crop runs in the ffmpeg
	// stage that already transcodes the stream, so it costs ~nothing per frame.
	// Use the detect_bars DoCommand to find the right values, and pair the crop
	// with v_fov_deg = the content band's true vertical FOV so the GPano metadata
	// places it correctly.
	CropTop    int `json:"crop_top,omitempty"`
	CropBottom int `json:"crop_bottom,omitempty"`
	// HFOVDeg/VFOVDeg describe the angular coverage of the (post-crop) output
	// frame, used to compute the GPano cropped-area metadata. Defaults are the
	// JVCU360 360 All-Around band: 360° horizontal, 53° vertical.
	HFOVDeg float64 `json:"h_fov_deg,omitempty"`
	VFOVDeg float64 `json:"v_fov_deg,omitempty"`
}

// Validate rejects negative sizes/rates and out-of-range FOVs; defaults are
// applied at construction. No dependencies — this camera references no other
// resources.
func (cfg *CameraConfig) Validate(path string) ([]string, []string, error) {
	if cfg.Width < 0 || cfg.Height < 0 {
		return nil, nil, fmt.Errorf("%s: width/height must be non-negative", path)
	}
	if cfg.FrameRate < 0 {
		return nil, nil, fmt.Errorf("%s: frame_rate must be non-negative", path)
	}
	if cfg.CropTop < 0 || cfg.CropBottom < 0 {
		return nil, nil, fmt.Errorf("%s: crop_top/crop_bottom must be non-negative", path)
	}
	if cfg.HFOVDeg < 0 || cfg.HFOVDeg > 360 {
		return nil, nil, fmt.Errorf("%s: h_fov_deg must be in (0, 360]", path)
	}
	if cfg.VFOVDeg < 0 || cfg.VFOVDeg > 180 {
		return nil, nil, fmt.Errorf("%s: v_fov_deg must be in (0, 180]", path)
	}
	return nil, nil, nil
}

type jvcuCamera struct {
	resource.AlwaysRebuild

	name       resource.Name
	logger     logging.Logger
	frameRate  int
	hFOV, vFOV float64
	capture    *camera360.Capture
}

func newCamera(ctx context.Context, _ resource.Dependencies, rawConf resource.Config, logger logging.Logger) (camera.Camera, error) {
	conf, err := resource.NativeConfig[*CameraConfig](rawConf)
	if err != nil {
		return nil, err
	}
	return NewCamera(ctx, rawConf.ResourceName(), conf, logger)
}

// NewCamera is exposed for the discovery CLI in cmd/uvc/main.go; the regular
// module path goes through newCamera.
func NewCamera(ctx context.Context, name resource.Name, conf *CameraConfig, logger logging.Logger) (camera.Camera, error) {
	device := conf.VideoDevice
	if device == "" {
		device = camera360.DefaultVideoDevice()
	}
	defaultWidth, defaultHeight := camera360.DefaultVideoSize()
	width := conf.Width
	if width == 0 {
		width = defaultWidth
	}
	height := conf.Height
	if height == 0 {
		height = defaultHeight
	}
	// Enforce the per-OS capture ceiling (logs once on macOS if it caps).
	width, height = camera360.ClampVideoSize(width, height, logger)
	frameRate := conf.FrameRate
	if frameRate == 0 {
		frameRate = defaultUVCFrameRate
	}
	inputFormat := conf.InputFormat
	if inputFormat == "" {
		inputFormat = defaultUVCFormat
	}
	hFOV := conf.HFOVDeg
	if hFOV == 0 {
		hFOV = defaultHFOVDeg
	}
	vFOV := conf.VFOVDeg
	if vFOV == 0 {
		vFOV = defaultVFOVDeg
	}

	logger.Infow("opening JVCU360 camera",
		"device", device, "size", fmt.Sprintf("%dx%d", width, height), "fps", frameRate,
		"h_fov_deg", hFOV, "v_fov_deg", vFOV)
	inputArgs := camera360.VideoInputArgs(device, width, height, frameRate, inputFormat)
	// A -vf crop filter is an output option, so it must follow -i (which is the
	// last thing VideoInputArgs emits) and precede the output args runOnce
	// appends. Use input-relative dimensions (iw/ih) so we needn't resolve the
	// negotiated frame size here — ffmpeg evaluates them per stream.
	if crop, ok := cropFilter(conf.CropTop, conf.CropBottom); ok {
		inputArgs = append(inputArgs, "-vf", crop)
		logger.Infow("cropping frames", "crop_top", conf.CropTop, "crop_bottom", conf.CropBottom, "filter", crop)
	}
	cp, err := camera360.NewCapture(ctx, inputArgs, device, logger)
	if err != nil {
		return nil, fmt.Errorf("jvcu360 capture: %w", err)
	}
	return &jvcuCamera{
		name:      name,
		logger:    logger,
		frameRate: frameRate,
		hFOV:      hFOV,
		vFOV:      vFOV,
		capture:   cp,
	}, nil
}

// cropFilter builds the ffmpeg -vf crop expression that trims top/bottom pixels
// off each frame, or ("", false) when no crop is requested. It uses
// input-relative dimensions (iw/ih) so the negotiated frame size needn't be
// known here; width is left full to preserve the 360° longitude wrap.
func cropFilter(top, bottom int) (string, bool) {
	if top <= 0 && bottom <= 0 {
		return "", false
	}
	return fmt.Sprintf("crop=iw:ih-%d:0:%d", top+bottom, top), true
}

func (c *jvcuCamera) Name() resource.Name { return c.name }

func (c *jvcuCamera) Images(ctx context.Context, filterSourceNames []string, _ map[string]any) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	// Pass-through exposes exactly one source; if a filter is given, it must
	// ask for that source.
	if len(filterSourceNames) > 0 {
		wanted := false
		for _, n := range filterSourceNames {
			if n == camera360.SourceRaw {
				wanted = true
			}
		}
		if !wanted {
			return nil, resource.ResponseMetadata{}, fmt.Errorf("unknown source(s) %v; only %q is available", filterSourceNames, camera360.SourceRaw)
		}
	}

	// Block briefly for the first frame on a cold start; after that Latest()
	// returns the most recent frame immediately.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.capture.WaitFirstFrame(waitCtx); err != nil {
		return nil, resource.ResponseMetadata{}, fmt.Errorf("waiting for first frame: %w", err)
	}
	// The device is assumed to be in its "360 All Around" mode (a partial
	// equatorial band, not a full sphere), so tag every frame with GPano
	// cropped-area XMP derived from the frame's dimensions and the configured
	// FOV. Lossless: injected into the device's own JPEG bytes, no re-encode.
	rawBytes, err := c.capture.LatestRaw()
	if err != nil {
		return nil, resource.ResponseMetadata{}, err
	}
	tagged, err := camera360.JPEGWithGPano(rawBytes, c.hFOV, c.vFOV)
	if err != nil {
		return nil, resource.ResponseMetadata{}, fmt.Errorf("tagging GPano frame: %w", err)
	}
	ni, err := camera.NamedImageFromBytes(tagged, camera360.SourceRaw, utils.MimeTypeJPEG, data.Annotations{})
	if err != nil {
		return nil, resource.ResponseMetadata{}, err
	}
	return []camera.NamedImage{ni}, resource.ResponseMetadata{CapturedAt: time.Now()}, nil
}

func (c *jvcuCamera) NextPointCloud(_ context.Context, _ map[string]any) (pointcloud.PointCloud, error) {
	return nil, camera360.ErrNotSupported
}

func (c *jvcuCamera) Properties(_ context.Context) (camera.Properties, error) {
	return camera.Properties{
		SupportsPCD: false,
		ImageType:   camera.ColorStream,
		MimeTypes:   []string{utils.MimeTypeJPEG},
		FrameRate:   float32(c.frameRate),
	}, nil
}

func (c *jvcuCamera) Geometries(_ context.Context, _ map[string]any) ([]spatialmath.Geometry, error) {
	return []spatialmath.Geometry{}, nil
}

// DoCommand currently supports one command, "detect_bars", a calibration aid
// for the crop_top/crop_bottom config fields. The module already owns the
// device, so it scans the latest decoded frame in-process for letterbox bars
// (rows whose every pixel is below a luma threshold) and returns the crop the
// config should use plus the resulting content band's aspect ratio.
//
//	{"command": "detect_bars"}                          // default luma threshold
//	{"command": "detect_bars", "luma_threshold": 24}    // override (0-255)
//
// NOTE: detection runs on the frame ffmpeg delivers, which is already cropped
// if crop_top/crop_bottom are set — so run it with no crop configured to find
// the bars, or with a crop set to confirm it reports 0/0.
func (c *jvcuCamera) DoCommand(_ context.Context, cmd map[string]any) (map[string]any, error) {
	switch cmd["command"] {
	case "detect_bars":
		thresh := uint32(16)
		if v, ok := cmd["luma_threshold"].(float64); ok {
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			thresh = uint32(v)
		}
		img, err := c.capture.Latest()
		if err != nil {
			return nil, fmt.Errorf("detect_bars: %w", err)
		}
		top, bottom := detectLetterbox(img, thresh)
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		bandH := h - top - bottom
		var aspect float64
		if bandH > 0 {
			aspect = float64(w) / float64(bandH)
		}
		return map[string]any{
			"crop_top":             top,
			"crop_bottom":          bottom,
			"frame_width":          w,
			"frame_height":         h,
			"content_width":        w,
			"content_height":       bandH,
			"content_aspect_ratio": aspect,
			"luma_threshold":       int(thresh),
		}, nil
	default:
		return nil, camera360.ErrNotSupported
	}
}

// detectLetterbox counts the leading (top) and trailing (bottom) rows of img
// that are uniformly below lumaThresh — i.e. the black letterbox bars. A row is
// "dark" only if every pixel is dark, so scene content touching an edge is not
// mistaken for a bar. If the whole frame is dark it returns (height, 0).
func detectLetterbox(img image.Image, lumaThresh uint32) (top, bottom int) {
	b := img.Bounds()
	rowDark := func(y int) bool {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			// RGBA() returns 16-bit; >>8 brings each channel to 0-255. Rec.601 luma.
			luma := (299*(r>>8) + 587*(g>>8) + 114*(bl>>8)) / 1000
			if luma > lumaThresh {
				return false
			}
		}
		return true
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		if !rowDark(y) {
			break
		}
		top++
	}
	if top == b.Dy() {
		return b.Dy(), 0 // entirely dark; nothing meaningful to crop
	}
	for y := b.Max.Y - 1; y >= b.Min.Y; y-- {
		if !rowDark(y) {
			break
		}
		bottom++
	}
	return top, bottom
}

func (c *jvcuCamera) Status(_ context.Context) (map[string]any, error) {
	return map[string]any{
		"source":      camera360.SourceRaw,
		"frame_rate":  c.frameRate,
		"passthrough": true,
		"h_fov_deg":   c.hFOV,
		"v_fov_deg":   c.vFOV,
	}, nil
}

func (c *jvcuCamera) Close(_ context.Context) error {
	return c.capture.Close()
}
