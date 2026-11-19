package tool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// Find locates files by glob pattern among the files grep would search.
type Find struct {
	workspace *Workspace
	rgPath    string
}

// NewFind constructs a find tool.
func NewFind(workspace *Workspace) (*Find, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	rgPath, err := lookupRipgrep()
	if err != nil {
		return nil, err
	}
	return &Find{workspace: workspace, rgPath: rgPath}, nil
}

// Definition returns the model-facing find contract.
func (f *Find) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:          "find",
		Description:   "Find files, resolving relative paths from the working directory.",
		InputSchema:   jsonSchema(findSchema),
		PromptSnippet: "Find files by glob pattern (respects .gitignore)",
	}
}

// Execute lists the requested directory and matches the pattern in path order.
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
	// List the symlinked root's target but report paths under the requested
	// root; nested links are neither followed nor listed.
	walkRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": resolve %q: %w", args.Path, err)
	}

	collector := newTextCollector(maxOutputBytes)
	count := 0
	entriesScanned := 0
	err = f.listFiles(ctx, walkRoot, func(filePath string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entriesScanned++
		if entriesScanned > maxWalkEntries {
			return errScanLimit
		}
		matchPath, relativeErr := filepath.Rel(walkRoot, filePath)
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
		resultPath := filepath.Join(rootPath, matchPath)
		if !filepath.IsAbs(args.Path) {
			resultPath, relativeErr = filepath.Rel(f.workspace.physicalPath, resultPath)
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
		return llm.ToolResult{}, fmt.Errorf("tool \"find\": list %q: %w", args.Path, err)
	}
	if err == errResultLimit && !collector.truncated {
		collector.WriteString(fmt.Sprintf("[result limit reached: %d]", args.Limit))
	}
	if err == errScanLimit && !collector.truncated {
		collector.WriteString(fmt.Sprintf("[scan limit reached: %d entries]", maxWalkEntries))
	}
	return textResult(call, strings.TrimSuffix(collector.String(), "\n"), false), nil
}

// listFiles streams the files ripgrep would search under root with grep's
// ignore rules: hidden files included, .gitignore/.ignore applied, nested
// symlinks neither followed nor listed. .git directories are skipped too.
func (f *Find) listFiles(ctx context.Context, root string, visit func(string) error) error {
	command := exec.CommandContext(ctx, f.rgPath,
		"--files", "--hidden", "--null", "--sort", "path", "--glob", "!.git", "--", root)
	command.Dir = f.workspace.Path()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open ripgrep output: %w", err)
	}
	stderr := newBoundedWriter(maxOutputBytes)
	command.Stderr = stderr
	configureProcess(command)
	cleanup, err := startProcessTree(command)
	if err != nil {
		return fmt.Errorf("start ripgrep: %w", err)
	}
	defer cleanup()

	reader := bufio.NewReader(stdout)
	for {
		filePath, readErr := reader.ReadString(0)
		if readErr != nil {
			break
		}
		if err := visit(strings.TrimSuffix(filePath, "\x00")); err != nil {
			// As with grep's match limit, the early stop is the result.
			_ = cancelRipgrep(command)
			_ = command.Wait()
			return err
		}
	}
	waitErr := command.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	var exitError *exec.ExitError
	if waitErr == nil || errors.As(waitErr, &exitError) && exitError.ExitCode() == 1 {
		return nil
	}
	if text := strings.TrimSpace(stderr.String()); text != "" {
		return fmt.Errorf("ripgrep failed: %s: %w", text, waitErr)
	}
	return fmt.Errorf("ripgrep failed: %w", waitErr)
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
