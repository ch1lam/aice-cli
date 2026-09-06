package streamcore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

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

// ValidateTemperature rejects temperatures above the protocol-supported
// maximum of 2.
func ValidateTemperature(temperature *float64) error {
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
