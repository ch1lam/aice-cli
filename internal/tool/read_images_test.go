package tool_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestReadImagesAndSavedOriginal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace, err := tool.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2200, 20))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "image.dat")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatal(err)
	}
	content, err := reader.Content(t.Context(), tool.ReadRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 1 || content[0].Image == nil || content[0].Image.Width != 2000 {
		t.Fatal("image was not detected and resized")
	}
	saved := *content[0].Image
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reader, err = tool.NewRead(workspace, tool.ReadOptions{LookupImage: func(context.Context, string) (llm.ImageContent, error) { return saved, nil }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Execute(t.Context(), toolCall(t, "read", map[string]any{"image_id": saved.ID, "crop": map[string]int{"x": 2100, "y": 0, "width": 20, "height": 20}}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Content[0].Image.Width != 20 || !bytes.Equal(result.Content[0].Image.Original.Data, data.Bytes()) {
		t.Fatal("crop did not recover deleted original")
	}
	if _, err := reader.Content(t.Context(), tool.ReadRequest{ImageID: saved.ID, Offset: 1}); err == nil {
		t.Fatal("image accepted text paging")
	}
	reader, err = tool.NewRead(workspace, tool.ReadOptions{LookupImage: func(context.Context, string) (llm.ImageContent, error) { return saved, nil }, CanReadImages: func() bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Content(t.Context(), tool.ReadRequest{ImageID: saved.ID}); err == nil {
		t.Fatal("non-vision model accepted image")
	}
}
