package streamcore

import (
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

// DescribeImages makes saved image identity and coordinate mapping model-visible.
// Only the view bytes are encoded by adapters; originals remain Session data.
func DescribeImages(content []llm.ContentPart) []llm.ContentPart {
	var result []llm.ContentPart
	for _, part := range content {
		if part.Image != nil {
			if note := media.Description(*part.Image); note != "" {
				result = append(result, llm.NewTextContent(note).Part())
			}
		}
		result = append(result, part)
	}
	return result
}
