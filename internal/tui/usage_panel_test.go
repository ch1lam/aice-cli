package tui

import (
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestUsageFieldsShowCacheHitRateAndOutputSpeed(t *testing.T) {
	t.Parallel()
	values := func(snapshot interaction.UsageSnapshot) map[string]string {
		fields := map[string]string{}
		for _, field := range usageFields(snapshot).Fields {
			fields[field.Label] = field.Value.Text
		}
		return fields
	}

	empty := values(interaction.UsageSnapshot{CostStatus: "Unavailable"})
	if empty["Cache hit rate"] != "No cache tokens reported" ||
		empty["Output speed (main agent)"] != "Not measured since this Session was opened" {
		t.Fatalf("unmeasured rows = %q / %q", empty["Cache hit rate"], empty["Output speed (main agent)"])
	}

	measured := values(interaction.UsageSnapshot{
		CostStatus: "Unavailable",
		Usage:      interaction.DisplayUsage{InputTokens: 200, CacheReadTokens: 1800},
		// The last call missed the cache entirely.
		LastCall:   interaction.DisplayUsage{InputTokens: 400},
		Throughput: interaction.DisplayThroughput{OutputTokens: 420, Duration: 10 * time.Second},
	})
	if got := measured["Cache hit rate"]; got != "90.0% Session · 0.0% last call" {
		t.Fatalf("cache hit rate = %q", got)
	}
	if got := measured["Output speed (main agent)"]; got != "42.0 tok/s · 420 output tokens over 10s of generation (this process)" {
		t.Fatalf("output speed = %q", got)
	}
}

// Subagent runs are counted apart, since their usage is part of the totals.
func TestUsageFieldsShowSubagentShare(t *testing.T) {
	t.Parallel()
	fields := map[string]string{}
	for _, field := range usageFields(interaction.UsageSnapshot{CostStatus: "Unavailable", Delegations: 2,
		DelegatedUsage: interaction.DisplayUsage{InputTokens: 300, OutputTokens: 40}}).Fields {
		fields[field.Label] = field.Value.Text
	}
	if got := fields["Subagents"]; got != "2 runs · 300 input / 40 output tokens (included above)" {
		t.Fatalf("subagents = %q", got)
	}
}
