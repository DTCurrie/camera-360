package camera360

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

// xmpSig is the Adobe XMP APP1 identifier prefixing an XMP packet's payload in a
// JPEG. Shared by the writer (addXMPToJPEG) and the detector (hasXMP).
const xmpSig = "http://ns.adobe.com/xap/1.0/\x00"

// xmpEquirectangular is the XMP packet that flags a JPEG as a full-sphere
// equirectangular (360°×180°) panorama. Viam's camera test-widget parses the
// JPEG's XMP APP1 segment and, when it finds viam:equirectangular == "true",
// offers the interactive 3D/360 viewer (see the widget's get-xmp-json-from-image
// + camera.svelte). The AKASO stitched ERP source embeds this; the JVCU360 is a
// partial band and uses GPano cropped-area XMP instead (see gpanoXMP).
const xmpEquirectangular = `
	<x:xmpmeta xmlns:x="adobe:ns:meta/">
	<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
	<rdf:Description xmlns:viam="https://www.viam.com/ns/1.0/">
	<viam:equirectangular>true</viam:equirectangular>
	</rdf:Description>
	</rdf:RDF>
	</x:xmpmeta>
	`

// JPEGWithEquirectangularXMP returns jpegBytes with the viam:equirectangular XMP
// segment inserted, marking it as a full-sphere panorama for the test-widget's 3D
// viewer. No-op (returns the input unchanged) if the JPEG already carries an XMP
// packet, so re-tagging is safe.
func JPEGWithEquirectangularXMP(jpegBytes []byte) ([]byte, error) {
	if hasXMP(jpegBytes) {
		return jpegBytes, nil
	}
	return addXMPToJPEG(jpegBytes, xmpEquirectangular)
}

// EncodeJPEG encodes an image to JPEG with the module's standard quality. Used
// by the AKASO stitched sources and the CLI smoke test (cmd/cli/main.go).
func EncodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// addXMPToJPEG inserts an XMP APP1 segment carrying xmpXML immediately after the
// JPEG's SOI marker, leaving the rest of the stream untouched (lossless — no
// re-encode).
func addXMPToJPEG(jpegBytes []byte, xmpXML string) ([]byte, error) {
	// JPEG SOI marker.
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		return nil, fmt.Errorf("not a jpeg")
	}

	// XMP APP1 segment: marker, big-endian length (covers the length bytes and
	// payload but not the marker), then the Adobe XMP identifier + the XML.
	xmpHeader := []byte(xmpSig)
	payload := append(xmpHeader, []byte(xmpXML)...)
	segmentLength := len(payload) + 2

	app1 := []byte{
		0xFF, 0xE1,
		byte(segmentLength >> 8),
		byte(segmentLength & 0xFF),
	}
	app1 = append(app1, payload...)

	var out bytes.Buffer
	out.Write(jpegBytes[:2]) // SOI
	out.Write(app1)          // APP1 immediately after SOI
	out.Write(jpegBytes[2:]) // rest of the JPEG
	return out.Bytes(), nil
}

// hasXMP reports whether jpegBytes already contains an XMP APP1 segment (payload
// prefixed with the Adobe xmpSig). It walks the JPEG marker segments from the SOI
// up to the start of scan (SOS) or end of image. We only inject metadata when
// none is present, so re-tagging an already-tagged frame is a no-op.
func hasXMP(jpegBytes []byte) bool {
	if len(jpegBytes) < 4 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		return false
	}
	for i := 2; i+4 <= len(jpegBytes); {
		if jpegBytes[i] != 0xFF {
			return false // not aligned to a marker; bail rather than misread
		}
		marker := jpegBytes[i+1]
		// Standalone markers (no length payload): SOI, EOI, RSTn, TEM.
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if marker == 0xDA { // SOS: compressed image data follows; stop.
			return false
		}
		segLen := int(jpegBytes[i+2])<<8 | int(jpegBytes[i+3])
		if segLen < 2 {
			return false
		}
		payloadEnd := i + 2 + segLen
		if payloadEnd > len(jpegBytes) {
			return false
		}
		if marker == 0xE1 { // APP1: does the payload start with the XMP signature?
			payload := jpegBytes[i+4 : payloadEnd]
			if len(payload) >= len(xmpSig) && string(payload[:len(xmpSig)]) == xmpSig {
				return true
			}
		}
		i = payloadEnd
	}
	return false
}

// gpanoXMP builds a Google Photo Sphere (GPano) XMP packet describing a w×h image
// as a centered cropped region of a larger equirectangular panorama spanning hFOV
// degrees horizontally and vFOV degrees vertically. A capable 360 viewer maps the
// crop onto only the longitudes/latitudes it covers, leaving the rest empty —
// correct for a partial-FOV band like the JVCU360's, instead of stretching it
// pole-to-pole. Emitted as child elements so the widget's generic XMP parser keys
// them as GPano:<field>.
func gpanoXMP(w, h int, hFOV, vFOV float64) string {
	round := func(f float64) int { return int(f + 0.5) }
	fullW := round(float64(w) * 360.0 / hFOV)
	fullH := round(float64(h) * 180.0 / vFOV)
	left := (fullW - w) / 2
	top := (fullH - h) / 2
	return fmt.Sprintf(`
	<x:xmpmeta xmlns:x="adobe:ns:meta/">
	<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
	<rdf:Description xmlns:GPano="http://ns.google.com/photos/1.0/panorama/">
	<GPano:ProjectionType>equirectangular</GPano:ProjectionType>
	<GPano:CroppedAreaImageWidthPixels>%d</GPano:CroppedAreaImageWidthPixels>
	<GPano:CroppedAreaImageHeightPixels>%d</GPano:CroppedAreaImageHeightPixels>
	<GPano:FullPanoWidthPixels>%d</GPano:FullPanoWidthPixels>
	<GPano:FullPanoHeightPixels>%d</GPano:FullPanoHeightPixels>
	<GPano:CroppedAreaLeftPixels>%d</GPano:CroppedAreaLeftPixels>
	<GPano:CroppedAreaTopPixels>%d</GPano:CroppedAreaTopPixels>
	</rdf:Description>
	</rdf:RDF>
	</x:xmpmeta>
	`, w, h, fullW, fullH, left, top)
}

// jpegWithGPano returns jpegBytes with a GPano cropped-area XMP segment inserted,
// derived from the JPEG's own pixel dimensions (read from the header only, via
// jpeg.DecodeConfig — no full decode) and the given horizontal/vertical FOV.
// No-op (returns the input unchanged) if the JPEG already carries an XMP packet.
func JPEGWithGPano(jpegBytes []byte, hFOV, vFOV float64) ([]byte, error) {
	if hasXMP(jpegBytes) {
		return jpegBytes, nil
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpegBytes))
	if err != nil {
		return nil, fmt.Errorf("gpano: decode jpeg header: %w", err)
	}
	return addXMPToJPEG(jpegBytes, gpanoXMP(cfg.Width, cfg.Height, hFOV, vFOV))
}
