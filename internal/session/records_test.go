package session_test

import (
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestTotalUsageIncludesAllTurnsAndCompactions(t *testing.T) {
	t.Parallel()

	snapshot := session.Snapshot{
		Turns: []session.Turn{
			{
				ID: "active-turn",
				Usage: llm.Usage{
					InputTokens:  100,
					OutputTokens: 20,
					TotalTokens:  120,
					Cost:         &llm.Cost{Input: 0.01, Total: 0.01},
				},
			},
			{
				ID: "abandoned-turn",
				Usage: llm.Usage{
					InputTokens:     40,
					OutputTokens:    10,
					CacheReadTokens: 30,
					TotalTokens:     80,
					Cost: &llm.Cost{
						Output:    0.02,
						CacheRead: 0.001,
						Total:     0.021,
					},
				},
			},
		},
		Compactions: []session.Compaction{{
			ID: "compaction",
			Usage: llm.Usage{
				InputTokens:  60,
				OutputTokens: 15,
				TotalTokens:  75,
				Cost:         &llm.Cost{Output: 0.003, Total: 0.003},
			},
		}},
	}

	got := session.TotalUsage(snapshot)
	want := llm.Usage{
		InputTokens:     200,
		OutputTokens:    45,
		CacheReadTokens: 30,
		TotalTokens:     275,
		Cost: &llm.Cost{
			Input:     0.01,
			Output:    0.023,
			CacheRead: 0.001,
			Total:     0.034,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TotalUsage() = %#v, want %#v", got, want)
	}
}
