package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

const sessionPersistenceTimeout = 5 * time.Second

func prepareSession(
	ctx context.Context,
	workspace *tool.Workspace,
	requestedPath string,
	createDefault bool,
) (*session.Store, []llm.AgentMessage, error) {
	if workspace == nil {
		return nil, nil, fmt.Errorf("app: workspace is required")
	}
	if requestedPath == "" && !createDefault {
		return nil, nil, nil
	}

	if requestedPath == "" {
		id, err := session.NewID()
		if err != nil {
			return nil, nil, fmt.Errorf("app: generate session id: %w", err)
		}
		path := filepath.Join(
			workspace.Path(),
			".aice",
			"sessions",
			id+".jsonl",
		)
		store, err := createSession(ctx, path, id, workspace.Path())
		return store, nil, err
	}

	store, snapshot, err := openExistingSession(ctx, workspace, requestedPath)
	if err == nil {
		history, historyErr := sessionHistory(snapshot)
		if historyErr != nil {
			return nil, nil, errors.Join(
				historyErr,
				store.Close(),
			)
		}
		return store, history, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}

	path, err := filepath.Abs(requestedPath)
	if err != nil {
		return nil, nil, fmt.Errorf("app: resolve session path: %w", err)
	}
	path = filepath.Clean(path)

	id, err := session.NewID()
	if err != nil {
		return nil, nil, fmt.Errorf("app: generate session id: %w", err)
	}
	store, err = createSession(ctx, path, id, workspace.Path())
	return store, nil, err
}

func createSession(
	ctx context.Context,
	path string,
	id string,
	workingDirectory string,
) (*session.Store, error) {
	store, err := session.Create(ctx, path, session.Metadata{
		ID:               id,
		CreatedAt:        time.Now().UnixMilli(),
		WorkingDirectory: workingDirectory,
	})
	if err != nil {
		return nil, fmt.Errorf("app: create session: %w", err)
	}
	return store, nil
}

func openExistingSession(
	ctx context.Context,
	workspace *tool.Workspace,
	requestedPath string,
) (*session.Store, session.Snapshot, error) {
	if workspace == nil {
		return nil, session.Snapshot{}, fmt.Errorf("app: workspace is required")
	}
	path, err := filepath.Abs(requestedPath)
	if err != nil {
		return nil, session.Snapshot{}, fmt.Errorf(
			"app: resolve session path: %w",
			err,
		)
	}
	path = filepath.Clean(path)
	store, err := session.Open(ctx, path)
	if err != nil {
		return nil, session.Snapshot{}, fmt.Errorf("app: open session: %w", err)
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		return nil, session.Snapshot{}, errors.Join(
			fmt.Errorf("app: read session: %w", err),
			store.Close(),
		)
	}
	if filepath.Clean(snapshot.Header.WorkingDirectory) != workspace.Path() {
		return nil, session.Snapshot{}, errors.Join(
			fmt.Errorf(
				"app: session working directory is %q, current working directory is %q",
				snapshot.Header.WorkingDirectory,
				workspace.Path(),
			),
			store.Close(),
		)
	}
	return store, snapshot, nil
}

func sessionHistory(snapshot session.Snapshot) ([]llm.AgentMessage, error) {
	history, err := session.BuildContext(snapshot)
	if err != nil {
		return nil, fmt.Errorf("app: build session context: %w", err)
	}
	return history, nil
}

func appendSessionRun(
	ctx context.Context,
	store *session.Store,
	messages []llm.AgentMessage,
) error {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if store == nil {
		return fmt.Errorf("app: session store is required")
	}
	id, err := session.NewID()
	if err != nil {
		return fmt.Errorf("app: generate session turn id: %w", err)
	}
	parentID, err := store.LeafID()
	if err != nil {
		return fmt.Errorf("app: read session leaf: %w", err)
	}
	turn, err := session.NewTurn(
		id,
		parentID,
		time.Now().UnixMilli(),
		messages,
	)
	if err != nil {
		return fmt.Errorf("app: create session turn: %w", err)
	}

	// A completed run is durable cleanup. Give it a short independent deadline
	// so cancellation of the model/tool request cannot discard its transcript.
	persistCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		sessionPersistenceTimeout,
	)
	defer cancel()
	if err := store.AppendTurn(persistCtx, turn); err != nil {
		return fmt.Errorf("app: append session turn: %w", err)
	}
	return nil
}
