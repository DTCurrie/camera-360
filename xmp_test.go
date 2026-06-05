package camera360

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"go.viam.com/rdk/logging"
)

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	b, err := EncodeJPEG(img)
	if err != nil {
		t.Fatalf("EncodeJPEG: %v", err)
	}
	return b
}

// jpegSized encodes a w×h JPEG, for GPano cropped-area math that reads the
// frame's own dimensions.
func jpegSized(t *testing.T, w, h int) []byte {
	t.Helper()
	b, err := EncodeJPEG(image.NewRGBA(image.Rect(0, 0, w, h)))
	if err != nil {
		t.Fatalf("EncodeJPEG: %v", err)
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
	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><viam:equirectangular>true</viam:equirectangular></x:xmpmeta>`

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
	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><viam:equirectangular>true</viam:equirectangular></x:xmpmeta>`

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

func TestJPEGWithEquirectangularXMP_DetectableByWidget(t *testing.T) {
	src := tinyJPEG(t)

	out, err := JPEGWithEquirectangularXMP(src)
	if err != nil {
		t.Fatalf("JPEGWithEquirectangularXMP: %v", err)
	}
	// The test-widget keys its 3D viewer off viam:equirectangular == "true" found
	// in an XMP APP1 segment; reproduce the two things it looks for.
	if !bytes.Contains(out, []byte(xmpSig)) {
		t.Error("output missing the Adobe XMP APP1 identifier the widget scans for")
	}
	if !bytes.Contains(out, []byte("<viam:equirectangular>true</viam:equirectangular>")) {
		t.Error("output missing the viam:equirectangular marker")
	}
	// And it must still be a valid JPEG.
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("jpeg.Decode of tagged output: %v", err)
	}
}

func TestHasXMP(t *testing.T) {
	bare := tinyJPEG(t)
	if hasXMP(bare) {
		t.Error("hasXMP: bare JPEG reported as having XMP")
	}
	tagged, err := JPEGWithEquirectangularXMP(bare)
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if !hasXMP(tagged) {
		t.Error("hasXMP: tagged JPEG reported as not having XMP")
	}
}

func TestGPanoXMP_CroppedAreaMath(t *testing.T) {
	// 1920×190 band, full 360° horizontal, 53° vertical → fullW = 1920 (left 0),
	// fullH = round(190*180/53) = 645, top = (645-190)/2 = 227.
	x := gpanoXMP(1920, 190, 360, 53)
	for _, want := range []string{
		"<GPano:ProjectionType>equirectangular</GPano:ProjectionType>",
		"<GPano:CroppedAreaImageWidthPixels>1920</GPano:CroppedAreaImageWidthPixels>",
		"<GPano:CroppedAreaImageHeightPixels>190</GPano:CroppedAreaImageHeightPixels>",
		"<GPano:FullPanoWidthPixels>1920</GPano:FullPanoWidthPixels>",
		"<GPano:FullPanoHeightPixels>645</GPano:FullPanoHeightPixels>",
		"<GPano:CroppedAreaLeftPixels>0</GPano:CroppedAreaLeftPixels>",
		"<GPano:CroppedAreaTopPixels>227</GPano:CroppedAreaTopPixels>",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("gpanoXMP missing %q", want)
		}
	}
}

