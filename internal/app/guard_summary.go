package app

import (
	"context"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// guardSummaryTimeout bounds the explainer side call; a later answer is
// omitted, as is any failure.
const guardSummaryTimeout = 8 * time.Second

// explainFunc describes one pending call; tests replace the model-backed one.
type explainFunc func(context.Context, classify.ExplainRequest) (classify.Explanation, error)

// guardExplainer is a Session's prompt summarizer and the bound on each side
// call. Its zero value summarizes nothing.
type guardExplainer struct {
	explain explainFunc
	timeout time.Duration
}

// explainGuardAsk runs the explainer on the small model of the Session's
// current settings, so a model switch or login applies to the next prompt.
func (s *interactiveSession) explainGuardAsk(ctx context.Context, req classify.ExplainRequest) (classify.Explanation, error) {
	settings := s.settingsSnapshot()
	configured, err := s.application.smallModel(configuredModel{
		configuration: settings.configuration,
		model:         settings.model,
		options:       settings.options,
		modelErr:      settings.modelErr,
	})
	if err != nil {
		return classify.Explanation{}, err
	}
	explainer, err := classify.NewExplainer(classifierModel{configured: configured})
	if err != nil {
		return classify.Explanation{}, err
	}
	return explainer.Explain(ctx, req)
}

// summarizeGuardAsk returns the prompt's Summarize hook, or nil when the
// Session has no explainer. The side call starts when a frontend first
// shows the prompt; ctx ends when the prompt is answered or cancelled, which
// cancels it. The goroutine owns the channel: it sends at most once into its
// buffer and closes it on every path.
func (s *interactiveSession) summarizeGuardAsk(ctx context.Context, call llm.ToolCall, approval agent.GuardApproval) func() <-chan interaction.GuardSummary {
	explainer := s.explainer
	if explainer.explain == nil {
		return nil
	}
	return sync.OnceValue(func() <-chan interaction.GuardSummary {
		return explainer.start(ctx, call, approval)
	})
}

func (explainer guardExplainer) start(ctx context.Context, call llm.ToolCall, approval agent.GuardApproval) <-chan interaction.GuardSummary {
	summaries := make(chan interaction.GuardSummary, 1)
	go func() {
		defer close(summaries)
		ctx, cancel := context.WithTimeout(ctx, explainer.timeout)
		defer cancel()
		if ctx.Err() != nil {
			return
		}
		entries, err := transcriptFrom(ctx)
		if err != nil {
			return
		}
		got, err := explainer.explain(ctx, explainRequest(entries, call, approval))
		if err != nil || ctx.Err() != nil {
			return
		}
		summaries <- interaction.GuardSummary{Intent: got.Intent, Action: got.Action, Risk: got.Risk}
	}()
	return summaries
}

// explainRequest is the explainer's view, derived as auto mode derives the
// classifier's: the text the user typed on the active branch and the pending
// call with the effects the gate resolved. Earlier calls, tool output,
// assistant text and reasoning are left out.
func explainRequest(entries []session.MessageEntry, call llm.ToolCall, approval agent.GuardApproval) classify.ExplainRequest {
	return classify.ExplainRequest{
		Intent:     transcriptRequest(entries, call).Intent,
		Action:     classifierAction(call, agent.GuardResult{Approvals: []agent.GuardApproval{approval}}),
		GateReason: approval.Reason,
	}
}
