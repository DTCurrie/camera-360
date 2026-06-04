// Package camera360 implements Viam components for 360 cameras. It registers
// several models: an RTSP camera that consumes a 360 H.264 stream (unlocked
// via an Ambarella JSON-over-TCP handshake on port 7878 where the camera
// requires it) and stitches dual-fisheye input into an equirectangular
// panorama with a steerable virtual pinhole view; a USB (UVC) pass-through
// camera; and a USB (UAC) microphone exposed as audio_in.
package camera360

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"sync"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/components/camera/rtppassthrough"
	"go.viam.com/rdk/data"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
	"go.viam.com/rdk/utils"
)

var (
	// AmbarellaCamera is the model identifier registered with the RDK.
	AmbarellaCamera = resource.NewModel("dtcurrie", "camera-360", "ambarella-camera")

	errNotSupported = errors.New("not supported by camera-360")
)

// Source name constants for the named-image views returned by Images().
const (
	SourceRaw             = "raw"
	SourceFront           = "front"
	SourceBack            = "back"
	SourceEquirectangular = "equirectangular"
	SourcePinhole         = "pinhole"
)

const (
	defaultHost          = "192.168.42.1"
	defaultPinholeWidth  = 1280
	defaultPinholeHeight = 720
	defaultPinholeFOV    = 90.0
	defaultERPWidth      = 1920
	defaultERPHeight     = 960
	heartbeatInterval    = 5 * time.Second
)

func init() {
	resource.RegisterComponent(camera.API, AmbarellaCamera,
		resource.Registration[camera.Camera, *AmbarellaConfig]{
			Constructor: newAmbarellaCamera,
		},
	)
}

// AmbarellaConfig is the user-supplied JSON config. Defaults are documented in the
// component's markdown page; all fields are optional.
type AmbarellaConfig struct {
	// Host is the camera's IP on its Wi-Fi hotspot. Almost always
	// 192.168.42.1 — the field exists for the rare firmware that uses a
	// different gateway (e.g. 192.168.169.1 on some variants).
	Host string `json:"host,omitempty"`

	// FrontLens / BackLens calibrate the two fisheye hemispheres. Centers
	// are in HALF-FRAME-LOCAL coordinates: the front lens config measures
	// from the right half (so 480,480 is the centre of the right half of a
	// 1920×960 frame); the back lens config measures from the left half.
	FrontLens *FisheyeLens `json:"front_lens,omitempty"`
	BackLens  *FisheyeLens `json:"back_lens,omitempty"`

	// ERPWidth/Height controls the stitched panorama's resolution.
	ERPWidth  int `json:"erp_width,omitempty"`
	ERPHeight int `json:"erp_height,omitempty"`

	// SeamFeatherDeg is the half-width of the seam blend region in degrees
	// of arc. 0 = use the lens's natural overlap. Smaller values give a
	// sharper seam (less ghosting from misaligned lenses but more visible
	// brightness/colour transition); larger values blend more aggressively.
	SeamFeatherDeg float64 `json:"seam_feather_deg,omitempty"`

	// BackExtrinsic{Yaw,Pitch,Roll}Deg correct for the back lens not being
	// perfectly 180° opposed to the front. Small angles (typically ±5°)
	// can improve seam alignment significantly on cameras with
	// manufacturing tolerance variation. Tune visually by adjusting until
	// vertical lines or horizon segments line up across the seam.
	BackExtrinsicYawDeg   float64 `json:"back_extrinsic_yaw_deg,omitempty"`
	BackExtrinsicPitchDeg float64 `json:"back_extrinsic_pitch_deg,omitempty"`
	BackExtrinsicRollDeg  float64 `json:"back_extrinsic_roll_deg,omitempty"`

	// LensModel: "equisolid" (default; suits most 360 fisheye lenses) or
	// "equidistant". Unknown values fall through to equisolid.
	LensModel string `json:"lens_model,omitempty"`

	// PinholeWidth/Height: dimensions of the virtual pinhole frame.
	PinholeWidth  int `json:"pinhole_width,omitempty"`
	PinholeHeight int `json:"pinhole_height,omitempty"`
	// PinholeFOVDeg: horizontal field of view of the virtual pinhole.
	PinholeFOVDeg float64 `json:"pinhole_fov_deg,omitempty"`
	// InitialYaw/Pitch: starting aim of the virtual pinhole, in degrees.
	InitialYaw   float64 `json:"initial_yaw_deg,omitempty"`
	InitialPitch float64 `json:"initial_pitch_deg,omitempty"`
}

