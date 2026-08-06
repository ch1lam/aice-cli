package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

// initPrompt drives the /init agent run. The model scans the workspace with
// its read-only tools and writes or improves AGENTS.md at the workspace root.
const initPrompt = `Initialize this project for future agent sessions by creating or improving AGENTS.md at the workspace root.

Explore the repository first with ls, find, grep, and read so the guidance is accurate. If AGENTS.md already exists, read it before changing anything and improve it in place, preserving content that is still valuable.

The file must be concise and project-specific, focused on what future agent sessions are most likely to need:
- build, lint, and test commands, and the order to run them for focused verification
- architecture and repo structure that are not obvious from filenames alone
- project-specific conventions, setup quirks, and operational gotchas
- references to existing instruction sources such as Cursor or Copilot rules when present

Write the final result to AGENTS.md with the write tool, resolving it from the workspace root. Do not modify any other file.`

// runInitCommand asks the current model to create or improve the workspace
// AGENTS.md, then records a trusted decision when the file was freshly
// created so the next run does not prompt for trust. The run is not persisted
// to the Session transcript; only its summary message is shown.
func (s *interactiveSession) runInitCommand(ctx context.Context) (string, error) {
	if s == nil {
		return "", fmt.Errorf("app: interactive Session is required")
	}
	if s.loop == nil {
		return "", credentialNotConfiguredError(s.configuration)
	}
	if s.workspace == nil {
		return "", fmt.Errorf("app: workspace is required")
	}
	if s.trustStore == nil {
		return "", fmt.Errorf("app: trust store is required")
	}

	existedBefore, _, err := workspaceAgentsFile(s.workspace)
	if err != nil {
		return "", err
	}

	prompt, err := llm.NewUserMessage(llm.NewTextContent(initPrompt).Part())
	if err != nil {
		return "", fmt.Errorf("app: create init prompt: %w", err)
	}
	_, runErr := s.loop.Run(ctx, agent.RunInput{
		Model:        s.model,
		SystemPrompt: s.systemPrompt,
		Prompt:       prompt,
		Options:      s.options,
	}, func(_ context.Context, _ agent.AgentEvent) error { return nil })
	if runErr != nil {
		return "", fmt.Errorf("app: run /init agent: %w", runErr)
	}

	exists, size, err := workspaceAgentsFile(s.workspace)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf(
			"app: /init finished without creating AGENTS.md at %s",
			s.workspace.PhysicalPath(),
		)
	}
	path := filepath.Join(s.workspace.PhysicalPath(), "AGENTS.md")
	if existedBefore {
		return fmt.Sprintf(
			"Updated AGENTS.md at %s (%d bytes). It is loaded on the next restart.",
			path,
			size,
		), nil
	}
	if err := s.trustStore.SetMany([]trust.Update{{
		Path:     s.workspacePath,
		Decision: trust.DecisionTrusted,
	}}); err != nil {
		return "", fmt.Errorf("app: record project trust for /init: %w", err)
	}
	return fmt.Sprintf(
		"Created AGENTS.md at %s (%d bytes). Trusted this workspace, so the file is loaded on the next restart.",
		path,
		size,
	), nil
}

// workspaceAgentsFile reports whether AGENTS.md exists at the workspace root
// and its size in bytes, reading through os.Root confinement.
func workspaceAgentsFile(workspace *tool.Workspace) (exists bool, size int64, err error) {
	root, err := os.OpenRoot(workspace.PhysicalPath())
	if err != nil {
		return false, 0, fmt.Errorf("app: open workspace root: %w", err)
	}
	defer root.Close()
	info, err := root.Stat("AGENTS.md")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("app: inspect workspace AGENTS.md: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, 0, fmt.Errorf("app: workspace AGENTS.md is not a regular file")
	}
	return true, info.Size(), nil
}
