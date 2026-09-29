package desktop

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func pixelFixture(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2100, 2))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
