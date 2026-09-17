package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// Read loads a fixed-size view of the file's complete prefix without
// repairing its tail or recovering tool calls. The returned data is owned by
// the caller. Browsing must never acquire a writable Store.
func Read(ctx context.Context, path string) (Snapshot, bool, error) {
	if err := validateContext(ctx); err != nil {
		return Snapshot{}, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Snapshot{}, false, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Snapshot{}, false, errors.Join(err, fmt.Errorf("session: expected a regular file"), file.Close())
	}
	// Another process may be appending. Do not follow its growing tail forever.
	state, _, incomplete, readErr := replayRecords(ctx, io.LimitReader(file, info.Size()))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return Snapshot{}, false, err
	}
	return Snapshot{
		Header: state.header, Messages: state.messages, Compactions: state.compactions,
		LeafMoves: state.leafMoves, Order: state.order, LeafID: state.leafID,
	}, incomplete, nil
}
