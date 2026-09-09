package tool_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestEditArgumentsRejectInvalidEntriesBeforeMutation(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, entry, field string }{
		{"missing newText", `{"oldText":"beta"}`, ".newText"},
		{"null newText", `{"oldText":"beta","newText":null}`, ".newText"},
		{"number newText", `{"oldText":"beta","newText":42}`, ".newText"},
		{"boolean newText", `{"oldText":"beta","newText":false}`, ".newText"},
		{"array newText", `{"oldText":"beta","newText":[]}`, ".newText"},
		{"object newText", `{"oldText":"beta","newText":{}}`, ".newText"},
		{"missing oldText", `{"newText":"two"}`, ".oldText"},
		{"null oldText", `{"oldText":null,"newText":"two"}`, ".oldText"},
		{"empty oldText", `{"oldText":"","newText":"two"}`, ".oldText"},
		{"number oldText", `{"oldText":42,"newText":"two"}`, ".oldText"},
		{"boolean oldText", `{"oldText":false,"newText":"two"}`, ".oldText"},
		{"array oldText", `{"oldText":[],"newText":"two"}`, ".oldText"},
		{"object oldText", `{"oldText":{},"newText":"two"}`, ".oldText"},
		{"null entry", `null`, ""},
		{"number entry", `42`, ""},
		{"boolean entry", `false`, ""},
		{"string entry", `"replacement"`, ""},
		{"array entry", `[]`, ""},
		{"empty entry", `{}`, ".oldText"},
		{"unknown field", `{"oldText":"beta","newText":"two","extra":true}`, ""},
	}
	for _, test := range tests {
		for index := range 3 {
			t.Run(fmt.Sprintf("%s/index%d", test.name, index), func(t *testing.T) {
				t.Parallel()
				entries := []string{`{"oldText":"alpha","newText":"one"}`, `{"oldText":"beta","newText":"two"}`, `{"oldText":"gamma","newText":"three"}`}
				entries[index] = test.entry
				args := `{"path":"file.txt","edits":[` + strings.Join(entries, ",") + `]}`
				assertEditArgumentsRejected(t, args, fmt.Sprintf("edits[%d]%s", index, test.field))
			})
		}
	}
}

func TestEditArgumentsRejectInvalidEnvelopeBeforeMutation(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, args, diagnostic string }{
		{"missing path", `{"edits":[{"oldText":"alpha","newText":"one"}]}`, "path"},
		{"null path", `{"path":null,"edits":[{"oldText":"alpha","newText":"one"}]}`, "path"},
		{"number path", `{"path":42,"edits":[{"oldText":"alpha","newText":"one"}]}`, "path"},
		{"empty path", `{"path":"","edits":[{"oldText":"alpha","newText":"one"}]}`, "path"},
		{"null byte path", `{"path":"file.txt\u0000","edits":[{"oldText":"alpha","newText":"one"}]}`, "null byte"},
		{"missing edits", `{"path":"file.txt"}`, "edits"},
		{"null edits", `{"path":"file.txt","edits":null}`, "edits"},
		{"empty edits", `{"path":"file.txt","edits":[]}`, "edits"},
		{"number edits", `{"path":"file.txt","edits":42}`, "edits"},
		{"boolean edits", `{"path":"file.txt","edits":false}`, "edits"},
		{"object edits", `{"path":"file.txt","edits":{"oldText":"alpha","newText":"one"}}`, "edits"},
		{"stringified edits", `{"path":"file.txt","edits":"[{\"oldText\":\"alpha\",\"newText\":\"one\"}]"}`, "edits"},
		{"legacy fields", `{"path":"file.txt","oldText":"alpha","newText":"one"}`, "unknown field"},
		{"unknown field", `{"path":"file.txt","edits":[{"oldText":"alpha","newText":"one"}],"extra":true}`, "unknown field"},
		{"null arguments", `null`, "path"},
		{"array arguments", `[]`, "decode arguments"},
		{"malformed json", `{"path":"file.txt","edits":[`, "decode arguments"},
		{"trailing json", `{"path":"file.txt","edits":[{"oldText":"alpha","newText":"one"}]} {}`, "multiple json values"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertEditArgumentsRejected(t, test.args, test.diagnostic)
		})
	}
}

func assertEditArgumentsRejected(t *testing.T, args, diagnostic string) {
	t.Helper()
	workspace, root := newWorkspace(t)
	const original = "alpha beta gamma\n"
	path := writeFixture(t, root, "file.txt", original)
	retained := filepath.Join(root, "original.txt")
	if err := os.Link(path, retained); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := tool.NewEdit(workspace)
	if err != nil {
		t.Fatal(err)
	}
	_, err = edit.Execute(t.Context(), llm.ToolCall{ID: "invalid-edit", Name: "edit", Arguments: []byte(args)})
	if err == nil || !strings.Contains(err.Error(), diagnostic) {
		t.Errorf("Execute() error = %v, want %q", err, diagnostic)
	}
	for _, filename := range []string{path, retained} {
		data, err := os.ReadFile(filename)
		if err != nil || string(data) != original {
			t.Errorf("%s content = %q, error = %v", filename, data, err)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.Stat(retained)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(saved, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Error("invalid arguments modified or replaced the file")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("unexpected mutation artifacts: %v", entries)
	}
}

func TestEditArgumentsAllowExplicitDeletion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, content, oldText, want string }{
		{"partial", "alpha beta gamma", "beta ", "alpha gamma"},
		{"whole file", "alpha", "alpha", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			workspace, root := newWorkspace(t)
			path := writeFixture(t, root, "file.txt", test.content)
			edit, err := tool.NewEdit(workspace)
			if err != nil {
				t.Fatal(err)
			}
			result, err := edit.Execute(t.Context(), toolCall(t, "edit", map[string]any{
				"path": "file.txt", "edits": []map[string]string{{"oldText": test.oldText, "newText": ""}},
			}))
			if err != nil || result.IsError {
				t.Fatalf("Execute() = %+v, %v", result, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != test.want {
				t.Fatalf("content = %q, error = %v, want %q", data, err, test.want)
			}
		})
	}
}
