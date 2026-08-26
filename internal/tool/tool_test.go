package tool_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func newWorkspace(t *testing.T) (*tool.Workspace, string) {
	t.Helper()
	path := t.TempDir()
	return newWorkspaceAt(t, path), path
}

func newWorkspaceAt(t *testing.T, path string) *tool.Workspace {
	t.Helper()
	workspace, err := tool.NewWorkspace(path)
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	return workspace
}

func toolCall(t *testing.T, name string, arguments any) llm.ToolCall {
	t.Helper()
	data, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return llm.ToolCall{ID: "call-1", Name: name, Arguments: data}
}

func resultText(t *testing.T, result llm.ToolResult) string {
	t.Helper()
	if len(result.Content) != 1 || result.Content[0].Type != llm.ContentTypeText {
		t.Fatalf("tool result content = %#v", result.Content)
	}
	return result.Content[0].Text
}

func writeFixture(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}

func TestToolDefinitionsUsePiNamesAndValidSchemas(t *testing.T) {
	t.Parallel()
	workspace, _ := newWorkspace(t)
	read, _ := tool.NewRead(workspace)
	write, _ := tool.NewWrite(workspace)
	edit, _ := tool.NewEdit(workspace)
	bash, err := tool.NewBash(workspace)
	if err != nil {
		t.Skipf("tool.NewBash() error = %v", err)
	}
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("tool.NewGrep() error = %v", err)
	}
	find, _ := tool.NewFind(workspace)
	ls, _ := tool.NewLS(workspace)
	skillTool := tool.NewSkill(nil)

	tools := []agent.Tool{read, write, edit, bash, grep, find, ls, skillTool}
	wantNames := []string{"read", "write", "edit", "bash", "grep", "find", "ls", "skill"}
	for index, currentTool := range tools {
		definition := currentTool.Definition()
		if definition.Name != wantNames[index] {
			t.Fatalf("tool %d name = %q, want %q", index, definition.Name, wantNames[index])
		}
		if !json.Valid(definition.InputSchema) {
			t.Fatalf("tool %q schema is invalid json: %s", definition.Name, definition.InputSchema)
		}
		if strings.Contains(strings.ToLower(definition.Description), "approval") {
			t.Fatalf("tool %q description still requires approval: %q", definition.Name, definition.Description)
		}
	}
}
