package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const maxRecordBytes = 64 * 1024 * 1024

var (
	// ErrClosed indicates that a store no longer accepts writes.
	ErrClosed = errors.New("session store is closed")
	// ErrCorrupt indicates a malformed complete record or invalid file structure.
	ErrCorrupt = errors.New("session log is corrupt")
	// ErrUnsupportedVersion indicates a session format newer or older than this build.
	ErrUnsupportedVersion = errors.New("session version is unsupported")
	// ErrEntryNotFound indicates that a requested tree node does not exist.
	ErrEntryNotFound = errors.New("session entry is not found")
)

// Store owns one append-only session file.
type Store struct {
	mu   sync.Mutex
	file *os.File
	path string
	storeState
	closed bool
}

// Create creates a new session file and writes its versioned header.
func Create(ctx context.Context, path string, metadata Metadata) (*Store, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if err := validatePath(path); err != nil {
		return nil, err
	}
	header := Header{
		Type:             RecordTypeSession,
		Version:          CurrentVersion,
		ID:               metadata.ID,
		CreatedAt:        metadata.CreatedAt,
		WorkingDirectory: metadata.WorkingDirectory,
	}
	if err := validateHeader(header); err != nil {
		return nil, err
	}

	directory := filepath.Dir(path)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("session: create directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: create file: %w", err)
	}
	cleanup := func(cause error) error {
		closeErr := file.Close()
		removeErr := os.Remove(path)
		if removeErr != nil && os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return errors.Join(cause, closeErr, removeErr)
	}

	data, err := json.Marshal(header)
	if err != nil {
		return nil, cleanup(fmt.Errorf("session: encode header: %w", err))
	}
	if err := writeRecord(file, data); err != nil {
		return nil, cleanup(fmt.Errorf("session: write header: %w", err))
	}
	if err := file.Sync(); err != nil {
		return nil, cleanup(fmt.Errorf("session: sync header: %w", err))
	}
	return newStore(file, path, newStoreState(Snapshot{Header: header})), nil
}

// Open loads an existing session and truncates only an incomplete final record.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if err := validatePath(path); err != nil {
		return nil, err
	}
	// Recover through a non-append handle. On Windows, O_APPEND intentionally
	// omits FILE_WRITE_DATA, so the same handle cannot truncate an incomplete
	// tail even when it was opened with O_RDWR.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("session: open file: %w", err)
	}
	state, validBytes, incompleteTail, err := readSnapshot(ctx, file)
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if incompleteTail {
		if err := file.Truncate(validBytes); err != nil {
			return nil, errors.Join(
				fmt.Errorf("session: truncate incomplete tail: %w", err),
				file.Close(),
			)
		}
		if err := file.Sync(); err != nil {
			return nil, errors.Join(
				fmt.Errorf("session: sync recovered file: %w", err),
				file.Close(),
			)
		}
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("session: close recovered file: %w", err)
	}
	appendFile, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return nil, fmt.Errorf("session: reopen file for append: %w", err)
	}
	return newStore(appendFile, path, state), nil
}

func newStore(file *os.File, path string, state storeState) *Store {
	return &Store{
		file:       file,
		path:       path,
		storeState: state,
	}
}

// Path returns the session file path supplied by the caller.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

// LeafID returns the active tree node. An empty ID represents the root.
func (s *Store) LeafID() (string, error) {
	if s == nil {
		return "", fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leafID, nil
}

// Snapshot returns a defensive copy of the loaded session records.
func (s *Store) Snapshot() (Snapshot, error) {
	if s == nil {
		return Snapshot{}, fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	turns, err := cloneTurns(s.turns)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Header:      s.header,
		Turns:       turns,
		Compactions: cloneCompactions(s.compactions),
		LeafMoves:   cloneLeaves(s.leafMoves),
		Order:       append([]string(nil), s.order...),
		LeafID:      s.leafID,
	}, nil
}

// AppendTurn durably appends one completed interaction as one tree node.
func (s *Store) AppendTurn(ctx context.Context, turn Turn) error {
	if s == nil {
		return fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateWritable(ctx); err != nil {
		return err
	}
	if err := turn.Validate(); err != nil {
		return err
	}
	if err := validateNode(
		turn.ID,
		turn.ParentID,
		s.leafID,
		s.index.recordIDs,
		s.index.nodeTypes,
	); err != nil {
		return err
	}

	data, err := json.Marshal(turn)
	if err != nil {
		return fmt.Errorf("session: encode turn: %w", err)
	}
	if err := s.appendData(ctx, "turn", data); err != nil {
		return err
	}
	var stored Turn
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("session: retain appended turn: %w", err)
	}
	s.retainTurn(stored)
	return nil
}

// AppendCompaction durably appends one derived checkpoint to the active branch.
func (s *Store) AppendCompaction(
	ctx context.Context,
	compaction Compaction,
) error {
	if s == nil {
		return fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateWritable(ctx); err != nil {
		return err
	}
	if err := compaction.Validate(); err != nil {
		return err
	}
	if err := validateNode(
		compaction.ID,
		compaction.ParentID,
		s.leafID,
		s.index.recordIDs,
		s.index.nodeTypes,
	); err != nil {
		return err
	}
	if err := validateCompactionBoundary(
		compaction,
		s.index.nodeTypes,
		s.index.parents,
		s.index.compactions,
	); err != nil {
		return err
	}

	data, err := json.Marshal(compaction)
	if err != nil {
		return fmt.Errorf("session: encode compaction: %w", err)
	}
	if err := s.appendData(ctx, "compaction", data); err != nil {
		return err
	}
	var stored Compaction
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("session: retain appended compaction: %w", err)
	}
	s.retainCompaction(stored)
	return nil
}

// AppendLeaf durably moves the active branch pointer without deleting history.
func (s *Store) AppendLeaf(ctx context.Context, leaf Leaf) error {
	if s == nil {
		return fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateWritable(ctx); err != nil {
		return err
	}
	if err := leaf.Validate(); err != nil {
		return err
	}
	if err := validateLeaf(
		leaf,
		s.leafID,
		s.index.recordIDs,
		s.index.nodeTypes,
	); err != nil {
		return err
	}

	data, err := json.Marshal(leaf)
	if err != nil {
		return fmt.Errorf("session: encode leaf: %w", err)
	}
	if err := s.appendData(ctx, "leaf", data); err != nil {
		return err
	}
	s.retainLeaf(leaf)
	return nil
}

func (s *Store) validateWritable(ctx context.Context) error {
	if s.closed {
		return ErrClosed
	}
	return validateContext(ctx)
}

func (s *Store) appendData(
	ctx context.Context,
	label string,
	data []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data)+1 > maxRecordBytes {
		return fmt.Errorf(
			"session: %s exceeds %d-byte record limit",
			label,
			maxRecordBytes,
		)
	}
	info, err := s.file.Stat()
	if err != nil {
		return fmt.Errorf("session: stat before %s append: %w", label, err)
	}
	rollback := func(cause error) error {
		truncateErr := s.file.Truncate(info.Size())
		syncErr := s.file.Sync()
		return errors.Join(cause, truncateErr, syncErr)
	}
	if err := writeRecord(s.file, data); err != nil {
		return rollback(fmt.Errorf("session: append %s: %w", label, err))
	}
	if err := s.file.Sync(); err != nil {
		return rollback(fmt.Errorf("session: sync %s: %w", label, err))
	}
	return nil
}

// Close releases the session file. It is safe to call more than once.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("session: close file: %w", err)
	}
	return nil
}
