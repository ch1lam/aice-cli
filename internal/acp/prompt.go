package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Prompt runs one interaction. Limits, cancellation and the last assistant
// stop reason become ACP stop reasons; other run failures are errors.
func (a *agent) Prompt(ctx context.Context, params sdk.PromptRequest) (sdk.PromptResponse, error) {
	input, err := runInput(params.Prompt)
	if err != nil {
		return sdk.PromptResponse{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := a.beginPrompt(params.SessionId, cancel); err != nil {
		return sdk.PromptResponse{}, err
	}
	defer a.endPrompt()

	before := a.frontend.State.RuntimeState().Usage
	turn := &promptTurn{agent: a, session: params.SessionId, cost: before.TotalCost}
	run, err := a.frontend.Runner.NewRun(runCtx, input, turn.forward)
	if err == nil {
		err = run.Run(runCtx)
	}
	usage := turnUsage(before, a.frontend.State.RuntimeState().Usage)
	switch {
	case runCtx.Err() != nil:
		return sdk.PromptResponse{StopReason: sdk.StopReasonCancelled, Usage: usage}, nil
	case turn.limited:
		return sdk.PromptResponse{StopReason: sdk.StopReasonMaxTurnRequests, Usage: usage}, nil
	case err != nil:
		return sdk.PromptResponse{}, sdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	return sdk.PromptResponse{StopReason: stopReason(turn.stop), Usage: usage}, nil
}

func (a *agent) beginPrompt(id sdk.SessionId, cancel context.CancelFunc) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionID == "" || id != a.sessionID {
		return sdk.NewInvalidParams(map[string]any{"error": "session " + string(id) + " is not the current session"})
	}
	if a.cancel != nil {
		return sdk.NewInvalidRequest(map[string]any{"error": "a prompt is already running"})
	}
	a.cancel = cancel
	return nil
}

func (a *agent) endPrompt() {
	a.mu.Lock()
	a.cancel = nil
	a.mu.Unlock()
}

func stopReason(reason llm.StopReason) sdk.StopReason {
	switch reason {
	case llm.StopReasonLength:
		return sdk.StopReasonMaxTokens
	case llm.StopReasonRefusal:
		return sdk.StopReasonRefusal
	default:
		return sdk.StopReasonEndTurn
	}
}

// turnUsage reports the prompt's own usage in ACP's inclusive convention:
// AICE input excludes cache reads and writes, ACP input includes them.
func turnUsage(before, after interaction.DisplayUsage) *sdk.Usage {
	input := int(after.InputTokens - before.InputTokens)
	cacheRead := int(after.CacheReadTokens - before.CacheReadTokens)
	cacheWrite := int(after.CacheWriteTokens - before.CacheWriteTokens)
	output := int(after.OutputTokens - before.OutputTokens)
	input += cacheRead + cacheWrite
	return &sdk.Usage{
		InputTokens:       input,
		OutputTokens:      output,
		CachedReadTokens:  &cacheRead,
		CachedWriteTokens: &cacheWrite,
		TotalTokens:       input + output,
	}
}

// runInput accepts the baseline content every agent must support (text and
// resource links) plus images. File links become explicit file references.
func runInput(blocks []sdk.ContentBlock) (interaction.RunInput, error) {
	var input interaction.RunInput
	var text strings.Builder
	for _, block := range blocks {
		switch {
		case block.Text != nil:
			text.WriteString(block.Text.Text)
		case block.Image != nil:
			data, err := base64.StdEncoding.DecodeString(block.Image.Data)
			if err != nil {
				return interaction.RunInput{}, sdk.NewInvalidParams(map[string]any{"error": "image data is not base64: " + err.Error()})
			}
			input.Images = append(input.Images, llm.ImageContent{Data: data, MIMEType: block.Image.MimeType})
		case block.ResourceLink != nil:
			link := block.ResourceLink
			if parsed, err := url.Parse(link.Uri); err == nil && parsed.Scheme == "file" {
				input.Files = append(input.Files, parsed.Path)
				continue
			}
			text.WriteString(link.Uri)
		default:
			return interaction.RunInput{}, sdk.NewInvalidParams(map[string]any{"error": "unsupported prompt content; send text, images or resource links"})
		}
	}
	input.Prompt = text.String()
	return input, nil
}

// promptTurn forwards one run's events. It records the final stop reason and
// whether a run limit ended the run, and accumulates cost for usage updates.
type promptTurn struct {
	agent   *agent
	session sdk.SessionId
	cost    float64
	stop    llm.StopReason
	limited bool
}

func (t *promptTurn) forward(ctx context.Context, event interaction.Event) error {
	var update sdk.SessionUpdate
	switch event.Kind {
	case interaction.EventAssistantDelta:
		switch event.Delta.Kind {
		case interaction.DeltaText:
			update = sdk.UpdateAgentMessageText(event.Delta.Delta)
		case interaction.DeltaThinking:
			update = sdk.UpdateAgentThoughtText(event.Delta.Delta)
		default:
			return nil
		}
	case interaction.EventAssistantEnd:
		t.stop = event.Assistant.StopReason
		if cost := event.Assistant.Usage.Cost; cost != nil {
			t.cost += cost.Total
		}
		if event.Context == nil || !event.Context.Known || event.Context.Window <= 0 {
			return nil
		}
		update = sdk.SessionUpdate{UsageUpdate: &sdk.SessionUsageUpdate{
			Used: int(event.Context.Tokens),
			Size: int(event.Context.Window),
			Cost: &sdk.Cost{Amount: t.cost, Currency: "USD"},
		}}
	case interaction.EventToolStart:
		update = toolStart(event.Tool)
	case interaction.EventToolEnd:
		update = toolEnd(event.Tool)
	case interaction.EventAgentEnd:
		t.limited = event.LimitReached
		return nil
	default:
		return nil
	}
	return t.agent.connection().SessionUpdate(ctx, sdk.SessionNotification{SessionId: t.session, Update: update})
}

func toolStart(tool interaction.ToolDisplay) sdk.SessionUpdate {
	return sdk.StartToolCall(sdk.ToolCallId(tool.ID), toolTitle(tool),
		sdk.WithStartKind(toolKind(tool.Kind)),
		sdk.WithStartStatus(sdk.ToolCallStatusInProgress),
		sdk.WithStartLocations(locations(tool.Locations)),
		sdk.WithStartRawInput(rawInput(tool.Arguments)))
}

// toolEnd reports the outcome. AICE results retain a unified diff rather
// than complete old and new file text, so edits are reported as diff text.
func toolEnd(tool interaction.ToolDisplay) sdk.SessionUpdate {
	status := sdk.ToolCallStatusCompleted
	if tool.Failed {
		status = sdk.ToolCallStatusFailed
	}
	var content []sdk.ToolCallContent
	if tool.Diff.Text != "" {
		content = append(content, sdk.ToolContent(sdk.TextBlock("```diff\n"+tool.Diff.Text+"\n```")))
	}
	opts := []sdk.ToolCallUpdateOpt{
		sdk.WithUpdateStatus(status),
		sdk.WithUpdateKind(toolKind(tool.Kind)),
		sdk.WithUpdateLocations(locations(tool.Locations)),
	}
	if tool.Output.Available {
		content = append(content, sdk.ToolContent(sdk.TextBlock(tool.Output.Text)))
		opts = append(opts, sdk.WithUpdateRawOutput(map[string]any{"output": tool.Output.Text}))
	}
	if content != nil {
		opts = append(opts, sdk.WithUpdateContent(content))
	}
	return sdk.UpdateToolCall(sdk.ToolCallId(tool.ID), opts...)
}

func toolKind(kind interaction.ToolKind) sdk.ToolKind {
	if kind == "" {
		return sdk.ToolKindOther
	}
	return sdk.ToolKind(kind)
}

func locations(paths []interaction.ToolLocation) []sdk.ToolCallLocation {
	if len(paths) == 0 {
		return nil
	}
	result := make([]sdk.ToolCallLocation, len(paths))
	for i, location := range paths {
		result[i] = sdk.ToolCallLocation{Path: location.Path}
	}
	return result
}

// rawInput sends the call's actual arguments; malformed JSON is wrapped so
// the protocol message stays valid.
func rawInput(arguments []byte) any {
	if len(arguments) == 0 {
		return map[string]any{}
	}
	if !json.Valid(arguments) {
		return map[string]any{"value": string(arguments)}
	}
	return json.RawMessage(arguments)
}

func toolTitle(tool interaction.ToolDisplay) string {
	if tool.Detail == "" {
		return tool.Name
	}
	return tool.Name + " " + strings.SplitN(tool.Detail, "\n", 2)[0]
}
