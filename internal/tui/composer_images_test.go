package tui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func composerTestImage(t *testing.T) llm.ImageContent {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	return llm.ImageContent{Data: buf.Bytes(), MIMEType: "image/png"}
}

func TestClipboardImagesSubmitThroughController(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", "explain this screenshot"} {
		t.Run("text="+text, func(t *testing.T) {
			requests := make(chan runRequest, 1)
			m := newModel(requests, make(chan struct{}))
			img := composerTestImage(t)
			m.clipboard = func() tea.Msg { return clipboardResult{image: &img} }
			m.input.SetValue(text)
			m, command, _ := m.handleKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
			if command == nil || !m.clipboardPending {
				t.Fatal("paste shortcut did not read clipboard")
			}
			m = updateModel(t, m, command())
			if len(m.composerImages()) != 1 || !strings.Contains(m.composerView(80), "[Image 1]") {
				t.Fatal("pasted image is not visible")
			}
			m, command, _ = m.submit()
			if command == nil {
				t.Fatal("image input was not submitted")
			}
			_ = command()
			request := <-requests
			var received RunInput
			runner := runnerFunc(func(_ context.Context, input RunInput, _ DisplayEventSink) error { received = input; return nil })
			if err := runOne(t.Context(), runner, request); err != nil {
				t.Fatal(err)
			}
			if received.Prompt != text || len(received.Images) != 1 || !bytes.Equal(received.Images[0].Data, img.Data) {
				t.Fatal("controller lost text or image")
			}
			if len(m.composerImages()) != 0 || m.input.Value() != "" {
				t.Fatal("submitted attachments left in composer")
			}
		})
	}
}

func TestImageDraftRestoredWhenModelRejectsInput(t *testing.T) {
	t.Parallel()
	m := newModel(make(chan runRequest), make(chan struct{}))
	m.insertImagePlaceholder(composerTestImage(t))
	m.input.InsertString("keep this draft")
	m, _, _ = m.submit()
	updated, _ := m.applyRunBatch(runBatchMsg{closed: true, updates: []runUpdate{{done: true, err: errors.New("model does not support images")}}})
	m = updated.(model)
	if m.running || m.input.Value() != "[Image 1]keep this draft" || len(m.composerImages()) != 1 {
		t.Fatal("rejection lost image draft")
	}
	if strings.Contains(m.transcriptView(), "keep this draft") {
		t.Fatal("rejected draft appeared as submitted transcript")
	}
	if !strings.Contains(m.transcriptView(), "does not support images") {
		t.Fatal("model rejection is not visible")
	}
}

func TestImageSteeringAndFollowUpPreserveDraftOnRejection(t *testing.T) {
	t.Parallel()
	for _, kind := range []deliveryMode{deliverySteer, deliveryQueue} {
		t.Run(deliveryID(uint64(kind)), func(t *testing.T) {
			m := newModel(make(chan runRequest), make(chan struct{}))
			m.running, m.acceptsDelivery = true, true
			m.insertImagePlaceholder(composerTestImage(t))
			m.activeRun = &activeRunFunc{deliver: func(input interaction.Delivery) error {
				if len(input.Images) != 1 || input.Text != "" || input.Kind != kind {
					t.Fatal("image-only delivery changed")
				}
				return errors.New("images unsupported")
			}}
			m, _, _ = m.submitDelivery(kind)
			if len(m.composerImages()) != 1 || len(m.pendingDeliveries) != 0 || !strings.Contains(m.composerView(80), "images unsupported") {
				t.Fatal("rejected image delivery was lost")
			}
			m.activeRun = &activeRunFunc{}
			m, _, _ = m.submitDelivery(kind)
			if len(m.composerImages()) != 0 || len(m.pendingDeliveries) != 1 || !strings.Contains(m.pendingDeliveries[0].text, "[Image 1]") {
				t.Fatal("accepted image delivery not shown")
			}
		})
	}
}

