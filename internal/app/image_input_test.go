package app

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
)

func inputImage(t *testing.T) llm.ImageContent {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return llm.ImageContent{Data: data.Bytes(), MIMEType: "image/png"}
}

func TestImageInputsReachModelAndReopenedSession(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "images.jsonl")
	store := createAppTestSession(t, path, t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	provider := &recordingModel{response: "seen"}
	loop, err := agent.NewLoop(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &interactiveSession{loop: loop, model: openai.DefaultModel(), conversation: conversationState{store: store}}
	img := inputImage(t)
	want := bytes.Clone(img.Data)
	active, err := runner.NewRun(interaction.RunInput{Images: []llm.ImageContent{img}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []interaction.DeliveryKind{interaction.DeliveryKindSteer, interaction.DeliveryKindFollowUp} {
		if err := active.Deliver(interaction.Delivery{ID: string(rune('a' + kind)), Kind: kind, Images: []llm.ImageContent{img}}); err != nil {
			t.Fatal(err)
		}
	}
	img.Data[0] = 0
	if err := active.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) < 2 {
		t.Fatal("follow-up did not reach model")
	}
	last := provider.requests[len(provider.requests)-1]
	count := 0
	for _, msg := range last.Messages {
		if user, ok := msg.(llm.UserMessage); ok {
			for _, part := range user.Content {
				if part.Image != nil {
					count++
					if !bytes.Equal(part.Image.Data, want) {
						t.Fatal("model received mutated image")
					}
				}
			}
		}
	}
	if count != 3 {
		t.Fatalf("model images = %d, want 3", count)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := openSessionSnapshot(t, path)
	count = 0
	for _, msg := range sessionSourceMessages(snapshot) {
		if user, ok := msg.(llm.UserMessage); ok {
			count++
			if len(user.Content) != 1 || user.Content[0].Image == nil || !bytes.Equal(user.Content[0].Image.Data, want) {
				t.Fatal("image-only input did not survive Session reopen")
			}
		}
	}
	if count != 3 {
		t.Fatalf("persisted image inputs = %d, want 3", count)
	}
}

func TestImageInputRejectedBeforeSessionCreation(t *testing.T) {
	t.Parallel()
	loop, err := agent.NewLoop(&recordingModel{response: "unused"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &interactiveSession{loop: loop, model: llm.Model{ID: "text-only"}}
	if _, err := runner.NewRun(interaction.RunInput{Images: []llm.ImageContent{inputImage(t)}}, nil); err == nil {
		t.Fatal("text-only model accepted image")
	}
	if runner.conversation.store != nil {
		t.Fatal("rejected input created Session")
	}
	run := &interactiveRun{model: runner.model, mailbox: interaction.NewMailbox()}
	if err := run.Deliver(interaction.Delivery{ID: "image", Kind: interaction.DeliveryKindFollowUp, Images: []llm.ImageContent{inputImage(t)}}); err == nil {
		t.Fatal("text-only model accepted image delivery")
	}
	if _, ok := run.mailbox.TakeFollowUp(); ok {
		t.Fatal("rejected input entered mailbox")
	}
}
