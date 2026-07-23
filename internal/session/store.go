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
	mu     sync.Mutex
	file   *os.File
	path   string
	header Header
	turns  []Turn
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
	return &Store{
		file:   file,
		path:   path,
		header: header,
		turns:  []Turn{},
	}, nil
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
	return &Store{
		file:   file,
		path:   path,
		header: snapshot.Header,
		turns:  snapshot.Turns,
	}, nil
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

// Snapshot returns a defensive copy of the loaded header and turns.
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
	return Snapshot{Header: s.header, Turns: turns}, nil
}

// AppendTurn durably appends one complete run as one physical JSONL record.
func (s *Store) AppendTurn(ctx context.Context, turn Turn) error {
	if s == nil {
		return fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if err := validateContext(ctx); err != nil {
		return err
	}

	data, err := json.Marshal(turn)
	if err != nil {
		return fmt.Errorf("session: encode turn: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data)+1 > maxRecordBytes {
		return fmt.Errorf("session: turn exceeds %d-byte record limit", maxRecordBytes)
	}
	info, err := s.file.Stat()
	if err != nil {
		return fmt.Errorf("session: stat before append: %w", err)
	}
	rollback := func(cause error) error {
		truncateErr := s.file.Truncate(info.Size())
		syncErr := s.file.Sync()
		return errors.Join(cause, truncateErr, syncErr)
	}
	if err := writeRecord(s.file, data); err != nil {
		return rollback(fmt.Errorf("session: append turn: %w", err))
	}
	if err := s.file.Sync(); err != nil {
		return rollback(fmt.Errorf("session: sync turn: %w", err))
	}

	var stored Turn
	if err := json.Unmarshal(data, &stored); err != nil {
		return rollback(fmt.Errorf("session: retain appended turn: %w", err))
	}
	s.turns = append(s.turns, stored)
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
		if envelope.Type != RecordTypeTurn {
			return Snapshot{}, 0, false, corrupt(
				lineNumber,
				recordOffset,
				fmt.Errorf("record has unsupported type %q", envelope.Type),
			)
		}
		var turn Turn
		if err := decodeRecord(line, &turn); err != nil {
			return Snapshot{}, 0, false, corrupt(lineNumber, recordOffset, err)
		}
		snapshot.Turns = append(snapshot.Turns, turn)
	}
	if lineNumber == 0 {
		return Snapshot{}, 0, false, corrupt(1, 0, errors.New("missing session header"))
	}
	return snapshot, validBytes, false, nil
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
