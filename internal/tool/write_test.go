package tool_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestWriteExecuteCreatesFileWithoutApproval(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	write, err := tool.NewWrite(workspace)
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}

	_, err = write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "created.txt", "content": "created",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "created.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got, want := string(data), "created"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}

func TestWriteExecuteCreatesAndAtomicallyReplacesFiles(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	path := writeFixture(t, root, "nested/file.txt", "old")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("os.Chmod() error = %v", err)
	}
	write, err := tool.NewWrite(workspace)
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}

	result, err := write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "nested/file.txt", "content": "new content",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := resultText(t, result); !strings.Contains(got, "11 bytes") {
		t.Fatalf("Execute() text = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got := string(data); got != "new content" {
		t.Fatalf("file content = %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("os.Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("file mode = %o, want 600", got)
		}
	}
}

func TestWriteExecuteUsesHostPaths(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	workspace := newWorkspaceAt(t, root)
	write, err := tool.NewWrite(workspace)
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}

	absolutePath := filepath.Join(t.TempDir(), "absolute.txt")
	targets := []struct {
		name string
		path string
		want string
	}{
		{
			name: "relative traversal",
			path: "../outside.txt",
			want: filepath.Join(parent, "outside.txt"),
		},
		{
			name: "absolute",
			path: absolutePath,
			want: absolutePath,
		},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			content := "written via " + target.name
			_, err := write.Execute(t.Context(), toolCall(t, "write", map[string]any{
				"path": target.path, "content": content,
			}))
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			data, err := os.ReadFile(target.want)
			if err != nil {
				t.Fatalf("os.ReadFile() error = %v", err)
			}
			if got := string(data); got != content {
				t.Fatalf("file content = %q, want %q", got, content)
			}
		})
	}
}

func TestWriteExecuteRejectsMalformedPathBeforeMutation(t *testing.T) {
	t.Parallel()

	workspace, root := newWorkspace(t)
	write, err := tool.NewWrite(workspace)
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}
	_, err = write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "bad\x00path", "content": "must not be written",
	}))
	if err == nil || !strings.Contains(err.Error(), "null byte") {
		t.Fatalf("Execute() error = %v, want null-byte error", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("os.ReadDir() error = %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace entries = %v, want none", entries)
	}
}

func TestWriteExecuteValidatesContentBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   any
		missing   bool
		wantError bool
	}{
		{name: "missing", missing: true, wantError: true},
		{name: "null", content: nil, wantError: true},
		{name: "number", content: 42, wantError: true},
		{name: "boolean", content: false, wantError: true},
		{name: "array", content: []string{}, wantError: true},
		{name: "object", content: map[string]string{}, wantError: true},
		{name: "empty string", content: ""},
		{name: "normal content", content: "hello 世界\n"},
	}
	for _, test := range tests {
		for _, existing := range []bool{false, true} {
			target := "new"
			if existing {
				target = "existing"
			}
			t.Run(test.name+"/"+target, func(t *testing.T) {
				t.Parallel()
				workspace, root := newWorkspace(t)
				path := filepath.Join(root, "nested", "file.txt")
				var before os.FileInfo
				if existing {
					writeFixture(t, root, "nested/file.txt", "keep me")
					var err error
					before, err = os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
				}
				write, err := tool.NewWrite(workspace)
				if err != nil {
					t.Fatal(err)
				}
				args := map[string]any{"path": "nested/file.txt"}
				if !test.missing {
					args["content"] = test.content
				}
				result, err := write.Execute(t.Context(), toolCall(t, "write", args))
				if (err != nil) != test.wantError {
					t.Errorf("Execute() error = %v, want error %v", err, test.wantError)
				}
				if test.wantError && err != nil && !strings.Contains(err.Error(), "content") {
					t.Errorf("error = %v, want content diagnostic", err)
				}
				if test.wantError && !existing {
					entries, err := os.ReadDir(root)
					if err != nil {
						t.Fatal(err)
					}
					if len(entries) != 0 {
						t.Fatalf("invalid content created entries: %v", entries)
					}
					return
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				want := "keep me"
				if !test.wantError {
					want = test.content.(string)
				}
				if string(data) != want {
					t.Errorf("file content = %q, want %q", data, want)
				}
				if test.wantError {
					after, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					if !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
						t.Error("invalid content modified or replaced the file")
					}
					entries, err := os.ReadDir(filepath.Dir(path))
					if err != nil {
						t.Fatal(err)
					}
					if len(entries) != 1 {
						t.Errorf("unexpected mutation artifacts: %v", entries)
					}
				} else if result.IsError || result.CallID == "" || !strings.Contains(resultText(t, result), "bytes") {
					t.Errorf("unexpected successful result: %#v", result)
				}
			})
		}
	}
}
