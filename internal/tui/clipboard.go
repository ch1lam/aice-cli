package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

type clipboardResult struct {
	image *llm.ImageContent
	text  string
	err   error
}

// pasteClipboard is owned by the TUI lifetime. Helpers are cancelled on exit
// and have a short deadline even when the desktop clipboard owner is stalled.
func pasteClipboard(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		result := readClipboard(ctx)
		if result.err == nil && result.image != nil {
			prepared, err := media.Prepare(ctx, *result.image, nil)
			result.image, result.err = &prepared, err
		}
		return result
	}
}

func readClipboard(ctx context.Context) clipboardResult {
	switch runtime.GOOS {
	case "darwin":
		data, err := clipboardOutput(ctx, "osascript", "-l", "JavaScript", "-e", macClipboardScript)
		return decodeClipboard(data, err)
	case "windows":
		data, err := clipboardOutput(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsClipboardScript)
		return decodeClipboard(data, err)
	case "linux":
		return readLinuxClipboard(ctx)
	default:
		return clipboardResult{err: fmt.Errorf("clipboard images are unavailable on %s", runtime.GOOS)}
	}
}

// clipboardOutput bounds helper output, including base64 expansion. No shell
// or user-controlled command text is involved.
func clipboardOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = time.Second
	output := limitedClipboardBuffer{limit: interaction.MaxImageBytes*4/3 + 65536}
	command.Stdout = &output
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("clipboard read timed out or was cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("clipboard helper %s failed: %w", name, err)
	}
	return output.buffer.Bytes(), nil
}

type limitedClipboardBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedClipboardBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.buffer.Len() {
		return 0, errors.New("clipboard content exceeds the input size limit")
	}
	return b.buffer.Write(data)
}

func decodeClipboard(data []byte, err error) clipboardResult {
	if err != nil {
		return clipboardResult{err: err}
	}
	var result struct {
		Data  []byte `json:"data"`
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &result); err != nil {
		return clipboardResult{err: fmt.Errorf("invalid clipboard response: %w", err)}
	}
	if result.Error != "" {
		return clipboardResult{err: errors.New(result.Error)}
	}
	if len(result.Data) > 0 {
		return clipboardResult{image: &llm.ImageContent{Data: result.Data, MIMEType: "image/png"}}
	}
	return clipboardResult{text: result.Text}
}

func readLinuxClipboard(ctx context.Context) clipboardResult {
	name, typeArgs := "xclip", []string{"-selection", "clipboard", "-o", "-t", "TARGETS"}
	wayland := os.Getenv("WAYLAND_DISPLAY") != ""
	if wayland {
		name, typeArgs = "wl-paste", []string{"--list-types"}
	}
	types, err := clipboardOutput(ctx, name, typeArgs...)
	if err != nil {
		return clipboardResult{err: fmt.Errorf("read desktop clipboard (Wayland needs wl-paste; X11 needs xclip): %w", err)}
	}
	for _, mime := range []string{"image/png", "image/jpeg"} {
		for _, available := range strings.Fields(string(types)) {
			if available != mime {
				continue
			}
			args := []string{"-selection", "clipboard", "-o", "-t", mime}
			if wayland {
				args = []string{"--type", mime}
			}
			data, err := clipboardOutput(ctx, name, args...)
			return clipboardResult{image: &llm.ImageContent{Data: data, MIMEType: mime}, err: err}
		}
	}
	args := []string{"-selection", "clipboard", "-o"}
	if wayland {
		args = []string{"--no-newline"}
	}
	text, err := clipboardOutput(ctx, name, args...)
	return clipboardResult{text: string(text), err: err}
}

const macClipboardScript = `ObjC.import('AppKit');
function run() {
  var p = $.NSPasteboard.generalPasteboard;
  var d = p.dataForType('public.png');
  if (d.isNil()) {
    var t = p.dataForType('public.tiff');
    if (!t.isNil()) {
      if (Number(t.length) > 67108864) return JSON.stringify({error:'Clipboard image is too large; copy a smaller image'});
      var r = $.NSBitmapImageRep.imageRepWithData(t);
      if (r.isNil() || Number(r.pixelsWide) > 8000 || Number(r.pixelsHigh) > 8000 || Number(r.pixelsWide)*Number(r.pixelsHigh) > 16000000)
        return JSON.stringify({error:'Clipboard image is too large or invalid; copy a smaller image'});
      d = r.representationUsingTypeProperties($.NSPNGFileType, $({}));
      if (d.isNil()) return JSON.stringify({error:'Could not convert clipboard image to PNG'});
    }
  }
  if (!d.isNil()) {
    if (Number(d.length) > 16777216) return JSON.stringify({error:'Clipboard image exceeds 16 MiB; copy a smaller image'});
    return JSON.stringify({data:ObjC.unwrap(d.base64EncodedStringWithOptions(0))});
  }
  var s = p.stringForType('public.utf8-plain-text');
  return JSON.stringify({text:s.isNil() ? '' : ObjC.unwrap(s)});
}`

const windowsClipboardScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
if ([System.Windows.Forms.Clipboard]::ContainsImage()) {
  $image = [System.Windows.Forms.Clipboard]::GetImage()
  $stream = New-Object System.IO.MemoryStream
  try {
    if ($image.Width -gt 8000 -or $image.Height -gt 8000 -or ([long]$image.Width * $image.Height) -gt 16000000) {
      @{error='Clipboard image is too large; copy a smaller image'} | ConvertTo-Json -Compress
    } else {
      $image.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
      if ($stream.Length -gt 16777216) {
        @{error='Clipboard image exceeds 16 MiB; copy a smaller image'} | ConvertTo-Json -Compress
      } else {
        @{data=[Convert]::ToBase64String($stream.ToArray())} | ConvertTo-Json -Compress
      }
    }
  } finally { $stream.Dispose(); $image.Dispose() }
} else {
  @{text=[System.Windows.Forms.Clipboard]::GetText()} | ConvertTo-Json -Compress
}`
