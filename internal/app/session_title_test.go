package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
)

// titleAnswer answers a Session title side call without touching a fake's
// conversation state, so fakes keep counting only main-run requests.
func titleAnswer(request llm.Request) (llm.Stream, bool) {
	if request.SystemPrompt != sessionTitleSystem {
		return nil, false
	}
	return textEventStream(request.Model, "Test title", llm.Usage{}, llm.StopReasonStop), true
}

// fakeTitleModel records title completions. A non-nil release holds each
// answer until it is closed or the call is cancelled.
type fakeTitleModel struct {
	mu       sync.Mutex
	answer   string
	err      error
	release  chan struct{}
	started  chan struct{}
	requests []classify.Completion
}

func (m *fakeTitleModel) open(configuredModel) (classify.Model, error) { return m, nil }

func (m *fakeTitleModel) Complete(ctx context.Context, req classify.Completion) (string, llm.Usage, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	if m.started != nil {
		close(m.started)
	}
	if m.release != nil {
		select {
		case <-m.release:
		case <-ctx.Done():
			return "", llm.Usage{}, ctx.Err()
		}
	}
	return m.answer, llm.Usage{}, m.err
}

func (m *fakeTitleModel) calls() []classify.Completion {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]classify.Completion(nil), m.requests...)
}

func storeTitles(t *testing.T, store *session.Store) []string {
	t.Helper()
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, title := range snapshot.Titles {
		titles = append(titles, title.Title)
	}
	return titles
}

