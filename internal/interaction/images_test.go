package interaction

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func testImage(t *testing.T) llm.ImageContent {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return llm.ImageContent{Data: data.Bytes(), MIMEType: "image/png"}
}

func TestValidateImages(t *testing.T) {
	t.Parallel()
	valid := testImage(t)
	for _, tt := range []struct {
		name      string
		images    []llm.ImageContent
		wantError bool
	}{
		{name: "no attachments"},
		{name: "valid", images: []llm.ImageContent{valid}},
		{name: "empty", images: []llm.ImageContent{{MIMEType: "image/png"}}, wantError: true},
		{name: "wrong mime", images: []llm.ImageContent{{Data: valid.Data, MIMEType: "image/jpeg"}}, wantError: true},
		{name: "truncated", images: []llm.ImageContent{{Data: valid.Data[:len(valid.Data)/2], MIMEType: "image/png"}}, wantError: true},
		{name: "too many", images: []llm.ImageContent{valid, valid, valid, valid, valid}, wantError: true},
		{name: "too large", images: []llm.ImageContent{{Data: make([]byte, MaxImageBytes+1), MIMEType: "image/png"}}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateImages(tt.images); (err != nil) != tt.wantError {
				t.Fatalf("ValidateImages() = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestMailboxOwnsImageOnlyDelivery(t *testing.T) {
	t.Parallel()
	images := []llm.ImageContent{testImage(t)}
	want := bytes.Clone(images[0].Data)
	mailbox := NewMailbox()
	if err := mailbox.Deliver(Delivery{ID: "image", Images: images, Kind: DeliveryKindSteer}); err != nil {
		t.Fatal(err)
	}
	images[0].Data[0] = 0
	images[0].MIMEType = "changed"
	delivery, ok := mailbox.TakeFollowUp()
	if !ok || delivery.Kind != DeliveryKindFollowUp || len(delivery.Images) != 1 {
		t.Fatalf("image delivery lost: %#v", delivery)
	}
	if !bytes.Equal(delivery.Images[0].Data, want) || delivery.Images[0].MIMEType != "image/png" {
		t.Fatal("mailbox retained caller-owned image data")
	}
}
