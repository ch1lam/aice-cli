package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/config"
)

// Inspect the real command declarations without executing an application,
// loading configuration, provisioning helpers, or contacting a provider.
func documentationCommand(t *testing.T) *cobra.Command {
	t.Helper()
	command, err := cli.NewRootCommand(cli.Dependencies{
		Printer:      &recordingPrinter{},
		Interactor:   &recordingInteractor{},
		Compactor:    &recordingCompactor{},
		Navigator:    &recordingNavigator{},
		Configurator: &recordingConfigurator{},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.InitDefaultHelpFlag()
	command.InitDefaultVersionFlag()
	return command
}

func configurationSection(t *testing.T, heading, next string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "\n"+heading+"\n")
	if !ok {
		t.Fatalf("configuration.md: missing %q", heading)
	}
	section, _, ok = strings.Cut(section, "\n"+next+"\n")
	if !ok {
		t.Fatalf("configuration.md: missing section boundary %q", next)
	}
	return section
}

func TestRootCommandDocumentationFlags(t *testing.T) {
	t.Parallel()
	command := documentationCommand(t)
	section := configurationSection(t, "## Command-line options", "## Agent Skills")
	pattern := regexp.MustCompile(`(?m)^--([a-z][a-z-]*)(?:, -([a-z]))?`)
	documented := make(map[string]bool)
	for _, match := range pattern.FindAllStringSubmatch(section, -1) {
		name := match[1]
		if documented[name] {
			t.Errorf("configuration.md: duplicate flag --%s", name)
		}
		documented[name] = true
		flag := command.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("configuration.md: unknown flag --%s", name)
			continue
		}
		if match[2] != flag.Shorthand {
			t.Errorf("--%s: documented shorthand %q, Cobra has %q", name, match[2], flag.Shorthand)
		}
	}
	command.Flags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Hidden && !documented[flag.Name] {
			t.Errorf("configuration.md: missing flag --%s", flag.Name)
		}
	})
}

func TestRootCommandDocumentationRunLimits(t *testing.T) {
	t.Parallel()
	command := documentationCommand(t)
	section := configurationSection(t, "### Run limits", "### Interactive persistence and multiple instances")
	// Each of the four table cells starts with a code span; prose may change.
	code := regexp.MustCompile("`([^`]+)`")
	rows := make(map[string][]string)
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `--") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 4 {
			t.Fatalf("configuration.md: expected four run-limit cells in %q", line)
		}
		values := make([]string, len(cells))
		for i, cell := range cells {
			match := code.FindStringSubmatch(cell)
			if match == nil {
				t.Fatalf("configuration.md: missing code span in %q", cell)
			}
			values[i] = match[1]
		}
		name := strings.TrimPrefix(strings.Fields(values[0])[0], "--")
		if _, exists := rows[name]; exists {
			t.Fatalf("configuration.md: duplicate run-limit row --%s", name)
		}
		rows[name] = values
	}
	for _, name := range []string{"max-turns", "run-token-budget", "run-timeout", "run-no-progress-limit"} {
		t.Run(name, func(t *testing.T) {
			row, ok := rows[name]
			if !ok {
				t.Fatalf("configuration.md: missing run-limit row --%s", name)
			}
			delete(rows, name)
			flag := command.Flags().Lookup(name)
			if flag == nil {
				t.Fatalf("Cobra: missing run-limit flag --%s", name)
			}
			key := strings.ReplaceAll(name, "-", "_")
			for _, definition := range config.SettingDefinitions() {
				if string(definition.ID) != key {
					continue
				}
				wantDefault := strconv.FormatInt(definition.Default.Int, 10)
				if definition.Kind == config.DurationValue {
					wantDefault = definition.Default.Duration.String()
				}
				if flag.DefValue != wantDefault {
					t.Errorf("--%s: Cobra default %q, schema default %q", name, flag.DefValue, wantDefault)
				}
				if row[1] != key || row[2] != definition.Environment || row[3] != wantDefault {
					t.Errorf("configuration.md: --%s has key/env/default %q; schema requires %q", name,
						row[1:], []string{key, definition.Environment, wantDefault})
				}
				return
			}
			t.Fatalf("schema: missing setting %s", key)
		})
	}
	for name := range rows {
		t.Errorf("configuration.md: unchecked run-limit row --%s; extend the test when adding a limit", name)
	}
}
