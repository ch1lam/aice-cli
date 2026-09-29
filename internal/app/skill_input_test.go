package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
	"github.com/ch1lam/aice-cli/internal/skill"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestAttachedSkillsUseTrustedCatalogAndFullBody(t *testing.T) {
	t.Parallel()
	root, user, project := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestSkill(t, user, "review", "user description")
	writeTestSkill(t, project, "private", "project only")
	catalog := discoverSkills(user, project, false).catalog
	_, gate, err := newExecutionGuard(root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	s := &interactiveSession{skills: catalog, guardAdapter: gate}
	item, _ := catalog.Lookup("review")
	input := interaction.RunInput{Prompt: "before [skill:review] after", Skills: []string{"review", "review"}}
	prepared, err := s.prepareSkillInput(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Skills) != 0 || !strings.Contains(prepared.Prompt, input.Prompt) || !strings.Contains(prepared.Prompt, item.Body) ||
		strings.Count(prepared.Prompt, `<skill_content name="review">`) != 1 || !strings.Contains(prepared.Prompt, item.Dir) {
		t.Fatal("attachment did not freeze complete instructions once")
	}
	for _, names := range [][]string{{"private"}, {"review", "missing"}, {"Review"}} {
		if result, err := s.prepareSkillInput(t.Context(), interaction.RunInput{Prompt: "task", Skills: names}); err == nil || result.Prompt != "" {
			t.Fatal("unknown or untrusted skill was partially accepted", names)
		}
	}
	literal := interaction.RunInput{Prompt: "example [skill:review] /skill:review"}
	result, err := s.prepareSkillInput(t.Context(), literal)
	if err != nil || result.Prompt != literal.Prompt {
		t.Fatal("text was reparsed into attachment")
	}
	s.guardAdapter = nil
	if _, err := s.prepareSkillInput(t.Context(), input); err == nil {
		t.Fatal("attachment bypassed unavailable Guard")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.prepareSkillInput(ctx, literal); err == nil {
		t.Fatal("cancelled preparation succeeded")
	}
}

func TestAttachedSkillDeliveryAndSessionRetainAcceptedSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace, err := tool.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	_, gate, err := newExecutionGuard(root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := skill.Merge([]skill.Skill{{Name: "review", Body: "Frozen instructions: inspect carefully. @missing.txt"}})
	provider := &recordingModel{response: "done"}
	loop, err := agent.NewLoop(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "session.jsonl")
	store := createAppTestSession(t, path, root)
	t.Cleanup(func() { _ = store.Close() })
	s := &interactiveSession{loop: loop, model: openai.DefaultModel(), workspace: workspace, guardAdapter: gate,
		skills: catalog, conversation: conversationState{store: store}}
	active, err := s.NewRun(t.Context(), interaction.RunInput{Prompt: "initial", Skills: []string{"review"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := active.Deliver(t.Context(), interaction.Delivery{ID: "next", Text: "next", Skills: []string{"review"}, Kind: interaction.DeliveryKindFollowUp}); err != nil {
		t.Fatal(err)
	}
	// Delivery owns resolved content; changing the source cannot affect dequeue.
	s.skills = skill.Catalog{}
	if err := active.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "Frozen instructions: inspect carefully.") != 2 || len(provider.requests) != 2 {
		t.Fatal("initial/delivery snapshots were lost or loaded lazily")
	}
	if err := interaction.NewMailbox().Deliver(interaction.Delivery{ID: "raw", Text: "task", Skills: []string{"review"}, Kind: interaction.DeliveryKindSteer}); err == nil {
		t.Fatal("mailbox accepted unresolved skill")
	}
}
