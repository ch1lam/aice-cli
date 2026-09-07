package interaction

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"image/png"
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Input image limits bound draft memory, queued deliveries and Session records.
const (
	MaxInputImages     = 4
	MaxImageBytes      = 4 * 1024 * 1024
	MaxInputImageBytes = 8 * 1024 * 1024
	maxImageDimension  = 8000
	maxImagePixels     = 16_000_000
)

// ValidateImages checks inline input before it crosses an ownership boundary.
// Decoding dimensions first bounds the memory needed to validate the full image.
func ValidateImages(images []llm.ImageContent) error {
	if len(images) > MaxInputImages {
		return fmt.Errorf("at most %d images can be attached to one input", MaxInputImages)
	}
	total := 0
	for index, img := range images {
		total += len(img.Data)
		if total > MaxInputImageBytes {
			return fmt.Errorf("attached images exceed the 8 MiB input limit")
		}
		if err := validateImage(img); err != nil {
			return fmt.Errorf("image %d: %w", index+1, err)
		}
	}
	return nil
}

func validateImage(img llm.ImageContent) error {
	if len(img.Data) == 0 || len(img.Data) > MaxImageBytes {
		return fmt.Errorf("image must contain between 1 byte and 4 MiB; copy a smaller image")
	}
	decodeConfig := png.DecodeConfig
	decode := png.Decode
	switch img.MIMEType {
	case "image/png":
	case "image/jpeg":
		decodeConfig, decode = jpeg.DecodeConfig, jpeg.Decode
	default:
		return fmt.Errorf("unsupported image format %q; use PNG or JPEG", img.MIMEType)
	}
	config, err := decodeConfig(bytes.NewReader(img.Data))
	if err != nil {
		return fmt.Errorf("invalid %s image: %w", img.MIMEType, err)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		config.Width > maxImageDimension || config.Height > maxImageDimension ||
		int64(config.Width)*int64(config.Height) > maxImagePixels {
		return fmt.Errorf("image exceeds 8000 pixels per side or 16 megapixels; copy a smaller image")
	}
	if _, err := decode(bytes.NewReader(img.Data)); err != nil {
		return fmt.Errorf("invalid %s image: %w", img.MIMEType, err)
	}
	return nil
}

// CloneImages transfers mutable image data into the receiver's ownership.
func CloneImages(images []llm.ImageContent) []llm.ImageContent {
	cloned := slices.Clone(images)
	for index := range cloned {
		cloned[index].Data = slices.Clone(cloned[index].Data)
	}
	return cloned
}
