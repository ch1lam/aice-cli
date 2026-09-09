package session_test

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/bmp"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
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

func TestConvertedOriginalSurvivesSessionReopen(t *testing.T) {
	t.Parallel()
	var data bytes.Buffer
	src := image.NewPaletted(image.Rect(0, 0, 8, 4), color.Palette{color.Black, color.White})
	src.SetColorIndex(6, 2, 1)
	if err := gif.Encode(&data, src, nil); err != nil {
		t.Fatal(err)
	}
	var bitmap bytes.Buffer
	if err := bmp.Encode(&bitmap, src); err != nil {
		t.Fatal(err)
	}
	webpData, err := os.ReadFile("../media/testdata/blocks-lossless.webp")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mime string
		data []byte
	}{
		{"image/gif", data.Bytes()}, {"image/bmp", bitmap.Bytes()}, {"image/webp", webpData},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			img, err := media.Prepare(t.Context(), llm.ImageContent{Data: tc.data, MIMEType: tc.mime}, nil)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "images.jsonl")
			store := mustCreate(t, path)
			message, err := llm.NewUserMessage(llm.ContentPart{Type: llm.ContentTypeImage, Image: &img})
			if err != nil {
				t.Fatal(err)
			}
			appendMessages(t, store, "gif", message)
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
			crop, err := media.Prepare(t.Context(), found, &llm.ImageRegion{X: 6, Y: 2, Width: 1, Height: 1})
			if err != nil {
				t.Fatal(err)
			}
			if crop.ID != img.ID || crop.Original.MIMEType != tc.mime || !bytes.Equal(crop.Original.Data, tc.data) {
				t.Fatal("session round trip lost converted source")
			}
			view, _, err := image.Decode(bytes.NewReader(crop.Data))
			if err != nil {
				t.Fatal(err)
			}
			r, g, b, a := view.At(0, 0).RGBA()
			wantR, wantG := uint32(65535), uint32(65535)
			if tc.mime == "image/webp" {
				wantR, wantG = 0, 0
			}
			if r != wantR || g != wantG || b != 65535 || a != 65535 {
				t.Fatal("crop did not recover source pixels")
			}
		})
	}
}
