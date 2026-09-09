package tool

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

const (
	defaultReadLines = 2000
	readBufferBytes  = 32 * 1024
	readSchema       = `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file or directory to read (relative or absolute)"},
    "image_id": {"type": "string", "description": "Saved image ID to re-read the original from this conversation instead of a path"},
    "crop": {"type": "object", "properties": {
      "x": {"type": "integer", "minimum": 0}, "y": {"type": "integer", "minimum": 0},
      "width": {"type": "integer", "minimum": 1}, "height": {"type": "integer", "minimum": 1}
    }, "required": ["x", "y", "width", "height"], "additionalProperties": false},
    "offset": {"type": "integer", "minimum": 1, "description": "Line number to start reading from (1-indexed)"},
    "limit": {"type": "integer", "minimum": 1, "description": "Maximum number of lines to read"}
  },
  "oneOf": [{"required": ["path"]}, {"required": ["image_id"]}],
  "additionalProperties": false
}`
)

var errBinaryContent = errors.New("binary content")

// ReadOptions supplies application-owned image history and model capability.
type ReadOptions struct {
	LookupImage   func(context.Context, string) (llm.ImageContent, error)
	CanReadImages func() bool
}

// ReadRequest selects file content or an original image already in the conversation.
type ReadRequest struct {
	Path    string           `json:"path"`
	ImageID string           `json:"image_id"`
	Offset  int              `json:"offset"`
	Limit   int              `json:"limit"`
	Crop    *llm.ImageRegion `json:"crop"`
}

// Read reads bounded text or image content.
type Read struct {
	workspace *Workspace
	options   ReadOptions
}

// NewRead constructs a read tool.
func NewRead(workspace *Workspace, options ...ReadOptions) (*Read, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("tool: at most one read options value is allowed")
	}
	r := &Read{workspace: workspace}
	if len(options) == 1 {
		r.options = options[0]
	}
	return r, nil
}

// Definition returns the model-facing read contract.
func (r *Read) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:          "read",
		Description:   "Read text, PNG/JPEG images, or a shallow directory listing. Text is limited to 2000 complete lines or 50 KiB; use offset/limit to continue. Images are resized automatically. Use image_id to recover a saved original, and crop {x,y,width,height} in original pixels to inspect details. Specify exactly one of path or image_id.",
		InputSchema:   jsonSchema(readSchema),
		PromptSnippet: "Read file contents",
		PromptGuidelines: []string{
			"Use read to examine files instead of cat or sed.",
		},
	}
}

// Execute reads the requested file.
func (r *Read) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	args, err := decodeArguments[ReadRequest](ctx, call, "read")
	if err != nil {
		return llm.ToolResult{}, err
	}
	var truncation llm.ToolTruncation
	content, err := r.content(ctx, args, &truncation)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool %q: %w", "read", err)
	}
	return llm.ToolResult{CallID: call.ID, Name: call.Name, Content: content, Truncation: truncation}, nil
}

// Content is the shared read implementation. Callers must authorize the resolved
// path before calling; this function never grants filesystem access.
func (r *Read) Content(ctx context.Context, args ReadRequest) ([]llm.ContentPart, error) {
	return r.content(ctx, args, nil)
}

