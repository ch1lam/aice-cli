package session_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/session"
)

func TestMessageHistory600Entries(t *testing.T) {
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	messages := toolMessages()[:3]
	start := time.Now()
	for round := range 200 {
		for position, message := range messages {
			appendMessages(t, store, fmt.Sprintf("round-%d-message-%d", round, position), message)
			contextMessages, err := session.BuildContext(snapshotOf(t, store))
			if position == 1 {
				if !errors.Is(err, session.ErrIncompleteGroup) {
					t.Fatalf("pending group: %v", err)
				}
			} else if err != nil || len(contextMessages) != round*3+position+1 {
				t.Fatalf("round=%d position=%d context=%d error=%v", round, position, len(contextMessages), err)
			}
		}
	}
	t.Logf("200 tool rounds / 600 durable messages, snapshot and BuildContext after each: %s", time.Since(start))
}
