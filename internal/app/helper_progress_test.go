package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/charmbracelet/x/ansi"
)

func TestHelperProgress(t *testing.T) {
	for _, name := range []string{"terminal", "redirected"} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			p := newHelperProgressPrinter(&output)
			p.terminal = name == "terminal"
			for _, count := range []int64{0, 100} {
				if err := p.Report(deps.Progress{Helper: "agent-browser", Version: "0.37.1", Downloaded: count, Total: 100}); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			got := ansi.Strip(output.String())
			if strings.Count(got, "downloading agent-browser") != 1 {
				t.Fatal(got)
			}
			if p.terminal && !strings.Contains(got, "100%") {
				t.Fatal(got)
			}
			if !p.terminal && strings.ContainsAny(got, "\r%") {
				t.Fatal(got)
			}
		})
	}
}