func TestImageComposerDeletionCommandsAndTextPaste(t *testing.T) {
	t.Parallel()
	m := newModel(make(chan runRequest), make(chan struct{}))
	img := composerTestImage(t)
	m = updateModel(t, m, clipboardResult{image: &img})
	m = updateModel(t, m, clipboardResult{image: &img})
	m.input.InsertString("/btw question")
	m, command, _ := m.submit()
	if command != nil || len(m.composerImages()) != 2 {
		t.Fatal("slash command discarded images")
	}
	m.input.SetCursorColumn(len("[Image 1]"))
	m, _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if len(m.composerImages()) != 1 || m.input.Value() != "[Image 2]/btw question" {
		t.Fatal("backspace did not remove inline image")
	}
	m.input.SetCursorColumn(0)
	m, _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyDelete})
	if len(m.composerImages()) != 0 || m.input.Value() != "/btw question" {
		t.Fatal("delete did not remove inline image")
	}
	m.input.Reset()
	text := strings.Repeat("pasted line\n", 20)
	m = updateModel(t, m, clipboardResult{text: text})
	if m.expandComposerText() != text || len(m.pastes) != 1 {
		t.Fatal("clipboard text no longer uses existing long-paste handling")
	}
}

func TestClipboardResultDoesNotCrossFocusBoundary(t *testing.T) {
	t.Parallel()
	m := newModel(make(chan runRequest), make(chan struct{}))
	m.clipboardPending = true
	m.side.isVisible = true
	m.input.SetValue("side draft")
	m = updateModel(t, m, clipboardResult{image: new(composerTestImage(t))})
	if len(m.composerImages()) != 0 || m.input.Value() != "side draft" {
		t.Fatal("late clipboard result crossed into side composer")
	}
}

func TestTypingDuringClipboardReadIsNotDropped(t *testing.T) {
	t.Parallel()
	m := newModel(make(chan runRequest), make(chan struct{}))
	m.clipboardPending = true
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.input.Value() != "x" {
		t.Fatal("clipboard read dropped typed caption")
	}
	m, command, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command != nil || m.running || m.input.Value() != "x" {
		t.Fatal("submitted before clipboard completed")
	}
	m = updateModel(t, m, clipboardResult{image: new(composerTestImage(t))})
	if len(m.composerImages()) != 1 || m.input.Value() != "x[Image 1]" {
		t.Fatal("clipboard completion lost caption")
	}
}

func TestImagePlaceholderEditingDuringRun(t *testing.T) {
	m := newModel(make(chan runRequest), make(chan struct{}))
	m.running, m.acceptsDelivery = true, true
	m.input.SetValue("before after")
	m.input.SetCursorColumn(7)
	m.insertImagePlaceholder(composerTestImage(t))
	m, _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.input.Column() != 7 {
		t.Fatal("left did not cross image token")
	}
	m, _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyDelete})
	if m.input.Value() != "before after" || len(m.composerImages()) != 0 {
		t.Fatal("running deletion lost surrounding text or retained image")
	}
}

func TestImagePlaceholderExternalEditor(t *testing.T) {
	m := newModel(make(chan runRequest), make(chan struct{}))
	m.insertImagePlaceholder(composerTestImage(t))
	m.insertPastePlaceholder(strings.Repeat("long text\n", 20))
	if !strings.Contains(m.expandPasteText(true), "[Image 1]long text") {
		t.Fatal("editor lost image token or text")
	}
	file := t.TempDir() + "/draft.md"
	if err := os.WriteFile(file, []byte("changed [Image 1] caption"), 0600); err != nil {
		t.Fatal(err)
	}
	m = m.applyEditorResult(editorFinishedMsg{file: file})
	if len(m.composerImages()) != 1 || m.expandComposerText() != "changed  caption" {
		t.Fatal("editor round trip lost attachment")
	}
	if err := os.WriteFile(file, []byte("caption only"), 0600); err != nil {
		t.Fatal(err)
	}
	m = m.applyEditorResult(editorFinishedMsg{file: file})
	if len(m.composerImages()) != 0 {
		t.Fatal("editor deletion retained image")
	}
}

func TestAdjacentImagePlaceholdersDeleteAtSharedBoundary(t *testing.T) {
	for _, code := range []rune{tea.KeyBackspace, tea.KeyDelete} {
		m := newModel(make(chan runRequest), make(chan struct{}))
		m.insertImagePlaceholder(composerTestImage(t))
		m.insertImagePlaceholder(composerTestImage(t))
		m.input.SetCursorColumn(len("[Image 1]"))
		m, _, _ = m.handleKey(tea.KeyPressMsg{Code: code})
		want := "[Image 1]"
		if code == tea.KeyBackspace {
			want = "[Image 2]"
		}
		if m.input.Value() != want || len(m.composerImages()) != 1 {
			t.Fatalf("key %v: got %q", code, m.input.Value())
		}
	}
}
