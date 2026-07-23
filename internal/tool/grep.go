package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	defaultGrepLimit = 100
	maxGrepContext   = 20
	grepSchema       = `{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Search pattern (regex or literal string)"},
    "path": {"type": "string", "description": "Directory or file to search (default: current directory)"},
    "glob": {"type": "string", "description": "Filter files by glob pattern, e.g. '*.go' or '**/*_test.go'"},
    "ignoreCase": {"type": "boolean", "description": "Case-insensitive search (default: false)"},
    "literal": {"type": "boolean", "description": "Treat pattern as a literal string instead of regex (default: false)"},
    "context": {"type": "integer", "minimum": 0, "description": "Number of lines to show before and after each match (default: 0)"},
    "limit": {"type": "integer", "minimum": 1, "description": "Maximum number of matches to return (default: 100)"}
  },
  "required": ["pattern"],
  "additionalProperties": false
}`
)

// Grep searches text files.
type Grep struct {
	workspace *Workspace
}

// NewGrep constructs a grep tool.
func NewGrep(workspace *Workspace) (*Grep, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	return &Grep{workspace: workspace}, nil
}

// Definition returns the model-facing grep contract.
func (g *Grep) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "grep",
		Description: "Search text files, resolving relative paths from the working directory.",
		InputSchema: jsonSchema(grepSchema),
	}
}

// Execute performs a recursive, bounded text search.
func (g *Grep) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		IgnoreCase bool   `json:"ignoreCase"`
		Literal    bool   `json:"literal"`
		Context    int    `json:"context"`
		Limit      int    `json:"limit"`
	}
	args, err := decodeArguments[arguments](ctx, call, "grep")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if args.Pattern == "" {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": pattern is required")
	}
	if len(args.Pattern) > maxPatternBytes || len(args.Glob) > maxPatternBytes {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": pattern and glob must not exceed %d bytes", maxPatternBytes)
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Context < 0 || args.Context > maxGrepContext {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": context must be between 0 and %d", maxGrepContext)
	}
	if args.Limit < 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": limit cannot be negative")
	}
	if args.Limit == 0 || args.Limit > defaultGrepLimit {
		args.Limit = defaultGrepLimit
	}

	pattern := args.Pattern
	if args.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	if args.IgnoreCase {
		pattern = "(?i:" + pattern + ")"
	}
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": compile pattern: %w", err)
	}
	if args.Glob != "" {
		if _, err := matchGlob(args.Glob, "probe"); err != nil {
			return llm.ToolResult{}, fmt.Errorf("tool \"grep\": invalid glob: %w", err)
		}
	}

	rootPath, err := g.workspace.resolvePath(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": %w", err)
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": stat %q: %w", args.Path, err)
	}

	collector := newTextCollector(maxOutputBytes)
	matches := 0
	skipped := 0
	entriesScanned := 0
	bytesScanned := int64(0)
	displayPath := func(filePath string) string {
		if filepath.IsAbs(args.Path) {
			return filepath.ToSlash(filePath)
		}
		relativePath, relativeErr := filepath.Rel(g.workspace.Path(), filePath)
		if relativeErr != nil {
			return filepath.ToSlash(filePath)
		}
		return filepath.ToSlash(relativePath)
	}
	searchFile := func(filePath string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Stat(filePath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
			skipped++
			return nil
		}
		if info.Size() > 0 && bytesScanned > maxSearchTotalBytes-info.Size() {
			return errScanLimit
		}
		bytesScanned += info.Size()
		matched, err := g.grepFile(
			ctx,
			filePath,
			displayPath(filePath),
			matcher,
			args.Context,
			args.Limit-matches,
			collector,
		)
		if err != nil {
			if err == errSkipFile {
				skipped++
				return nil
			}
			return err
		}
		matches += matched
		if matches >= args.Limit {
			return errResultLimit
		}
		if collector.truncated {
			return errOutputLimit
		}
		return nil
	}

	if info.Mode().IsRegular() {
		if args.Glob != "" {
			matched, matchErr := matchGlob(args.Glob, filepath.Base(rootPath))
			if matchErr != nil {
				return llm.ToolResult{}, matchErr
			}
			if !matched {
				return textResult(call, "", false), nil
			}
		}
		err = searchFile(rootPath)
	} else if info.IsDir() {
		err = filepath.WalkDir(rootPath, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			entriesScanned++
			if entriesScanned > maxWalkEntries {
				return errScanLimit
			}
			if entry.IsDir() {
				if filePath != rootPath && entry.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			if args.Glob != "" {
				relativePath, relativeErr := filepath.Rel(rootPath, filePath)
				if relativeErr != nil {
					return relativeErr
				}
				matched, matchErr := matchGlob(args.Glob, relativePath)
				if matchErr != nil {
					return matchErr
				}
				if !matched {
					return nil
				}
			}
			return searchFile(filePath)
		})
	} else {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": %q is not a file or directory", args.Path)
	}
	if err != nil && err != errResultLimit && err != errOutputLimit && err != errScanLimit {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": search %q: %w", args.Path, err)
	}
	if err == errResultLimit && !collector.truncated {
		collector.WriteString(fmt.Sprintf("[match limit reached: %d]\n", args.Limit))
	}
	if skipped > 0 && !collector.truncated {
		collector.WriteString(fmt.Sprintf("[skipped %d binary or oversized file(s)]\n", skipped))
	}
	if err == errScanLimit && !collector.truncated {
		collector.WriteString(fmt.Sprintf("[scan limit reached: %d entries or 100 mib]\n", maxWalkEntries))
	}
	return textResult(call, strings.TrimSuffix(collector.String(), "\n"), false), nil
}

var errSkipFile = errors.New("skip file")

func (g *Grep) grepFile(
	ctx context.Context,
	filePath string,
	displayPath string,
	matcher *regexp.Regexp,
	contextLines int,
	limit int,
	collector *textCollector,
) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
		return 0, errSkipFile
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSearchFileBytes+1))
	if err != nil {
		return 0, err
	}
	if len(data) > maxSearchFileBytes || bytes.IndexByte(data, 0) >= 0 {
		return 0, errSkipFile
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	matches := 0
	for index, line := range lines {
		if err := ctx.Err(); err != nil {
			return matches, err
		}
		if !matcher.MatchString(line) {
			continue
		}
		if matches >= limit {
			return matches, nil
		}
		matches++
		start := max(0, index-contextLines)
		end := min(len(lines), index+contextLines+1)
		for lineIndex := start; lineIndex < end; lineIndex++ {
			separator := "-"
			if lineIndex == index {
				separator = ":"
			}
			output := fmt.Sprintf(
				"%s%s%d%s%s\n",
				displayPath,
				separator,
				lineIndex+1,
				separator,
				truncateLine(lines[lineIndex]),
			)
			if !collector.WriteString(output) {
				return matches, nil
			}
		}
	}
	return matches, nil
}
