package app

import (
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func validateImageModel(model llm.Model, images []llm.ImageContent) error {
	if len(images) > 0 && !slices.Contains(model.InputModalities, llm.InputModalityImage) {
		return fmt.Errorf("model %s does not support image input; choose a vision model or remove the images", model.ID)
	}
	return nil
}

func newImageInput(input interaction.RunInput, model llm.Model) (llm.UserMessage, error) {
	if err := validateImageModel(model, input.Images); err != nil {
		return llm.UserMessage{}, err
	}
	if err := interaction.ValidateImages(input.Images); err != nil {
		return llm.UserMessage{}, err
	}
	parts := make([]llm.ContentPart, 0, 1+len(input.Images))
	if input.Prompt != "" || len(input.Images) == 0 {
		parts = append(parts, llm.NewTextContent(input.Prompt).Part())
	}
	for _, img := range interaction.CloneImages(input.Images) {
		parts = append(parts, llm.ContentPart{Type: llm.ContentTypeImage, Image: &img})
	}
	return llm.NewUserMessage(parts...)
}
