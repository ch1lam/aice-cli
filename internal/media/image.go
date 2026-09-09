// Package media prepares bounded image content independently of its input source.
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/bmp"
	"golang.org/x/image/webp"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	MaxSourceBytes   = 16 * 1024 * 1024
	MaxViewBytes     = 3 * 1024 * 1024
	MaxDimension     = 8000
	MaxPixels        = 16_000_000
	MaxViewDimension = 2000
)

// Inspect validates the encoded image before allocating a decoded pixel buffer.
// A supplied MIME type must agree with the bytes; filenames are not trusted.
func Inspect(data []byte, mime string) (image.Config, string, error) {
	if len(data) == 0 || len(data) > MaxSourceBytes {
		return image.Config{}, "", fmt.Errorf("image must contain between 1 byte and 16 MiB")
	}
	var config image.Config
	var err error
	actual := ""
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		actual = "image/png"
		config, err = png.DecodeConfig(bytes.NewReader(data))
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		actual = "image/jpeg"
		config, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		actual = "image/gif"
		config, err = gif.DecodeConfig(bytes.NewReader(data))
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		actual = "image/webp"
		config, err = inspectWebP(data)
	case bytes.HasPrefix(data, []byte("BM")):
		actual = "image/bmp"
		config, err = bmp.DecodeConfig(bytes.NewReader(data))
	default:
		return config, "", fmt.Errorf("unsupported image format; export as PNG or JPEG before reading again; do not retry unchanged input")
	}
	if mime != "" && mime != actual {
		return config, "", fmt.Errorf("image MIME type %q does not match %q", mime, actual)
	}
	if err != nil {
		return config, "", imageDecodeError(actual, err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxDimension ||
		config.Height > MaxDimension || int64(config.Width)*int64(config.Height) > MaxPixels {
		return config, "", fmt.Errorf("image exceeds 8000 pixels per side or 16 megapixels")
	}
	return config, actual, nil
}

// Validate checks both the view and any retained source, including decoding.
func Validate(img llm.ImageContent) error {
	_, mime, err := Inspect(img.Data, img.MIMEType)
	if err != nil {
		return err
	}
	if _, err := decode(img.Data, mime); err != nil {
		return err
	}
	if img.Original != nil {
		_, mime, err := Inspect(img.Original.Data, img.Original.MIMEType)
		if err != nil {
			return fmt.Errorf("original image: %w", err)
		}
		if _, err := decode(img.Original.Data, mime); err != nil {
			return fmt.Errorf("original image: %w", err)
		}
	}
	return nil
}

// Prepare derives a model-sized view from the original, retaining exact source
// bytes only when the view differs. Region coordinates always refer to the original.
func Prepare(ctx context.Context, input llm.ImageContent, region *llm.ImageRegion) (llm.ImageContent, error) {
	if err := ctx.Err(); err != nil {
		return llm.ImageContent{}, err
	}
	data, mime := input.Data, input.MIMEType
	if input.Original != nil {
		data, mime = input.Original.Data, input.Original.MIMEType
	}
	config, mime, err := Inspect(data, mime)
	if err != nil {
		return llm.ImageContent{}, err
	}
	src, err := decode(data, mime)
	if err != nil {
		return llm.ImageContent{}, err
	}
	if err := ctx.Err(); err != nil {
		return llm.ImageContent{}, err
	}
	area := src.Bounds()
	if region != nil {
		if region.X < 0 || region.Y < 0 || region.Width <= 0 || region.Height <= 0 ||
			region.X > config.Width || region.Y > config.Height ||
			region.Width > config.Width-region.X || region.Height > config.Height-region.Y {
			return llm.ImageContent{}, fmt.Errorf("crop must fit inside the original %dx%d image", config.Width, config.Height)
		}
		area = image.Rect(region.X, region.Y, region.X+region.Width, region.Y+region.Height)
	}
	hash := sha256.Sum256(data)
	result := llm.ImageContent{
		ID: fmt.Sprintf("image:%x", hash), Source: input.Source,
		Data: bytes.Clone(data), MIMEType: mime, Width: config.Width, Height: config.Height,
	}
	if (mime == "image/png" || mime == "image/jpeg") && region == nil &&
		config.Width <= MaxViewDimension && config.Height <= MaxViewDimension && len(data) <= MaxViewBytes {
		return result, nil
	}
	scale := math.Min(1, float64(MaxViewDimension)/float64(max(area.Dx(), area.Dy())))
	w, h := max(1, int(float64(area.Dx())*scale)), max(1, int(float64(area.Dy())*scale))
	for {
		view, err := resize(ctx, src, area, w, h)
		if err != nil {
			return llm.ImageContent{}, err
		}
		var encoded bytes.Buffer
		// PNG preserves screenshot text and alpha. Fall back to JPEG on white
		// for byte-heavy images, then progressively reduce dimensions if needed.
		if err := png.Encode(&encoded, view); err != nil {
			return llm.ImageContent{}, err
		}
		viewMIME := "image/png"
		if encoded.Len() > MaxViewBytes {
			for y := 0; y < h; y++ {
				if err := ctx.Err(); err != nil {
					return llm.ImageContent{}, err
				}
				for x := 0; x < w; x++ {
					c := view.RGBAAt(x, y)
					white := 255 - c.A
					view.SetRGBA(x, y, color.RGBA{c.R + white, c.G + white, c.B + white, 255})
				}
			}
			encoded.Reset()
			if err := jpeg.Encode(&encoded, view, &jpeg.Options{Quality: 85}); err != nil {
				return llm.ImageContent{}, err
			}
			viewMIME = "image/jpeg"
		}
		if encoded.Len() <= MaxViewBytes {
			result.Original = &llm.ImageOriginal{Data: result.Data, MIMEType: mime, Width: config.Width, Height: config.Height}
			result.Data, result.MIMEType, result.Width, result.Height = encoded.Bytes(), viewMIME, w, h
			if region != nil {
				copied := *region
				result.Region = &copied
			}
			return result, ctx.Err()
		}
		if w == 1 && h == 1 {
			return llm.ImageContent{}, fmt.Errorf("image cannot fit the 3 MiB view limit")
		}
		w, h = max(1, w*3/4), max(1, h*3/4)
	}
}

func decode(data []byte, mime string) (image.Image, error) {
	var src image.Image
	var err error
	switch mime {
	case "image/png":
		src, err = png.Decode(bytes.NewReader(data))
	case "image/jpeg":
		src, err = jpeg.Decode(bytes.NewReader(data))
	case "image/bmp":
		src, err = bmp.Decode(bytes.NewReader(data))
	case "image/webp":
		src, err = webp.Decode(bytes.NewReader(data))
	case "image/gif":
		// Decode only the first frame, keeping memory independent of frame count.
		// A frame can occupy a subrectangle of the logical screen; retain the
		// full canvas so original coordinates do not shift to the frame origin.
		var config image.Config
		config, err = gif.DecodeConfig(bytes.NewReader(data))
		if err == nil {
			src, err = gif.Decode(bytes.NewReader(data))
		}
		if err == nil {
			canvas := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
			draw.Draw(canvas, src.Bounds(), src, src.Bounds().Min, draw.Src)
			src = canvas
		}
	default:
		return nil, fmt.Errorf("unsupported image format; export as PNG or JPEG before reading again")
	}
	if err != nil {
		return nil, imageDecodeError(mime, err)
	}
	return src, nil
}

func imageDecodeError(mime string, err error) error {
	return fmt.Errorf("cannot decode %s image: %w; re-export a valid PNG or JPEG (or extract the desired animation frame) and read that file; do not retry unchanged input", mime, err)
}

// Area averaging preserves thin lines when reducing screenshots, unlike point sampling.
func resize(ctx context.Context, src image.Image, area image.Rectangle, w, h int) (*image.RGBA, error) {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		y0, y1 := area.Min.Y+y*area.Dy()/h, area.Min.Y+(y+1)*area.Dy()/h
		for x := 0; x < w; x++ {
			x0, x1 := area.Min.X+x*area.Dx()/w, area.Min.X+(x+1)*area.Dx()/w
			var r, g, b, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r += uint64(cr)
					g += uint64(cg)
					b += uint64(cb)
					a += uint64(ca)
					n++
				}
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(b / n >> 8), uint8(a / n >> 8)})
		}
	}
	return out, nil
}

// Description exposes identity and coordinate mapping without embedding bytes.
func Description(img llm.ImageContent) string {
	if img.ID == "" {
		return ""
	}
	note := fmt.Sprintf("Image %s (%dx%d)", img.ID, img.Width, img.Height)
	if img.Source != "" {
		note += fmt.Sprintf("; source %q", img.Source)
	}
	if img.Original != nil {
		if img.Original.MIMEType != img.MIMEType {
			note += fmt.Sprintf("; converted from %s to %s", img.Original.MIMEType, img.MIMEType)
		}
		if img.Original.MIMEType == "image/gif" {
			note += "; GIF first frame only on the original canvas; unpainted pixels start transparent (white in a JPEG view); later animation frames are not shown"
		}
		note += fmt.Sprintf("; original %dx%d", img.Original.Width, img.Original.Height)
		region := llm.ImageRegion{Width: img.Original.Width, Height: img.Original.Height}
		if img.Region != nil {
			region = *img.Region
		}
		note += fmt.Sprintf("; original coordinates: x=%d+x*%.6f, y=%d+y*%.6f", region.X,
			float64(region.Width)/float64(img.Width), region.Y, float64(region.Height)/float64(img.Height))
	}
	return note + "."
}
