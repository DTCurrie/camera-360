package camera360

// Discovery service for UVC webcams. It detects connected USB cameras (see
// enumerate.go) and returns ready-to-paste configs for this module's
// jvcu360-camera and jvcu360-mic models with the correct device handles already
// filled in — most importantly the right /dev/videoN, which the user otherwise
// has to find by hand and which defaults wrongly on a Raspberry Pi.
//
// Modeled on viam/find-webcams, but pure-Go (no pion/mediadevices): the heavy
// lifting is the sysfs enumeration in enumerate.go.

import (
	"context"
	"fmt"
	"strings"

	"go.viam.com/rdk/components/audioin"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/discovery"
	"go.viam.com/rdk/utils"
)

// Discovery is the model identifier for the UVC webcam discovery service.
var Discovery = resource.NewModel("dtcurrie", "camera-360", "discovery")

func init() {
	resource.RegisterService(discovery.API, Discovery,
		resource.Registration[discovery.Service, *DiscoveryConfig]{
			Constructor: newDiscovery,
		},
	)
}

// DiscoveryConfig is the user-supplied JSON config; all fields are optional.
type DiscoveryConfig struct {
	// IncludeMic emits a jvcu360-mic config alongside each camera that has a USB
	// sound card. Pointer so the default is true (an absent field means "yes").
	IncludeMic *bool `json:"include_mic,omitempty"`
	// IncludeAllUVC returns every confirmed UVC webcam, not just those classified
	// as 360/fisheye. Default false (360-only) — flip it on as a fallback when
	// best-effort 360 detection misses a real 360 camera.
	IncludeAllUVC bool `json:"include_all_uvc,omitempty"`
	// NamePrefix overrides the base name for emitted configs (the device's
	// product string is used when empty). Multiple devices get -1, -2 … suffixes.
	NamePrefix string `json:"name_prefix,omitempty"`
}

// Validate has no work to do: every field is optional and there are no
// dependencies on other resources.
func (cfg *DiscoveryConfig) Validate(_ string) ([]string, []string, error) {
	return nil, nil, nil
}

type uvcDiscovery struct {
	resource.Named
	resource.TriviallyReconfigurable
	resource.TriviallyCloseable

	logger        logging.Logger
	includeMic    bool
	includeAllUVC bool
	namePrefix    string
	// enumerate is the detection function; a field so tests can stub it.
	enumerate func(context.Context, logging.Logger) ([]DiscoveredWebcam, error)
}

func newDiscovery(_ context.Context, _ resource.Dependencies, rawConf resource.Config, logger logging.Logger) (discovery.Service, error) {
	conf, err := resource.NativeConfig[*DiscoveryConfig](rawConf)
	if err != nil {
		return nil, err
	}
	includeMic := true
	if conf.IncludeMic != nil {
		includeMic = *conf.IncludeMic
	}
	return &uvcDiscovery{
		Named:         rawConf.ResourceName().AsNamed(),
		logger:        logger,
		includeMic:    includeMic,
		includeAllUVC: conf.IncludeAllUVC,
		namePrefix:    conf.NamePrefix,
		enumerate:     EnumerateUVCWebcams,
	}, nil
}

// DiscoverResources enumerates UVC webcams and returns a jvcu360-camera config for
// each (plus a jvcu360-mic config when the device has a mic and include_mic is set).
// An empty result is not an error — it just means nothing matched.
func (d *uvcDiscovery) DiscoverResources(ctx context.Context, _ map[string]any) ([]resource.Config, error) {
	webcams, err := d.enumerate(ctx, d.logger)
	if err != nil {
		return nil, err
	}

	kept := webcams
	if !d.includeAllUVC {
		kept = make([]DiscoveredWebcam, 0, len(webcams))
		for _, w := range webcams {
			if w.LensHint != "" {
				kept = append(kept, w)
			}
		}
		if len(kept) == 0 && len(webcams) > 0 {
			d.logger.Infow("found UVC webcam(s) but none classified as 360/fisheye; "+
				"set include_all_uvc=true to include them", "uvc_count", len(webcams))
		}
	}

	taken := map[string]bool{}
	var out []resource.Config
	for _, w := range kept {
		base := d.namePrefix
		if base == "" {
			base = sanitizeName(w.Label)
		}
		name := uniqueName(base, taken)
		camCfg, err := cameraConfigFor(name, w)
		if err != nil {
			return nil, err
		}
		out = append(out, camCfg)

		if d.includeMic && w.AudioDevice != "" {
			micCfg, err := micConfigFor(uniqueName(name+"-mic", taken), w)
			if err != nil {
				return nil, err
			}
			out = append(out, micCfg)
		}
	}
	return out, nil
}

// DoCommand is unused by this service.
func (d *uvcDiscovery) DoCommand(_ context.Context, _ map[string]any) (map[string]any, error) {
	return nil, ErrNotSupported
}

// cameraConfigFor builds a jvcu360-camera component config for a discovered
// webcam. Only video_device is set (the rest fall back to the model's defaults),
// so the result is a ready-to-apply config with no fields the model can't read.
// We emit a plain Attributes map rather than a typed ConvertedAttributes so this
// shared package needn't import the model subpackage (which imports it back) —
// the module decodes the attributes into the model's config at construction.
func cameraConfigFor(name string, w DiscoveredWebcam) (resource.Config, error) {
	return resource.Config{
		Name:       name,
		API:        camera.API,
		Model:      JVCU360Camera,
		Attributes: utils.AttributeMap{"video_device": w.VideoDevice},
	}, nil
}

// micConfigFor builds a jvcu360-mic config for a discovered webcam's microphone.
func micConfigFor(name string, w DiscoveredWebcam) (resource.Config, error) {
	return resource.Config{
		Name:       name,
		API:        audioin.API,
		Model:      JVCU360Mic,
		Attributes: utils.AttributeMap{"audio_device": w.AudioDevice},
	}, nil
}

// sanitizeName lowercases s and keeps only [a-z0-9-], collapsing runs of other
// characters to a single dash, so it's a valid Viam resource name.
func sanitizeName(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// uniqueName returns base (or "jvcu360-camera" if empty), suffixing -1, -2 … until
// it's unused, and records the result in taken.
func uniqueName(base string, taken map[string]bool) string {
	if base == "" {
		base = "jvcu360-camera"
	}
	name := base
	for n := 1; taken[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	taken[name] = true
	return name
}
