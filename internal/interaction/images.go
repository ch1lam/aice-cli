package interaction

import (
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

// Input image limits bound draft memory, queued deliveries and Session records.
const (
	MaxInputImages     = 4
	MaxImageBytes      = media.MaxSourceBytes
	MaxInputImageBytes = 32 * 1024 * 1024
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
		if img.Original != nil {
			total += len(img.Original.Data)
		}
		if total > MaxInputImageBytes {
			return fmt.Errorf("attached images exceed the 32 MiB input limit")
		}
		if err := media.Validate(img); err != nil {
			return fmt.Errorf("image %d: %w", index+1, err)
		}
	}
	return nil
}

// CloneImages transfers mutable image data into the receiver's ownership.
func CloneImages(images []llm.ImageContent) []llm.ImageContent {
	cloned := slices.Clone(images)
	for index := range cloned {
		cloned[index] = cloned[index].Clone()
	}
	return cloned
}
