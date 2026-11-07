package tui

import (
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestGuardSummaryPendingThenArrived(t *testing.T) {
	t.Parallel()
	summaries := make(chan interaction.GuardSummary, 1)
	started := 0
	current := newGuardTestModel(t, interaction.GuardRequest{
		ToolName: "bash",
		Command:  "rm -rf build",
		Options:  guardTestOptions(),
		Summarize: func() <-chan interaction.GuardSummary {
			started++
			return summaries
		},
	})
	if started != 1 {
		t.Fatalf("showing the prompt started %d summaries, want 1", started)
	}
	view := guardViewText(current)
	if !strings.Contains(view, "Summarizing…") || !strings.Contains(view, "1. Allow once") {
		t.Fatalf("pending prompt should show options and a placeholder at once:\n%s", view)
	}

	want := interaction.GuardSummary{Intent: "清理构建产物", Action: "删除 build 目录", Risk: "目录里的文件会丢失"}
	summaries <- want
	close(summaries)
	message := current.guard.waitForSummary()()
	arrived, ok := message.(guardSummaryMsg)
	if !ok || !arrived.ok || arrived.summary != want {
		t.Fatalf("summary message = %#v", message)
	}
	current = updateModel(t, current, arrived)
	view = guardViewText(current)
	for _, line := range []string{"Why   清理构建产物", "Does  删除 build 目录", "Risk  目录里的文件会丢失"} {
		if !strings.Contains(view, line) {
			t.Errorf("view is missing %q:\n%s", line, view)
		}
	}
	if strings.Contains(view, "Summarizing…") {
		t.Errorf("placeholder remains after arrival:\n%s", view)
	}
	if selected := current.guard.selection; selected != 0 || len(current.guard.request.Options) != len(guardTestOptions()) {
		t.Fatal("the summary changed the prompt's options or selection")
	}
}

func TestGuardSummaryOmittedOrStale(t *testing.T) {
	t.Parallel()
	summaries := make(chan interaction.GuardSummary)
	current := newGuardTestModel(t, interaction.GuardRequest{
		ToolName: "bash", Command: "ls", Options: guardTestOptions(),
		Summarize: func() <-chan interaction.GuardSummary { return summaries },
	})

	// A summary for an earlier request is ignored.
	current = updateModel(t, current, guardSummaryMsg{reply: make(chan interaction.GuardReply), summary: interaction.GuardSummary{Risk: "stale"}, ok: true})
	if view := guardViewText(current); strings.Contains(view, "stale") || !strings.Contains(view, "Summarizing…") {
		t.Fatalf("stale summary was applied:\n%s", view)
	}

	// A closed channel (timeout or error) removes the placeholder quietly.
	close(summaries)
	current = updateModel(t, current, current.guard.waitForSummary()())
	if view := guardViewText(current); strings.Contains(view, "Summarizing…") || strings.Contains(view, "Why") {
		t.Fatalf("omitted summary left text behind:\n%s", view)
	}

	// Requests without a summary channel never show the placeholder.
	plain := newGuardTestModel(t, interaction.GuardRequest{ToolName: "bash", Command: "ls", Options: guardTestOptions()})
	if view := guardViewText(plain); strings.Contains(view, "Summarizing…") || plain.guard.waitForSummary() != nil {
		t.Fatalf("prompt without a summary shows a placeholder:\n%s", view)
	}
}

// An expired request is never shown, so its summary never starts.
func TestGuardSummaryNotStartedForExpiredRequest(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)
	current := testModel(t, 80, 24)
	current = updateModel(t, current, guardRequestMsg{req: &interaction.GuardRequest{
		Done: done, Reply: make(chan interaction.GuardReply, 1), Options: guardTestOptions(),
		Summarize: func() <-chan interaction.GuardSummary {
			t.Error("an expired request started its summary")
			return nil
		},
	}})
	if current.guard.request != nil {
		t.Fatal("an expired request was shown")
	}
}

func TestGuardSummaryWaitEndsWithRequest(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)
	summaries := make(chan interaction.GuardSummary)
	request := &interaction.GuardRequest{Done: done, Summarize: func() <-chan interaction.GuardSummary { return summaries }}
	if message := newGuardPanel(request).waitForSummary()(); message != nil {
		t.Fatalf("finished request produced %#v", message)
	}
}
