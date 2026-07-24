package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// CorruptionError identifies the physical record that made recovery unsafe.
type CorruptionError struct {
	Line   int
	Offset int64
	Cause  error
}

// Error describes the corrupt record location.
func (e *CorruptionError) Error() string {
	return fmt.Sprintf(
		"session: corrupt record at line %d offset %d: %v",
		e.Line,
		e.Offset,
		e.Cause,
	)
}

// Unwrap supports errors.Is for both ErrCorrupt and the underlying cause.
func (e *CorruptionError) Unwrap() []error {
	return []error{ErrCorrupt, e.Cause}
}

// Store owns one append-only session file.
type Store struct {
	mu             sync.Mutex
	file           *os.File
	path           string
	header         Header
	turns          []Turn
	compactions    []Compaction
	leafMoves      []Leaf
	order          []string
	nodeTypes      map[string]RecordType
	parents        map[string]string
	compactionByID map[string]Compaction
	recordIDs      map[string]struct{}
	leafID         string
	closed         bool
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
	return newStore(file, path, Snapshot{Header: header}), nil
}

// Open loads an existing session and truncates only an incomplete final record.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if err := validatePath(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return nil, fmt.Errorf("session: open file: %w", err)
	}
	snapshot, validBytes, incompleteTail, err := readSnapshot(ctx, file)
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
	return newStore(file, path, snapshot), nil
}

