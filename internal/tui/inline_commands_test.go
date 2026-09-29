package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func skillComposer() model {
	m := completionTestModel()
	m.commands = append(m.commands, SlashCommand{
		Name: "skill:review", SkillName: "review", Description: "Skill · code audit",
	})
	return m
}

func TestInlineSkillKeepsSurroundingDraftAndAttachments(t *testing.T) {
	t.Parallel()
	m := skillComposer()
	m.input.SetValue("开始\n请 /audit 后续\n")
	m = attachTestFile(t, m, "some file.go")
	m.insertImagePlaceholder(composerTestImage(t))
	m.insertPastePlaceholder(strings.Repeat("literal @missing /help\n", 20))
	before := m.input.Value()
	m.input.MoveToBegin()
	m.input.MoveToEnd()
	m.input.setCursorOffset(utf8.RuneCountInString("开始\n请 /audit"))
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	want := strings.Replace(before, "/audit", "[skill:review]", 1)
	if m.input.Value() != want || !reflect.DeepEqual(m.composerSkills(), []string{"review"}) ||
		len(m.composerImages()) != 1 || !reflect.DeepEqual(m.composerFiles(), []string{"some file.go"}) {
		t.Fatalf("selection lost draft or attachments: %q / %+v", m.input.Value(), m.input.skills)
	}
	if m.input.cursorOffset() != utf8.RuneCountInString("开始\n请 [skill:review]") {
		t.Fatal("cursor moved away from insertion point")
	}
	m, _, _ = m.submit()
	if len(m.submittedInput.Skills) != 1 || !strings.Contains(m.submittedInput.Prompt, "literal @missing /help") {
		t.Fatal("submission lost attachment or literal paste")
	}
	m.restoreSubmittedInput()
	if m.input.Value() != want || len(m.input.skills) != 1 || len(m.composerImages()) != 1 {
		t.Fatal("rejected run lost attachment binding")
	}
}

func TestSkillAtomicEditingDistinguishesTypedCopies(t *testing.T) {
	t.Parallel()
	for _, code := range []rune{tea.KeyBackspace, tea.KeyDelete} {
		m := skillComposer()
		m.attachSkill(0, 0, "review")
		m.input.MoveToBegin()
		m.input.InsertString("[skill:review] 新行\n")
		m.input.MoveToEnd()
		m.input.SetCursorColumn(0)
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
		if m.input.Column() != len("[skill:review]") {
			t.Fatal("right entered skill chip")
		}
		if code == tea.KeyDelete {
			m.input.SetCursorColumn(0)
		}
		m = updateModel(t, m, tea.KeyPressMsg{Code: code})
		if m.input.Value() != "[skill:review] 新行\n " || len(m.composerSkills()) != 0 {
			t.Fatalf("deleted typed copy or retained binding: %q", m.input.Value())
		}
	}
}

func TestSkillHighlightWrapAndExternalEditing(t *testing.T) {
	t.Parallel()
	m := skillComposer()
	m.input.SetValue("正文 ")
	m.attachSkill(3, 3, "中文-review-long-name")
	for _, width := range []int{10, 20, 80} {
		m.input.SetWidth(width)
		before := *m.input.Cursor()
		view := m.input.View()
		if ansi.Strip(view) != ansi.Strip(m.input.Model.View()) || *m.input.Cursor() != before ||
			!strings.Contains(view, "38;2;122;148;113m") {
			t.Fatalf("width %d changed layout or lost skill color: %q", width, view)
		}
	}
	file := filepath.Join(t.TempDir(), "draft")
	if err := os.WriteFile(file, []byte("moved\n"+m.input.Value()+" [skill:unknown]"), 0600); err != nil {
		t.Fatal(err)
	}
	m = m.applyEditorResult(editorFinishedMsg{file: file})
	if !reflect.DeepEqual(m.composerSkills(), []string{"中文-review-long-name"}) || m.input.skills[0].start != 9 {
		t.Fatal("editor lost existing binding or attached unknown text")
	}
}

