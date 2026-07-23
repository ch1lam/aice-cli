package tool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestNewWorkspaceNormalizesWorkingDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	workingDirectory := filepath.Join(parent, "work")
	if err := os.Mkdir(workingDirectory, 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}

	uncleanPath := workingDirectory + string(os.PathSeparator) + ".." +
		string(os.PathSeparator) + "work"
	workspace, err := tool.NewWorkspace(uncleanPath)
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	if got, want := workspace.Path(), filepath.Clean(workingDirectory); got != want {
		t.Fatalf("Workspace.Path() = %q, want %q", got, want)
	}
}

func TestNewWorkspaceRejectsInvalidWorkingDirectory(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o640); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "blank", path: "  ", wantErr: "path is required"},
		{name: "null byte", path: "bad\x00path", wantErr: "null byte"},
		{name: "missing", path: filepath.Join(t.TempDir(), "missing"), wantErr: "inspect working directory"},
		{name: "file", path: filePath, wantErr: "not a directory"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := tool.NewWorkspace(test.path)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("NewWorkspace() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}
