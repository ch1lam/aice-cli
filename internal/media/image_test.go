package media_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

func TestImageResizeRetainsOriginalAndCrop(t *testing.T) {
	t.Parallel()
	src := image.NewRGBA(image.Rect(0, 0, 4000, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 4000; x++ {
			c := color.RGBA{R: 255, A: 255}
			if x >= 2000 {
				c = color.RGBA{B: 255, A: 255}
			}
			src.SetRGBA(x, y, c)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}
	raw := bytes.Clone(encoded.Bytes())
	prepared, err := media.Prepare(t.Context(), llm.ImageContent{Data: raw, MIMEType: "image/png", Source: "design.png"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Width != 2000 || prepared.Height != 50 || prepared.Original == nil || !bytes.Equal(prepared.Original.Data, raw) {
		t.Fatal("resize did not retain the original and aspect ratio")
	}
	data, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var reopened llm.ImageContent
	if err := json.Unmarshal(data, &reopened); err != nil {
		t.Fatal(err)
	}
	cropped, err := media.Prepare(t.Context(), reopened, &llm.ImageRegion{X: 3000, Y: 10, Width: 100, Height: 50})
	if err != nil {
		t.Fatal(err)
	}
	if cropped.ID != prepared.ID || cropped.Source != "design.png" || !bytes.Equal(cropped.Original.Data, raw) {
		t.Fatal("crop lost source identity")
	}
	decoded, err := png.Decode(bytes.NewReader(cropped.Data))
	if err != nil {
		t.Fatal(err)
	}
	r, _, b, _ := decoded.At(0, 0).RGBA()
	if r != 0 || b != 65535 {
		t.Fatal("crop did not select original pixels")
	}
	cloned := reopened.Clone()
	cloned.Original.Data[0] = 0
	if reopened.Original.Data[0] == 0 {
		t.Fatal("clone aliases original")
	}
	_, err = media.Prepare(t.Context(), reopened, &llm.ImageRegion{X: 4000, Width: 1, Height: 1})
	if err == nil {
		t.Fatal("accepted crop outside original")
	}
}

func TestImageRejectsCancellationAndInvalidContent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := media.Prepare(ctx, llm.ImageContent{}, nil); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := media.Prepare(t.Context(), llm.ImageContent{Data: []byte("not a PNG"), MIMEType: "image/png"}, nil); err == nil {
		t.Fatal("accepted invalid image")
	}
}
