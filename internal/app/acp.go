package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/acp"
	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/session"
)

// ServeACP serves one ACP client over stdio. Project trust is never prompted:
// stdio carries the protocol, so only the explicit override applies.
func (a *application) ServeACP(ctx context.Context, request cli.ACPRequest) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if request.Input == nil || request.Output == nil {
		return fmt.Errorf("app: ACP input and output are required")
	}
	configured, err := a.loadRunModel(ctx, request.Workspace, request.ProjectTrustOverride, nil)
	if err != nil {
		return err
	}
	// The connected client answers Guard asks; ACP has no question request.
	environment, err := a.prepareRunEnvironment(ctx, request.Workspace, request.ProjectTrustOverride, nil, request.Yolo, configured, runFrontend{asks: true})
	if err != nil {
		return err
	}
	defer environment.web.closeBackend()
	defer func() { returnErr = errors.Join(returnErr, environment.mcp.Close()) }()
	if !providerConfigured(a.dependencies.providers, environment.configuration) {
		return credentialNotConfiguredError(a.dependencies.providers, environment.configuration)
	}
	store, history, usage, err := prepareSession(ctx, environment.workspace, request.Session)
	if err != nil {
		return err
	}
	runner, err := a.newInteractiveSession(environment, store, history, usage)
	if err != nil {
		return errors.Join(err, store.Close())
	}
	// The client is told about generated titles as session_info_update.
	runner.titleUpdates = make(chan interaction.SessionTitle, 4)
	serveErr := acp.Serve(ctx, runner.frontend(), acp.Options{
		Workspace: environment.workspace.PhysicalPath(),
		Version:   buildinfo.Build(),
		Host:      runner,
	}, request.Input, request.Output)
	runner.titles.close()
	return errors.Join(serveErr, closeInteractiveStore(runner.conversation.store))
}

// StartSession makes a fresh durable Session current for a protocol client
// and returns its ID. A current Session without messages, such as a file
// named by --session, is used as is; otherwise /new detaches it first.
func (s *interactiveSession) StartSession(ctx context.Context) (string, error) {
	if snapshot, ok, err := s.currentSnapshot(); err != nil {
		return "", err
	} else if ok && len(snapshot.Messages) > 0 {
		if _, err := s.RunSlashCommand(ctx, interaction.CommandRequest{Name: "new"}); err != nil {
			return "", err
		}
	}
	if err := s.ensureSessionStore(ctx); err != nil {
		return "", err
	}
	snapshot, _, err := s.currentSnapshot()
	return snapshot.Header.ID, err
}

// LoadSession makes the project Session with id current, recovering
// interrupted tools as /history does, and returns its active branch.
func (s *interactiveSession) LoadSession(ctx context.Context, id string) (*interaction.Transcript, error) {
	if snapshot, ok, err := s.currentSnapshot(); err != nil {
		return nil, err
	} else if ok && snapshot.Header.ID == id {
		return sessionTranscript(snapshot)
	}
	summaries, err := s.SearchSessions(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, summary := range summaries {
		if summary.ID != id {
			continue
		}
		if _, err := s.RunSlashCommand(ctx, interaction.CommandRequest{Name: "history", Arguments: summary.Key}); err != nil {
			return nil, err
		}
		return s.RuntimeState().Transcript, nil
	}
	return nil, fmt.Errorf("app: session %q is not in this project", id)
}

func (s *interactiveSession) currentSnapshot() (session.Snapshot, bool, error) {
	s.conversation.historySyncMu.Lock()
	store := s.conversation.store
	s.conversation.historySyncMu.Unlock()
	if store == nil {
		return session.Snapshot{}, false, nil
	}
	snapshot, err := store.Snapshot()
	return snapshot, err == nil, err
}
