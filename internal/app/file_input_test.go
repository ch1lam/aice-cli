package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestFileInputFreezesTextAndImageWithReadSemantics(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	workspace, err := tool.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, gate, err := newExecutionGuard(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	original := inputImage(t)
	if err := os.WriteFile(filepath.Join(dir, "图 片.dat"), original.Data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "text.txt"), []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := prepareFileInput(t.Context(), interaction.RunInput{Prompt: "compare", Files: []string{"text.txt", "图 片.dat", "text.txt"}}, workspace, gate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(input.Prompt, "original") || strings.Count(input.Prompt, "[File ") != 1 || len(input.Images) != 1 || input.Images[0].ID == "" {
		t.Fatalf("input = %s, images %d", input.Prompt, len(input.Images))
	}
	if err := os.WriteFile(filepath.Join(dir, "text.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input.Images[0].Data, original.Data) || !strings.Contains(input.Prompt, "original") {
		t.Fatal("snapshot changed")
	}
}

func TestFileInputDoesNotBypassGuardOrPartiallyAccept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := t.TempDir()
	workspace, err := tool.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, gate, err := newExecutionGuard(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "external.txt")
	if err := os.WriteFile(target, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if _, err := prepareFileInput(t.Context(), interaction.RunInput{Files: []string{link}}, workspace, gate, nil); err == nil {
		t.Fatal("symlink escaped path gate")
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	_, yolo, err := newExecutionGuard(dir, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareFileInput(t.Context(), interaction.RunInput{Files: []string{".env"}}, workspace, yolo, nil); err == nil {
		t.Fatal("yolo bypassed deny")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := prepareFileInput(ctx, interaction.RunInput{Files: []string{target}}, workspace, gate, nil); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestFileDeliveryFreezesBeforeMailboxAndDoesNotReparseFileText(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	workspace, err := tool.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, gate, err := newExecutionGuard(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("old @missing.txt"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &recordingModel{response: "done"}
	loop, err := agent.NewLoop(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := createAppTestSession(t, filepath.Join(dir, "session.jsonl"), dir)
	t.Cleanup(func() { _ = store.Close() })
	runner := &interactiveSession{loop: loop, model: openai.DefaultModel(), workspace: workspace, guardAdapter: gate, conversation: conversationState{store: store}}
	active, err := runner.NewRun(t.Context(), interaction.RunInput{Prompt: "initial"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := active.Deliver(t.Context(), interaction.Delivery{ID: "next", Text: "@note.txt", Files: []string{"note.txt"}, Kind: interaction.DeliveryKindFollowUp}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := active.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("old @missing.txt")) {
		t.Fatal("accepted file snapshot lost")
	}
}