func TestInlineCommandsRestoreDraftAfterExecutionAndCancellation(t *testing.T) {
	t.Parallel()
	for _, cancel := range []bool{false, true} {
		m := reasoningCompletionModel()
		m.input.SetValue("前文 /thinking 后文")
		m.input.setCursorOffset(utf8.RuneCountInString("前文 /thinking"))
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if m.commandMenu == nil || m.commandDraft == nil || m.input.Value() != "/thinking " {
			t.Fatal("inline command did not open its menu")
		}
		if cancel {
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
		} else {
			m, _, _ = m.selectCommandMenuOption()
			if !m.running {
				t.Fatal("selected command did not execute")
			}
			m.finishRun(errors.New("simulated command failure"))
		}
		if m.input.Value() != "前文  后文" || m.commandDraft != nil || m.input.cursorOffset() != 3 {
			t.Fatalf("command lost draft: %q, pending=%+v cursor=%d cancel=%v", m.input.Value(), m.commandDraft, m.input.cursorOffset(), cancel)
		}
	}
}

func TestInlineLocalCommandPreservesAllAttachmentBindings(t *testing.T) {
	t.Parallel()
	m := skillComposer()
	m.attachSkill(0, 0, "review")
	m = attachTestFile(t, m, "main.go")
	m.insertImagePlaceholder(composerTestImage(t))
	m.insertPastePlaceholder(strings.Repeat("literal\n", 10))
	before := m.input.Value()
	m.input.InsertString(" /help 尾部")
	m.input.setCursorOffset(utf8.RuneCountInString(before + " /help"))
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.input.Value() != before+"  尾部" || len(m.composerSkills()) != 1 || len(m.composerImages()) != 1 || len(m.composerFiles()) != 1 || len(m.pastes) != 2 {
		t.Fatal("local command discarded attached draft")
	}
}

func TestSlashCompletionIgnoresLiteralTokensAndUsesCursor(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"https://example/help", "dir/help", "`some /help", "``some /help", "```go\n/help", "/usr/bin", "[skill:/help]"} {
		m := skillComposer()
		m.input.SetValue(text)
		if m.slashCommandMenuVisible() {
			t.Fatalf("literal %q opened command menu", text)
		}
	}
	m := skillComposer()
	m.input.SetValue("before /reviezzz after")
	m.input.setCursorOffset(len("before /revie"))
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "before [skill:review] after" {
		t.Fatalf("did not replace entire active token: %q", m.input.Value())
	}
}

func TestSkillCompletionWhileRunningAndDeliveryRollback(t *testing.T) {
	t.Parallel()
	m := skillComposer()
	m.running, m.acceptsDelivery = true, true
	m.input.SetValue("follow up /review")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.composerSkills()) != 1 {
		t.Fatal("running composer cannot attach skills")
	}
	draft := m.captureComposerDraft()
	m.input.Reset()
	next, _ := m.applyDeliveryResult(deliveryResult{draft: draft, err: interaction.ErrClosed})
	m = next.(model)
	if len(m.composerSkills()) != 1 || m.input.Value() != draft.text {
		t.Fatal("delivery failure lost skill")
	}
}

func TestRunningNavigationWinsOverSkillDescriptionMatch(t *testing.T) {
	t.Parallel()
	m := skillComposer()
	m.commands = append(m.commands, btwSlashCommand(), SlashCommand{
		Name: "skill:desktop-guide", SkillName: "desktop-guide", Description: "desktop settings btw",
	})
	m.running, m.acceptsDelivery = true, true
	m.readSettings = func(uint64) (tea.Cmd, context.CancelFunc) { return nil, func() {} }
	for _, command := range []string{"/desktop", "/settings", "/btw"} {
		m.input.SetValue(command)
		if m.slashCommandMenuVisible() {
			t.Fatalf("skill menu captured running navigation %s", command)
		}
	}
}
