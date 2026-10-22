package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/utf8text"
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
	_, _ = c.builder.WriteString(utf8text.Prefix(value, remaining))
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
		text = utf8text.Prefix(text, max(0, maximum))
	}
	return text + marker
}

// utf8Window returns the end of the readable page [offset, end) of value, at
// most length bytes and ending on a rune boundary. Offsets come from a
// previous page's next_offset, so one that splits a character is rejected.
func utf8Window(value string, offset, length int) (int, error) {
	if offset > len(value) || offset < len(value) && !utf8.RuneStart(value[offset]) {
		return 0, fmt.Errorf("Offset is outside the text or splits UTF-8; use the returned next_offset.")
	}
	end := offset + len(utf8text.Prefix(value[offset:], length))
	if end == offset && end < len(value) {
		return 0, fmt.Errorf("Length is too small for the next UTF-8 character.")
	}
	return end, nil
}
