package camera360

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"go.viam.com/rdk/components/audioin"
	"go.viam.com/rdk/logging"
)

func TestMicConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     MicConfig
		wantErr bool
	}{
		{"empty defaults ok", MicConfig{}, false},
		{"explicit values ok", MicConfig{AudioDevice: "plughw:1,0", SampleRateHz: 48000, NumChannels: 1}, false},
		{"negative sample rate", MicConfig{SampleRateHz: -1}, true},
		{"negative channels", MicConfig{NumChannels: -1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, opt, err := tc.cfg.Validate("test")
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req != nil || opt != nil {
				t.Errorf("expected no dependencies, got required=%v optional=%v", req, opt)
			}
		})
	}
}

func TestAudioInputArgs(t *testing.T) {
	args := audioInputArgs("plughw:1,0")
	if len(args) < 2 || args[len(args)-2] != "-i" || args[len(args)-1] != "plughw:1,0" {
		t.Fatalf("expected args to end with -i plughw:1,0, got %v", args)
	}
	if !(slices.Contains(args, "alsa") || slices.Contains(args, "avfoundation")) {
		t.Errorf("expected an input format (alsa/avfoundation) in args, got %v", args)
	}
}

// testMic builds a mic with a tiny sample rate so chunks are small and the
// arithmetic is easy to check: 1000Hz mono, 100ms chunks => 100 samples =>
// 200 bytes (s16le) per chunk.
func testMic(t *testing.T) *jvcu360Mic {
	t.Helper()
	return &jvcu360Mic{logger: logging.NewTestLogger(t), sampleRate: 1000, numChannels: 1}
}

const testBytesPerChunk = 200 // 1000Hz * 0.1s * 2 bytes * 1 channel

func TestStreamChunksSlicesPCM(t *testing.T) {
	m := testMic(t)
	const numChunks = 3
	pcm := make([]byte, testBytesPerChunk*numChunks)
	for i := range pcm {
		pcm[i] = byte(i)
	}

	out := make(chan *audioin.AudioChunk, 8)
	// durationSeconds=0 reads until the in-memory reader is exhausted, which
	// surfaces as a clean EOF after the last full chunk.
	err := m.streamChunks(context.Background(), bytes.NewReader(pcm), out, 0, 0)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after exhausting reader, got %v", err)
	}
	close(out)

	var got []*audioin.AudioChunk
	for c := range out {
		got = append(got, c)
	}
	if len(got) != numChunks {
		t.Fatalf("expected %d chunks, got %d", numChunks, len(got))
	}
	const chunkNs = int64(100) * 1e6
	for i, c := range got {
		if c.Sequence != int32(i) {
			t.Errorf("chunk %d: sequence = %d, want %d", i, c.Sequence, i)
		}
		if len(c.AudioData) != testBytesPerChunk {
			t.Errorf("chunk %d: %d bytes, want %d", i, len(c.AudioData), testBytesPerChunk)
		}
		if c.AudioInfo == nil || c.AudioInfo.Codec != "pcm16" || c.AudioInfo.SampleRateHz != 1000 || c.AudioInfo.NumChannels != 1 {
			t.Errorf("chunk %d: unexpected AudioInfo %+v", i, c.AudioInfo)
		}
		if c.EndTimestampNanoseconds-c.StartTimestampNanoseconds != chunkNs {
			t.Errorf("chunk %d: duration = %dns, want %dns", i, c.EndTimestampNanoseconds-c.StartTimestampNanoseconds, chunkNs)
		}
		if i > 0 && c.StartTimestampNanoseconds-got[i-1].StartTimestampNanoseconds != chunkNs {
			t.Errorf("chunk %d: start delta = %dns, want %dns", i, c.StartTimestampNanoseconds-got[i-1].StartTimestampNanoseconds, chunkNs)
		}
	}
}

func TestStreamChunksRespectsDuration(t *testing.T) {
	m := testMic(t)
	pcm := make([]byte, testBytesPerChunk*10) // more than enough data

	out := make(chan *audioin.AudioChunk, 16)
	// 0.25s at 100ms/chunk => ceil(2.5) => 3 chunks, then a clean nil return.
	if err := m.streamChunks(context.Background(), bytes.NewReader(pcm), out, 0.25, 0); err != nil {
		t.Fatalf("expected nil after duration reached, got %v", err)
	}
	close(out)
	n := 0
	for range out {
		n++
	}
	if n != 3 {
		t.Fatalf("expected 3 chunks for 0.25s, got %d", n)
	}
}

func TestStreamChunksUsesPreviousTimestamp(t *testing.T) {
	m := testMic(t)
	const base = int64(5_000_000_000) // resume at t=5s
	out := make(chan *audioin.AudioChunk, 4)
	_ = m.streamChunks(context.Background(), bytes.NewReader(make([]byte, testBytesPerChunk)), out, 0, base)
	close(out)
	c := <-out
	if c == nil || c.StartTimestampNanoseconds != base {
		t.Fatalf("expected first chunk start = %d, got %+v", base, c)
	}
}
