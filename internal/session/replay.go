package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
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

func readSnapshot(
	ctx context.Context,
	file *os.File,
) (storeState, int64, bool, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return storeState{}, 0, false, fmt.Errorf("session: seek file: %w", err)
	}
	reader := bufio.NewReader(file)
	state := storeState{
		index: indexRecords(nil, nil, nil),
	}
	var validBytes int64
	lineNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			return storeState{}, 0, false, err
		}
		line, complete, err := readPhysicalLine(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return storeState{}, 0, false, corrupt(lineNumber+1, validBytes, err)
		}
		if !complete {
			if lineNumber == 0 {
				return storeState{}, 0, false, corrupt(
					1,
					0,
					errors.New("incomplete session header"),
				)
			}
			return state, validBytes, true, nil
		}

		lineNumber++
		recordOffset := validBytes
		validBytes += int64(len(line))
		var envelope struct {
			Type RecordType `json:"type"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
		}
		if lineNumber == 1 {
			if envelope.Type != RecordTypeSession {
				return storeState{}, 0, false, corrupt(
					lineNumber,
					recordOffset,
					fmt.Errorf("first record has type %q", envelope.Type),
				)
			}
			if err := decodeRecord(line, &state.header); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateHeader(state.header); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			continue
		}
		switch envelope.Type {
		case RecordTypeTurn:
			var turn Turn
			if err := decodeRecord(line, &turn); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateNode(
				turn.ID,
				turn.ParentID,
				state.leafID,
				state.index.recordIDs,
				state.index.nodeTypes,
			); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			state.retainTurn(turn)
		case RecordTypeCompaction:
			var compaction Compaction
			if err := decodeRecord(line, &compaction); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateNode(
				compaction.ID,
				compaction.ParentID,
				state.leafID,
				state.index.recordIDs,
				state.index.nodeTypes,
			); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateCompactionBoundary(
				compaction,
				state.index.nodeTypes,
				state.index.parents,
				state.index.compactions,
			); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			state.retainCompaction(compaction)
		case RecordTypeLeaf:
			var leaf Leaf
			if err := decodeRecord(line, &leaf); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			if err := validateLeaf(
				leaf,
				state.leafID,
				state.index.recordIDs,
				state.index.nodeTypes,
			); err != nil {
				return storeState{}, 0, false, corrupt(lineNumber, recordOffset, err)
			}
			state.retainLeaf(leaf)
		default:
			return storeState{}, 0, false, corrupt(
				lineNumber,
				recordOffset,
				fmt.Errorf("record has unsupported type %q", envelope.Type),
			)
		}
	}
	if lineNumber == 0 {
		return storeState{}, 0, false, corrupt(1, 0, errors.New("missing session header"))
	}
	return state, validBytes, false, nil
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
	return jsonutil.DecodeStrict(data, target)
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

func corrupt(line int, offset int64, cause error) error {
	return &CorruptionError{Line: line, Offset: offset, Cause: cause}
}