func TestJPEGWithGPano_TagsAndIsIdempotent(t *testing.T) {
	src := jpegSized(t, 1920, 190)
	out, err := JPEGWithGPano(src, 360, 53)
	if err != nil {
		t.Fatalf("JPEGWithGPano: %v", err)
	}
	if !bytes.Contains(out, []byte("xmlns:GPano=")) || !bytes.Contains(out, []byte("<GPano:FullPanoHeightPixels>645</GPano:FullPanoHeightPixels>")) {
		t.Error("output missing GPano packet derived from frame dimensions")
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("jpeg.Decode of GPano output: %v", err)
	}
	// Re-tagging an already-tagged frame is a no-op (returns it unchanged).
	again, err := JPEGWithGPano(out, 360, 53)
	if err != nil {
		t.Fatalf("re-tag: %v", err)
	}
	if !bytes.Equal(again, out) {
		t.Error("JPEGWithGPano not idempotent: re-tagging changed the bytes")
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

func TestIndexSOI(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		from int
		want int
	}{
		{"already aligned", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}, 0, 0},
		{"garbage prefix", []byte{0x01, 0x11, 0x10, 0x42, 0x99, 0xFF, 0xD8, 0xFF, 0xE0}, 0, 5},
		// FF 00 (stuffed), FF FE (comment), FF 7F (junk) are not SOIs; the real
		// FF D8 is at index 6.
		{"lone FF bytes before SOI", []byte{0xFF, 0x00, 0xFF, 0xFE, 0xFF, 0x7F, 0xFF, 0xD8, 0xFF, 0xE0}, 0, 6},
		{"no SOI", []byte{0x01, 0x02, 0x03, 0x04}, 0, -1},
		{"trailing lone FF can't confirm", []byte{0x01, 0xFF}, 0, -1},
		{"from skips first SOI", []byte{0xFF, 0xD8, 0x00, 0xFF, 0xD8}, 2, 3},
		{"empty", nil, 0, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := indexSOI(tc.data, tc.from); got != tc.want {
				t.Errorf("indexSOI(%x, %d) = %d, want %d", tc.data, tc.from, got, tc.want)
			}
		})
	}
}

func TestReadFrames_SplitsAndStoresLatest(t *testing.T) {
	// Leading junk, then two real JPEGs with junk glued between them — the shape
	// ffmpeg image2pipe occasionally produces. readFrames must resync to the
	// first SOI, split on SOI boundaries, and leave the *latest* frame in
	// latestRaw (still decodable).
	one := tinyJPEG(t)
	two := tinyJPEG(t)
	junk := []byte{0x01, 0x11, 0x10, 0x11, 0x11, 0x42}
	stream := bytes.Join([][]byte{junk, one, junk, two}, nil)

	c := &Capture{
		logger:        logging.NewTestLogger(t),
		gotFirstFrame: make(chan struct{}),
		rawReady:      make(chan struct{}, 1),
	}
	frames, err := c.readFrames(bytes.NewReader(stream))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("readFrames: want io.EOF after stream end, got %v", err)
	}
	if frames != 2 {
		t.Fatalf("readFrames: want 2 frames split, got %d", frames)
	}
	p := c.latestRaw.Load()
	if p == nil {
		t.Fatal("no raw frame stored in latestRaw")
	}
	// The latest stored frame must be the second JPEG and still decode cleanly.
	if (*p)[0] != 0xFF || (*p)[1] != 0xD8 {
		t.Fatalf("stored frame missing SOI: %x", (*p)[:min(4, len(*p))])
	}
	if _, err := jpeg.Decode(bytes.NewReader(*p)); err != nil {
		t.Fatalf("stored raw frame did not decode: %v", err)
	}
}

func TestReadFrames_DropsStaleAcrossChunkBoundaries(t *testing.T) {
	// Feed many frames through a deliberately tiny-chunk reader so SOI markers
	// land across Read boundaries; readFrames must still count every frame and
	// keep only the last one as latest.
	const n = 25
	var frames [][]byte
	for i := 0; i < n; i++ {
		frames = append(frames, tinyJPEG(t))
	}
	stream := bytes.Join(frames, nil)

	c := &Capture{
		logger:        logging.NewTestLogger(t),
		gotFirstFrame: make(chan struct{}),
		rawReady:      make(chan struct{}, 1),
	}
	got, err := c.readFrames(iotest.OneByteReader(bytes.NewReader(stream)))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("readFrames: want io.EOF, got %v", err)
	}
	if got != n {
		t.Fatalf("readFrames: want %d frames, got %d", n, got)
	}
	p := c.latestRaw.Load()
	if p == nil || !bytes.Equal(*p, frames[n-1]) {
		t.Fatal("latestRaw is not the final frame")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