// Validate accepts any combination of fields; we apply defaults at
// construction. We return no dependencies — this camera doesn't reference
// other Viam resources.
func (cfg *AmbarellaConfig) Validate(path string) ([]string, []string, error) {
	if cfg.PinholeWidth < 0 || cfg.PinholeHeight < 0 {
		return nil, nil, fmt.Errorf("%s: pinhole_width/height must be non-negative", path)
	}
	if cfg.PinholeFOVDeg < 0 || cfg.PinholeFOVDeg >= 180 {
		return nil, nil, fmt.Errorf("%s: pinhole_fov_deg must be in [0, 180)", path)
	}
	return nil, nil, nil
}

type ambarellaCamera struct {
	resource.AlwaysRebuild

	name   resource.Name
	logger logging.Logger
	cfg    *AmbarellaConfig

	session   *Session
	capture   *Capture
	stitcher  *FisheyeStitcher
	projector *PinholeProjector

	cancelCtx  context.Context
	cancelFunc context.CancelFunc
	wg         sync.WaitGroup
}

func newAmbarellaCamera(ctx context.Context, _ resource.Dependencies, rawConf resource.Config, logger logging.Logger) (camera.Camera, error) {
	conf, err := resource.NativeConfig[*AmbarellaConfig](rawConf)
	if err != nil {
		return nil, err
	}
	return NewAmbarellaCamera(ctx, rawConf.ResourceName(), conf, logger)
}

// NewAmbarellaCamera is exposed for the CLI smoke test in cmd/cli/main.go; the regular
// module path goes through newAmbarellaCamera.
func NewAmbarellaCamera(ctx context.Context, name resource.Name, conf *AmbarellaConfig, logger logging.Logger) (camera.Camera, error) {
	host := conf.Host
	if host == "" {
		host = defaultHost
	}
	pw := conf.PinholeWidth
	if pw == 0 {
		pw = defaultPinholeWidth
	}
	ph := conf.PinholeHeight
	if ph == 0 {
		ph = defaultPinholeHeight
	}
	pfov := conf.PinholeFOVDeg
	if pfov == 0 {
		pfov = defaultPinholeFOV
	}
	erpW := conf.ERPWidth
	if erpW == 0 {
		erpW = defaultERPWidth
	}
	erpH := conf.ERPHeight
	if erpH == 0 {
		erpH = defaultERPHeight
	}
	defFront, defBack := DefaultLenses()
	front := defFront
	if conf.FrontLens != nil {
		front = *conf.FrontLens
	}
	back := defBack
	if conf.BackLens != nil {
		back = *conf.BackLens
	}

	logger.Infow("opening 360 camera over RTSP", "host", host)
	session, err := DialSession(ctx, host, defaultAmbarellaPort, logger)
	if err != nil {
		return nil, fmt.Errorf("ambarella session: %w", err)
	}
	logger.Infow("ambarella session established; opening rtsp", "url", session.RTSPURL())

	capture, err := NewCaptureFromRTSP(ctx, session.RTSPURL(), logger)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("rtsp capture: %w", err)
	}

	// The dual-fisheye source frame is 1920×960 per the camera's RTSP
	// preview; the stitcher's LUT is tied to that size. If a future
	// firmware ships a different size, the stitcher's Apply path will
	// produce a garbled result and we'd need to rebuild — handle that when
	// it happens rather than over-engineering now.
	lensModel := LensEquisolid
	if conf.LensModel == "equidistant" {
		lensModel = LensEquidistant
	}
	stitcher := BuildFisheyeStitcherOpts(front, back, 1920, 960, erpW, erpH, StitcherOpts{
		SeamFeatherDeg:        conf.SeamFeatherDeg,
		BackExtrinsicYawDeg:   conf.BackExtrinsicYawDeg,
		BackExtrinsicPitchDeg: conf.BackExtrinsicPitchDeg,
		BackExtrinsicRollDeg:  conf.BackExtrinsicRollDeg,
		LensModel:             lensModel,
	})

	view := PinholeView{
		Yaw:    conf.InitialYaw,
		Pitch:  conf.InitialPitch,
		FOVDeg: pfov,
		Width:  pw,
		Height: ph,
	}
	projector := NewPinholeProjector(view, erpW, erpH)

	cctx, cancel := context.WithCancel(context.Background())
	c := &ambarellaCamera{
		name:       name,
		logger:     logger,
		cfg:        conf,
		session:    session,
		capture:    capture,
		stitcher:   stitcher,
		projector:  projector,
		cancelCtx:  cctx,
		cancelFunc: cancel,
	}
	c.wg.Add(1)
	go c.heartbeatLoop()
	return c, nil
}

