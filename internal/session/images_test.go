package session_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestSavedImageSurvivesReopenAndHonorsBranch(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "images.jsonl")
	store := mustCreate(t, path)
	img := llm.ImageContent{ID: "image:saved", Data: []byte("view"), MIMEType: "image/png", Original: &llm.ImageOriginal{Data: []byte("original"), MIMEType: "image/png", Width: 4000, Height: 100}}
	user, err := llm.NewUserMessage(llm.ContentPart{Type: llm.ContentTypeImage, Image: &img})
	if err != nil {
		t.Fatal(err)
	}
	entries := appendMessages(t, store, "image", user)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	found, err := reopened.Image(t.Context(), img.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(found.Original.Data, img.Original.Data) {
		t.Fatal("source was not retained")
	}
	found.Original.Data[0] = 0
	again, err := reopened.Image(t.Context(), img.ID)
	if err != nil || again.Original.Data[0] == 0 {
		t.Fatal("lookup aliases store")
	}
	leaf, err := session.NewLeaf("root-move", entries[0].ID, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.AppendLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Image(t.Context(), img.ID); err == nil {
		t.Fatal("lookup crossed into inactive branch")
	}
}