func TestSessionTitleGeneratedOnceAfterFirstRun(t *testing.T) {
	s := browserHarness(t)
	loop, err := agent.NewLoop(&recordingModel{response: "先检查 parser.go 的空指针。"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	titles := &fakeTitleModel{answer: "  标题：“修复解析器空指针”。\n第二行"}
	s.loop, s.model, s.titleModel = loop, deepseek.DefaultModel(), titles.open
	s.titleUpdates = make(chan interaction.SessionTitle, 1)
	t.Cleanup(func() {
		if s.conversation.store != nil {
			s.conversation.store.Close()
		}
	})

	if err := runInteractive(t.Context(), s, "为什么解析器会空指针？", nil); err != nil {
		t.Fatal(err)
	}
	s.titles.running.Wait()
	store := s.conversation.store
	if got := storeTitles(t, store); len(got) != 1 || got[0] != "修复解析器空指针" {
		t.Fatalf("titles = %q", got)
	}
	calls := titles.calls()
	if len(calls) != 1 || calls[0].System != sessionTitleSystem || calls[0].Thinking != llm.ThinkingLevelOff ||
		!strings.Contains(calls[0].Prompt, "为什么解析器会空指针？") || !strings.Contains(calls[0].Prompt, "先检查 parser.go 的空指针。") {
		t.Fatalf("title requests = %+v", calls)
	}
	info, _ := store.Info()
	if announced := <-s.titleUpdates; announced != (interaction.SessionTitle{SessionID: info.Header.ID, Title: "修复解析器空指针"}) {
		t.Fatalf("announced = %+v", announced)
	}

	// Later turns never regenerate, even after the title is cleared by hand.
	empty, _ := session.NewTitle("cleared", "", 1)
	if err := store.AppendTitle(t.Context(), empty); err != nil {
		t.Fatal(err)
	}
	s.titles = sessionTitler{}
	if err := runInteractive(t.Context(), s, "再看看测试", nil); err != nil {
		t.Fatal(err)
	}
	s.titles.close()
	if len(titles.calls()) != 1 || len(storeTitles(t, store)) != 2 {
		t.Fatal("a titled Session was titled again")
	}
}

func TestSessionTitleSkipsTitledResumeAndRetriesNothingAfterFailure(t *testing.T) {
	s := browserHarness(t)
	titled := browserFixture(t, s, "titled", "Question", "Answer", 100)
	manual, _ := session.NewTitle("manual", "Manual", 1)
	if err := titled.AppendTitle(t.Context(), manual); err != nil {
		t.Fatal(err)
	}
	titles := &fakeTitleModel{err: errors.New("small model unavailable")}
	s.titleModel = titles.open
	s.titleSession(titled, configuredModel{})
	s.titles.running.Wait()
	if len(titles.calls()) != 0 {
		t.Fatal("resumed titled Session asked for a title")
	}

	failed := browserFixture(t, s, "failed", "First question", "Answer", 200)
	for range 2 {
		s.titleSession(failed, configuredModel{})
		s.titles.running.Wait()
	}
	if len(titles.calls()) != 1 || len(storeTitles(t, failed)) != 0 {
		t.Fatalf("failure retried or saved: calls %d", len(titles.calls()))
	}
	items, err := s.SearchSessions(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Key == "failed" && item.Title != "First question" {
			t.Fatalf("fallback title = %q", item.Title)
		}
	}

	unusable := browserFixture(t, s, "unusable", "Second question", "Answer", 300)
	titles.err, titles.answer = nil, " “。” \n"
	s.titleSession(unusable, configuredModel{})
	s.titles.close()
	if len(storeTitles(t, unusable)) != 0 {
		t.Fatal("empty model output saved as a title")
	}
}

func TestSessionTitleNeverOverwritesConcurrentRename(t *testing.T) {
	s := browserHarness(t)
	store := browserFixture(t, s, "one", "Question", "Answer", 100)
	s.conversation.store = store
	if items, err := s.SearchSessions(t.Context(), ""); err != nil || items[0].Title != "Question" {
		t.Fatal(items, err)
	}
	titles := &fakeTitleModel{answer: "Generated", release: make(chan struct{}), started: make(chan struct{})}
	s.titleModel = titles.open
	s.titleUpdates = make(chan interaction.SessionTitle, 1)
	s.titleSession(store, configuredModel{})
	<-titles.started
	if _, err := s.RenameSession(t.Context(), "one", "Manual"); err != nil {
		t.Fatal(err)
	}
	close(titles.release)
	s.titles.close()
	if got := storeTitles(t, store); len(got) != 1 || got[0] != "Manual" {
		t.Fatalf("titles = %q", got)
	}
	if len(s.titleUpdates) != 0 {
		t.Fatal("dropped title announced")
	}
}

func TestSessionTitleShowsInCatalogAndCloseCancels(t *testing.T) {
	s := browserHarness(t)
	store := browserFixture(t, s, "one", "Question", "Answer", 100)
	if items, err := s.SearchSessions(t.Context(), ""); err != nil || items[0].Title != "Question" {
		t.Fatal(items, err)
	}
	titles := &fakeTitleModel{answer: "Generated title"}
	s.titleModel = titles.open
	s.titleSession(store, configuredModel{})
	s.titles.running.Wait()
	if items, err := s.SearchSessions(t.Context(), ""); err != nil || items[0].Title != "Generated title" {
		t.Fatal("cached catalog kept the fallback", items, err)
	}

	pending := browserFixture(t, s, "pending", "Question", "Answer", 200)
	blocked := &fakeTitleModel{answer: "Late", release: make(chan struct{}), started: make(chan struct{})}
	s.titleModel = blocked.open
	s.titleSession(pending, configuredModel{})
	<-blocked.started
	s.titles.close()
	if len(storeTitles(t, pending)) != 0 {
		t.Fatal("cancelled title saved")
	}
	s.titleSession(browserFixture(t, s, "after", "Question", "Answer", 300), configuredModel{})
	if len(blocked.calls()) != 1 {
		t.Fatal("closed titler started another title")
	}
}

func TestCleanSessionTitle(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"Fix parser crash.":                  "Fix parser crash",
		"\"Fix parser crash\"":               "Fix parser crash",
		"Title: **Refactor Guard**":          "Refactor Guard",
		"# 重构会话标题生成\n\n说明":                   "重构会话标题生成",
		"「排查 CI 失败」？":                        "排查 CI 失败",
		"Fix‮ parser\tcrash (Go)":            "Fix parser crash (Go)",
		strings.Repeat("长", 60):              strings.Repeat("长", 50),
		strings.Repeat("a", 49) + " b. tail": strings.Repeat("a", 49),
		"\n  \n":                             "",
	} {
		if got := cleanSessionTitle(input); got != want {
			t.Errorf("cleanSessionTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFirstExchangeUsesTypedTextAndVisibleReply(t *testing.T) {
	t.Parallel()
	user := llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("explain main.go" + attachedFileMarker + "main.go]\npackage main").Part()}}
	tool := llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentPart{
		{Type: llm.ContentTypeThinking, Text: "hidden"},
		{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "1", Name: "read"}},
	}}
	answer := llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.NewTextContent("It starts the CLI.").Part()}}
	gotUser, gotReply := firstExchange([]session.MessageEntry{{Message: user}, {Message: tool}, {Message: answer}})
	if gotUser != "explain main.go" || gotReply != "It starts the CLI." {
		t.Fatalf("first exchange = %q, %q", gotUser, gotReply)
	}
	prompt := sessionTitlePrompt(strings.Repeat("问", sessionTitleUserRunes+5), "reply")
	if strings.Count(prompt, "问") != sessionTitleUserRunes || !strings.Contains(prompt, "…") {
		t.Fatal("user text not truncated to its budget")
	}
}
