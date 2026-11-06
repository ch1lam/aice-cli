package interaction

import (
	"testing"
	"time"
)

func TestCacheHitRateCountsReadsOverAllPromptTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		usage DisplayUsage
		want  float64
		known bool
	}{
		{name: "no cache telemetry", usage: DisplayUsage{InputTokens: 900, OutputTokens: 50}},
		{name: "write only is a known miss", usage: DisplayUsage{InputTokens: 100, CacheWriteTokens: 900}, want: 0, known: true},
		{name: "reads over input reads and writes", usage: DisplayUsage{InputTokens: 100, CacheReadTokens: 800, CacheWriteTokens: 100}, want: 0.8, known: true},
	}
	for _, test := range tests {
		got, known := test.usage.CacheHitRate()
		if got != test.want || known != test.known {
			t.Errorf("%s: CacheHitRate() = %v, %v; want %v, %v", test.name, got, known, test.want, test.known)
		}
	}
}

func TestTokensPerSecondRequiresMeasuredOutput(t *testing.T) {
	t.Parallel()
	if _, ok := (DisplayThroughput{OutputTokens: 10}).TokensPerSecond(); ok {
		t.Fatal("rate known without measured duration")
	}
	if _, ok := (DisplayThroughput{Duration: time.Second}).TokensPerSecond(); ok {
		t.Fatal("rate known without output tokens")
	}
	got, ok := DisplayThroughput{OutputTokens: 300, Duration: 4 * time.Second}.TokensPerSecond()
	if !ok || got != 75 {
		t.Fatalf("TokensPerSecond() = %v, %v; want 75, true", got, ok)
	}
}