// heartbeatLoop polls the control session so the camera doesn't decide the
// client went away. The probes showed the Ambarella firmware tears down the
// preview if the control socket goes idle (the symptom is RTSP suddenly
// 404'ing); a get_settings every few seconds is cheap and keeps the session
// warm.
func (c *ambarellaCamera) heartbeatLoop() {
	defer c.wg.Done()
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-c.cancelCtx.Done():
			return
		case <-t.C:
			if err := c.session.Heartbeat(); err != nil {
				c.logger.Warnw("ambarella heartbeat failed", "err", err)
			}
		}
	}
}

func (c *ambarellaCamera) Name() resource.Name { return c.name }

func (c *ambarellaCamera) Images(ctx context.Context, filterSourceNames []string, _ map[string]interface{}) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	// Block briefly for the first frame on a cold start. After that, Latest()
	// always returns immediately with the most recent frame.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.capture.WaitFirstFrame(waitCtx); err != nil {
		return nil, resource.ResponseMetadata{}, fmt.Errorf("waiting for first frame: %w", err)
	}
	raw, err := c.capture.Latest()
	if err != nil {
		return nil, resource.ResponseMetadata{}, err
	}

	want := func(name string) bool {
		if len(filterSourceNames) == 0 {
			return true
		}
		for _, n := range filterSourceNames {
			if n == name {
				return true
			}
		}
		return false
	}

	// Stitching is the expensive step (~erpW*erpH samples); only run it if
	// some downstream consumer actually needs ERP or pinhole output.
	var erp *image.RGBA
	needERP := want(SourceEquirectangular) || want(SourcePinhole)
	if needERP {
		erp = c.stitcher.StitchToERP(raw)
	}

	var out []camera.NamedImage
	add := func(img image.Image, name string) error {
		ni, nerr := camera.NamedImageFromImage(img, name, utils.MimeTypeJPEG, data.Annotations{})
		if nerr != nil {
			return nerr
		}
		out = append(out, ni)
		return nil
	}
	if want(SourceEquirectangular) {
		xmp := `
		<x:xmpmeta xmlns:x="adobe:ns:meta/">
		<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
		<rdf:Description xmlns:viam="https://www.viam.com/ns/1.0/">
		<viam:is360>true</viam:is360>
		</rdf:Description>
		</rdf:RDF>
		</x:xmpmeta>
		`
		imagebytes, err := encodeJPEG(erp)
		if err != nil {
			return nil, resource.ResponseMetadata{}, fmt.Errorf("failed to encode equirectangular jpeg: %w", err)
		}
		imagebytes, err = addXMPToJPEG(imagebytes, xmp)
		if err != nil {
			return nil, resource.ResponseMetadata{}, err
		}
		namedImg, err := camera.NamedImageFromBytes(imagebytes, SourceEquirectangular, utils.MimeTypeJPEG, data.Annotations{})
		if err != nil {
			return nil, resource.ResponseMetadata{}, fmt.Errorf("failed to create named image: %w", err)
		}
		out = append(out, namedImg)
	}
	if want(SourceRaw) {
		if err := add(raw, SourceRaw); err != nil {
			return nil, resource.ResponseMetadata{}, err
		}
	}
	if want(SourceFront) {
		if err := add(c.stitcher.HalfFrame(raw, "front"), SourceFront); err != nil {
			return nil, resource.ResponseMetadata{}, err
		}
	}
	if want(SourceBack) {
		if err := add(c.stitcher.HalfFrame(raw, "back"), SourceBack); err != nil {
			return nil, resource.ResponseMetadata{}, err
		}
	}
	if want(SourcePinhole) {
		flat := c.projector.Project(erp)
		if err := add(flat, SourcePinhole); err != nil {
			return nil, resource.ResponseMetadata{}, err
		}
	}
	return out, resource.ResponseMetadata{CapturedAt: time.Now()}, nil
}

func (c *ambarellaCamera) NextPointCloud(_ context.Context, _ map[string]interface{}) (pointcloud.PointCloud, error) {
	return nil, errNotSupported
}

