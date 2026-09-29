package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

type resultReadFixture struct {
	value StoredToolResult
	calls int
}

func (f *resultReadFixture) ReadToolResult(context.Context, string, string) (StoredToolResult, error) {
	f.calls++
	return f.value, nil
}

func TestResultReadSectionsAndUTF8Paging(t *testing.T) {
	raw := `{"n":9007199254740993,"text":"中文🙂"}`
	f := &resultReadFixture{value: StoredToolResult{ID: "entry", Durable: true, Message: llm.ToolResultMessage{ToolCallID: "old", ToolName: "source", Content: []llm.ContentPart{llm.NewTextContent("a中文🙂z").Part(), {Type: llm.ContentTypeImage, Image: &llm.ImageContent{MIMEType: "image/png", Data: []byte("image")}}}, Details: &llm.ToolResultDetails{State: llm.ExecutionUnknown, StructuredContent: json.RawMessage(raw), Loss: "audio not saved"}}}}
	reader, _ := NewToolResultRead(f)
	read := func(args string) llm.ToolResult {
		r, err := reader.Execute(t.Context(), llm.ToolCall{ID: "new", Name: "tool_result_read", Arguments: []byte(args)})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	meta := read(`{"call_id":"old"}`)
	if meta.IsError || !strings.Contains(meta.Content[0].Text, "audio not saved") || !strings.Contains(meta.Content[0].Text, `"execution_state":"unknown"`) {
		t.Fatal("lost metadata")
	}
	page := read(`{"call_id":"old","section":"content","block":0,"length":5}`)
	if page.IsError || page.Content[1].Text != "a中" || !strings.Contains(page.Content[0].Text, `"next_offset":4`) {
		t.Fatal("bad UTF-8 page", page)
	}
	if !read(`{"call_id":"old","section":"content","offset":2}`).IsError {
		t.Fatal("split UTF-8 accepted")
	}
	var reconstructed strings.Builder
	args := `{"call_id":"old","section":"structured","length":7}`
	for {
		page := read(args)
		if page.IsError {
			t.Fatal(page)
		}
		var meta struct {
			Complete bool            `json:"complete"`
			Fragment bool            `json:"fragment"`
			Format   string          `json:"format"`
			NextRead json.RawMessage `json:"next_read"`
		}
		if err := json.Unmarshal([]byte(page.Content[0].Text), &meta); err != nil || !meta.Fragment || meta.Format != "raw_json_text" {
			t.Fatal("missing fragment information", err)
		}
		reconstructed.WriteString(page.Content[1].Text)
		if meta.Complete {
			break
		}
		if !json.Valid(meta.NextRead) || !strings.Contains(string(meta.NextRead), `"entry_id":"entry"`) {
			t.Fatal("missing exact continuation", string(meta.NextRead))
		}
		args = string(meta.NextRead)
	}
	if reconstructed.String() != raw {
		t.Fatal("structured JSON lexemes changed")
	}
	image := read(`{"call_id":"old","section":"content","block":1}`)
	if len(image.Content) != 2 || string(image.Content[1].Image.Data) != "image" {
		t.Fatal("image not read")
	}
	image.Content[1].Image.Data[0] = '!'
	if string(f.value.Message.Content[1].Image.Data) != "image" {
		t.Fatal("image aliases source")
	}
	before := f.calls
	for _, args := range []string{`{}`, `{"call_id":"a","entry_id":"b"}`, `{"call_id":"a","length":8193}`, `{"call_id":"a","offset":-1}`, `{"call_id":"a","session":"arbitrary"}`} {
		if !read(args).IsError {
			t.Fatal("invalid input accepted", args)
		}
	}
	if f.calls != before {
		t.Fatal("invalid input reached reader")
	}
}

func TestResultReadMetadataPages(t *testing.T) {
	f := &resultReadFixture{value: StoredToolResult{ID: "entry"}}
	for range 70 {
		f.value.Message.Content = append(f.value.Message.Content, llm.NewTextContent("x").Part())
	}
	reader, err := NewToolResultRead(f)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	args := json.RawMessage(`{"entry_id":"entry"}`)
	for {
		result, err := reader.Execute(t.Context(), llm.ToolCall{ID: "read", Name: "tool_result_read", Arguments: args})
		if err != nil || result.IsError {
			t.Fatal("metadata page failed", err, result)
		}
		var page struct {
			Blocks []struct {
				Index int `json:"index"`
			} `json:"blocks"`
			NextBlock  int             `json:"next_block"`
			BlockCount int             `json:"block_count"`
			NextRead   json.RawMessage `json:"next_read"`
		}
		if err := json.Unmarshal([]byte(result.Content[0].Text), &page); err != nil {
			t.Fatal(err)
		}
		if page.BlockCount != 70 || len(page.Blocks) > 32 || len(page.Blocks) == 0 {
			t.Fatal("unbounded/empty metadata page")
		}
		for _, block := range page.Blocks {
			if block.Index != seen {
				t.Fatal("lost block index")
			}
			seen++
		}
		if page.NextBlock == 0 {
			if len(page.NextRead) != 0 {
				t.Fatal("last page supplied a continuation")
			}
			break
		}
		if !json.Valid(page.NextRead) {
			t.Fatal("metadata missing exact continuation")
		}
		args = page.NextRead
	}
	if seen != 70 {
		t.Fatal("metadata lost blocks", seen)
	}
}
