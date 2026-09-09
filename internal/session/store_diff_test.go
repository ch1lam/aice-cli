package session_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestDiffMetadataSurvivesSessionReplay(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			store := mustCreate(t, path)
			messages := toolMessages()
			result := messages[2].(llm.ToolResultMessage)
			if !legacy {
				result.Diff = llm.ToolDiff{Text: "@@ -1 +1 @@\n-old\n+new\n", Truncated: true}
			}
			messages[2] = result
			appendMessages(t, store, "read", messages...)
			raw := fileBytes(t, path)
			if strings.Contains(string(raw), `"diff"`) == legacy {
				t.Fatalf("unexpected optional field: %s", raw)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := session.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			history, err := session.BuildContext(snapshotOf(t, reopened))
			if err != nil {
				t.Fatal(err)
			}
			got := history[2].(llm.ToolResultMessage)
			if got.Diff != result.Diff {
				t.Fatalf("replay lost metadata: %+v", got)
			}
		})
	}
}