func newStore(file *os.File, path string, snapshot Snapshot) *Store {
	store := &Store{
		file:           file,
		path:           path,
		header:         snapshot.Header,
		turns:          snapshot.Turns,
		compactions:    snapshot.Compactions,
		leafMoves:      snapshot.LeafMoves,
		order:          snapshot.Order,
		nodeTypes:      make(map[string]RecordType),
		parents:        make(map[string]string),
		compactionByID: make(map[string]Compaction),
		recordIDs:      make(map[string]struct{}),
		leafID:         snapshot.LeafID,
	}
	for _, turn := range store.turns {
		store.nodeTypes[turn.ID] = turn.Type
		store.parents[turn.ID] = turn.ParentID
		store.recordIDs[turn.ID] = struct{}{}
	}
	for _, compaction := range store.compactions {
		store.nodeTypes[compaction.ID] = compaction.Type
		store.parents[compaction.ID] = compaction.ParentID
		store.compactionByID[compaction.ID] = compaction
		store.recordIDs[compaction.ID] = struct{}{}
	}
	for _, leaf := range store.leafMoves {
		store.recordIDs[leaf.ID] = struct{}{}
	}
	return store
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

// AppendTurn durably appends one complete run as one tree node.
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
	if err := s.validateNodeBoundary(turn.ID, turn.ParentID); err != nil {
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
	s.turns = append(s.turns, stored)
	s.retainNode(stored.ID, stored.ParentID, stored.Type)
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
	if err := s.validateNodeBoundary(
		compaction.ID,
		compaction.ParentID,
	); err != nil {
		return err
	}
	if err := validateCompactionBoundary(
		compaction,
		s.nodeTypes,
		s.parents,
		s.compactionByID,
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
	s.compactions = append(s.compactions, stored)
	s.compactionByID[stored.ID] = stored
	s.retainNode(stored.ID, stored.ParentID, stored.Type)
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
	if _, exists := s.recordIDs[leaf.ID]; exists {
		return fmt.Errorf("session: duplicate record id %q", leaf.ID)
	}
	if leaf.ParentID != s.leafID {
		return fmt.Errorf(
			"session: leaf parent %q is not current leaf %q",
			leaf.ParentID,
			s.leafID,
		)
	}
	if leaf.TargetID != "" {
		if _, exists := s.nodeTypes[leaf.TargetID]; !exists {
			return fmt.Errorf("%w: %s", ErrEntryNotFound, leaf.TargetID)
		}
	}

	data, err := json.Marshal(leaf)
	if err != nil {
		return fmt.Errorf("session: encode leaf: %w", err)
	}
	if err := s.appendData(ctx, "leaf", data); err != nil {
		return err
	}
	s.leafMoves = append(s.leafMoves, leaf)
	s.recordIDs[leaf.ID] = struct{}{}
	s.leafID = leaf.TargetID
	return nil
}

func (s *Store) validateWritable(ctx context.Context) error {
	if s.closed {
		return ErrClosed
	}
	return validateContext(ctx)
}

func (s *Store) validateNodeBoundary(id string, parentID string) error {
	if _, exists := s.recordIDs[id]; exists {
		return fmt.Errorf("session: duplicate record id %q", id)
	}
	if parentID != s.leafID {
		return fmt.Errorf(
			"session: node parent %q is not current leaf %q",
			parentID,
			s.leafID,
		)
	}
	if parentID != "" {
		if _, exists := s.nodeTypes[parentID]; !exists {
			return fmt.Errorf("%w: %s", ErrEntryNotFound, parentID)
		}
	}
	return nil
}

func (s *Store) retainNode(id string, parentID string, recordType RecordType) {
	s.nodeTypes[id] = recordType
	s.parents[id] = parentID
	s.recordIDs[id] = struct{}{}
	s.order = append(s.order, id)
	s.leafID = id
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

func readSnapshot(
	ctx context.Context,
	file *os.File,
) (Snapshot, int64, bool, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Snapshot{}, 0, false, fmt.Errorf("session: seek file: %w", err)
	}
	reader := bufio.NewReader(file)
	var snapshot Snapshot
	nodeTypes := make(map[string]RecordType)
	parents := make(map[string]string)
	compactionByID := make(map[string]Compaction)
	recordIDs := make(map[string]struct{})
	var validBytes int64
	lineNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, 0, false, err
		}
		line, complete, err := readPhysicalLine(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Snapshot{}, 0, false, corrupt(lineNumber+1, validBytes, err)
		}
		if !complete {
			if lineNumber == 0 {
				return Snapshot{}, 0, false, corrupt(
					1,
					0,
					errors.New("incomplete session header"),
				)
			}
			return snapshot, validBytes, true, nil
		}

		lineNumber++
		recordOffset := validBytes
		validBytes += int64(len(line))
		var envelope struct {
			Type RecordType `json:"type"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
		}
		if lineNumber == 1 {
			if envelope.Type != RecordTypeSession {
				return Snapshot{}, 0, false, corrupt(
					lineNumber,
					recordOffset,
					fmt.Errorf("first record has type %q", envelope.Type),
				)
			}
			if err := decodeRecord(line, &snapshot.Header); err != nil {
				return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateHeader(snapshot.Header); err != nil {
				return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			continue
		}
		switch envelope.Type {
		case RecordTypeTurn:
			var turn Turn
			if err := decodeRecord(line, &turn); err != nil {
				return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateReplayNode(
				turn.ID,
				turn.ParentID,
				snapshot.LeafID,
				recordIDs,
				nodeTypes,
			); err != nil {
				return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			snapshot.Turns = append(snapshot.Turns, turn)
			retainReplayNode(&snapshot, turn.ID, turn.ParentID, turn.Type, recordIDs, nodeTypes, parents)
		case RecordTypeCompaction:
			var compaction Compaction
			if err := decodeRecord(line, &compaction); err != nil {
				return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateReplayNode(
				compaction.ID,
				compaction.ParentID,
				snapshot.LeafID,
				recordIDs,
				nodeTypes,
			); err != nil {
				return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateCompactionBoundary(
				compaction,
				nodeTypes,
				parents,
				compactionByID,
			); err != nil {
				return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			snapshot.Compactions = append(snapshot.Compactions, compaction)
			compactionByID[compaction.ID] = compaction
			retainReplayNode(
				&snapshot,
				compaction.ID,
				compaction.ParentID,
				compaction.Type,
				recordIDs,
				nodeTypes,
				parents,
			)
		case RecordTypeLeaf:
			var leaf Leaf
			if err := decodeRecord(line, &leaf); err != nil {
				return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if _, exists := recordIDs[leaf.ID]; exists {
				return Snapshot{}, 0, false, corrupt(
					lineNumber,
					recordOffset,
					fmt.Errorf("duplicate record id %q", leaf.ID),
				)
			}
			if leaf.ParentID != snapshot.LeafID {
				return Snapshot{}, 0, false, corrupt(
					lineNumber,
					recordOffset,
					fmt.Errorf(
						"leaf parent %q is not current leaf %q",
						leaf.ParentID,
						snapshot.LeafID,
					),
				)
			}
			if leaf.TargetID != "" {
				if _, exists := nodeTypes[leaf.TargetID]; !exists {
					return Snapshot{}, 0, false, corrupt(
						lineNumber,
						recordOffset,
						fmt.Errorf("%w: %s", ErrEntryNotFound, leaf.TargetID),
					)
				}
			}
			snapshot.LeafMoves = append(snapshot.LeafMoves, leaf)
			recordIDs[leaf.ID] = struct{}{}
			snapshot.LeafID = leaf.TargetID
		default:
			return Snapshot{}, 0, false, corrupt(
				lineNumber,
				recordOffset,
				fmt.Errorf("record has unsupported type %q", envelope.Type),
			)
		}
	}
	if lineNumber == 0 {
		return Snapshot{}, 0, false, corrupt(1, 0, errors.New("missing session header"))
	}
	return snapshot, validBytes, false, nil
}

func validateReplayNode(
	id string,
	parentID string,
	leafID string,
	recordIDs map[string]struct{},
	nodeTypes map[string]RecordType,
) error {
	if _, exists := recordIDs[id]; exists {
		return fmt.Errorf("duplicate record id %q", id)
	}
	if parentID != leafID {
		return fmt.Errorf(
			"node parent %q is not current leaf %q",
			parentID,
			leafID,
		)
	}
	if parentID != "" {
		if _, exists := nodeTypes[parentID]; !exists {
			return fmt.Errorf("%w: %s", ErrEntryNotFound, parentID)
		}
	}
	return nil
}

func retainReplayNode(
	snapshot *Snapshot,
	id string,
	parentID string,
	recordType RecordType,
	recordIDs map[string]struct{},
	nodeTypes map[string]RecordType,
	parents map[string]string,
) {
	recordIDs[id] = struct{}{}
	nodeTypes[id] = recordType
	parents[id] = parentID
	snapshot.Order = append(snapshot.Order, id)
	snapshot.LeafID = id
}

func validateCompactionBoundary(
	compaction Compaction,
	nodeTypes map[string]RecordType,
	parents map[string]string,
	compactions map[string]Compaction,
) error {
	if err := compaction.Validate(); err != nil {
		return err
	}
	path, err := pathToRoot(compaction.ParentID, nodeTypes, parents)
	if err != nil {
		return err
	}
	activeTurnIDs, err := activeTurnIDs(path, nodeTypes, compactions)
	if err != nil {
		return err
	}
	if len(activeTurnIDs) != compaction.ActiveTurnCount {
		return fmt.Errorf(
			"session: compaction active turn count is %d, current branch has %d",
			compaction.ActiveTurnCount,
			len(activeTurnIDs),
		)
	}
	firstKept := -1
	for index, turnID := range activeTurnIDs {
		if turnID == compaction.FirstKeptTurnID {
			firstKept = index
			break
		}
	}
	if firstKept <= 0 {
		return fmt.Errorf(
			"session: compaction first kept turn %q does not leave older branch history",
			compaction.FirstKeptTurnID,
		)
	}
	if retained := len(activeTurnIDs) - firstKept; retained != compaction.RetainedTurnCount {
		return fmt.Errorf(
			"session: compaction retained turn count is %d, branch boundary retains %d",
			compaction.RetainedTurnCount,
			retained,
		)
	}
	return nil
}

func pathToRoot(
	leafID string,
	nodeTypes map[string]RecordType,
	parents map[string]string,
) ([]string, error) {
	if leafID == "" {
		return nil, nil
	}
	path := make([]string, 0)
	seen := make(map[string]struct{})
	current := leafID
	for current != "" {
		if _, exists := seen[current]; exists {
			return nil, fmt.Errorf("session: cycle at entry %q", current)
		}
		seen[current] = struct{}{}
		if _, exists := nodeTypes[current]; !exists {
			return nil, fmt.Errorf("%w: %s", ErrEntryNotFound, current)
		}
		path = append(path, current)
		current = parents[current]
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path, nil
}

func activeTurnIDs(
	path []string,
	nodeTypes map[string]RecordType,
	compactions map[string]Compaction,
) ([]string, error) {
	latestCompaction := -1
	for index, id := range path {
		if nodeTypes[id] == RecordTypeCompaction {
			latestCompaction = index
		}
	}
	start := 0
	if latestCompaction >= 0 {
		compaction, exists := compactions[path[latestCompaction]]
		if !exists {
			return nil, fmt.Errorf(
				"session: compaction entry %q is missing",
				path[latestCompaction],
			)
		}
		start = -1
		for index := 0; index < latestCompaction; index++ {
			if path[index] == compaction.FirstKeptTurnID {
				start = index
				break
			}
		}
		if start < 0 {
			return nil, fmt.Errorf(
				"session: compaction first kept turn %q is not an ancestor",
				compaction.FirstKeptTurnID,
			)
		}
	}
	turnIDs := make([]string, 0)
	for index, id := range path {
		if latestCompaction >= 0 && index < start {
			continue
		}
		if nodeTypes[id] == RecordTypeTurn {
			turnIDs = append(turnIDs, id)
		}
	}
	return turnIDs, nil
}

func readPhysicalLine(reader *bufio.Reader) ([]byte, bool, error) {
	line := make([]byte, 0, 4096)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxRecordBytes {
			return nil, false, fmt.Errorf(
				"record exceeds %d-byte limit",
				maxRecordBytes,
			)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return nil, false, io.EOF
			}
			return line, false, nil
		default:
			return nil, false, err
		}
	}
}

func decodeRecord(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("record contains multiple json values")
		}
		return fmt.Errorf("decode trailing record data: %w", err)
	}
	return nil
}

func writeRecord(file *os.File, data []byte) error {
	if len(data)+1 > maxRecordBytes {
		return fmt.Errorf("record exceeds %d-byte limit", maxRecordBytes)
	}
	record := make([]byte, 0, len(data)+1)
	record = append(record, data...)
	record = append(record, '\n')
	for len(record) > 0 {
		written, err := file.Write(record)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		record = record[written:]
	}
	return nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("session: context is required")
	}
	return ctx.Err()
}

func validatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("session: file path is required")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("session: file path contains a null byte")
	}
	return nil
}

func validateHeader(header Header) error {
	if header.Type != RecordTypeSession {
		return fmt.Errorf("session: header has type %q", header.Type)
	}
	if header.Version != CurrentVersion {
		return fmt.Errorf(
			"%w: got %d, want %d",
			ErrUnsupportedVersion,
			header.Version,
			CurrentVersion,
		)
	}
	if strings.TrimSpace(header.ID) == "" {
		return fmt.Errorf("session: header id is required")
	}
	if strings.IndexByte(header.ID, 0) >= 0 {
		return fmt.Errorf("session: header id contains a null byte")
	}
	if header.CreatedAt <= 0 {
		return fmt.Errorf("session: header creation time must be positive")
	}
	if strings.TrimSpace(header.WorkingDirectory) == "" {
		return fmt.Errorf("session: header working directory is required")
	}
	if strings.IndexByte(header.WorkingDirectory, 0) >= 0 {
		return fmt.Errorf("session: header working directory contains a null byte")
	}
	if !filepath.IsAbs(header.WorkingDirectory) {
		return fmt.Errorf("session: header working directory must be absolute")
	}
	return nil
}

func corrupt(line int, offset int64, cause error) error {
	return &CorruptionError{Line: line, Offset: offset, Cause: cause}
}