func (c *ambarellaCamera) Properties(_ context.Context) (camera.Properties, error) {
	return camera.Properties{
		SupportsPCD: false,
		ImageType:   camera.ColorStream,
		MimeTypes:   []string{utils.MimeTypeJPEG},
		FrameRate:   29.97,
	}, nil
}

func (c *ambarellaCamera) Stream(_ context.Context, _ ...gostream.ErrorHandler) (gostream.VideoStream, error) {
	reader := gostream.VideoReaderFunc(func(ctx context.Context) (image.Image, func(), error) {
		if err := c.capture.WaitFirstFrame(ctx); err != nil {
			return nil, nil, err
		}
		raw, err := c.capture.Latest()
		if err != nil {
			return nil, nil, err
		}
		return c.stitcher.StitchToERP(raw), func() {}, nil
	})
	return gostream.NewEmbeddedVideoStreamFromReader(reader), nil
}

func (c *ambarellaCamera) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	if v, ok := cmd["set_pinhole"]; ok {
		params, ok := v.(map[string]interface{})
		if !ok {
			return nil, errors.New("set_pinhole: expected object")
		}
		yaw, pitch, fov := c.projector.View()
		if y, ok := params["yaw_deg"].(float64); ok {
			yaw = y
		}
		if p, ok := params["pitch_deg"].(float64); ok {
			pitch = p
		}
		if f, ok := params["fov_deg"].(float64); ok {
			if f <= 0 || f >= 180 {
				return nil, fmt.Errorf("fov_deg must be in (0, 180), got %v", f)
			}
			fov = f
		}
		c.projector.SetView(yaw, pitch, fov)
		return map[string]interface{}{"yaw_deg": yaw, "pitch_deg": pitch, "fov_deg": fov}, nil
	}
	if _, ok := cmd["get_pinhole"]; ok {
		yaw, pitch, fov := c.projector.View()
		return map[string]interface{}{"yaw_deg": yaw, "pitch_deg": pitch, "fov_deg": fov}, nil
	}
	if v, ok := cmd["set_stitch"]; ok {
		params, ok := v.(map[string]interface{})
		if !ok {
			return nil, errors.New("set_stitch: expected object")
		}
		p := c.stitcher.Params()
		if err := applyStitchUpdate(&p, params); err != nil {
			return nil, fmt.Errorf("set_stitch: %w", err)
		}
		c.stitcher.Reconfigure(p)
		return stitchParamsToMap(p), nil
	}
	if _, ok := cmd["get_stitch"]; ok {
		return stitchParamsToMap(c.stitcher.Params()), nil
	}
	return nil, fmt.Errorf("unknown command; supported: set_pinhole, get_pinhole, set_stitch, get_stitch")
}

// applyStitchUpdate merges a partial DoCommand payload onto an existing
// StitchParams. Only specified fields change; unspecified fields are left
// alone. Returns an error if a value has an unexpected type or out-of-range
// value.
func applyStitchUpdate(p *StitchParams, params map[string]interface{}) error {
	getF := func(key string, dst *float64) error {
		v, ok := params[key]
		if !ok {
			return nil
		}
		f, ok := v.(float64)
		if !ok {
			return fmt.Errorf("%s: expected number, got %T", key, v)
		}
		*dst = f
		return nil
	}
	for _, err := range []error{
		getF("seam_feather_deg", &p.Opts.SeamFeatherDeg),
		getF("back_extrinsic_yaw_deg", &p.Opts.BackExtrinsicYawDeg),
		getF("back_extrinsic_pitch_deg", &p.Opts.BackExtrinsicPitchDeg),
		getF("back_extrinsic_roll_deg", &p.Opts.BackExtrinsicRollDeg),
	} {
		if err != nil {
			return err
		}
	}
	if v, ok := params["lens_model"]; ok {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("lens_model: expected string, got %T", v)
		}
		switch s {
		case "equisolid":
			p.Opts.LensModel = LensEquisolid
		case "equidistant":
			p.Opts.LensModel = LensEquidistant
		default:
			return fmt.Errorf("lens_model: must be \"equisolid\" or \"equidistant\", got %q", s)
		}
	}
	if v, ok := params["front_lens"]; ok {
		if err := applyLensUpdate(&p.Front, v); err != nil {
			return fmt.Errorf("front_lens: %w", err)
		}
	}
	if v, ok := params["back_lens"]; ok {
		if err := applyLensUpdate(&p.Back, v); err != nil {
			return fmt.Errorf("back_lens: %w", err)
		}
	}
	return nil
}

