//go:build darwin && integration

package tui

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Exercise the real AppKit bridge with an isolated pasteboard. The user's
// general clipboard is never read or changed by this test.
func TestMacClipboardNativeFormats(t *testing.T) {
	for _, format := range []string{"empty", "text", "png", "tiff"} {
		t.Run(format, func(t *testing.T) {
			setup := "ObjC.import('AppKit'); var board=$.NSPasteboard.pasteboardWithUniqueName; board.clearContents;\n"
			if format == "text" {
				setup += "board.setStringForType('hello 世界', 'public.utf8-plain-text');\n"
			} else if format != "empty" {
				setup += "var data=$.NSData.alloc.initWithBase64EncodedStringOptions('" + base64.StdEncoding.EncodeToString(composerTestImage(t).Data) + "', 0);\n"
				if format == "tiff" {
					setup += "data=$.NSBitmapImageRep.imageRepWithData(data).TIFFRepresentation;\n"
				}
				setup += "board.setDataForType(data,'public." + format + "');\n"
			}
			script := strings.ReplaceAll(macClipboardScript, "$.NSPasteboard.generalPasteboard", "board")
			script = strings.Replace(script, "function run()", "function readBoard()", 1)
			script = setup + script + "\nfunction run(){try{return readBoard();}finally{board.releaseGlobally;}}"
			data, err := clipboardOutput(t.Context(), "osascript", "-l", "JavaScript", "-e", script)
			result := decodeClipboard(data, err)
			if result.err != nil {
				t.Fatal(result.err)
			}
			switch format {
			case "empty":
				if result.image != nil || result.text != "" {
					t.Fatal("empty clipboard changed")
				}
			case "text":
				if result.text != "hello 世界" {
					t.Fatalf("text = %q", result.text)
				}
			default:
				if result.image == nil {
					t.Fatal("native image missing")
				}
				if err := interaction.ValidateImages([]llm.ImageContent{*result.image}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