func (r *Read) content(ctx context.Context, args ReadRequest, truncation *llm.ToolTruncation) ([]llm.ContentPart, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if (args.Path == "") == (args.ImageID == "") {
		return nil, fmt.Errorf("specify exactly one of path or image_id")
	}
	if args.ImageID != "" {
		if r.options.LookupImage == nil {
			return nil, fmt.Errorf("saved image lookup is unavailable")
		}
		img, err := r.options.LookupImage(ctx, args.ImageID)
		if err != nil {
			return nil, err
		}
		return r.imageContent(ctx, img, args)
	}
	originalArgs := args
	userLimit := args.Limit
	if args.Offset < 0 || args.Limit < 0 {
		return nil, fmt.Errorf("tool \"read\": offset and limit cannot be negative")
	}
	if args.Offset == 0 {
		args.Offset = 1
	}
	if args.Limit == 0 || args.Limit > defaultReadLines {
		args.Limit = defaultReadLines
	}

	// An explicit limit smaller than the default page size is a pagination
	// hint: after the page, count how many lines remain so the notice can say
	// \"N more lines\" instead of the generic 2000-line hint.
	userLimited := userLimit > 0 && userLimit < defaultReadLines

	path, err := r.ResolvePath(args.Path)
	if err != nil {
		return nil, fmt.Errorf("tool \"read\": %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("tool \"read\": open %q: %w", args.Path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("tool \"read\": stat %q: %w", args.Path, err)
	}
	if info.IsDir() {
		return readDirectory(ctx, file, originalArgs)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("tool \"read\": %q is not a regular file", args.Path)
	}

	sample := make([]byte, 512)
	n, err := file.Read(sample)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	mime := http.DetectContentType(sample[:n])
	if strings.HasPrefix(mime, "image/") {
		if info.Size() > media.MaxSourceBytes {
			return nil, fmt.Errorf("image exceeds 16 MiB source limit")
		}
		data, err := io.ReadAll(io.LimitReader(file, media.MaxSourceBytes+1))
		if err != nil {
			return nil, err
		}
		return r.imageContent(ctx, llm.ImageContent{Data: data, MIMEType: mime, Source: path}, originalArgs)
	}
	if args.Crop != nil {
		return nil, fmt.Errorf("crop is only supported for images")
	}

	text, details, err := readTextPage(ctx, file, args.Offset, args.Limit, args.Path, userLimited)
	if err != nil {
		if errors.Is(err, errBinaryContent) {
			return nil, fmt.Errorf(
				"tool \"read\": %q appears to be a binary file",
				args.Path,
			)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("tool \"read\": read %q: %w", args.Path, err)
	}
	if truncation != nil {
		*truncation = details
	}
	return []llm.ContentPart{llm.NewTextContent(text).Part()}, nil
}

type boundedLine struct {
	data  []byte
	bytes int
	found bool
}

type readStopReason uint8

const (
	readStopNone readStopReason = iota
	readStopRequestedLines
	readStopDefaultLines
	readStopBytes
)

// readPageInfo carries the context needed to render a continuation notice:
// why reading stopped, the path to suggest in fallback commands, and the
// remaining-line count for explicitly limited reads.
type readPageInfo struct {
	reason    readStopReason
	path      string
	remaining int
	capped    bool
}

func readTextPage(
	ctx context.Context,
	source io.Reader,
	offset, limit int,
	path string,
	userLimited bool,
) (string, llm.ToolTruncation, error) {
	reader := bufio.NewReaderSize(source, readBufferBytes)
	info := readPageInfo{path: path}

	for lineNumber := 1; lineNumber < offset; lineNumber++ {
		line, err := readBoundedLine(ctx, reader, 0)
		if err != nil {
			return "", llm.ToolTruncation{}, err
		}
		if !line.found {
			return offsetBeyondEnd(offset), llm.ToolTruncation{}, nil
		}
	}

	content := make([]byte, 0, min(maxOutputBytes, 8*1024))
	lineEnds := make([]int, 0, limit)
	stopReason := readStopNone

	for len(lineEnds) < limit {
		lineNumber := offset + len(lineEnds)
		line, err := readBoundedLine(ctx, reader, maxOutputBytes+1)
		if err != nil {
			return "", llm.ToolTruncation{}, err
		}
		if !line.found {
			if len(lineEnds) == 0 && offset > 1 {
				return offsetBeyondEnd(offset), llm.ToolTruncation{}, nil
			}
			break
		}
		if line.bytes > maxOutputBytes || len(content)+len(line.data) > maxOutputBytes {
			if len(lineEnds) == 0 {
				return oversizedLineMessage(lineNumber, path), llm.ToolTruncation{Reason: llm.TruncationOversizedLine, NextOffset: lineNumber}, nil
			}
			stopReason = readStopBytes
			break
		}

		content = append(content, line.data...)
		lineEnds = append(lineEnds, len(content))
	}

	if stopReason == readStopNone && len(lineEnds) == limit {
		if userLimited {
			remaining, capped, err := countRemainingLines(ctx, reader, maxReadBytes)
			if err != nil {
				return "", llm.ToolTruncation{}, err
			}
			if remaining > 0 {
				info.remaining = remaining
				info.capped = capped
				stopReason = readStopRequestedLines
			}
		} else {
			more, err := hasMoreText(ctx, reader)
			if err != nil {
				return "", llm.ToolTruncation{}, err
			}
			if more {
				if limit == defaultReadLines {
					stopReason = readStopDefaultLines
				} else {
					stopReason = readStopRequestedLines
				}
			}
		}
	}

	if stopReason == readStopNone {
		return string(content), llm.ToolTruncation{}, nil
	}
	info.reason = stopReason
	text, details := formatReadPage(content, lineEnds, offset, info)
	return text, details, nil
}

func readBoundedLine(
	ctx context.Context,
	reader *bufio.Reader,
	captureLimit int,
) (boundedLine, error) {
	var line boundedLine
	if captureLimit > 0 {
		line.data = make([]byte, 0, min(captureLimit, readBufferBytes))
	}

	for {
		if err := ctx.Err(); err != nil {
			return boundedLine{}, err
		}

		fragment, err := reader.ReadSlice('\n')
		if bytes.IndexByte(fragment, 0) >= 0 {
			return boundedLine{}, errBinaryContent
		}
		line.bytes += len(fragment)
		if remaining := captureLimit - len(line.data); remaining > 0 {
			line.data = append(line.data, fragment[:min(len(fragment), remaining)]...)
		}
		if err := ctx.Err(); err != nil {
			return boundedLine{}, err
		}

		switch {
		case err == nil:
			line.found = true
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			line.found = line.bytes > 0
			return line, nil
		default:
			return boundedLine{}, err
		}
	}
}

func hasMoreText(ctx context.Context, reader *bufio.Reader) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	data, err := reader.Peek(1)
	if bytes.IndexByte(data, 0) >= 0 {
		return false, errBinaryContent
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	if err == nil {
		return true, nil
	}
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	return false, err
}

// countRemainingLines counts the complete lines left in reader, stopping once
// budget bytes have been consumed so an explicit small limit cannot turn into
// a whole-file scan. It reports whether the count was cut short by the budget.
func countRemainingLines(
	ctx context.Context,
	reader *bufio.Reader,
	budget int,
) (count int, capped bool, err error) {
	for budget > 0 {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		line, err := readBoundedLine(ctx, reader, 0)
		if err != nil {
			return 0, false, err
		}
		if !line.found {
			return count, false, nil
		}
		count++
		budget -= line.bytes
	}
	return count, true, nil
}

func formatReadPage(
	content []byte,
	lineEnds []int,
	offset int,
	info readPageInfo,
) (string, llm.ToolTruncation) {
	details := llm.ToolTruncation{}
	if info.reason == readStopRequestedLines && !info.capped {
		details.TotalLinesKnown = true
		details.TotalLines = offset - 1 + len(lineEnds) + info.remaining
	}
	for len(lineEnds) > 0 {
		endLine := offset + len(lineEnds) - 1
		nextOffset := endLine + 1
		notice := readContinuationNotice(offset, endLine, nextOffset, info)
		contentEnd := lineEnds[len(lineEnds)-1]
		page := content[:contentEnd]
		separator := "\n\n"
		if len(page) > 0 && page[len(page)-1] == '\n' {
			separator = "\n"
		}
		if len(page)+len(separator)+len(notice) <= maxOutputBytes {
			result := make([]byte, 0, len(page)+len(separator)+len(notice))
			result = append(result, page...)
			result = append(result, separator...)
			result = append(result, notice...)
			details.OutputLines = len(lineEnds)
			details.OutputBytes = len(page)
			details.NextOffset = nextOffset
			switch info.reason {
			case readStopRequestedLines:
				details.Reason = llm.TruncationRequestedLines
			case readStopDefaultLines:
				details.Reason = llm.TruncationLineLimit
			case readStopBytes:
				details.Reason = llm.TruncationByteLimit
			}
			return string(result), details
		}

		lineEnds = lineEnds[:len(lineEnds)-1]
		info.reason = readStopBytes
	}

	details.Reason = llm.TruncationOversizedLine
	details.NextOffset = offset
	return oversizedLineMessage(offset, info.path), details
}

func readContinuationNotice(start, end, next int, info readPageInfo) string {
	switch info.reason {
	case readStopRequestedLines:
		noun := "lines"
		if info.remaining == 1 {
			noun = "line"
		}
		if info.capped {
			return fmt.Sprintf(
				"[at least %d more %s in file. Use offset=%d to continue.]",
				info.remaining,
				noun,
				next,
			)
		}
		return fmt.Sprintf(
			"[%d more %s in file. Use offset=%d to continue.]",
			info.remaining,
			noun,
			next,
		)
	case readStopDefaultLines:
		return fmt.Sprintf(
			"[Showing lines %d-%d (2000 line limit). Use offset=%d to continue.]",
			start,
			end,
			next,
		)
	case readStopBytes:
		return fmt.Sprintf(
			"[Showing lines %d-%d (50 KiB limit). Use offset=%d to continue.]",
			start,
			end,
			next,
		)
	default:
		return fmt.Sprintf(
			"[Showing lines %d-%d. Use offset=%d to continue.]",
			start,
			end,
			next,
		)
	}
}

func oversizedLineMessage(lineNumber int, path string) string {
	return fmt.Sprintf(
		"[Line %d exceeds the 50 KiB output limit. Read it with bash: sed -n '%dp' %s | head -c %d]",
		lineNumber,
		lineNumber,
		shellQuote(path),
		maxOutputBytes,
	)
}

// shellQuote wraps value in single quotes for use in a bash command, escaping
// embedded single quotes the way bash expects (close quote, escaped quote,
// reopen quote).
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func offsetBeyondEnd(offset int) string {
	return fmt.Sprintf("[offset %d is beyond end of file]", offset)
}

// ResolvePath returns the physical, normalized read target for permission checks.
func (r *Read) ResolvePath(input string) (string, error) {
	path, err := r.resolveReadTarget(input)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func (r *Read) imageContent(ctx context.Context, img llm.ImageContent, args ReadRequest) ([]llm.ContentPart, error) {
	if args.Offset != 0 || args.Limit != 0 {
		return nil, fmt.Errorf("offset and limit apply only to text files")
	}
	if r.options.CanReadImages != nil && !r.options.CanReadImages() {
		return nil, fmt.Errorf("current model does not support image input")
	}
	prepared, err := media.Prepare(ctx, img, args.Crop)
	if err != nil {
		return nil, err
	}
	return []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &prepared}}, nil
}

func readDirectory(ctx context.Context, file *os.File, args ReadRequest) ([]llm.ContentPart, error) {
	if args.Crop != nil || args.Offset != 0 || args.Limit != 0 {
		return nil, fmt.Errorf("directory listings do not support crop, offset or limit")
	}
	var out strings.Builder
	for i := 0; i < defaultReadLines; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := file.ReadDir(1)
		if errors.Is(err, io.EOF) {
			return []llm.ContentPart{llm.NewTextContent(out.String()).Part()}, nil
		}
		if err != nil {
			return nil, err
		}
		name := entries[0].Name()
		if entries[0].IsDir() {
			name += "/"
		}
		if out.Len()+len(name)+1 > maxOutputBytes-80 {
			break
		}
		out.WriteString(name)
		out.WriteByte('\n')
	}
	out.WriteString("[Directory listing truncated; use ls or find to explore further.]\n")
	return []llm.ContentPart{llm.NewTextContent(out.String()).Part()}, nil
}
