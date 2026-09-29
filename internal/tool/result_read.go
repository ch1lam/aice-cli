package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// StoredToolResult is a copied result from the active Session branch. Lookup
// never executes the source tool and does not restore its binding's authority.
type StoredToolResult struct {
	ID      string
	Durable bool
	Message llm.ToolResultMessage
}

type ToolResultReader interface {
	ReadToolResult(context.Context, string, string) (StoredToolResult, error)
}

type ToolResultRead struct{ reader ToolResultReader }

type toolResultReadArgs struct {
	CallID  string `json:"call_id"`
	EntryID string `json:"entry_id"`
	Section string `json:"section"`
	Block   int    `json:"block"`
	Offset  int    `json:"offset"`
	Length  int    `json:"length"`
}

func NewToolResultRead(reader ToolResultReader) (*ToolResultRead, error) {
	if reader == nil {
		return nil, fmt.Errorf("tool_result_read reader is required")
	}
	return &ToolResultRead{reader: reader}, nil
}

func (*ToolResultRead) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: "tool_result_read", Description: "Read a retained tool result from the active Session branch, including before compaction. Never calls the original tool or fetches links. Supply call_id, or entry_id to disambiguate repeated call IDs. Default metadata lists up to 32 content block indices, execution state and unrecoverable loss. section=content reads one block; section=structured reads exact raw JSON bytes as text, and a page may be an incomplete JSON fragment. Text offsets and lengths are UTF-8 bytes. When present, next_read contains exact arguments for the next page (next_block/next_offset also report its position). Images are returned one at a time. Unstored data cannot be recovered.", InputSchema: jsonSchema(`{"type":"object","properties":{"call_id":{"type":"string"},"entry_id":{"type":"string"},"section":{"type":"string","enum":["metadata","content","structured"]},"block":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0},"length":{"type":"integer","minimum":1,"maximum":8192}},"additionalProperties":false}`)}
}

func (r *ToolResultRead) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	args, err := decodeArguments[toolResultReadArgs](ctx, call, "tool_result_read")
	if err != nil || (args.CallID == "") == (args.EntryID == "") || len(args.CallID) > 4096 || len(args.EntryID) > 128 || args.Offset < 0 || args.Block < 0 || args.Length < 0 || args.Length > 8192 {
		return textResult(call, "Invalid result selector or bounds; use exactly one of call_id and entry_id.", true), nil
	}
	if args.Section == "" {
		args.Section = "metadata"
	}
	if args.Length == 0 {
		args.Length = 8192
	}
	if args.Section != "metadata" && args.Section != "content" && args.Section != "structured" {
		return textResult(call, "Unknown result section.", true), nil
	}
	stored, err := r.reader.ReadToolResult(ctx, args.CallID, args.EntryID)
	if err != nil {
		return textResult(call, err.Error(), true), nil
	}
	message := stored.Message
	metadata := map[string]any{"entry_id": stored.ID, "call_id": message.ToolCallID, "tool": message.ToolName, "is_error": message.IsError, "section": args.Section}
	metadata["durable"] = stored.Durable
	if message.Details != nil {
		metadata["execution_state"], metadata["binding"], metadata["storage_loss"] = message.Details.State, message.Details.Binding, message.Details.Loss
	}
	var image *llm.ImageContent
	var value string
	switch args.Section {
	case "metadata":
		if args.Block > len(message.Content) {
			return textResult(call, "Metadata block offset is outside the result.", true), nil
		}
		blocks := make([]map[string]any, 0, 32)
		for i := args.Block; i < len(message.Content); i++ {
			if i == args.Block+32 {
				metadata["next_block"] = i
				break
			}
			part := message.Content[i]
			block := map[string]any{"index": i, "type": part.Type}
			if part.Type == llm.ContentTypeText {
				block["bytes"] = len(part.Text)
			}
			if part.Image != nil {
				block["mime_type"], block["bytes"] = part.Image.MIMEType, len(part.Image.Data)
			}
			blocks = append(blocks, block)
		}
		metadata["blocks"], metadata["block_count"] = blocks, len(message.Content)
		if message.Details != nil {
			metadata["structured_bytes"] = len(message.Details.StructuredContent)
		}
	case "content":
		if args.Block >= len(message.Content) {
			return textResult(call, "Content block does not exist.", true), nil
		}
		metadata["block"] = args.Block
		part := message.Content[args.Block]
		switch part.Type {
		case llm.ContentTypeText:
			value = part.Text
		case llm.ContentTypeImage:
			if args.Offset != 0 || part.Image == nil {
				return textResult(call, "Image requires offset 0 and retained image data.", true), nil
			}
			if len(part.Image.Data) > 16<<20 {
				return textResult(call, "Stored image exceeds the 16 MiB readback limit; source history is unchanged.", true), nil
			}
			copy := part.Image.Clone()
			image = &copy
		default:
			return textResult(call, "This saved content type is not supported by result readback.", true), nil
		}
	case "structured":
		if message.Details == nil || len(message.Details.StructuredContent) == 0 {
			return textResult(call, "No structured JSON was saved for this result.", true), nil
		}
		value = string(message.Details.StructuredContent)
	}
	if args.Section != "metadata" && image == nil {
		if args.Offset > len(value) || args.Offset < len(value) && !utf8.RuneStart(value[args.Offset]) {
			return textResult(call, "Offset is outside the section or splits UTF-8; use the returned next_offset.", true), nil
		}
		end := min(len(value), args.Offset+args.Length)
		for end > args.Offset && end < len(value) && !utf8.RuneStart(value[end]) {
			end--
		}
		if end == args.Offset && end < len(value) {
			return textResult(call, "Length is too small for the next UTF-8 character.", true), nil
		}
		metadata["offset"], metadata["total_bytes"], metadata["complete"] = args.Offset, len(value), end == len(value)
		if end < len(value) {
			metadata["next_offset"] = end
		}
		if args.Section == "structured" {
			metadata["format"] = "raw_json_text"
			metadata["fragment"] = args.Offset != 0 || end < len(value)
		}
		value = value[args.Offset:end]
	}
	if next, ok := metadata["next_offset"]; ok {
		metadata["next_read"] = resultReadContinuation(stored.ID, args, "offset", next)
	} else if next, ok := metadata["next_block"]; ok {
		metadata["next_read"] = resultReadContinuation(stored.ID, args, "block", next)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil || len(encoded) > 32<<10 {
		return textResult(call, "Result metadata exceeds its readback bound.", true), nil
	}
	result := textResult(call, string(encoded), false)
	if image != nil {
		result.Content = append(result.Content, llm.ContentPart{Type: llm.ContentTypeImage, Image: image})
	} else if args.Section != "metadata" {
		result.Content = append(result.Content, llm.NewTextContent(value).Part())
	}
	return result, nil
}

func resultReadContinuation(entryID string, args toolResultReadArgs, position string, next any) map[string]any {
	continuation := map[string]any{"section": args.Section, "block": args.Block, "length": args.Length}
	continuation[position] = next
	if entryID != "" {
		continuation["entry_id"] = entryID
	} else if args.EntryID != "" {
		continuation["entry_id"] = args.EntryID
	} else {
		continuation["call_id"] = args.CallID
	}
	return continuation
}
