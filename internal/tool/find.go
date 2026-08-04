package tool

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	defaultFindLimit = 1000
	findSchema       = `{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Glob pattern to match files, e.g. '*.go', '**/*.json', or 'internal/**/*_test.go'"},
    "path": {"type": "string", "description": "Directory to search in (default: current directory)"},
    "limit": {"type": "integer", "minimum": 1, "description": "Maximum number of results (default: 1000)"}
  },
  "required": ["pattern"],
  "additionalProperties": false
}`
)

// Find locates files by glob pattern.
type Find struct {
	workspace *Workspace
}

// NewFind constructs a find tool.
func NewFind(workspace *Workspace) (*Find, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	return &Find{workspace: workspace}, nil
}

// Definition returns the model-facing find contract.
func (f *Find) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "find",
		Description: "Find files, resolving relative paths from the working directory.",
		InputSchema: jsonSchema(findSchema),
	}
}

// Execute walks the requested directory without following directory symlinks.
func (f *Find) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	args, err := decodeArguments[arguments](ctx, call, "find")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if args.Pattern == "" {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": pattern is required")
	}
	if len(args.Pattern) > maxPatternBytes {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": pattern exceeds %d bytes", maxPatternBytes)
	}
	if _, err := path.Match(strings.ReplaceAll(args.Pattern, "**", "*"), "probe"); err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": invalid pattern: %w", err)
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Limit < 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": limit cannot be negative")
	}
	if args.Limit == 0 || args.Limit > defaultFindLimit {
		args.Limit = defaultFindLimit
	}

	rootPath, err := f.workspace.resolvePath(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": %w", err)
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": stat %q: %w", args.Path, err)
	}
	if !info.IsDir() {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": %q is not a directory", args.Path)
	}

	collector := newTextCollector(maxOutputBytes)
	count := 0
	entriesScanned := 0
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
		if filePath == rootPath {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		matchPath, relativeErr := filepath.Rel(rootPath, filePath)
		if relativeErr != nil {
			return relativeErr
		}
		matched, matchErr := matchGlob(args.Pattern, matchPath)
		if matchErr != nil {
			return matchErr
		}
		if !matched {
			return nil
		}
		if count >= args.Limit {
			return errResultLimit
		}
		count++
		resultPath := filePath
		if !filepath.IsAbs(args.Path) {
			resultPath, relativeErr = filepath.Rel(f.workspace.physicalPath, filePath)
			if relativeErr != nil {
				return relativeErr
			}
		}
		if !collector.WriteString(filepath.ToSlash(resultPath) + "\n") {
			return errOutputLimit
		}
		return nil
	})
	if err != nil && err != errResultLimit && err != errOutputLimit && err != errScanLimit {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": walk %q: %w", args.Path, err)
	}
	if err == errResultLimit && !collector.truncated {
		collector.WriteString(fmt.Sprintf("[result limit reached: %d]", args.Limit))
	}
	if err == errScanLimit && !collector.truncated {
		collector.WriteString(fmt.Sprintf("[scan limit reached: %d entries]", maxWalkEntries))
	}
	return textResult(call, strings.TrimSuffix(collector.String(), "\n"), false), nil
}

var (
	errResultLimit = errors.New("result limit reached")
	errOutputLimit = errors.New("output limit reached")
	errScanLimit   = errors.New("scan limit reached")
)

func matchGlob(pattern, name string) (bool, error) {
	pattern = path.Clean(filepath.ToSlash(pattern))
	name = path.Clean(filepath.ToSlash(name))
	if !strings.Contains(pattern, "/") {
		return path.Match(pattern, path.Base(name))
	}
	return matchGlobSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchGlobSegments(pattern, name []string) (bool, error) {
	if len(pattern) == 0 {
		return len(name) == 0, nil
	}
	if pattern[0] == "**" {
		matched, err := matchGlobSegments(pattern[1:], name)
		if err != nil || matched {
			return matched, err
		}
		if len(name) == 0 {
			return false, nil
		}
		return matchGlobSegments(pattern, name[1:])
	}
	if len(name) == 0 {
		return false, nil
	}
	matched, err := path.Match(pattern[0], name[0])
	if err != nil || !matched {
		return false, err
	}
	return matchGlobSegments(pattern[1:], name[1:])
}
