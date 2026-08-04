package tool_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/tool"
)

var _ agent.Tool = (*tool.Unavailable)(nil)

func TestUnavailableDefinitionAdvertisesValidTool(t *testing.T) {
	t.Parallel()
	unavailable := tool.NewUnavailable("grep", errors.New("ripgrep not installed"))

	definition := unavailable.Definition()
	if definition.Name != "grep" {
		t.Fatalf("Definition().Name = %q, want grep", definition.Name)
	}
	if !strings.Contains(strings.ToLower(definition.Description), "unavailable") {
		t.Fatalf("Definition().Description = %q, want unavailable notice", definition.Description)
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatalf("Definition().InputSchema is not valid JSON: %v (%s)", err, definition.InputSchema)
	}
}

func TestUnavailableExecuteReturnsReason(t *testing.T) {
	t.Parallel()
	reason := errors.New("missing ripgrep")
	unavailable := tool.NewUnavailable("grep", reason)

	result, err := unavailable.Execute(t.Context(), toolCall(t, "grep", map[string]any{}))
	if err == nil || !strings.Contains(err.Error(), "missing ripgrep") {
		t.Fatalf("Execute() error = %v, want missing ripgrep", err)
	}
	if len(result.Content) != 0 {
		t.Fatalf("Execute() result = %#v, want empty result on error", result)
	}
}
