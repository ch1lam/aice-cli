package app

import (
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestCompactionTranscriptBudget(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ window, budget int64 }{
		{-1, 0}, {0, 0}, {1, 1}, {1000, 500}, {4096, 1024},
		{10000, 5452}, {65536, 47104}, {100000, 81568},
	} {
		if got := compactionTranscriptBudget(test.window); got != test.budget {
			t.Fatalf("window=%d budget=%d want=%d", test.window, got, test.budget)
		}
	}
}

func TestCompactionTranscriptTruncationKeepsHeadAndTailWithinBudget(t *testing.T) {
	t.Parallel()
	budget := compactionTranscriptBudget(4096)
	transcript := "HEAD" + strings.Repeat(" middle ", 4000) + "TAIL"
	got := truncateCompactionTranscript(transcript, budget)
	if !strings.HasPrefix(got, "HEAD") || !strings.HasSuffix(got, "TAIL") ||
		!strings.Contains(got, "older transcript omitted") {
		t.Fatal("truncation lost head, tail, or omission marker")
	}
	if tokens := llm.EstimateTextTokens(got); tokens > budget {
		t.Fatalf("truncated tokens=%d budget=%d", tokens, budget)
	}
}
