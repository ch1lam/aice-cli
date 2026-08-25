package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	maxOutputBytes   = 50 * 1024
	maxReadBytes     = 10 * 1024 * 1024
	maxMutationBytes = 4 * 1024 * 1024
	maxPatternBytes  = 4096
	maxWalkEntries   = 100000
)

func decodeArguments[T any](ctx context.Context, call llm.ToolCall, toolName string) (T, error) {
	var arguments T
	if ctx == nil {
		return arguments, fmt.Errorf("tool %q: context is required", toolName)
	}
	if err := ctx.Err(); err != nil {
		return arguments, err
	}
	if call.Name != toolName {
		return arguments, fmt.Errorf("tool %q: received call for %q", toolName, call.Name)
	}

	if err := jsonutil.DecodeStrict(call.Arguments, &arguments); err != nil {
		return arguments, fmt.Errorf("tool %q: decode arguments: %w", toolName, err)
	}
	return arguments, nil
}

func textResult(call llm.ToolCall, text string, isError bool) llm.ToolResult {
	return llm.ToolResult{
		CallID: call.ID,
		Name:   call.Name,
		Content: []llm.ContentPart{
			llm.NewTextContent(text).Part(),
		},
		IsError: isError,
	}
}

func jsonSchema(schema string) json.RawMessage {
	return json.RawMessage(schema)
}

type textCollector struct {
	builder   strings.Builder
	limit     int
	truncated bool
}

func newTextCollector(limit int) *textCollector {
	return &textCollector{limit: limit}
}

func (c *textCollector) WriteString(value string) bool {
	if c.truncated {
		return false
	}
	remaining := c.limit - c.builder.Len()
	if remaining <= 0 {
		c.truncated = true
		return false
	}
	if len(value) <= remaining {
		_, _ = c.builder.WriteString(value)
		return true
	}
	_, _ = c.builder.WriteString(validUTF8Prefix(value, remaining))
	c.truncated = true
	return false
}

func (c *textCollector) String() string {
	text := c.builder.String()
	if !c.truncated {
		return text
	}
	const marker = "\n[output truncated]"
	text = strings.TrimRight(text, "\n")
	if maximum := c.limit - len(marker); len(text) > maximum {
		text = validUTF8Prefix(text, max(0, maximum))
	}
	return text + marker
}

func validUTF8Prefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	if limit <= 0 {
		return ""
	}
	prefix := value[:limit]
	for !utf8.ValidString(prefix) && len(prefix) > 0 {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}
