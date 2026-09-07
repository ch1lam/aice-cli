package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDecodeClipboard(t *testing.T) {
	t.Parallel()
	img := composerTestImage(t)
	data, err := json.Marshal(map[string]any{"data": img.Data})
	if err != nil {
		t.Fatal(err)
	}
	result := decodeClipboard(data, nil)
	if result.err != nil || result.image == nil || !bytes.Equal(result.image.Data, img.Data) {
		t.Fatal("clipboard lost PNG data")
	}
	for _, tt := range []struct {
		name, data, text string
		wantError        bool
	}{
		{name: "text", data: `{"text":"你好\nworld"}`, text: "你好\nworld"},
		{name: "empty", data: `{"text":""}`},
		{name: "native error", data: `{"error":"too large"}`, wantError: true},
		{name: "invalid base64", data: `{"data":"!!!"}`, wantError: true},
		{name: "invalid json", data: `broken`, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := decodeClipboard([]byte(tt.data), nil)
			if (result.err != nil) != tt.wantError || result.text != tt.text {
				t.Fatalf("clipboard result = %#v", result)
			}
		})
	}
}

func TestClipboardOutputBoundsAndCancellation(t *testing.T) {
	t.Parallel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"text", "large", "fail", "wait"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			if mode == "wait" {
				cancel()
			}
			data, err := clipboardOutput(ctx, executable, "-test.run=^TestClipboardHelperProcess$", "--", "clipboard-test", mode)
			if mode == "text" {
				if err != nil || string(data) != "hello" {
					t.Fatalf("output = %q, %v", data, err)
				}
			} else if err == nil {
				t.Fatal("helper failure accepted")
			}
			if mode == "wait" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation = %v", err)
			}
		})
	}
}

func TestClipboardHelperProcess(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "clipboard-test" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "text":
		fmt.Print("hello")
	case "large":
		fmt.Print(strings.Repeat("x", 6*1024*1024))
	case "fail":
		os.Exit(2)
	case "wait":
		time.Sleep(5 * time.Second)
	}
	os.Exit(0)
}