func applyLensUpdate(lens *FisheyeLens, v interface{}) error {
	m, ok := v.(map[string]interface{})
	if !ok {
		return fmt.Errorf("expected object, got %T", v)
	}
	getF := func(key string, dst *float64) error {
		v, ok := m[key]
		if !ok {
			return nil
		}
		f, ok := v.(float64)
		if !ok {
			return fmt.Errorf("%s: expected number, got %T", key, v)
		}
		*dst = f
		return nil
	}
	for _, err := range []error{
		getF("center_x", &lens.CenterX),
		getF("center_y", &lens.CenterY),
		getF("radius", &lens.Radius),
		getF("fov_deg", &lens.FOVDeg),
		getF("rotation_deg", &lens.RotationDeg),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

func stitchParamsToMap(p StitchParams) map[string]interface{} {
	lensModel := "equisolid"
	if p.Opts.LensModel == LensEquidistant {
		lensModel = "equidistant"
	}
	return map[string]interface{}{
		"seam_feather_deg":         p.Opts.SeamFeatherDeg,
		"back_extrinsic_yaw_deg":   p.Opts.BackExtrinsicYawDeg,
		"back_extrinsic_pitch_deg": p.Opts.BackExtrinsicPitchDeg,
		"back_extrinsic_roll_deg":  p.Opts.BackExtrinsicRollDeg,
		"lens_model":               lensModel,
		"front_lens": map[string]interface{}{
			"center_x":     p.Front.CenterX,
			"center_y":     p.Front.CenterY,
			"radius":       p.Front.Radius,
			"fov_deg":      p.Front.FOVDeg,
			"rotation_deg": p.Front.RotationDeg,
		},
		"back_lens": map[string]interface{}{
			"center_x":     p.Back.CenterX,
			"center_y":     p.Back.CenterY,
			"radius":       p.Back.Radius,
			"fov_deg":      p.Back.FOVDeg,
			"rotation_deg": p.Back.RotationDeg,
		},
	}
}

func (c *ambarellaCamera) Geometries(_ context.Context, _ map[string]interface{}) ([]spatialmath.Geometry, error) {
	return []spatialmath.Geometry{}, nil
}

func (c *ambarellaCamera) Status(_ context.Context) (map[string]interface{}, error) {
	yaw, pitch, fov := c.projector.View()
	return map[string]interface{}{
		"rtsp_url":  c.session.RTSPURL(),
		"yaw_deg":   yaw,
		"pitch_deg": pitch,
		"fov_deg":   fov,
	}, nil
}

func (c *ambarellaCamera) SubscribeRTP(_ context.Context, _ int, _ rtppassthrough.PacketCallback) (rtppassthrough.Subscription, error) {
	return rtppassthrough.Subscription{}, errNotSupported
}

func (c *ambarellaCamera) Unsubscribe(_ context.Context, _ rtppassthrough.SubscriptionID) error {
	return errNotSupported
}

func (c *ambarellaCamera) Close(_ context.Context) error {
	c.cancelFunc()
	c.wg.Wait()
	var firstErr error
	if err := c.capture.Close(); err != nil {
		firstErr = err
	}
	if err := c.session.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// encodeJPEG is a small helper used by the CLI smoke test (cmd/cli/main.go) to
// dump frames to disk for manual inspection. Kept here so the helper has
// access to the same JPEG encoder settings the module uses internally.
func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addXMPToJPEG(jpeg []byte, xmpXML string) ([]byte, error) {
	// JPEG SOI marker
	if len(jpeg) < 2 || jpeg[0] != 0xFF || jpeg[1] != 0xD8 {
		return nil, fmt.Errorf("not a jpeg")
	}

	// XMP APP1 marker format
	xmpHeader := []byte("http://ns.adobe.com/xap/1.0/\x00")

	payload := append(xmpHeader, []byte(xmpXML)...)

	segmentLength := len(payload) + 2

	app1 := []byte{
		0xFF, 0xE1,
		byte(segmentLength >> 8),
		byte(segmentLength & 0xFF),
	}

	app1 = append(app1, payload...)

	var out bytes.Buffer

	// Write SOI
	out.Write(jpeg[:2])

	// Insert APP1 segment immediately after SOI
	out.Write(app1)

	// Rest of JPEG
	out.Write(jpeg[2:])

	return out.Bytes(), nil
}
