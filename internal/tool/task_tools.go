package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	maxTaskWait      = 10 * time.Minute
	maxTaskOutputIDs = 8
	taskOutputSchema = `{
  "type": "object",
  "properties": {
    "task_ids": {"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 8, "description": "Background task ids returned by bash or agent"},
    "wait": {"type": "number", "minimum": 0, "maximum": 600, "description": "Seconds to wait for every listed task to finish before returning (default: 0, maximum: 600)"}
  },
  "required": ["task_ids"],
  "additionalProperties": false
}`
	taskKillSchema = `{
  "type": "object",
  "properties": {
    "task_id": {"type": "string", "description": "Background task id returned by bash or agent"}
  },
  "required": ["task_id"],
  "additionalProperties": false
}`
	// taskOutputPreviewBytes keeps eight previews within maxOutputBytes.
	taskOutputPreviewBytes = 4 << 10
)

// TaskOutput reports background task status and recent output.
type TaskOutput struct{ tasks *Tasks }

// TaskKill terminates one background task.
type TaskKill struct{ tasks *Tasks }

// NewTaskOutput constructs the task_output tool.
func NewTaskOutput(tasks *Tasks) (*TaskOutput, error) {
	if tasks == nil {
		return nil, fmt.Errorf("tool: background tasks are required")
	}
	return &TaskOutput{tasks: tasks}, nil
}

// NewTaskKill constructs the task_kill tool.
func NewTaskKill(tasks *Tasks) (*TaskKill, error) {
	if tasks == nil {
		return nil, fmt.Errorf("tool: background tasks are required")
	}
	return &TaskKill{tasks: tasks}, nil
}

// Definition returns the model-facing task_output contract.
func (*TaskOutput) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:          "task_output",
		Description:   "Report the status and recent output of background tasks, optionally waiting for them to finish. The full output stays in each task's output file; a subagent's file holds its report. A subagent's report arrives on its own as a notification: to wait only for subagents, end your turn instead of waiting here.",
		InputSchema:   jsonSchema(taskOutputSchema),
		PromptSnippet: "Check or wait for background tasks started by bash or agent",
	}
}

// Execute waits as requested and renders each task's status.
func (o *TaskOutput) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		TaskIDs []string `json:"task_ids"`
		Wait    float64  `json:"wait"`
	}
	args, err := decodeArguments[arguments](ctx, call, "task_output")
	if err != nil {
		return llm.ToolResult{}, err
	}
	if len(args.TaskIDs) == 0 || len(args.TaskIDs) > maxTaskOutputIDs {
		return llm.ToolResult{}, fmt.Errorf("tool \"task_output\": task_ids must list 1 to %d tasks", maxTaskOutputIDs)
	}
	wait := time.Duration(args.Wait * float64(time.Second))
	if args.Wait < 0 || wait > maxTaskWait {
		return llm.ToolResult{}, fmt.Errorf("tool \"task_output\": wait must be between 0 and 600 seconds")
	}
	statuses, err := o.tasks.Wait(ctx, args.TaskIDs, wait)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"task_output\": %w", err)
	}
	reports := make([]string, len(statuses))
	for index, status := range statuses {
		reports[index] = taskReport(status, taskOutputPreviewBytes)
	}
	return withTaskDelegations(textResult(call, strings.Join(reports, "\n\n"), false), statuses...), nil
}

// withTaskDelegations records the subagent tasks a result reports first, so
// their usage is counted exactly once.
func withTaskDelegations(result llm.ToolResult, statuses ...TaskStatus) llm.ToolResult {
	for _, status := range statuses {
		if status.FirstReport && status.Delegation != nil {
			if result.Details == nil {
				result.Details = &llm.ToolResultDetails{State: llm.ExecutionReturned}
			}
			result.Details.Delegations = append(result.Details.Delegations, *status.Delegation)
		}
	}
	return result
}

// Definition returns the model-facing task_kill contract.
func (*TaskKill) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:          "task_kill",
		Description:   "Stop a background task: a command and its child processes are interrupted, then killed after a short grace period; a subagent is cancelled. Stopping a finished task is not an error.",
		InputSchema:   jsonSchema(taskKillSchema),
		PromptSnippet: "Stop a background task started by bash or agent",
	}
}

// Execute terminates the task and reports how it ended.
func (k *TaskKill) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		TaskID string `json:"task_id"`
	}
	args, err := decodeArguments[arguments](ctx, call, "task_kill")
	if err != nil {
		return llm.ToolResult{}, err
	}
	status, err := k.tasks.Kill(ctx, args.TaskID)
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool \"task_kill\": %w", err)
	}
	return withTaskDelegations(textResult(call, taskReport(status, taskOutputPreviewBytes), false), status), nil
}

