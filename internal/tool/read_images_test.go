package tool_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/bmp"

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

func TestReadGIFAndConversionError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace, err := tool.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	src := image.NewPaletted(image.Rect(0, 0, 2200, 20), color.Palette{color.Black, color.White})
	if err := gif.Encode(&data, src, nil); err != nil {
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
	saved := *content[0].Image
	if saved.MIMEType != "image/png" || saved.Width != 2000 || saved.Original.MIMEType != "image/gif" {
		t.Fatal("GIF was not converted and resized")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reader, err = tool.NewRead(workspace, tool.ReadOptions{LookupImage: func(_ context.Context, id string) (llm.ImageContent, error) {
		if id != saved.ID {
			t.Fatalf("lookup ID = %q", id)
		}
		return saved, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	content, err = reader.Content(t.Context(), tool.ReadRequest{ImageID: saved.ID, Crop: &llm.ImageRegion{X: 2100, Y: 0, Width: 20, Height: 20}})
	if err != nil {
		t.Fatal(err)
	}
	cropped := content[0].Image
	if cropped.ID != saved.ID || cropped.Width != 20 || !bytes.Equal(cropped.Original.Data, data.Bytes()) {
		t.Fatal("lost deleted GIF original on crop")
	}
	if err := os.WriteFile(path, data.Bytes()[:13], 0600); err != nil {
		t.Fatal(err)
	}
	_, err = reader.Content(t.Context(), tool.ReadRequest{Path: path})
	if err == nil || !strings.Contains(err.Error(), "cannot decode image/gif") || !strings.Contains(err.Error(), "re-export") || !strings.Contains(err.Error(), "do not retry unchanged input") {
		t.Fatalf("conversion failure lacks recovery advice: %v", err)
	}
}

func TestReadConvertedWebPAndBMP(t *testing.T) {
	t.Parallel()
	var bitmap bytes.Buffer
	if err := bmp.Encode(&bitmap, image.NewRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bmp", "blocks-lossless.webp", "blocks-lossy.webp", "blocks-alpha.webp"} {
		t.Run(name, func(t *testing.T) {
			data := bitmap.Bytes()
			mime := "image/bmp"
			if name != "bmp" {
				var err error
				data, err = os.ReadFile(filepath.Join("..", "media", "testdata", name))
				if err != nil {
					t.Fatal(err)
				}
				mime = "image/webp"
			}
			root := t.TempDir()
			workspace, err := tool.NewWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "image.dat")
			if err := os.WriteFile(path, data, 0600); err != nil {
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
			saved := *content[0].Image
			if saved.MIMEType != "image/png" || saved.Original.MIMEType != mime || !bytes.Equal(saved.Original.Data, data) {
				t.Fatal("read lost converted original")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			reader, err = tool.NewRead(workspace, tool.ReadOptions{LookupImage: func(_ context.Context, id string) (llm.ImageContent, error) {
				if id != saved.ID {
					t.Fatalf("wrong ID %q", id)
				}
				return saved, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			content, err = reader.Content(t.Context(), tool.ReadRequest{ImageID: saved.ID, Crop: &llm.ImageRegion{X: 4, Y: 1, Width: 3, Height: 2}})
			if err != nil {
				t.Fatal(err)
			}
			crop := content[0].Image
			if crop.ID != saved.ID || crop.Width != 3 || crop.Height != 2 || !bytes.Equal(crop.Original.Data, data) {
				t.Fatal("could not crop deleted source")
			}
		})
	}
}
