package camera360

import (
	"bufio"
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"strings"
	"testing"

	"go.viam.com/rdk/logging"
)

const xmpSig = "http://ns.adobe.com/xap/1.0/\x00"

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	b, err := encodeJPEG(img)
	if err != nil {
		t.Fatalf("encodeJPEG: %v", err)
	}
	return b
}

func TestAddXMPToJPEG_RejectsNonJPEG(t *testing.T) {
	_, err := addXMPToJPEG([]byte("not a jpeg"), "<x/>")
	if err == nil {
		t.Fatal("expected error for non-jpeg input, got nil")
	}
	if !strings.Contains(err.Error(), "not a jpeg") {
		t.Errorf("error should mention 'not a jpeg', got %q", err.Error())
	}

	if _, err := addXMPToJPEG(nil, "<x/>"); err == nil {
		t.Error("expected error for nil input")
	}
	if _, err := addXMPToJPEG([]byte{0xFF}, "<x/>"); err == nil {
		t.Error("expected error for 1-byte input")
	}
}

func TestAddXMPToJPEG_OutputStillJPEG(t *testing.T) {
	src := tinyJPEG(t)
	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><viam:is360>true</viam:is360></x:xmpmeta>`

	out, err := addXMPToJPEG(src, xmp)
	if err != nil {
		t.Fatalf("addXMPToJPEG: %v", err)
	}
	if len(out) < 2 || out[0] != 0xFF || out[1] != 0xD8 {
		t.Fatalf("output missing SOI marker: %x", out[:min(4, len(out))])
	}
	// Re-decode to confirm the JPEG is still valid after insertion.
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("jpeg.Decode of XMP'd output: %v", err)
	}
}

func TestAddXMPToJPEG_ContainsAPP1AndPayload(t *testing.T) {
	src := tinyJPEG(t)
	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><viam:is360>true</viam:is360></x:xmpmeta>`

	out, err := addXMPToJPEG(src, xmp)
	if err != nil {
		t.Fatalf("addXMPToJPEG: %v", err)
	}

	// APP1 marker (FF E1) should appear immediately after SOI.
	if out[2] != 0xFF || out[3] != 0xE1 {
		t.Errorf("APP1 marker not at offset 2-3: got %x %x", out[2], out[3])
	}
	if !bytes.Contains(out, []byte(xmpSig)) {
		t.Error("output missing XMP namespace identifier")
	}
	if !bytes.Contains(out, []byte(xmp)) {
		t.Error("output missing XMP body")
	}
}

func TestAddXMPToJPEG_SegmentLengthCorrect(t *testing.T) {
	src := tinyJPEG(t)
	xmp := "<x:xmpmeta>some content</x:xmpmeta>"

	out, err := addXMPToJPEG(src, xmp)
	if err != nil {
		t.Fatalf("addXMPToJPEG: %v", err)
	}

	// Bytes 4-5 are the APP1 segment length (big-endian), and per the JPEG
	// spec that length covers the length bytes themselves but not the marker
	// — so it should equal len(xmpSig) + len(xmp) + 2.
	declared := int(out[4])<<8 | int(out[5])
	want := len(xmpSig) + len(xmp) + 2
	if declared != want {
		t.Errorf("APP1 length: declared %d, want %d", declared, want)
	}
}

func TestScanToSOI_AlreadyAligned(t *testing.T) {
	data := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}
	br := bufio.NewReader(bytes.NewReader(data))

	if err := scanToSOI(br); err != nil {
		t.Fatalf("scanToSOI: %v", err)
	}
	head, err := br.Peek(2)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if head[0] != 0xFF || head[1] != 0xD8 {
		t.Errorf("after scan: want FF D8, got %x %x", head[0], head[1])
	}
}

func TestScanToSOI_SkipsGarbagePrefix(t *testing.T) {
	garbage := []byte{0x01, 0x11, 0x10, 0x11, 0x11, 0x42, 0x99}
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	data := append(garbage, jpeg...)
	br := bufio.NewReader(bytes.NewReader(data))

	if err := scanToSOI(br); err != nil {
		t.Fatalf("scanToSOI: %v", err)
	}
	head, err := br.Peek(4)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if !bytes.Equal(head, jpeg) {
		t.Errorf("after scan: want %x, got %x", jpeg, head)
	}
}

func TestScanToSOI_SkipsLoneFFBytes(t *testing.T) {
	// FF 00 (stuffed FF), FF FE (comment marker), FF 7F (junk) before SOI.
	// Each false FF should be discarded until the real FF D8 is found.
	data := []byte{0xFF, 0x00, 0xFF, 0xFE, 0xFF, 0x7F, 0xFF, 0xD8, 0xFF, 0xE0}
	br := bufio.NewReader(bytes.NewReader(data))

	if err := scanToSOI(br); err != nil {
		t.Fatalf("scanToSOI: %v", err)
	}
	head, err := br.Peek(2)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if head[0] != 0xFF || head[1] != 0xD8 {
		t.Errorf("after scan: want FF D8, got %x %x", head[0], head[1])
	}
}

func TestScanToSOI_NoSOIReturnsEOF(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	br := bufio.NewReader(bytes.NewReader(data))

	err := scanToSOI(br)
	if !errors.Is(err, io.EOF) {
		t.Errorf("want io.EOF, got %v", err)
	}
}

func TestDecodeStream_HandlesGarbageBetweenFrames(t *testing.T) {
	// Two real JPEGs glued together with junk in between — exactly the shape
	// that ffmpeg image2pipe occasionally produces and that scanToSOI is
	// meant to recover from.
	one := tinyJPEG(t)
	two := tinyJPEG(t)
	junk := []byte{0x01, 0x11, 0x10, 0x11, 0x11, 0x42}
	stream := bytes.Join([][]byte{one, junk, two}, nil)

	c := &Capture{
		logger:        logging.NewTestLogger(t),
		gotFirstFrame: make(chan struct{}),
	}
	frames, err := c.decodeStream(bytes.NewReader(stream))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("decodeStream: want io.EOF after stream end, got %v", err)
	}
	if frames < 1 {
		t.Fatalf("decodeStream: want at least one frame decoded, got %d", frames)
	}
	if c.latest.Load() == nil {
		t.Fatal("no frame stored in latest")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
