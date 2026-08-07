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

// ValidateToolName rejects a tool with an empty name.
func ValidateToolName(index int, tool llm.ToolDefinition) error {
	if tool.Name == "" {
		return fmt.Errorf("tool %d name is required", index)
	}
	return nil
}

// DecodeToolSchemas validates tool definitions and decodes their JSON schemas.
func DecodeToolSchemas(tools []llm.ToolDefinition) ([]map[string]any, error) {
	schemas := make([]map[string]any, 0, len(tools))
	for index, tool := range tools {
		if err := ValidateToolName(index, tool); err != nil {
			return nil, err
		}
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
