package llm

import (
	"fmt"
	"slices"
)

// RequestConfig is the effective non-message input of one model call. With
// the source history it determines the request exactly: BuildRequest and
// FitOutputTokens are the only derivation steps.
type RequestConfig struct {
	Model        Model            `json:"model"`
	SystemPrompt string           `json:"system_prompt,omitempty"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
	Options      StreamOptions    `json:"options"`
	// ResultViewTokens bounds each detailed tool result's model view.
	ResultViewTokens int64 `json:"result_view_tokens,omitempty"`
}

// Clone deep-copies every mutable field so a recorded configuration cannot
// alias the live run.
func (c RequestConfig) Clone() RequestConfig {
	cloned := c
	cloned.Model.ThinkingLevelMap = c.Model.ThinkingLevelMap.Clone()
	cloned.Model.InputModalities = slices.Clone(c.Model.InputModalities)
	if c.Options.Temperature != nil {
		temperature := *c.Options.Temperature
		cloned.Options.Temperature = &temperature
	}
	cloned.Tools = make([]ToolDefinition, len(c.Tools))
	for index, definition := range c.Tools {
		definition.InputSchema = slices.Clone(definition.InputSchema)
		definition.PromptGuidelines = slices.Clone(definition.PromptGuidelines)
		cloned.Tools[index] = definition
	}
	return cloned
}

// BuildRequest projects source history into one request. changed reports that
// a result view or image omission altered the projection, so provider usage
// from earlier calls may describe different content.
func BuildRequest(config RequestConfig, history []AgentMessage) (request Request, changed bool, err error) {
	messages, err := AgentMessagesToMessages(history)
	if err != nil {
		return Request{}, false, fmt.Errorf("llm: project history: %w", err)
	}
	changed = BoundToolResultViews(messages, config.ResultViewTokens)
	if !slices.Contains(config.Model.InputModalities, InputModalityImage) {
		changed = omitToolResultImages(messages) || changed
	}
	tools := config.Clone().Tools
	return Request{
		Model:        config.Model,
		SystemPrompt: config.SystemPrompt,
		Messages:     messages,
		Tools:        tools,
		Options:      config.Options,
	}, changed, nil
}

// FitOutputTokens lowers the output limit to what the context window leaves
// after the estimated request and safety margin. It returns the estimate and
// false when no output budget remains; unknown windows pass unchanged.
func FitOutputTokens(request Request) (Request, int64, bool) {
	contextWindow := request.Model.ContextWindow
	if contextWindow <= 0 {
		return request, 0, true
	}
	_, safetyTokens := ContextBudgets(contextWindow)
	estimate := EstimateContextTokens(request).Tokens
	requested := request.Options.MaxTokens
	if requested == 0 {
		requested = request.Model.MaxTokens
	}
	available := contextWindow - estimate - safetyTokens
	if available <= 0 {
		return Request{}, estimate, false
	}
	if requested > available {
		request.Options.MaxTokens = available
	}
	return request, estimate, true
}

// omitToolResultImages keeps a text-only model's request valid when a tool,
// such as an MCP service, returned images. Only the projected request changes;
// the recorded source result keeps its images for display and readback.
func omitToolResultImages(messages []Message) bool {
	omitted := false
	for i, message := range messages {
		result, ok := message.(ToolResultMessage)
		if !ok || !slices.ContainsFunc(result.Content, func(part ContentPart) bool { return part.Type == ContentTypeImage }) {
			continue
		}
		content := make([]ContentPart, len(result.Content))
		for j, part := range result.Content {
			content[j] = part
			if part.Type == ContentTypeImage {
				mime := "image"
				if part.Image != nil && part.Image.MIMEType != "" {
					mime = part.Image.MIMEType
				}
				content[j] = NewTextContent("[" + mime + " omitted: the current model does not accept image input. The recorded result keeps it; use an image-capable model to inspect it.]").Part()
			}
		}
		result.Content = content
		messages[i] = result
		omitted = true
	}
	return omitted
}
