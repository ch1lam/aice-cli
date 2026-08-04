package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// TestNewBuiltInToolsDegradesGracefully verifies that when bash and ripgrep are
// missing from PATH the app still builds the full 7-tool set and the affected
// tools become unavailable stubs instead of failing startup.
func TestNewBuiltInToolsDegradesGracefully(t *testing.T) {
	workspace, err := tool.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	// Not parallel: hides bash and ripgrep from PATH for this test only.
	t.Setenv("PATH", t.TempDir())

	tools, err := newBuiltInTools(workspace)
	if err != nil {
		t.Fatalf("newBuiltInTools() error = %v", err)
	}
	names := make([]string, len(tools))
	for index, current := range tools {
		names[index] = current.Definition().Name
	}
	want := []string{"read", "write", "edit", "bash", "grep", "find", "ls"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("newBuiltInTools() names = %v, want %v", names, want)
	}

	for index, name := range want {
		if name != "bash" && name != "grep" {
			continue
		}
		call := llm.ToolCall{ID: "call-1", Name: name, Arguments: []byte(`{}`)}
		result, err := tools[index].Execute(t.Context(), call)
		if err == nil {
			t.Fatalf("%s tool executed despite missing executable", name)
		}
		if !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("%s Execute() error = %v, want unavailable", name, err)
		}
		if len(result.Content) != 0 {
			t.Fatalf("%s Execute() result = %#v, want empty", name, result)
		}
	}
}
