package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/trust"
)

// TrustPromptOptions configures the pre-startup project trust selector.
type TrustPromptOptions struct {
	Input   io.Reader
	Output  io.Writer
	CWD     string
	Choices []trust.Choice
}

var trustInterruptKeys = key.NewBinding(
	key.WithKeys("ctrl+c"),
	key.WithHelp("ctrl+C", "cancel"),
)

// RunTrustPrompt shows the project trust choices before the main TUI starts.
// It only renders and returns a selection; it never reads the trust store,
// discovers resources, or assembles the agent. Cancellation selects a
// session-only "do not trust" choice with no persisted updates.
func RunTrustPrompt(ctx context.Context, options TrustPromptOptions) (trust.Choice, error) {
	if ctx == nil {
		return trust.Choice{}, fmt.Errorf("tui: context is required")
	}
	if options.Input == nil {
		return trust.Choice{}, fmt.Errorf("tui: input is required")
	}
	if options.Output == nil {
		return trust.Choice{}, fmt.Errorf("tui: output is required")
	}
	if strings.TrimSpace(options.CWD) == "" {
		return trust.Choice{}, fmt.Errorf("tui: working directory is required")
	}
	if len(options.Choices) == 0 {
		return trust.Choice{}, fmt.Errorf("tui: trust choices are required")
	}

	model := trustPromptModel{
		cwd:     options.CWD,
		choices: options.Choices,
	}
	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
		tea.WithoutSignalHandler(),
	)
	result, err := program.Run()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return trust.Choice{}, ctxErr
		}
		return trust.Choice{}, fmt.Errorf("tui: run trust prompt: %w", err)
	}
	final, ok := result.(trustPromptModel)
	if !ok {
		return trust.Choice{}, fmt.Errorf("tui: unexpected trust prompt result")
	}
	return final.choice, nil
}

// trustPromptModel is a minimal selector that terminates before the main TUI
// starts, so it owns no run controller or agent goroutine.
type trustPromptModel struct {
	cwd      string
	choices  []trust.Choice
	selected int
	choice   trust.Choice
	done     bool
}

func (m trustPromptModel) Init() tea.Cmd {
	return nil
}

func (m trustPromptModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.KeyPressMsg:
		switch {
		case msg.Code == tea.KeyUp:
			if m.selected > 0 {
				m.selected--
			}
		case msg.Code == tea.KeyDown:
			if m.selected < len(m.choices)-1 {
				m.selected++
			}
		case msg.Code == tea.KeyEnter:
			m.choice = m.choices[m.selected]
			m.done = true
			return m, tea.Quit
		case msg.Code == tea.KeyEscape,
			key.Matches(msg, trustInterruptKeys):
			// Cancellation is a session-only "do not trust" without updates.
			m.choice = trust.Choice{Decision: trust.DecisionUntrusted}
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m trustPromptModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	var builder strings.Builder
	builder.WriteString(headerStyle.Render("Trust project folder?"))
	builder.WriteString("\n")
	builder.WriteString(bodyStyle.Render(m.cwd))
	builder.WriteString("\n\n")
	builder.WriteString(mutedStyle.Render(
		"This allows AICE to load project-local prompts and future protected " +
			"resources. It does not sandbox tools or restrict filesystem access.",
	))
	builder.WriteString("\n\n")
	for index, choice := range m.choices {
		label := choice.Label
		if index == m.selected {
			builder.WriteString(slashCommandSelectedStyle.Render("> " + label))
		} else {
			builder.WriteString(slashCommandRowStyle.Render("  " + label))
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	builder.WriteString(mutedStyle.Render(
		"Use ↑/↓ to choose, Enter to select, Esc to cancel.",
	))
	return tea.NewView(builder.String())
}
