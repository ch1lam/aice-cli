package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	defaultGrepLimit  = 100
	maxGrepLineChars  = 500
	grepNoticeReserve = 512
	grepSchema        = `{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Search pattern (regex or literal string)"},
    "path": {"type": "string", "description": "Directory or file to search (default: current directory)"},
    "glob": {"type": "string", "description": "Filter files by glob pattern, e.g. '*.go' or '**/*_test.go'"},
    "ignoreCase": {"type": "boolean", "description": "Case-insensitive search (default: false)"},
    "literal": {"type": "boolean", "description": "Treat pattern as a literal string instead of regex (default: false)"},
    "context": {"type": "integer", "description": "Number of lines to show before and after each match (default: 0)"},
    "limit": {"type": "integer", "description": "Maximum number of matches to return (default: 100)"}
  },
  "required": ["pattern"],
  "additionalProperties": false
}`
)

type grepArguments struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	IgnoreCase bool   `json:"ignoreCase"`
	Literal    bool   `json:"literal"`
	Context    int    `json:"context"`
	Limit      *int   `json:"limit"`
}

type ripgrepText struct {
	Text string `json:"text"`
}

type ripgrepEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       ripgrepText `json:"path"`
		Lines      ripgrepText `json:"lines"`
		LineNumber int         `json:"line_number"`
	} `json:"data"`
}

type grepMatch struct {
	filePath   string
	lineNumber int
	lineText   string
}

type grepSearchResult struct {
	matches      []grepMatch
	matchCount   int
	limitReached bool
}

// Grep searches text files with ripgrep.
type Grep struct {
	workspace *Workspace
	rgPath    string
}

// NewGrep constructs a grep tool.
func NewGrep(workspace *Workspace) (*Grep, error) {
	if workspace == nil || workspace.path == "" {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("tool: find ripgrep (rg) executable: %w", err)
	}
	if !filepath.IsAbs(rgPath) {
		return nil, fmt.Errorf("tool: ripgrep (rg) executable path must be absolute")
	}
	return &Grep{workspace: workspace, rgPath: rgPath}, nil
}

// Definition returns the model-facing grep contract.
func (g *Grep) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "grep",
		Description: "Search file contents for a pattern. Returns matching lines with file paths and line numbers. " +
			"Respects .gitignore. Output is truncated to 100 matches or 50KB (whichever is hit first). " +
			"Long lines are truncated to 500 chars.",
		InputSchema: jsonSchema(grepSchema),
	}
}

// Execute searches with the host ripgrep executable.
func (g *Grep) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	args, err := decodeArguments[grepArguments](ctx, call, "grep")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Context < 0 {
		args.Context = 0
	}
	limit := defaultGrepLimit
	if args.Limit != nil {
		limit = max(1, *args.Limit)
	}

	searchPath, err := g.workspace.resolvePath(args.Path)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": %w", err)
	}
	searchPath = filepath.Clean(searchPath)
	info, err := os.Stat(searchPath)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": stat %q: %w", args.Path, err)
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return llm.ToolResult{}, fmt.Errorf("tool \"grep\": %q is not a file or directory", args.Path)
	}

	searchResult, err := g.runRipgrep(
		ctx,
		g.rgPath,
		searchPath,
		args,
		limit,
	)
	if err != nil {
		return llm.ToolResult{}, err
	}
	if searchResult.matchCount == 0 {
		return textResult(call, "No matches found", false), nil
	}

	output, outputTruncated, linesTruncated := formatGrepMatches(
		searchResult.matches,
		searchPath,
		info.IsDir(),
		args.Context,
	)
	notices := make([]string, 0, 3)
	if searchResult.limitReached {
		notices = append(
			notices,
			fmt.Sprintf(
				"%d matches limit reached. Use limit=%d for more, or refine pattern",
				limit,
				nextGrepLimit(limit),
			),
		)
	}
	if outputTruncated {
		notices = append(notices, "50KB limit reached")
	}
	if linesTruncated {
		notices = append(
			notices,
			fmt.Sprintf(
				"Some lines truncated to %d chars. Use read tool to see full lines",
				maxGrepLineChars,
			),
		)
	}
	if len(notices) > 0 {
		if output != "" {
			output += "\n\n"
		}
		output += "[" + strings.Join(notices, ". ") + "]"
	}
	return textResult(call, output, false), nil
}

