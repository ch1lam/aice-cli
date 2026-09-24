package app

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// questionBridgeCapacity bounds queued prompts, including expired prompts
// the frontend has not consumed yet. Sequential tool execution still allows
// only one live prompt; sends to a full queue remain cancellable.
const questionBridgeCapacity = 4

// QuestionRequests exposes pending question prompts for the TUI.
func (s *interactiveSession) QuestionRequests() <-chan interaction.QuestionPrompt {
	if s == nil {
		return nil
	}
	return s.questionRequests
}

// AskQuestion implements interaction.QuestionAsker. It publishes one prompt
// for the active frontend and waits for a single validated submission. The
// send and the wait both honor the run context, and no Session or Guard lock
// is held while waiting.
func (s *interactiveSession) AskQuestion(
	ctx context.Context,
	request interaction.QuestionRequest,
) (interaction.QuestionReply, error) {
	if s == nil || s.questionRequests == nil {
		return interaction.QuestionReply{}, fmt.Errorf(
			"app: question frontend is not available; " +
				"state the missing information and the affected scope in your output",
		)
	}
	if err := interaction.ValidateQuestionRequest(request); err != nil {
		return interaction.QuestionReply{}, err
	}
	promptCtx, closePrompt := context.WithCancel(ctx)
	defer closePrompt()
	reply := make(chan interaction.QuestionReply, 1)
	prompt := interaction.QuestionPrompt{
		Request: request,
		Done:    promptCtx.Done(),
		Reply:   reply,
	}
	select {
	case <-ctx.Done():
		return interaction.QuestionReply{}, ctx.Err()
	case s.questionRequests <- prompt:
	}
	select {
	case <-ctx.Done():
		return interaction.QuestionReply{}, ctx.Err()
	case answered := <-reply:
		// Cancellation wins over a racing submission: a cancelled run must
		// not record the late answer as a valid result.
		if err := ctx.Err(); err != nil {
			return interaction.QuestionReply{}, err
		}
		if err := interaction.ValidateQuestionReply(request, answered); err != nil {
			return interaction.QuestionReply{}, err
		}
		return answered, nil
	}
}

// questionAskerSetter is implemented by tools that need their frontend bound
// after the interactive Session exists.
type questionAskerSetter interface {
	SetAsker(interaction.QuestionAsker)
}

// bindQuestionTool connects the run-environment question tool to this
// Session. The tool is constructed unbound so the environment (built before
// the Session exists) can advertise its schema and prompt guidance; only an
// interactive Session can answer it.
func (s *interactiveSession) bindQuestionTool() {
	if s == nil {
		return
	}
	// tools and baseTools contain the same host-tool instances. Bind each
	// instance once; web rebinding reuses the bound baseTools.
	for _, current := range s.tools {
		if setter, ok := current.(questionAskerSetter); ok {
			setter.SetAsker(s)
		}
	}
}
