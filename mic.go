package camera360

import (
	"context"
	"fmt"
	"io"
	"time"

	goutils "go.viam.com/utils"

	"go.viam.com/rdk/components/audioin"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
	"go.viam.com/rdk/utils"
)

// JVCU360Mic is the model for the JVCU360's built-in omnidirectional
// microphone, which enumerates as a standard USB Audio Class (UAC) capture
// device. It implements the RDK audio_in API by shelling out to ffmpeg for PCM.
var JVCU360Mic = resource.NewModel("dtcurrie", "camera-360", "jvcu360-mic")

const (
	micDefaultSampleRate  = 48000
	micDefaultNumChannels = 1
	micCodec              = "pcm16"
	micChunkDurationMs    = 100 // PCM is emitted in 100ms chunks, matching the RDK fake audio_in.
)

func init() {
	resource.RegisterComponent(audioin.API, JVCU360Mic,
		resource.Registration[audioin.AudioIn, *MicConfig]{
			Constructor: newJVCU360Mic,
		},
	)
}

// MicConfig is the user-supplied JSON config; all fields are optional.
type MicConfig struct {
	// AudioDevice is the OS handle for the mic: an ALSA device such as
	// "plughw:1,0" on Linux, or an avfoundation audio index like ":0" on macOS.
	// Empty uses the per-OS default — note that on Linux that is the system
	// default input, so set this explicitly to the JVCU360's card.
	AudioDevice string `json:"audio_device,omitempty"`
	// SampleRateHz / NumChannels request a capture format. The JVCU360 mic is
	// mono; 48000 Hz is a safe default.
	SampleRateHz int `json:"sample_rate_hz,omitempty"`
	NumChannels  int `json:"num_channels,omitempty"`
}

// Validate rejects negative values; defaults are applied at construction. No
// dependencies — this mic doesn't reference other resources.
func (cfg *MicConfig) Validate(path string) ([]string, []string, error) {
	if cfg.SampleRateHz < 0 {
		return nil, nil, fmt.Errorf("%s: sample_rate_hz must be non-negative", path)
	}
	if cfg.NumChannels < 0 {
		return nil, nil, fmt.Errorf("%s: num_channels must be non-negative", path)
	}
	return nil, nil, nil
}

type jvcu360Mic struct {
	resource.Named
	resource.AlwaysRebuild

	logger      logging.Logger
	audioDevice string
	sampleRate  int
	numChannels int
	workers     goutils.StoppableWorkers
}

func newJVCU360Mic(ctx context.Context, _ resource.Dependencies, rawConf resource.Config, logger logging.Logger) (audioin.AudioIn, error) {
	conf, err := resource.NativeConfig[*MicConfig](rawConf)
	if err != nil {
		return nil, err
	}
	return NewMic(ctx, rawConf.ResourceName(), conf, logger)
}

// NewMic is exposed for the discovery CLI in cmd/uvc/main.go; the regular
// module path goes through newJVCU360Mic.
func NewMic(_ context.Context, name resource.Name, conf *MicConfig, logger logging.Logger) (audioin.AudioIn, error) {
	device := conf.AudioDevice
	if device == "" {
		device = defaultAudioDevice()
	}
	sampleRate := conf.SampleRateHz
	if sampleRate == 0 {
		sampleRate = micDefaultSampleRate
	}
	numChannels := conf.NumChannels
	if numChannels == 0 {
		numChannels = micDefaultNumChannels
	}
	return &jvcu360Mic{
		Named:       name.AsNamed(),
		logger:      logger,
		audioDevice: device,
		sampleRate:  sampleRate,
		numChannels: numChannels,
		workers:     *goutils.NewBackgroundStoppableWorkers(),
	}, nil
}

