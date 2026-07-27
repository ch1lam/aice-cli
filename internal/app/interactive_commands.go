package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func (s *interactiveSession) SlashCommands() []tui.SlashCommand {
	return []tui.SlashCommand{
		{
			Name:        "session",
			Description: "Show current Session information",
		},
		{
			Name:        "tree",
			Description: "Show all Session branches and the active leaf",
		},
		{
			Name:         "checkout",
			Description:  "Move the active leaf without deleting later branches",
			ArgumentHint: "<entry|root>",
		},
		{
			Name:        "compact",
			Description: "Compact the active branch at the current turn boundary",
		},
	}
}

func (s *interactiveSession) RunSlashCommand(
	ctx context.Context,
	request tui.SlashCommandRequest,
) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("app: context is required")
	}
	if s == nil || s.store == nil {
		return "", fmt.Errorf("app: interactive Session is required")
	}

	switch request.Name {
	case "session":
		if err := requireNoSlashCommandArguments(request); err != nil {
			return "", err
		}
		return s.sessionInformation()
	case "tree":
		if err := requireNoSlashCommandArguments(request); err != nil {
			return "", err
		}
		snapshot, err := s.store.Snapshot()
		if err != nil {
			return "", fmt.Errorf("app: read Session tree: %w", err)
		}
		output := new(bytes.Buffer)
		if err := writeSessionTree(output, snapshot); err != nil {
			return "", err
		}
		return output.String(), nil
	case "checkout":
		entry, err := slashCommandEntry(request)
		if err != nil {
			return "", err
		}
		output := new(bytes.Buffer)
		if err := checkoutSessionStore(ctx, s.store, entry, output); err != nil {
			return "", err
		}
		if err := s.reloadHistory(); err != nil {
			return "", err
		}
		return output.String(), nil
	case "compact":
		if err := requireNoSlashCommandArguments(request); err != nil {
			return "", err
		}
		if s.application == nil {
			return "", fmt.Errorf("app: application is required")
		}
		output, err := s.application.compactSession(ctx, s.store)
		if err != nil {
			return "", err
		}
		if err := s.reloadHistory(); err != nil {
			return "", err
		}
		return output, nil
	default:
		return "", fmt.Errorf("app: unsupported slash command /%s", request.Name)
	}
}

func (s *interactiveSession) sessionInformation() (string, error) {
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return "", fmt.Errorf("app: read Session information: %w", err)
	}
	nodes, err := session.Nodes(snapshot)
	if err != nil {
		return "", fmt.Errorf("app: read Session nodes: %w", err)
	}
	leaf := snapshot.LeafID
	if leaf == "" {
		leaf = "root"
	}
	return fmt.Sprintf(
		"Session %s\nPath: %s\nActive leaf: %s\nNodes: %d\nTurns: %d\nCompactions: %d",
		snapshot.Header.ID,
		s.store.Path(),
		leaf,
		len(nodes),
		len(snapshot.Turns),
		len(snapshot.Compactions),
	), nil
}

func (s *interactiveSession) reloadHistory() error {
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return fmt.Errorf("app: reload Session snapshot: %w", err)
	}
	history, err := sessionHistory(snapshot)
	if err != nil {
		return fmt.Errorf("app: reload Session history: %w", err)
	}
	s.history = history
	return nil
}

func requireNoSlashCommandArguments(
	request tui.SlashCommandRequest,
) error {
	if request.Arguments == "" {
		return nil
	}
	return fmt.Errorf("app: /%s does not accept arguments", request.Name)
}

func slashCommandEntry(request tui.SlashCommandRequest) (string, error) {
	fields := strings.Fields(request.Arguments)
	if len(fields) != 1 {
		return "", fmt.Errorf("app: usage: /checkout <entry|root>")
	}
	return fields[0], nil
}
