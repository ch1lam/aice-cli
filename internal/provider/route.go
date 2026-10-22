package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Route is a provider's request gate, built in the provider constructor. It
// accepts a request only for its provider identity and a catalog model, checks
// replayed content against that model's capabilities, and dispatches to the
// adapter serving the model's API. Every error carries the provider prefix.
type Route struct {
	// Provider identifies the provider and prefixes every error.
	Provider llm.ProviderID
	// Label names the model family in content errors.
	Label string
	// Models is the catalog a request must match by ID and API.
	Models []llm.Model
	// ModelForID, when set, describes IDs outside Models instead of rejecting
	// them; it serves providers without a fixed catalog.
	ModelForID func(id string) llm.Model
	// Adapters serves each API used by the catalog. A Route used only for
	// Check, by a provider that builds its adapter per request, has none.
	Adapters map[llm.API]llm.Streamer
	// RedactedThinking lists the APIs through which the provider accepts
	// replayed redacted thinking.
	RedactedThinking []llm.API
	// NestedToolResultTextOnly rejects non-text tool results nested in user
	// content unless the model accepts images.
	NestedToolResultTextOnly bool
}

// Stream checks the request and delegates it to the adapter for its API.
func (r Route) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if err := r.Check(request); err != nil {
		return nil, err
	}
	adapter, ok := r.Adapters[request.Model.API]
	if !ok {
		return nil, fmt.Errorf("%s: model %q uses unsupported API %q", r.Provider, request.Model.ID, request.Model.API)
	}
	return adapter.Stream(ctx, request)
}

// Check rejects a request for another provider, an unknown model, a model API
// that differs from the catalog, or content the catalog model cannot accept.
func (r Route) Check(request llm.Request) error {
	if request.Model.Provider != r.Provider {
		return fmt.Errorf("%s: model provider %q does not match %q", r.Provider, request.Model.Provider, r.Provider)
	}
	model, ok := r.model(request.Model.ID)
	if !ok {
		return fmt.Errorf("%s: unsupported model %q", r.Provider, request.Model.ID)
	}
	if request.Model.API != model.API {
		return fmt.Errorf("%s: model %q API %q does not match %q", r.Provider, request.Model.ID, request.Model.API, model.API)
	}
	return r.validateMessages(request.Messages, model)
}

func (r Route) model(id string) (llm.Model, bool) {
	if strings.TrimSpace(id) == "" {
		return llm.Model{}, false
	}
	for _, model := range r.Models {
		if model.ID == id {
			return model, true
		}
	}
	if r.ModelForID != nil {
		return r.ModelForID(id), true
	}
	return llm.Model{}, false
}

// validateMessages rejects message content the provider cannot send to model.
// Image support comes from the catalog model's input modalities.
func (r Route) validateMessages(messages []llm.Message, model llm.Model) error {
	image := slices.Contains(model.InputModalities, llm.InputModalityImage)
	accepts := func(part llm.ContentPart) bool {
		return part.Type == llm.ContentTypeText || part.Type == llm.ContentTypeImage && image
	}
	for messageIndex, message := range messages {
		var content []llm.ContentPart
		switch value := message.(type) {
		case llm.UserMessage:
			content = value.Content
		case llm.AssistantMessage:
			content = value.Content
		case llm.ToolResultMessage:
			for partIndex, part := range value.Content {
				if !accepts(part) {
					return fmt.Errorf(
						"%s: message %d content %d: non-text tool results are not supported by %s models",
						r.Provider, messageIndex, partIndex, r.Label,
					)
				}
			}
			continue
		case nil:
			return fmt.Errorf("%s: message %d is nil", r.Provider, messageIndex)
		default:
			return fmt.Errorf("%s: message %d has unsupported type %T", r.Provider, messageIndex, message)
		}
		for partIndex, part := range content {
			if err := r.validateContent(part, model.API, image, accepts); err != nil {
				return fmt.Errorf("%s: message %d content %d: %w", r.Provider, messageIndex, partIndex, err)
			}
		}
	}
	return nil
}

func (r Route) validateContent(
	part llm.ContentPart,
	api llm.API,
	image bool,
	accepts func(llm.ContentPart) bool,
) error {
	switch part.Type {
	case llm.ContentTypeImage:
		if !image {
			return errors.New("image content is not supported by " + r.Label + " models")
		}
	case llm.ContentTypeThinking:
		if part.Redacted && !slices.Contains(r.RedactedThinking, api) {
			return errors.New("redacted thinking is not supported by " + r.Label + " models")
		}
	case llm.ContentTypeToolResult:
		if !r.NestedToolResultTextOnly || part.ToolResult == nil {
			return nil
		}
		for _, nested := range part.ToolResult.Content {
			if !accepts(nested) {
				return errors.New("non-text tool results are not supported by " + r.Label + " models")
			}
		}
	}
	return nil
}
