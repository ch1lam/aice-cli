package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ch1lam/aice-cli/internal/session"
)

// SessionStats reads each Session file without a lock or tail repair, so it
// also inspects Sessions recorded on other hosts, such as evaluation trials.
func (a *application) SessionStats(ctx context.Context, paths []string, output io.Writer) error {
	if output == nil {
		return fmt.Errorf("app: output is required")
	}
	encoder := json.NewEncoder(output)
	for _, path := range paths {
		snapshot, incomplete, err := session.Read(ctx, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := encoder.Encode(struct {
			Path string `json:"path"`
			// Incomplete reports an unfinished final record that was ignored.
			Incomplete bool `json:"incomplete,omitempty"`
			session.Stats
		}{path, incomplete, session.ComputeStats(snapshot)}); err != nil {
			return err
		}
	}
	return nil
}
