package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/media"
)

const (
	maxMCPResultBlocks = 256
	maxMCPTextBytes    = 1 << 20
	maxMCPImageBytes   = 16 << 20
)

func (m *MCP) mapResult(call llm.ToolCall, source mcpclient.Result, callErr error) llm.ToolResult {
	binding := m.binding
	details := &llm.ToolResultDetails{State: source.State, Binding: &binding}
	result := llm.ToolResult{CallID: call.ID, Name: call.Name, IsError: source.IsError || callErr != nil, Details: details}
	losses := []string{}
	loss := func(reason string) {
		if !slices.Contains(losses, reason) {
			losses = append(losses, reason)
		}
	}
	switch source.State {
	case llm.ExecutionNotDispatched, llm.ExecutionReturned, llm.ExecutionUnknown:
	default:
		details.State, result.IsError = llm.ExecutionUnknown, true
		loss("Backend supplied no valid execution state.")
	}
	if source.Loss != "" {
		loss("The MCP transport reported omitted source data.")
	}
	textBytes, imageBytes := 0, 0
	addText := func(text string) {
		if !utf8.ValidString(text) {
			text = strings.ToValidUTF8(text, "�")
			loss("Invalid UTF-8 text was replaced.")
		}
		redacted := m.redact.text(text)
		if redacted != text {
			loss("Known credential values were redacted.")
		}
		if len(redacted) > maxMCPTextBytes-textBytes {
			redacted = validUTF8Prefix(redacted, maxMCPTextBytes-textBytes)
			loss("Text exceeded the 1 MiB storage limit.")
		}
		textBytes += len(redacted)
		result.Content = append(result.Content, llm.NewTextContent(redacted).Part())
	}
	// Cancellation must not discard an already returned result. Conversion
	// gets a separate finite local deadline, without restarting remote work.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, block := range source.Content {
		if i >= maxMCPResultBlocks {
			loss("Content exceeded the 256-block storage limit.")
			break
		}
		resource := block.Resource
		resource.URI = m.redact.text(resource.URI)
		resource.Name = m.redact.text(resource.Name)
		resource.Title = m.redact.text(resource.Title)
		resource.Description = m.redact.text(resource.Description)
		resource.MIMEType = m.redact.text(resource.MIMEType)
		if resource != block.Resource {
			loss("Known credential values were redacted.")
		}
		block.Resource = resource
		switch block.Kind {
		case mcpclient.BlockText:
			addText(block.Text)
		case mcpclient.BlockResourceLink:
			encoded, _ := json.Marshal(block.Resource)
			addText("Resource link (not fetched): " + string(encoded))
		case mcpclient.BlockResourceText:
			addText(fmt.Sprintf("Embedded resource %q (%s):\n%s", block.Resource.URI, block.Resource.MIMEType, block.Text))
		case mcpclient.BlockImage, mcpclient.BlockResourceBlob:
			mime := block.MIMEType
			if mime == "" {
				mime = block.Resource.MIMEType
			}
			if block.Kind == mcpclient.BlockResourceBlob && !strings.HasPrefix(mime, "image/") {
				addText(fmt.Sprintf("Unsupported binary resource %q (%s); bytes were not saved.", block.Resource.URI, mime))
				loss("Unsupported binary resource bytes were omitted.")
				continue
			}
			if len(block.Data) > maxMCPImageBytes-imageBytes || m.redact.contains(string(block.Data)) {
				addText("Image omitted because it exceeded the source storage limit or contained a known credential.")
				loss("An image was omitted before storage.")
				continue
			}
			image, err := media.Prepare(ctx, llm.ImageContent{Data: block.Data, MIMEType: mime, Source: m.redact.text(block.Resource.URI)}, nil)
			if err != nil {
				addText("Image could not be validated or prepared; bytes were not saved.")
				loss("An invalid, unsupported or unprepared image was omitted.")
				continue
			}
			size := len(image.Data)
			if image.Original != nil {
				size += len(image.Original.Data)
			}
			if size > maxMCPImageBytes-imageBytes {
				addText("Image and original exceed the aggregate image storage limit; neither was saved.")
				loss("Prepared images exceeded the 16 MiB aggregate storage limit.")
				continue
			}
			imageBytes += size
			result.Content = append(result.Content, llm.ContentPart{Type: llm.ContentTypeImage, Image: &image})
		case mcpclient.BlockAudio:
			addText("Unsupported audio content; audio bytes were not saved.")
			loss("Audio bytes were omitted.")
		default:
			addText("Unsupported or malformed MCP content block; its payload was not saved.")
			loss("Unsupported content payload was omitted.")
		}
	}
	if len(source.StructuredContent) > 0 {
		if len(source.StructuredContent) > llm.MaxStructuredResultBytes {
			loss("Structured content exceeded the 1 MiB storage limit.")
		} else {
			raw, changed, err := m.redact.json(source.StructuredContent)
			if err != nil || len(raw) > llm.MaxStructuredResultBytes {
				loss("Structured content could not be safely retained.")
			} else {
				details.StructuredContent = raw
				if changed {
					loss("Known credential values were redacted.")
				}
			}
		}
	}
	if callErr != nil {
		// Raw backend errors can contain endpoint URLs or response bodies.
		addText("MCP operation failed; any returned content is partial. See the recorded execution state before deciding whether to retry.")
	}
	if len(result.Content) == 0 {
		addText("MCP returned no content blocks.")
	}
	details.Loss = strings.Join(losses, " ")
	return result
}
