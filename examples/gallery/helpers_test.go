package gallery

// Shared test fixtures. makeThumb really decodes since F9, so uploads
// need real image bytes.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	tako "github.com/gsandoval/tako"
)

// pngBytes renders a small real PNG (8×6, solid color).
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for x := 0; x < 8; x++ {
		for y := 0; y < 6; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 80, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pngBlob wraps fresh PNG bytes as an upload blob.
func pngBlob(t *testing.T) tako.Blob {
	t.Helper()
	raw := pngBytes(t)
	return tako.Blob{Reader: bytes.NewReader(raw), ContentType: "image/png", Size: int64(len(raw))}
}

// decodePNG asserts bytes are a well-formed PNG and returns its bounds.
func decodePNG(t *testing.T, raw []byte) image.Rectangle {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil || format != "png" {
		t.Fatalf("thumb does not decode as png: format=%q err=%v", format, err)
	}
	return img.Bounds()
}