func (g *Grep) runRipgrep(
	ctx context.Context,
	rgPath string,
	searchPath string,
	args grepArguments,
	limit int,
) (grepSearchResult, error) {
	commandArgs := []string{"--json", "--line-number", "--color=never", "--hidden"}
	if args.IgnoreCase {
		commandArgs = append(commandArgs, "--ignore-case")
	}
	if args.Literal {
		commandArgs = append(commandArgs, "--fixed-strings")
	}
	if args.Glob != "" {
		commandArgs = append(commandArgs, "--glob", args.Glob)
	}
	commandArgs = append(commandArgs, "--", args.Pattern, searchPath)

	command := exec.CommandContext(ctx, rgPath, commandArgs...)
	command.Dir = g.workspace.Path()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return grepSearchResult{}, fmt.Errorf("tool \"grep\": open ripgrep output: %w", err)
	}
	stderr := newBoundedWriter(maxOutputBytes)
	command.Stderr = stderr
	configureProcess(command)
	if err := command.Start(); err != nil {
		return grepSearchResult{}, fmt.Errorf("tool \"grep\": start ripgrep: %w", err)
	}

	result := grepSearchResult{
		matches: make([]grepMatch, 0, min(limit, defaultGrepLimit)),
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxReadBytes)
	var cancelErr error
	for scanner.Scan() {
		var event ripgrepEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Type != "match" {
			continue
		}
		result.matchCount++
		if event.Data.Path.Text != "" && event.Data.LineNumber > 0 {
			result.matches = append(result.matches, grepMatch{
				filePath:   event.Data.Path.Text,
				lineNumber: event.Data.LineNumber,
				lineText:   event.Data.Lines.Text,
			})
		}
		if result.matchCount >= limit {
			result.limitReached = true
			cancelErr = cancelRipgrep(command)
			break
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil && !result.limitReached {
		cancelErr = cancelRipgrep(command)
	}
	waitErr := command.Wait()

	if err := ctx.Err(); err != nil {
		return grepSearchResult{}, err
	}
	if scanErr != nil {
		return grepSearchResult{}, errors.Join(
			fmt.Errorf("tool \"grep\": read ripgrep output: %w", scanErr),
			cancelErr,
		)
	}
	if result.limitReached {
		return result, nil
	}
	if cancelErr != nil {
		return grepSearchResult{}, fmt.Errorf("tool \"grep\": stop ripgrep: %w", cancelErr)
	}
	if waitErr == nil {
		return result, nil
	}

	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) && exitError.ExitCode() == 1 {
		return result, nil
	}
	errorText := strings.TrimSpace(stderr.String())
	if errorText != "" {
		return grepSearchResult{}, fmt.Errorf(
			"tool \"grep\": ripgrep failed: %s: %w",
			errorText,
			waitErr,
		)
	}
	return grepSearchResult{}, fmt.Errorf("tool \"grep\": ripgrep failed: %w", waitErr)
}

func cancelRipgrep(command *exec.Cmd) error {
	if command.Cancel != nil {
		err := command.Cancel()
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	if command.Process == nil {
		return nil
	}
	err := command.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func formatGrepMatches(
	matches []grepMatch,
	searchPath string,
	searchingDirectory bool,
	contextLines int,
) (string, bool, bool) {
	collector := newGrepOutputCollector(maxOutputBytes - grepNoticeReserve)
	linesTruncated := false
	for _, match := range matches {
		displayPath := formatGrepPath(searchPath, match.filePath, searchingDirectory)
		if contextLines == 0 && match.lineText != "" {
			line := normalizeGrepLine(match.lineText)
			line, truncated := truncateGrepLine(line)
			linesTruncated = linesTruncated || truncated
			if !collector.WriteLine(
				fmt.Sprintf("%s:%d: %s", displayPath, match.lineNumber, line),
			) {
				break
			}
			continue
		}

		lines, err := readGrepLines(match.filePath)
		if err != nil || len(lines) == 0 {
			if !collector.WriteLine(
				fmt.Sprintf("%s:%d: (unable to read file)", displayPath, match.lineNumber),
			) {
				break
			}
			continue
		}
		start := max(1, match.lineNumber-contextLines)
		end := min(len(lines), match.lineNumber+contextLines)
		for lineNumber := start; lineNumber <= end; lineNumber++ {
			line := lines[lineNumber-1]
			line, truncated := truncateGrepLine(line)
			linesTruncated = linesTruncated || truncated
			separator := "-"
			if lineNumber == match.lineNumber {
				separator = ":"
			}
			if !collector.WriteLine(
				fmt.Sprintf(
					"%s%s%d%s %s",
					displayPath,
					separator,
					lineNumber,
					separator,
					line,
				),
			) {
				break
			}
		}
		if collector.truncated {
			break
		}
	}
	return collector.String(), collector.truncated, linesTruncated
}

type grepOutputCollector struct {
	builder   strings.Builder
	limit     int
	truncated bool
}

func newGrepOutputCollector(limit int) *grepOutputCollector {
	return &grepOutputCollector{limit: limit}
}

func (c *grepOutputCollector) WriteLine(line string) bool {
	if c.truncated {
		return false
	}
	requiredBytes := len(line)
	if c.builder.Len() > 0 {
		requiredBytes++
	}
	if requiredBytes > c.limit-c.builder.Len() {
		c.truncated = true
		return false
	}
	if c.builder.Len() > 0 {
		_ = c.builder.WriteByte('\n')
	}
	_, _ = c.builder.WriteString(line)
	return true
}

func (c *grepOutputCollector) String() string {
	return c.builder.String()
}

func formatGrepPath(searchPath, filePath string, searchingDirectory bool) string {
	if searchingDirectory {
		relativePath, err := filepath.Rel(searchPath, filePath)
		isInsideSearchPath := err == nil &&
			relativePath != "." &&
			relativePath != ".." &&
			!strings.HasPrefix(relativePath, ".."+string(os.PathSeparator))
		if isInsideSearchPath {
			return filepath.ToSlash(relativePath)
		}
	}
	return filepath.Base(filePath)
}

func normalizeGrepLine(line string) string {
	line = strings.ReplaceAll(line, "\r\n", "\n")
	line = strings.ReplaceAll(line, "\r", "")
	return strings.TrimSuffix(line, "\n")
}

func truncateGrepLine(line string) (string, bool) {
	characters := []rune(line)
	if len(characters) <= maxGrepLineChars {
		return line, false
	}
	return string(characters[:maxGrepLineChars]) + "... [truncated]", true
}

func readGrepLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxReadBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxReadBytes)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n"), nil
}

func nextGrepLimit(limit int) int {
	if limit > int(^uint(0)>>1)/2 {
		return limit
	}
	return limit * 2
}
