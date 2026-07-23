package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	defaultBashTimeout = 120 * time.Second
	maxBashTimeout     = 10 * time.Minute
	bashSchema         = `{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "Bash command to execute"},
    "timeout": {"type": "number", "exclusiveMinimum": 0, "maximum": 600, "description": "Timeout in seconds (default: 120, maximum: 600)"}
  },
  "required": ["command"],
  "additionalProperties": false
}`
)

// Bash executes a shell command in the workspace.
type Bash struct {
	workspace *Workspace
	shellPath string
}

// NewBash constructs a bash tool.
func NewBash(workspace *Workspace) (*Bash, error) {
	if workspace == nil || workspace.root == nil {
		return nil, fmt.Errorf("tool: workspace is required")
	}
	if !supportsProcessTreeTermination() {
		return nil, fmt.Errorf("tool: bash is unsupported because process-tree termination is unavailable")
	}
	shellPath, err := exec.LookPath("bash")
	if err != nil {
		return nil, fmt.Errorf("tool: find bash executable: %w", err)
	}
	if !filepath.IsAbs(shellPath) {
		return nil, fmt.Errorf("tool: bash executable path must be absolute")
	}
	return &Bash{workspace: workspace, shellPath: shellPath}, nil
}

// Definition returns the model-facing bash contract.
func (b *Bash) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "bash",
		Description: "Execute a Bash command in the workspace with bounded time and output.",
		InputSchema: jsonSchema(bashSchema),
	}
}

// Execute runs one command with a sanitized environment.
func (b *Bash) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Command string  `json:"command"`
		Timeout float64 `json:"timeout"`
	}
	args, err := decodeArguments[arguments](ctx, call, "bash")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if strings.TrimSpace(args.Command) == "" {
		return llm.ToolResult{}, fmt.Errorf("tool \"bash\": command is required")
	}
	timeout := defaultBashTimeout
	if args.Timeout < 0 {
		return llm.ToolResult{}, fmt.Errorf("tool \"bash\": timeout must be positive")
	}
	if args.Timeout > 0 {
		timeout = time.Duration(args.Timeout * float64(time.Second))
	}
	if timeout <= 0 || timeout > maxBashTimeout {
		return llm.ToolResult{}, fmt.Errorf("tool \"bash\": timeout must not exceed 600 seconds")
	}

	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Raw shell text intentionally reaches bash at this explicit tool boundary.
	// The command has bounded output and lifetime and runs in its own process group.
	command := exec.CommandContext(commandCtx, b.shellPath, "--noprofile", "--norc", "-c", args.Command)
	command.Dir = b.workspace.Path()
	command.Env = commandEnvironment(b.workspace.Path())
	output := newBoundedWriter(maxOutputBytes - 256)
	command.Stdout = output
	command.Stderr = output
	configureProcess(command)

	runErr := command.Run()
	text := output.String()
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		text = appendStatus(text, fmt.Sprintf("command timed out after %s", timeout))
		return textResult(call, text, true), nil
	}
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}
	if runErr == nil {
		return textResult(call, appendStatus(text, "exit code: 0"), false), nil
	}
	var exitError *exec.ExitError
	if errors.As(runErr, &exitError) {
		return textResult(
			call,
			appendStatus(text, fmt.Sprintf("exit code: %d", exitError.ExitCode())),
			true,
		), nil
	}
	return llm.ToolResult{}, fmt.Errorf("tool \"bash\": execute command: %w", runErr)
}

func commandEnvironment(workspacePath string) []string {
	environment := []string{
		"HOME=" + workspacePath,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=dumb",
	}
	if path := os.Getenv("PATH"); path != "" {
		environment = append(environment, "PATH="+path)
	}
	if temporaryDirectory := os.Getenv("TMPDIR"); temporaryDirectory != "" {
		environment = append(environment, "TMPDIR="+temporaryDirectory)
	}
	return environment
}

func appendStatus(output, status string) string {
	if output == "" {
		return "[" + status + "]"
	}
	return strings.TrimRight(output, "\n") + "\n[" + status + "]"
}

type boundedWriter struct {
	mu        sync.Mutex
	collector *textCollector
}

func newBoundedWriter(limit int) *boundedWriter {
	return &boundedWriter{collector: newTextCollector(limit)}
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.collector.WriteString(string(data))
	return len(data), nil
}

func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.collector.String()
}

var _ io.Writer = (*boundedWriter)(nil)
