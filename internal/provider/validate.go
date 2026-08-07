package provider

import (
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// MessageCapabilities declares the message content a provider accepts. The ID
// prefixes validation errors and Label names the model family.
type MessageCapabilities struct {
	ID                       llm.ProviderID
	Label                    string
	SupportsImage            bool
	SupportsRedactedThinking bool
	NestedToolResultTextOnly bool
}

// ValidateMessages rejects message content the provider cannot send.
func ValidateMessages(
	messages []llm.Message,
	capabilities MessageCapabilities,
) error {
	for messageIndex, message := range messages {
		var content []llm.ContentPart
		switch value := message.(type) {
		case llm.UserMessage:
			content = value.Content
		case llm.AssistantMessage:
			content = value.Content
		case llm.ToolResultMessage:
			for partIndex, part := range value.Content {
				if part.Type != llm.ContentTypeText {
					return fmt.Errorf(
						"%s: message %d content %d: non-text tool results "+
							"are not supported by %s models",
						capabilities.ID,
						messageIndex,
						partIndex,
						capabilities.Label,
					)
				}
			}
			continue
		case nil:
			return fmt.Errorf("%s: message %d is nil", capabilities.ID, messageIndex)
		default:
			return fmt.Errorf(
				"%s: message %d has unsupported type %T",
				capabilities.ID,
				messageIndex,
				message,
			)
		}
		for partIndex, part := range content {
			if err := validateContent(part, capabilities); err != nil {
				return fmt.Errorf(
					"%s: message %d content %d: %w",
					capabilities.ID,
					messageIndex,
					partIndex,
					err,
				)
			}
		}
	}
	return nil
}

func validateContent(part llm.ContentPart, capabilities MessageCapabilities) error {
	switch part.Type {
	case llm.ContentTypeImage:
		if !capabilities.SupportsImage {
			return errors.New(
				"image content is not supported by " + capabilities.Label + " models",
			)
		}
	case llm.ContentTypeThinking:
		if !capabilities.SupportsRedactedThinking && part.Redacted {
			return errors.New(
				"redacted thinking is not supported by " + capabilities.Label + " models",
			)
		}
	case llm.ContentTypeToolResult:
		if !capabilities.NestedToolResultTextOnly || part.ToolResult == nil {
			return nil
		}
		for _, nested := range part.ToolResult.Content {
			if nested.Type != llm.ContentTypeText {
				return errors.New(
					"non-text tool results are not supported by " + capabilities.Label + " models",
				)
			}
		}
	}
	return nil
}
