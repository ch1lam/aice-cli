package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

func validateImageModel(model llm.Model, images []llm.ImageContent) error {
	if len(images) > 0 && !slices.Contains(model.InputModalities, llm.InputModalityImage) {
		return fmt.Errorf("model %s does not support image input; choose a vision model or remove the images", model.ID)
	}
	return nil
}

func newImageInput(input interaction.RunInput, model llm.Model) (llm.UserMessage, error) {
	return newImageInputContext(context.Background(), input, model)
}

func newImageInputContext(ctx context.Context, input interaction.RunInput, model llm.Model) (llm.UserMessage, error) {
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
	prepared := make([]llm.ImageContent, 0, len(input.Images))
	for _, source := range input.Images {
		img, err := media.Prepare(ctx, source, source.Region)
		if err != nil {
			return llm.UserMessage{}, err
		}
		prepared = append(prepared, img)
		parts = append(parts, llm.ContentPart{Type: llm.ContentTypeImage, Image: &img})
	}
	if err := interaction.ValidateImages(prepared); err != nil {
		return llm.UserMessage{}, err
	}
	return llm.NewUserMessage(parts...)
}
