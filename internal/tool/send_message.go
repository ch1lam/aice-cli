package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// MessageRequest is one message to a background subagent.
type MessageRequest struct {
	// To is the subagent's task ID.
	To      string
	Message string
	// Interrupt stops a running subagent's current step before it reads the
	// message.
	Interrupt bool
}

// MessageResult is how a message reached its subagent. Text tells the parent
// what happens next; Details carry delegations the result reports first.
type MessageResult struct {
	Text    string
	Details *llm.ToolResultDetails
}

// Messenger delivers messages to background subagents.
type Messenger interface {
	Send(ctx context.Context, request MessageRequest) (MessageResult, error)
}

// SendMessage steers a running background subagent or continues a finished
// one with its whole conversation.
type SendMessage struct {
	messenger Messenger
}

// NewSendMessage constructs the send_message tool.
func NewSendMessage(messenger Messenger) (*SendMessage, error) {
	if messenger == nil {
		return nil, fmt.Errorf("tool %q: messenger is required", "send_message")
	}
	return &SendMessage{messenger: messenger}, nil
}

// Definition returns the model-facing messaging contract.
func (*SendMessage) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "send_message",
		Description: "Send a message to a background subagent you started, by its task id. " +
			"A running subagent reads it at its next step; interrupt stops its current step first. " +
			"A finished subagent continues in the background from where it stopped, with its whole conversation and the same type and workspace, and its new report arrives as a notification. " +
			"A subagent stopped with task_kill cannot continue.",
		InputSchema: jsonSchema(`{
			"type": "object",
			"properties": {
				"to": {"type": "string", "description": "The subagent's task id"},
				"message": {"type": "string", "description": "What to do next or what to change; the subagent sees only this and its own conversation"},
				"interrupt": {"type": "boolean", "description": "Stop a running subagent's current step before it reads the message (default: false)"}
			},
			"required": ["to", "message"],
			"additionalProperties": false
		}`),
		PromptSnippet: "Steer a running background subagent or continue a finished one",
		PromptGuidelines: []string{
			"To follow up on a subagent's report, correct it or extend its task, send_message to it instead of starting a new subagent: it keeps everything it read and did",
		},
	}
}

// Execute delivers the message.
func (s *SendMessage) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		To        string `json:"to"`
		Message   string `json:"message"`
		Interrupt bool   `json:"interrupt,omitempty"`
	}
	args, err := decodeArguments[arguments](ctx, call, "send_message")
	if err != nil {
		return llm.ToolResult{}, err
	}
	args.To, args.Message = strings.TrimSpace(args.To), strings.TrimSpace(args.Message)
	if args.To == "" || args.Message == "" {
		return llm.ToolResult{}, fmt.Errorf("tool %q: to and message are required", "send_message")
	}
	sent, err := s.messenger.Send(ctx, MessageRequest{To: args.To, Message: args.Message, Interrupt: args.Interrupt})
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool %q: %w", "send_message", err)
	}
	result := textResult(call, sent.Text, false)
	result.Details = sent.Details
	return result, nil
}
