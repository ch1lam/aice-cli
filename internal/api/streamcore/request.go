package streamcore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ResolveMaxTokens applies the request option, falling back to the model
// default, and rejects non-positive totals.
func ResolveMaxTokens(request llm.Request) (int64, error) {
	maxTokens := request.Options.MaxTokens
	if maxTokens == 0 {
		maxTokens = request.Model.MaxTokens
	}
	if maxTokens <= 0 {
		return 0, errors.New("max tokens must be positive")
	}
	return maxTokens, nil
}

// ValidateBaseURL requires an absolute HTTP(S) endpoint with a host.
func ValidateBaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse base URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("base URL scheme %q is not HTTP(S)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("base URL must include a host")
	}
	return nil
}

// CheckRequest applies the protocol-independent checks every adapter performs
// before building wire parameters for api.
func CheckRequest(ctx context.Context, request llm.Request, api llm.API) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate request: %w", err)
	}
	if request.Model.API != api {
		return fmt.Errorf("model API %q does not match %q", request.Model.API, api)
	}
	return validateTemperature(request.Options.Temperature)
}

// SameModel reports whether an assistant message was produced by the target
// model, so its opaque signatures and reasoning can be replayed verbatim.
func SameModel(message llm.AssistantMessage, target llm.Model) bool {
	return message.Provider == target.Provider &&
		message.API == target.API &&
		message.ModelID == target.ID
}

// validateTemperature rejects temperatures above the protocol-supported
// maximum of 2.
func validateTemperature(temperature *float64) error {
	if temperature != nil && *temperature > 2 {
		return errors.New("temperature cannot exceed 2")
	}
	return nil
}

// DecodeToolSchemas decodes JSON schemas from tool definitions already checked
// by Request.Validate at the adapter entry point.
func DecodeToolSchemas(tools []llm.ToolDefinition) ([]map[string]any, error) {
	schemas := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, fmt.Errorf("tool %q input schema: %w", tool.Name, err)
		}
		schemas = append(schemas, schema)
	}
	return schemas, nil
}

// ImageDataURL encodes inline image content as a data URL.
func ImageDataURL(image llm.ImageContent) string {
	return "data:" + image.MIMEType + ";base64," +
		base64.StdEncoding.EncodeToString(image.Data)
}
