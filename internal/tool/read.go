package tool

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	defaultReadLines = 2000
	readBufferBytes  = 32 * 1024
	readSchema       = `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file to read (relative or absolute)"},
    "offset": {"type": "integer", "minimum": 1, "description": "Line number to start reading from (1-indexed)"},
    "limit": {"type": "integer", "minimum": 1, "description": "Maximum number of lines to read"}
  },
  "required": ["path"],
  "additionalProperties": false
}`
)

var errBinaryContent = errors.New("binary content")

// Read reads bounded text content from one file.
type Read struct {
	workspace *Workspace
}

// NewRead constructs a read tool.
func NewRead(workspace *Workspace) (*Read, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	return &Read{workspace: workspace}, nil
}

// Definition returns the model-facing read contract.
func (r *Read) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:          "read",
		Description:   "Read a text file, resolving relative paths from the working directory. Output is limited to 2000 complete lines or 50 KiB; use offset and limit, then follow continuation notices for large files.",
		InputSchema:   jsonSchema(readSchema),
		PromptSnippet: "Read file contents",
		PromptGuidelines: []string{
			"Use read to examine files instead of cat or sed.",
		},
	}
}

// Execute reads the requested file.
func (r *Read) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	args, err := decodeArguments[arguments](ctx, call, "read")
	if err != nil {
		return llm.ToolResult{}, err
	}
	userLimit := args.Limit
	if args.Offset < 0 || args.Limit < 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": offset and limit cannot be negative")
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

	path, err := r.resolveReadTarget(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": open %q: %w", args.Path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": stat %q: %w", args.Path, err)
	}
	if !info.Mode().IsRegular() {
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": %q is not a regular file", args.Path)
	}

	text, err := readTextPage(ctx, file, args.Offset, args.Limit, args.Path, userLimited)
	if err != nil {
		if errors.Is(err, errBinaryContent) {
			return llm.ToolResult{}, fmt.Errorf(
				"tool \"read\": %q appears to be a binary file",
				args.Path,
			)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return llm.ToolResult{}, err
		}
		return llm.ToolResult{}, fmt.Errorf("tool \"read\": read %q: %w", args.Path, err)
	}
	return textResult(call, text, false), nil
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
) (string, error) {
	reader := bufio.NewReaderSize(source, readBufferBytes)
	info := readPageInfo{path: path}

	for lineNumber := 1; lineNumber < offset; lineNumber++ {
		line, err := readBoundedLine(ctx, reader, 0)
		if err != nil {
			return "", err
		}
		if !line.found {
			return offsetBeyondEnd(offset), nil
		}
	}

	content := make([]byte, 0, min(maxOutputBytes, 8*1024))
	lineEnds := make([]int, 0, limit)
	stopReason := readStopNone

	for len(lineEnds) < limit {
		lineNumber := offset + len(lineEnds)
		line, err := readBoundedLine(ctx, reader, maxOutputBytes+1)
		if err != nil {
			return "", err
		}
		if !line.found {
			if len(lineEnds) == 0 && offset > 1 {
				return offsetBeyondEnd(offset), nil
			}
			break
		}
		if line.bytes > maxOutputBytes || len(content)+len(line.data) > maxOutputBytes {
			if len(lineEnds) == 0 {
				return oversizedLineMessage(lineNumber, path), nil
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
				return "", err
			}
			if remaining > 0 {
				info.remaining = remaining
				info.capped = capped
				stopReason = readStopRequestedLines
			}
		} else {
			more, err := hasMoreText(ctx, reader)
			if err != nil {
				return "", err
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
		return string(content), nil
	}
	info.reason = stopReason
	return formatReadPage(content, lineEnds, offset, info), nil
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
) string {
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
			return string(result)
		}

		lineEnds = lineEnds[:len(lineEnds)-1]
		info.reason = readStopBytes
	}

	return oversizedLineMessage(offset, info.path)
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
// embedded single quotes the way bash expects ('\”).
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func offsetBeyondEnd(offset int) string {
	return fmt.Sprintf("[offset %d is beyond end of file]", offset)
}