// GetAudio starts an ffmpeg PCM capture and streams fixed-size chunks on the
// returned channel until durationSeconds elapses (0 = until the request context
// or the component is closed). codec must be "pcm16" (the only format we emit).
func (m *jvcu360Mic) GetAudio(reqCtx context.Context, codec string, durationSeconds float32, previousTimestampNs int64, _ map[string]interface{}) (chan *audioin.AudioChunk, error) {
	if codec != "" && codec != micCodec {
		return nil, fmt.Errorf("unsupported codec %q; only %q is supported", codec, micCodec)
	}
	chunkChan := make(chan *audioin.AudioChunk)
	m.workers.Add(func(workerCtx context.Context) {
		defer close(chunkChan)
		// Stop when EITHER the component closes (workerCtx, via Close→Stop) or
		// the client cancels/ends the request (reqCtx).
		ctx, cancel := context.WithCancel(workerCtx)
		defer cancel()
		go func() {
			select {
			case <-reqCtx.Done():
				cancel()
			case <-ctx.Done():
			}
		}()
		if err := m.streamPCM(ctx, chunkChan, durationSeconds, previousTimestampNs); err != nil && ctx.Err() == nil {
			m.logger.Warnw("audio capture ended", "err", err)
		}
	})
	return chunkChan, nil
}

// streamPCM owns the ffmpeg subprocess for one GetAudio call and feeds its PCM
// output to streamChunks.
func (m *jvcu360Mic) streamPCM(ctx context.Context, out chan<- *audioin.AudioChunk, durationSeconds float32, previousTimestampNs int64) error {
	ac, err := NewAudioCapture(ctx, audioInputArgs(m.audioDevice), m.sampleRate, m.numChannels, m.logger)
	if err != nil {
		return err
	}
	defer ac.Close()
	return m.streamChunks(ctx, ac, out, durationSeconds, previousTimestampNs)
}

// streamChunks slices a raw s16le PCM stream into fixed-duration AudioChunks
// with monotonic timestamps. It is split out from streamPCM so it can be tested
// against an in-memory reader without ffmpeg or a real device.
func (m *jvcu360Mic) streamChunks(ctx context.Context, r io.Reader, out chan<- *audioin.AudioChunk, durationSeconds float32, previousTimestampNs int64) error {
	samplesPerChunk := m.sampleRate * micChunkDurationMs / 1000
	bytesPerChunk := samplesPerChunk * 2 * m.numChannels // s16le => 2 bytes per sample per channel
	chunkDurationNs := int64(micChunkDurationMs) * 1e6

	// Base the chunk timeline on previousTimestampNs when resuming, else now.
	baseNs := time.Now().UnixNano()
	if previousTimestampNs > 0 {
		baseNs = previousTimestampNs
	}

	totalChunks := int64(-1) // -1 == stream until ctx ends or the reader is exhausted
	if durationSeconds > 0 {
		totalChunks = int64(float64(durationSeconds)*1000/float64(micChunkDurationMs) + 0.999)
	}

	buf := make([]byte, bytesPerChunk)
	var sequence int32
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if totalChunks >= 0 && int64(sequence) >= totalChunks {
			return nil
		}
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		startNs := baseNs + int64(sequence)*chunkDurationNs
		chunk := &audioin.AudioChunk{
			AudioData: append([]byte(nil), buf...), // copy: buf is reused next iteration
			AudioInfo: &utils.AudioInfo{
				Codec:        micCodec,
				SampleRateHz: int32(m.sampleRate),
				NumChannels:  int32(m.numChannels),
			},
			Sequence:                  sequence,
			StartTimestampNanoseconds: startNs,
			EndTimestampNanoseconds:   startNs + chunkDurationNs,
		}
		select {
		case out <- chunk:
			sequence++
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *jvcu360Mic) Properties(_ context.Context, _ map[string]interface{}) (utils.Properties, error) {
	return utils.Properties{
		SupportedCodecs: []string{micCodec},
		SampleRateHz:    int32(m.sampleRate),
		NumChannels:     int32(m.numChannels),
	}, nil
}

// Geometries satisfies resource.Shaped for parity with the reference audio_in
// implementation; the mic has no meaningful geometry.
func (m *jvcu360Mic) Geometries(_ context.Context, _ map[string]interface{}) ([]spatialmath.Geometry, error) {
	return []spatialmath.Geometry{}, nil
}

func (m *jvcu360Mic) Close(_ context.Context) error {
	m.workers.Stop()
	return nil
}
