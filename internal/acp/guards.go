package acp

import (
	"context"
	"strings"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// serveGuards turns each Guard ask into session/request_permission. A Guard
// deny never reaches this path, so no client answer can allow it.
func (a *agent) serveGuards(ctx context.Context, requests <-chan interaction.GuardRequest) {
	for {
		select {
		case <-ctx.Done():
			return
		case request, ok := <-requests:
			if !ok {
				return
			}
			a.answerGuard(ctx, request)
		}
	}
}

// answerGuard replies with the client's selection, or with the Guard's deny
// option when the client cancels, fails or selects nothing.
func (a *agent) answerGuard(ctx context.Context, request interaction.GuardRequest) {
	reply := interaction.GuardReply{OptionID: denyOption(request.Options)}
	defer func() {
		select {
		case request.Reply <- reply:
		case <-request.Done:
		}
	}()
	a.mu.Lock()
	session := a.sessionID
	a.mu.Unlock()
	options := make([]sdk.PermissionOption, 0, len(request.Options))
	for _, option := range request.Options {
		options = append(options, sdk.PermissionOption{
			OptionId: sdk.PermissionOptionId(option.ID),
			Name:     option.Label,
			Kind:     permissionKind(option),
		})
	}
	title := request.Title
	if title == "" {
		title = strings.TrimSpace(request.ToolName + " " + request.Command + request.Path)
	}
	promptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-request.Done:
			cancel()
		case <-promptCtx.Done():
		}
	}()
	if request.Summarize != nil {
		summaries := request.Summarize()
		// Join the summary before replying, so it can never overwrite the
		// tool's final content.
		summarized := make(chan struct{})
		go func() {
			defer close(summarized)
			a.showGuardSummary(promptCtx, session, request.ID, summaries)
		}()
		defer func() {
			cancel()
			<-summarized
		}()
	}
	response, err := a.connection().RequestPermission(promptCtx, sdk.RequestPermissionRequest{
		SessionId: session,
		ToolCall:  sdk.ToolCallUpdate{ToolCallId: sdk.ToolCallId(request.ID), Title: &title},
		Options:   options,
	})
	if err != nil || response.Outcome.Selected == nil {
		return
	}
	reply.OptionID = string(response.Outcome.Selected.OptionId)
}

// showGuardSummary adds the advisory summary to the pending tool call's
// content while the permission request is open. A request cannot change
// once sent, so the summary travels as a tool_call_update.
func (a *agent) showGuardSummary(ctx context.Context, session sdk.SessionId, id string, summaries <-chan interaction.GuardSummary) {
	select {
	case summary, ok := <-summaries:
		text := guardSummaryText(summary)
		if !ok || text == "" || ctx.Err() != nil {
			return
		}
		update := sdk.UpdateToolCall(sdk.ToolCallId(id), sdk.WithUpdateContent([]sdk.ToolCallContent{sdk.ToolContent(sdk.TextBlock(text))}))
		_ = a.connection().SessionUpdate(ctx, sdk.SessionNotification{SessionId: session, Update: update})
	case <-ctx.Done():
	}
}

func guardSummaryText(summary interaction.GuardSummary) string {
	var lines []string
	for _, row := range []struct{ label, text string }{
		{"Why", summary.Intent}, {"Does", summary.Action}, {"Risk", summary.Risk},
	} {
		if text := strings.TrimSpace(row.text); text != "" {
			lines = append(lines, row.label+": "+text)
		}
	}
	return strings.Join(lines, "\n")
}

func permissionKind(option interaction.GuardOption) sdk.PermissionOptionKind {
	switch {
	case option.Deny:
		return sdk.PermissionOptionKindRejectOnce
	case option.ID == "allow-once":
		return sdk.PermissionOptionKindAllowOnce
	default:
		return sdk.PermissionOptionKindAllowAlways
	}
}

func denyOption(options []interaction.GuardOption) string {
	for _, option := range options {
		if option.Deny {
			return option.ID
		}
	}
	return ""
}