// noticeHeader opens a task notification so the model never takes it for
// the user's words.
const noticeHeader = "[AICE notification, not from the user] Background tasks finished; no need to poll them."

// agentNoticeBytes shows a subagent's whole report, already bounded by
// MaxAgentReportBytes, with the lines that follow it.
const agentNoticeBytes = MaxAgentReportBytes + 8<<10

// TakeNotice reports the background tasks that finished since the last report
// in one notification, marks them reported and returns its input ID. It
// carries finished subagents' delegation records; ok is false when no task
// finished.
func (t *Tasks) TakeNotice() (message llm.UserMessage, id string, ok bool) {
	completions := t.TakeCompletions()
	if len(completions) == 0 {
		return llm.UserMessage{}, "", false
	}
	reports := []string{noticeHeader}
	summaries := make([]string, 0, len(completions))
	notice := &llm.Notice{}
	for _, status := range completions {
		preview := int64(taskPreviewBytes)
		if status.Kind == TaskAgent {
			preview = agentNoticeBytes
		}
		reports = append(reports, taskReport(status, preview))
		summaries = append(summaries, taskSummary(status))
		if status.Delegation != nil && len(notice.Delegations) < llm.MaxToolDelegations {
			notice.Delegations = append(notice.Delegations, *status.Delegation)
		}
	}
	notice.Summary = strings.Join(summaries, "\n")
	return llm.UserMessage{
		Role:      llm.RoleUser,
		Content:   []llm.ContentPart{llm.NewTextContent(strings.Join(reports, "\n\n")).Part()},
		Timestamp: time.Now().UnixMilli(),
		Notice:    notice,
	}, "notice-" + completions[0].ID, true
}

// taskSummary names a task and its state in one line.
func taskSummary(status TaskStatus) string {
	if status.Kind == TaskAgent {
		return fmt.Sprintf("Subagent %s %s: %s", status.ID, taskState(status), firstLine(status.Description))
	}
	return fmt.Sprintf("Task %s %s: %s", status.ID, taskState(status), firstLine(taskLabel(status)))
}

func taskState(status TaskStatus) string {
	switch {
	case status.Running:
		return "running"
	case status.Reason != "":
		return "terminated: " + status.Reason
	case status.Kind != TaskAgent:
		return fmt.Sprintf("exited with code %d", status.ExitCode)
	case status.Failure != "":
		return "stopped before finishing: " + firstLine(status.Failure)
	default:
		return "finished"
	}
}

func taskLabel(status TaskStatus) string {
	if status.Description != "" {
		return status.Description
	}
	return status.Command
}

// taskReport renders a status line and a preview of the task's output: the
// tail of a command's output, or the beginning of a subagent's report.
func taskReport(status TaskStatus, previewBytes int64) string {
	if status.Kind == TaskAgent {
		return agentTaskReport(status, previewBytes)
	}
	report := fmt.Sprintf("[task %s %s; output file: %s]\nCommand: %s",
		status.ID, taskState(status), status.OutputPath, firstLine(taskLabel(status)))
	tail, truncated, err := outputTail(status.OutputPath, previewBytes)
	switch {
	case err != nil:
		return report + "\n(output unavailable: " + err.Error() + ")"
	case strings.TrimSpace(tail) == "":
		return report + "\n(no output)"
	case truncated:
		return report + "\nLast output (earlier output is in the file):\n" + strings.TrimRight(tail, "\n")
	default:
		return report + "\nOutput:\n" + strings.TrimRight(tail, "\n")
	}
}

func agentTaskReport(status TaskStatus, previewBytes int64) string {
	report := fmt.Sprintf("[subagent task %s %s; report file: %s]\nTask: %s", status.ID, taskState(status), status.OutputPath, firstLine(status.Description))
	if status.Running {
		return report
	}
	head, truncated, err := outputHead(status.OutputPath, previewBytes)
	switch {
	case err != nil:
		return report + "\n(report unavailable: " + err.Error() + ")"
	case strings.TrimSpace(head) == "":
		return report + "\n(no report)"
	case truncated:
		return report + "\nReport (continues in the file):\n" + strings.TrimRight(head, "\n")
	default:
		return report + "\nReport:\n" + strings.TrimRight(head, "\n")
	}
}

func firstLine(text string) string {
	line, _, more := strings.Cut(strings.TrimSpace(text), "\n")
	if more {
		return line + " …"
	}
	return line
}
