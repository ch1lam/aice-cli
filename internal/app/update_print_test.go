package app

import (
	"bytes"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/update"
)

func TestPrintUpdateCheck(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result update.CheckResult
		want   string
	}{
		{"available", update.CheckResult{Current: "1.0.0", Latest: "1.2.0", Available: true, Comparable: true}, "1.0.0 -> 1.2.0"},
		{"prefixed", update.CheckResult{Current: "v1.2.0", Latest: "1.2.0", Comparable: true}, "up to date (v1.2.0)"},
		{"ahead", update.CheckResult{Current: "1.3.0", Latest: "1.2.0", Comparable: true}, "up to date (1.3.0)"},
		{"development", update.CheckResult{Current: "dev", Latest: "1.2.0"}, "aice update --force"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := printUpdateCheck(&output, tt.result); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), tt.want) {
				t.Fatalf("output = %q, want %q", output.String(), tt.want)
			}
		})
	}
}

func TestPrintUpdateResultKeepsInstalledVersion(t *testing.T) {
	var output bytes.Buffer
	if err := printUpdateResult(&output, update.UpdateResult{Current: "1.3.0", Latest: "1.2.0"}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "aice is up to date (1.3.0)\n" {
		t.Fatalf("output = %q", output.String())
	}
}

func TestUpdateProgressPrinter(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(fmt.Sprintf("terminal=%v", terminal), func(t *testing.T) {
			var output bytes.Buffer
			p := newUpdateProgressPrinter(&output)
			p.terminal = terminal
			for _, count := range []int64{0, 50, 100} {
				p.lastDraw = time.Time{}
				if err := p.Report(update.Progress{Current: "1.0.0", Latest: "1.2.0", Downloaded: count, Total: 100}); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Report(update.Progress{Latest: "1.2.0", Verifying: true}); err != nil {
				t.Fatal(err)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			rendered := ansi.Strip(output.String())
			if terminal {
				for _, want := range []string{"0%", "50%", "100%", "\nverifying"} {
					if !strings.Contains(rendered, want) {
						t.Fatalf("missing %q in %q", want, rendered)
					}
				}
			} else if strings.ContainsAny(rendered, "\r%") || strings.Contains(output.String(), "\x1b") {
				t.Fatalf("redirected output has terminal controls: %q", output.String())
			}
		})
	}
}

func TestUpdateProgressUnknownSizeAndFailureCleanup(t *testing.T) {
	var output bytes.Buffer
	p := newUpdateProgressPrinter(&output)
	p.terminal = true
	if err := p.Report(update.Progress{Latest: "1.2.0", Downloaded: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1.0 MiB") || strings.Contains(output.String(), "%") || !strings.HasSuffix(output.String(), "\n") {
		t.Fatalf("output = %q", output.String())
	}
	before := output.String()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if output.String() != before {
		t.Fatal("Close wrote another newline")
	}
}
