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
	for offset := 0; offset < len(raw); {
		args, _ := json.Marshal(map[string]any{"entry_id": "entry", "section": "structured", "offset": offset, "length": 7})
		page := read(string(args))
		if page.IsError {
			t.Fatal(page)
		}
		reconstructed.WriteString(page.Content[1].Text)
		offset += len(page.Content[1].Text)
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
	for start := 0; start < 70; {
		args, _ := json.Marshal(map[string]any{"entry_id": "entry", "block": start})
		result, err := reader.Execute(t.Context(), llm.ToolCall{ID: "read", Name: "tool_result_read", Arguments: args})
		if err != nil || result.IsError {
			t.Fatal("metadata page failed", err, result)
		}
		var page struct {
			Blocks []struct {
				Index int `json:"index"`
			} `json:"blocks"`
			NextBlock  int `json:"next_block"`
			BlockCount int `json:"block_count"`
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
			break
		}
		start = page.NextBlock
	}
	if seen != 70 {
		t.Fatal("metadata lost blocks", seen)
	}
}
