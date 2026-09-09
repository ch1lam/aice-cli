package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/bmp"

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

func TestGIFConversionRetainsCanvasAndFirstFrame(t *testing.T) {
	t.Parallel()
	palette := color.Palette{color.Transparent, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	first := image.NewPaletted(image.Rect(2, 1, 4, 3), palette)
	for i := range first.Pix {
		first.Pix[i] = 1
	}
	second := image.NewPaletted(image.Rect(0, 0, 6, 4), palette)
	for i := range second.Pix {
		second.Pix[i] = 2
	}
	var data bytes.Buffer
	err := gif.EncodeAll(&data, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{1, 1}, Config: image.Config{ColorModel: palette, Width: 6, Height: 4}})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := media.Prepare(t.Context(), llm.ImageContent{Data: data.Bytes(), MIMEType: "image/gif"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.MIMEType != "image/png" || prepared.Width != 6 || prepared.Height != 4 || prepared.Original == nil || !bytes.Equal(prepared.Original.Data, data.Bytes()) {
		t.Fatal("conversion lost canvas or source")
	}
	if err := media.Validate(prepared); err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(prepared.Data))
	if err != nil {
		t.Fatal(err)
	}
	r, _, b, a := decoded.At(2, 1).RGBA()
	if r != 65535 || b != 0 || a != 65535 {
		t.Fatal("first frame pixels missing")
	}
	_, _, _, a = decoded.At(0, 0).RGBA()
	if a != 0 {
		t.Fatal("unpainted canvas should be transparent")
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var reopened llm.ImageContent
	if err := json.Unmarshal(encoded, &reopened); err != nil {
		t.Fatal(err)
	}
	cropped, err := media.Prepare(t.Context(), reopened, &llm.ImageRegion{X: 2, Y: 1, Width: 2, Height: 2})
	if err != nil {
		t.Fatal(err)
	}
	if cropped.ID != prepared.ID || cropped.Width != 2 || cropped.Original.MIMEType != "image/gif" {
		t.Fatal("crop lost original identity")
	}
	decoded, err = png.Decode(bytes.NewReader(cropped.Data))
	if err != nil {
		t.Fatal(err)
	}
	r, _, b, a = decoded.At(0, 0).RGBA()
	if r != 65535 || b != 0 || a != 65535 {
		t.Fatal("crop shifted first-frame coordinates")
	}
	note := media.Description(cropped)
	for _, want := range []string{"converted from image/gif to image/png", "first frame only", "later animation frames are not shown", "x=2+x*1.000000, y=1+y*1.000000"} {
		if !strings.Contains(note, want) {
			t.Fatalf("missing %q in %s", want, note)
		}
	}
	restored, err := media.Prepare(t.Context(), cropped, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != prepared.ID || restored.Region != nil || !bytes.Equal(restored.Data, prepared.Data) {
		t.Fatal("full re-read did not restore original canvas")
	}
}

func TestGIFSourceLimitsAndMIME(t *testing.T) {
	t.Parallel()
	var encoded bytes.Buffer
	src := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White})
	if err := gif.Encode(&encoded, src, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		width, height uint16
	}{
		{"side", 8001, 1}, {"pixels", 4001, 4000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Clone(encoded.Bytes())
			binary.LittleEndian.PutUint16(data[6:8], tc.width)
			binary.LittleEndian.PutUint16(data[8:10], tc.height)
			if _, err := media.Prepare(t.Context(), llm.ImageContent{Data: data, MIMEType: "image/gif"}, nil); err == nil || !strings.Contains(err.Error(), "16 megapixels") {
				t.Fatalf("accepted oversized header: %v", err)
			}
		})
	}
	if _, err := media.Prepare(t.Context(), llm.ImageContent{Data: encoded.Bytes(), MIMEType: "image/png"}, nil); err == nil {
		t.Fatal("accepted mismatched MIME")
	}
	oversized := make([]byte, media.MaxSourceBytes+1)
	copy(oversized, encoded.Bytes())
	if _, err := media.Prepare(t.Context(), llm.ImageContent{Data: oversized, MIMEType: "image/gif"}, nil); err == nil {
		t.Fatal("accepted oversized source")
	}
}

