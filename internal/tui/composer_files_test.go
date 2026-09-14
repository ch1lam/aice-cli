package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func attachTestFile(t *testing.T, m model, path string) model {
	t.Helper()
	m.input.InsertString("@query")
	m.requestFileCompletion()
	m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: []interaction.FileCompletion{{Path: path}}})
	return updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
}

func TestComposerFileConfirmedAndTypedCopiesEditDifferently(t *testing.T) {
	t.Parallel()
	m := attachTestFile(t, completionTestModel(), "internal/config/")
	m.input.InsertString("@internal/config/")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.input.Value() != "@internal/config/ @internal/config" || len(m.input.files) != 1 {
		t.Fatalf("typed reference was atomic: %q", m.input.Value())
	}
	m.input.SetCursorColumn(len("@internal/config/"))
	if _, ok := m.fileReferenceAtCursor(); ok {
		t.Fatal("confirmed file reopened completion")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.input.Column() != 0 {
		t.Fatal("left did not skip attached path")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.input.Column() != len("@internal/config/") {
		t.Fatal("right did not skip attached path")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.input.Value() != " @internal/config" || len(m.input.files) != 0 {
		t.Fatalf("attached reference was not removed as a whole: %q", m.input.Value())
	}
}

func TestComposerFileRebasesAcrossEditsAndAtomicDeletion(t *testing.T) {
	t.Parallel()
	for _, code := range []rune{tea.KeyBackspace, tea.KeyDelete} {
		m := completionTestModel()
		m.input.SetValue("before ")
		m = attachTestFile(t, m, "中文 目录/file.go")
		m.input.InsertString("after")
		m.input.MoveToBegin()
		m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "新行\n"})
		m.input.MoveToEnd()
		m.input.SetCursorColumn(len("before "))
		if code == tea.KeyBackspace {
			m.input.SetCursorColumn(len("before ") + utf8.RuneCountInString("@中文 目录/file.go"))
		}
		m = updateModel(t, m, tea.KeyPressMsg{Code: code})
		if m.input.Value() != "新行\nbefore  after" || len(m.input.files) != 0 {
			t.Fatalf("key %d lost surrounding text: %q", code, m.input.Value())
		}
	}
}

func TestComposerFileTypedIdenticalPrefixDoesNotStealAttachment(t *testing.T) {
	t.Parallel()
	m := attachTestFile(t, completionTestModel(), "main.go")
	m.input.MoveToBegin()
	m.input.InsertString("@main.go ")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft}) // Before separating space.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.input.Value() != "@main.g @main.go " || len(m.input.files) != 1 {
		t.Fatalf("typed duplicate became attached: %q", m.input.Value())
	}
}

func TestComposerFileSubmissionAndRejectionPreservePathsAndSpans(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	path := `images/中文 "shot" @literal.png`
	m = attachTestFile(t, m, path)
	m.input.InsertString("@typed.go ")
	m.insertPastePlaceholder(strings.Repeat("literal @missing\n", 10))
	text := m.input.Value()
	want := []string{path, "typed.go"}
	if !reflect.DeepEqual(m.composerFiles(), want) {
		t.Fatalf("file paths = %#v", m.composerFiles())
	}
	m, _, _ = m.submit()
	if !reflect.DeepEqual(m.submittedInput.Files, want) || !strings.Contains(m.submittedInput.Prompt, interaction.QuoteFileReference(path)) {
		t.Fatalf("submitted input = %#v", m.submittedInput)
	}
	if len(m.input.files) != 0 {
		t.Fatal("submitted span survived in fresh composer")
	}
	m.restoreSubmittedInput()
	if m.input.Value() != text || len(m.input.files) != 1 || !reflect.DeepEqual(m.composerFiles(), want) {
		t.Fatal("failed submission lost file attachment state")
	}
}

func TestComposerFileDeliveryRejectionRestoresAttachment(t *testing.T) {
	t.Parallel()
	for _, kind := range []deliveryMode{deliverySteer, deliveryQueue} {
		m := attachTestFile(t, completionTestModel(), "my folder/main.go")
		m.running, m.acceptsDelivery = true, true
		m.activeRun = &activeRunFunc{deliver: func(input interaction.Delivery) error {
			if !reflect.DeepEqual(input.Files, []string{"my folder/main.go"}) {
				t.Fatalf("delivery paths = %#v", input.Files)
			}
			return errors.New("not accepted")
		}}
		var command tea.Cmd
		m, command, _ = m.submitDelivery(kind)
		m = updateModel(t, m, command())
		if len(m.input.files) != 1 || m.input.Value() != "@my folder/main.go " {
			t.Fatal("rejected delivery lost attachment")
		}
	}
}

func TestComposerFileColorsWrapWithoutChangingTextOrCursor(t *testing.T) {
	t.Parallel()
	for _, width := range []int{24, 40, 100} {
		m := completionTestModel()
		m.input.SetWidth(width)
		m.input.SetValue("prefix ")
		m = attachTestFile(t, m, "internal/中文 文件夹/configuration.go")
		m.input.InsertString(" plain")
		m.input.SetWidth(width)
		before := m.input.Cursor()
		plain := m.input.Model.View()
		view := m.input.View()
		if ansi.Strip(view) != ansi.Strip(plain) || !reflect.DeepEqual(before, m.input.Cursor()) {
			t.Fatalf("width %d: styling changed layout/cursor", width)
		}
		if !strings.Contains(view, "38;2;143;132;119m") || !strings.Contains(view, "38;2;201;160;99m") {
			t.Fatalf("width %d: attachment colors missing: %q", width, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d: highlighted line overflowed", width)
			}
		}
	}
}

func TestComposerFileVerticalNavigationDoesNotEnterAttachment(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m.input.SetValue("12345\n")
	m = attachTestFile(t, m, "internal/config/")
	m.input.MoveToBegin()
	m.input.SetCursorColumn(5)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.input.Line() != 1 || m.input.Column() != 0 {
		t.Fatalf("vertical movement entered attached path: %d/%d", m.input.Line(), m.input.Column())
	}
}

func TestComposerFileColorDoesNotApplyToTypedCopy(t *testing.T) {
	t.Parallel()
	m := attachTestFile(t, completionTestModel(), "main.go")
	m.input.InsertString("@main.go")
	m.input.SetWidth(100)
	view := m.input.View()
	if !strings.Contains(view, "38;2;201;160;99mmain.go") || !strings.Contains(view, bodyStyle.Render(" @main.go")) {
		t.Fatalf("confirmed and typed colors were not distinguished: %q", view)
	}
}

func TestComposerFileColorSurvivesViewportScroll(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m.input.SetValue("one\ntwo\nthree\nfour\nfive\nsix\nseven\n")
	m = attachTestFile(t, m, "internal/config.go")
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	view := m.input.View()
	if !strings.Contains(view, "38;2;143;132;119m") || !strings.Contains(view, "38;2;201;160;99m") {
		t.Fatalf("scrolled attachment lost color: %q", view)
	}
}

func TestComposerFileRemainsAttachedWhenSeparatorIsEdited(t *testing.T) {
	t.Parallel()
	m := attachTestFile(t, completionTestModel(), "my file.go")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "suffix"})
	if !reflect.DeepEqual(m.composerFiles(), []string{"my file.go"}) || m.input.Value() != "@my file.gosuffix" {
		t.Fatalf("editing surrounding text detached file: %q / %#v", m.input.Value(), m.composerFiles())
	}
}
