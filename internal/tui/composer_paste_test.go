package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeText(t *testing.T, current model, text string) model {
	t.Helper()
	for _, r := range text {
		current = updateModel(t, current, tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
	return current
}

func largePasteFixture(first string, lines int) string {
	rows := make([]string, lines)
	for index := range rows {
		rows[index] = first
	}
	return strings.Join(rows, "\n")
}

func TestPastePlaceholderKeepsSurroundingOrder(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = typeText(t, current, "AAAA")
	paste := largePasteFixture("pasted body", 50)
	current = updateModel(t, current, tea.PasteMsg{Content: paste})
	current = typeText(t, current, "bbb")

	if len(current.pastes) != 1 {
		t.Fatalf("paste attachments = %d, want 1", len(current.pastes))
	}
	token := current.pastes[0].token
	if got := current.input.Value(); got != "AAAA"+token+"bbb" {
		t.Errorf("composer value = %q, want surrounding text kept around the token", got)
	}
	if got, want := current.expandComposerText(), "AAAA"+paste+"bbb"; got != want {
		t.Error("expanded text lost the leading input before the paste")
	}
}

func TestSmallPasteStaysPlain(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = updateModel(t, current, tea.PasteMsg{Content: "one\ntwo\nthree"})

	if len(current.pastes) != 0 {
		t.Error("small paste collapsed into a placeholder")
	}
	if got := current.input.Value(); got != "one\ntwo\nthree" {
		t.Errorf("pasted value = %q, want it editable in place", got)
	}
}

func TestLongSingleLinePasteCollapses(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = updateModel(t, current, tea.PasteMsg{Content: strings.Repeat("x", 1200)})

	if len(current.pastes) != 1 {
		t.Fatal("long single-line paste did not collapse into a placeholder")
	}
	if got := current.input.Value(); !strings.Contains(got, current.pastes[0].token) {
		t.Errorf("composer value = %q, want the placeholder token inline", got)
	}
}

func TestPlaceholderCursorMovesAsOne(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = typeText(t, current, "AAAA")
	current = updateModel(t, current, tea.PasteMsg{Content: largePasteFixture("body", 50)})
	tokenRunes := len([]rune(current.pastes[0].token))

	// Left from right after the token jumps over the whole token.
	updated := updateModel(t, current, tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	if got, want := updated.input.Column(), 4; got != want {
		t.Fatalf("column after left = %d, want token start %d", got, want)
	}
	// One more step moves into the normal text rune by rune.
	updated = updateModel(t, updated, tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	if got, want := updated.input.Column(), 3; got != want {
		t.Fatalf("column after second left = %d, want %d", got, want)
	}
	// Right back to the token start, then one step jumps over it.
	updated = updateModel(t, updated, tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	updated = updateModel(t, updated, tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	if got, want := updated.input.Column(), 4+tokenRunes; got != want {
		t.Fatalf("column after jumping right = %d, want token end %d", got, want)
	}
}

func TestBackspaceRemovesPlaceholderAsOne(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = typeText(t, current, "AAAA")
	current = updateModel(t, current, tea.PasteMsg{Content: largePasteFixture("remove me", 50)})
	current = typeText(t, current, "bbb")

	// Move to the token end: left jumps over the tail word-wise? No—step
	// left across "bbb" then the token jump lands at its start; instead go
	// left 3 for the tail then the atomic jump takes the token.
	for range 3 {
		current = updateModel(t, current, tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	}
	// Cursor is at the token end; one backspace removes token and attachment.
	updated := updateModel(t, current, tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	if len(updated.pastes) != 0 {
		t.Fatal("backspace did not drop the placeholder attachment")
	}
	if got, want := updated.input.Value(), "AAAAbbb"; got != want {
		t.Errorf("composer value = %q, want %q", got, want)
	}
}

func TestDeleteForwardRemovesPlaceholderAsOne(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = typeText(t, current, "AAAA")
	current = updateModel(t, current, tea.PasteMsg{Content: largePasteFixture("remove me", 50)})

	// Cursor starts after the token; left jumps to its start.
	current = updateModel(t, current, tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	updated := updateModel(t, current, tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	if len(updated.pastes) != 0 {
		t.Fatal("delete did not drop the placeholder attachment")
	}
	if got, want := updated.input.Value(), "AAAA"; got != want {
		t.Errorf("composer value = %q, want %q", got, want)
	}
}

func TestTypingAfterPlaceholderKeepsIt(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	paste := largePasteFixture("keep me", 50)
	current = updateModel(t, current, tea.PasteMsg{Content: paste})
	updated := typeText(t, current, "hi")

	if len(updated.pastes) != 1 {
		t.Fatal("typing after the placeholder unfolded it")
	}
	if got, want := updated.expandComposerText(), paste+"hi"; got != want {
		t.Error("typing after the placeholder changed the pasted content")
	}
}

func TestBackspaceInTailDeletesNormally(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = updateModel(t, current, tea.PasteMsg{Content: largePasteFixture("tail test", 50)})
	token := current.pastes[0].token
	updated := typeText(t, current, "ab")
	updated = updateModel(t, updated, tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))

	if len(updated.pastes) != 1 {
		t.Fatal("backspace inside the tail disturbed the placeholder")
	}
	if got, want := updated.input.Value(), token+"a"; got != want {
		t.Errorf("composer value = %q, want %q", got, want)
	}
}

func TestSubmitExpandsPlaceholdersInOrder(t *testing.T) {
	t.Parallel()

	requests := make(chan runRequest, 1)
	current := newModel(requests, make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	paste := largePasteFixture("ordered paste", 10)
	current = updateModel(t, current, tea.PasteMsg{Content: paste})
	current = typeText(t, current, "tail")

	updated, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !handled || command == nil {
		t.Fatal("enter with a placeholder did not submit")
	}
	if message := command(); message == nil {
		t.Fatal("submit command produced no message")
	}
	request := <-requests
	if request.prompt != paste+"tail" {
		t.Error("submitted prompt did not expand the placeholder in order")
	}
	if len(updated.pastes) != 0 {
		t.Error("submit did not clear the placeholders")
	}
}

func TestDuplicatePastesGainUniqueTokens(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	paste := largePasteFixture("same body", 20)
	current = updateModel(t, current, tea.PasteMsg{Content: paste})
	current = updateModel(t, current, tea.PasteMsg{Content: paste})

	if len(current.pastes) != 2 {
		t.Fatalf("paste attachments = %d, want 2", len(current.pastes))
	}
	if current.pastes[0].token == current.pastes[1].token {
		t.Error("duplicate pastes share one token")
	}
	if got, want := current.expandComposerText(), paste+paste; got != want {
		t.Error("duplicate placeholders did not expand in order")
	}
}

func TestHistoryDraftKeepsExpandedText(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.promptHistory = []string{"old prompt"}
	paste := largePasteFixture("draft body", 20)
	current = updateModel(t, current, tea.PasteMsg{Content: paste})

	recalled := current.recallHistory(-1)
	if recalled.historyDraft != paste {
		t.Error("history draft did not keep the expanded pasted text")
	}
	if len(recalled.pastes) != 0 {
		t.Error("recalling history kept placeholder attachments")
	}
	if got := recalled.input.Value(); got != "old prompt" {
		t.Errorf("recalled value = %q, want the history entry", got)
	}
	restored := recalled.recallHistory(1)
	if got := restored.input.Value(); got != paste {
		t.Error("restored draft is not the plain expanded text")
	}
}

func TestEditorRefillClearsPlaceholders(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current = updateModel(t, current, tea.PasteMsg{Content: largePasteFixture("edited body", 20)})
	if len(current.pastes) != 1 {
		t.Fatal("large paste did not collapse into a placeholder")
	}

	edited := "line one\nline two edited"
	file := filepath.Join(t.TempDir(), "compose.md")
	if err := os.WriteFile(file, []byte(edited+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated := current.applyEditorResult(editorFinishedMsg{file: file})

	if len(updated.pastes) != 0 {
		t.Error("editor refill kept placeholder attachments")
	}
	if got := updated.input.Value(); got != edited {
		t.Errorf("refilled value = %q, want plain edited text %q", got, edited)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("editor temp file was not removed")
	}
}

func TestEditorErrorKeepsDraft(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	paste := largePasteFixture("kept body", 20)
	current = updateModel(t, current, tea.PasteMsg{Content: paste})
	before := current.input.Value()

	updated := current.applyEditorResult(editorFinishedMsg{file: filepath.Join(t.TempDir(), "missing.md"), err: os.ErrNotExist})

	if len(updated.pastes) != 1 {
		t.Error("failed editor run dropped the placeholder")
	}
	if got := updated.input.Value(); got != before {
		t.Error("failed editor run changed the draft")
	}
}

func TestEditorShortcutOpensExternalEditor(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	_, command, handled := current.handleKey(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	if !handled {
		t.Fatal("ctrl+g was not handled")
	}
	if command == nil {
		t.Fatal("ctrl+g produced no editor command")
	}
}

func TestKeyMapEditorShowsInFullHelp(t *testing.T) {
	t.Parallel()

	keys := newKeyMap()
	if got := keys.editor.Help(); got.Key != "ctrl+g" || got.Desc != "editor" {
		t.Errorf("editor label = %#v, want ctrl+g editor", got)
	}
	found := false
	for _, row := range keys.FullHelp() {
		for _, binding := range row {
			if binding.Help().Key == "ctrl+g" {
				found = true
			}
		}
	}
	if !found {
		t.Error("full help is missing the ctrl+g editor binding")
	}
}

func TestExceedsPasteThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "short text", value: "hello", want: false},
		{name: "five lines", value: "1\n2\n3\n4\n5", want: false},
		{name: "six lines", value: "1\n2\n3\n4\n5\n6", want: false},
		{name: "seven lines", value: "1\n2\n3\n4\n5\n6\n7", want: true},
		{name: "thousand runes", value: strings.Repeat("x", 1000), want: false},
		{name: "over thousand runes", value: strings.Repeat("x", 1001), want: true},
		{name: "multibyte runes stay intact", value: strings.Repeat("你", 1001), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := exceedsPasteThreshold(tt.value); got != tt.want {
				t.Errorf("exceedsPasteThreshold() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPasteExcerpt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		lines int
		want  string
	}{
		{name: "cjk first five", text: "你好世界真棒啊\nsecond", lines: 2, want: "[你好世界真·2lines]"},
		{name: "spaces fold", text: "hello world foo\nsecond", lines: 2, want: "[hello·2lines]"},
		{name: "empty first line", text: "\nbody", lines: 2, want: "[paste·2lines]"},
		{name: "trailing newline counts once", text: "a\nb\n", lines: 2, want: "[a·2lines]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			current := newModel(make(chan runRequest), make(chan struct{}))
			attachment := current.newPasteAttachment(tt.text)
			if attachment.token != tt.want {
				t.Errorf("token = %q, want %q", attachment.token, tt.want)
			}
			if attachment.lines != tt.lines {
				t.Errorf("lines = %d, want %d", attachment.lines, tt.lines)
			}
		})
	}
}

func TestUnknownBracketsStayPlain(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.input.SetValue("hello [ typed ] world")
	if got := current.expandComposerText(); got != "hello [ typed ] world" {
		t.Errorf("expanded = %q, want hand-typed brackets left alone", got)
	}
}

func TestSplitInputChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		previous   string
		next       string
		wantBefore string
		wantAdded  string
		wantAfter  string
	}{
		{
			name:       "insert at end",
			previous:   "hello",
			next:       "hello world",
			wantBefore: "hello",
			wantAdded:  " world",
			wantAfter:  "",
		},
		{
			name:       "insert in middle",
			previous:   "helloworld",
			next:       "hello brave world",
			wantBefore: "hello",
			wantAdded:  " brave ",
			wantAfter:  "world",
		},
		{
			name:       "pure deletion adds nothing",
			previous:   "hello world",
			next:       "hello",
			wantBefore: "hello",
			wantAdded:  "",
			wantAfter:  "",
		},
		{
			name:       "multibyte runes stay intact",
			previous:   "你好世界",
			next:       "你好Go语言世界",
			wantBefore: "你好",
			wantAdded:  "Go语言",
			wantAfter:  "世界",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before, added, after := splitInputChange(tt.previous, tt.next)
			if before != tt.wantBefore || added != tt.wantAdded || after != tt.wantAfter {
				t.Errorf(
					"splitInputChange(%q, %q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.previous,
					tt.next,
					before,
					added,
					after,
					tt.wantBefore,
					tt.wantAdded,
					tt.wantAfter,
				)
			}
		})
	}
}