func TestConvertedFormatsRetainOriginalAndCrop(t *testing.T) {
	t.Parallel()
	var bitmap bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := range 4 {
		for x := range 8 {
			src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			if x >= 4 {
				src.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	if err := bmp.Encode(&bitmap, src); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, mime string
		data       []byte
	}{{"bmp", "image/bmp", bitmap.Bytes()}}
	for _, name := range []string{"blocks-lossless.webp", "blocks-lossy.webp", "blocks-alpha.webp"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct {
			name, mime string
			data       []byte
		}{name, "image/webp", data})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := llm.ImageContent{Data: tc.data, MIMEType: tc.mime, Source: tc.name}
			prepared, err := media.Prepare(t.Context(), input, nil)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(tc.data)
			if prepared.ID != fmt.Sprintf("image:%x", hash) || prepared.Width != 8 || prepared.Height != 4 || prepared.MIMEType != "image/png" || prepared.Original == nil || prepared.Original.MIMEType != tc.mime || !bytes.Equal(prepared.Original.Data, tc.data) {
				t.Fatalf("incorrect conversion: %+v", prepared)
			}
			if err := media.Validate(prepared); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(media.Description(prepared), "converted from "+tc.mime+" to image/png") {
				t.Fatal("missing conversion note")
			}
			persisted, err := json.Marshal(prepared)
			if err != nil {
				t.Fatal(err)
			}
			var reopened llm.ImageContent
			if err := json.Unmarshal(persisted, &reopened); err != nil {
				t.Fatal(err)
			}
			cropped, err := media.Prepare(t.Context(), reopened, &llm.ImageRegion{X: 4, Y: 1, Width: 3, Height: 2})
			if err != nil {
				t.Fatal(err)
			}
			if cropped.ID != prepared.ID || cropped.Source != tc.name || !bytes.Equal(cropped.Original.Data, tc.data) || !strings.Contains(media.Description(cropped), "x=4+x*1.000000, y=1+y*1.000000") {
				t.Fatal("crop lost identity or coordinates")
			}
			original, _, err := image.Decode(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			view, err := png.Decode(bytes.NewReader(cropped.Data))
			if err != nil {
				t.Fatal(err)
			}
			for y := range 2 {
				for x := range 3 {
					if color.RGBAModel.Convert(view.At(x, y)) != color.RGBAModel.Convert(original.At(x+4, y+1)) {
						t.Fatal("crop changed source pixels")
					}
				}
			}
			if strings.Contains(tc.name, "alpha") {
				full, err := png.Decode(bytes.NewReader(prepared.Data))
				if err != nil {
					t.Fatal(err)
				}
				_, _, _, a := full.At(0, 0).RGBA()
				if a != 0 {
					t.Fatal("conversion lost alpha")
				}
			}
			restored, err := media.Prepare(t.Context(), cropped, nil)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Region != nil || restored.ID != prepared.ID || !bytes.Equal(restored.Data, prepared.Data) {
				t.Fatal("re-read did not recover full original")
			}
			if _, err := media.Prepare(t.Context(), llm.ImageContent{Data: tc.data, MIMEType: "image/jpeg"}, nil); err == nil {
				t.Fatal("accepted mismatched MIME")
			}
		})
	}
}

func TestWebPAndBMPRejections(t *testing.T) {
	t.Parallel()
	animated, err := os.ReadFile("testdata/blocks-animated.webp")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := os.ReadFile("testdata/blocks-alpha.webp")
	if err != nil {
		t.Fatal(err)
	}
	lossless, err := os.ReadFile("testdata/blocks-lossless.webp")
	if err != nil {
		t.Fatal(err)
	}
	// Extended 1x1 canvas followed by a lossless frame declaring 16384x16384.
	// Inspect must reject the frame before the decoder allocates that buffer.
	mismatch := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00"), make([]byte, 10)...)
	mismatch = append(mismatch, lossless[12:]...)
	binary.LittleEndian.PutUint32(mismatch[4:8], uint32(len(mismatch)-8))
	binary.LittleEndian.PutUint32(mismatch[39:43], 0x0fffffff)
	largeCanvas := bytes.Clone(alpha)
	largeCanvas[24], largeCanvas[25], largeCanvas[26] = 0x40, 0x1f, 0 // 8001 pixels wide.
	var bitmap bytes.Buffer
	if err := bmp.Encode(&bitmap, image.NewRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	compressedBMP := bytes.Clone(bitmap.Bytes())
	binary.LittleEndian.PutUint32(compressedBMP[30:34], 1)
	largeBMP := bytes.Clone(bitmap.Bytes())
	binary.LittleEndian.PutUint32(largeBMP[18:22], 8001)
	for _, tc := range []struct {
		name, mime, want string
		data             []byte
	}{
		{"animated", "image/webp", "animated WebP", animated},
		{"frame mismatch", "image/webp", "do not match canvas", mismatch},
		{"large canvas", "image/webp", "16 megapixels", largeCanvas},
		{"truncated WebP", "image/webp", "do not retry unchanged input", alpha[:30]},
		{"compressed BMP", "image/bmp", "re-export", compressedBMP},
		{"large BMP", "image/bmp", "16 megapixels", largeBMP},
		{"truncated BMP", "image/bmp", "do not retry unchanged input", bitmap.Bytes()[:54]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := media.Prepare(t.Context(), llm.ImageContent{Data: tc.data, MIMEType: tc.mime}, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestPNGAndJPEGWithinLimitsRemainUnchanged(t *testing.T) {
	t.Parallel()
	src := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for _, mime := range []string{"image/png", "image/jpeg"} {
		t.Run(mime, func(t *testing.T) {
			var data bytes.Buffer
			var err error
			if mime == "image/png" {
				err = png.Encode(&data, src)
			} else {
				err = jpeg.Encode(&data, src, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := media.Prepare(t.Context(), llm.ImageContent{Data: data.Bytes(), MIMEType: mime}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Original != nil || prepared.MIMEType != mime || !bytes.Equal(prepared.Data, data.Bytes()) {
				t.Fatal("unnecessarily changed compatible image")
			}
		})
	}
}

func TestBMPConversionHonorsViewByteLimit(t *testing.T) {
	t.Parallel()
	src := image.NewRGBA(image.Rect(0, 0, 1100, 1100))
	random := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(src.Pix); i += 4 {
		n := random.Uint64()
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = byte(n), byte(n>>8), byte(n>>16), 255
	}
	var data bytes.Buffer
	if err := bmp.Encode(&data, src); err != nil {
		t.Fatal(err)
	}
	prepared, err := media.Prepare(t.Context(), llm.ImageContent{Data: data.Bytes(), MIMEType: "image/bmp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Data) > media.MaxViewBytes || prepared.MIMEType != "image/jpeg" || prepared.Width != 1100 || prepared.Height != 1100 || !bytes.Equal(prepared.Original.Data, data.Bytes()) {
		t.Fatal("conversion failed to honor view byte limit and retain original")
	}
}
