package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/classify"
)

func TestAutoModeDefaultsPrintsTemplate(t *testing.T) {
	t.Parallel()
	command := newAutoModeCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"defaults"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range append([]string{classify.PromptVersion, classify.DefaultBlockRules[0], classify.DefaultAllowExceptions[0]}, "auto_mode.block") {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if strings.Count(text, "\n- ") != len(classify.DefaultBlockRules)+len(classify.DefaultAllowExceptions) {
		t.Errorf("unexpected list size:\n%s", text)
	}
}
