package session_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestEvidenceMetadataSurvivesSessionReplay(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			store := mustCreate(t, path)
			messages := toolMessages()
			result := messages[2].(llm.ToolResultMessage)
			if !legacy {
				source, err := evidence.NewSource("https://example.com/docs", "Docs 文档", "2024-01-02")
				if err != nil {
					t.Fatal(err)
				}
				result.Evidence = &evidence.Bundle{
					Sources: []evidence.Source{source},
					Items: []evidence.Evidence{{SourceID: source.ID, Kind: evidence.KindExcerpt, Text: "excerpt", Format: evidence.FormatText,
						Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 5, ReturnedBytes: 7}},
					Diagnostics: evidence.Diagnostics{UpstreamRequestID: "req", Warnings: []string{"w"}, ReportedCost: &evidence.Cost{Amount: 0.01, Currency: "USD", Source: "test"}},
				}
			}
			messages[2] = result
			appendMessages(t, store, "web_search", messages...)
			raw := fileBytes(t, path)
			if strings.Contains(string(raw), `"evidence"`) == legacy {
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
			if !reflect.DeepEqual(got.Evidence, result.Evidence) {
				t.Fatalf("replay lost evidence: %+v want %+v", got.Evidence, result.Evidence)
			}
			if legacy && got.Evidence != nil {
				t.Fatal("legacy record acquired evidence")
			}
		})
	}
}
