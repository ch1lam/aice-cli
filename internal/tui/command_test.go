package tui

import (
	"strings"
	"testing"
)

func TestSlashCommandCatalogKeepsLocalCommandsAndNormalizesExternalCommands(
	t *testing.T,
) {
	t.Parallel()

	commands := slashCommandCatalog([]SlashCommand{
		{Name: "/TREE", Description: "Show tree"},
		{Name: "help", Description: "Conflicting help"},
		{Name: "bad command", Description: "Invalid"},
	})

	local := localSlashCommands()
	if got, want := len(commands), len(local)+1; got != want {
		t.Fatalf("catalog length = %d, want %d: %#v", got, want, commands)
	}
	if got := commands[len(commands)-1].Name; got != "tree" {
		t.Errorf("external command name = %q, want tree", got)
	}
	if got := commands[0].Description; got != local[0].Description {
		t.Errorf("local command was replaced by conflicting external command: %q", got)
	}
}

func TestParseSlashCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		value       string
		wantRequest SlashCommandRequest
		wantSlash   bool
	}{
		{
			name:        "command only",
			value:       "/HELP",
			wantRequest: SlashCommandRequest{Name: "help"},
			wantSlash:   true,
		},
		{
			name:  "command with argument",
			value: "  /checkout   root  ",
			wantRequest: SlashCommandRequest{
				Name:      "checkout",
				Arguments: "root",
			},
			wantSlash: true,
		},
		{
			name:  "ordinary prompt",
			value: "explain /compact",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			request, slash := parseSlashCommand(tt.value)
			if slash != tt.wantSlash {
				t.Fatalf("parseSlashCommand() slash = %v, want %v", slash, tt.wantSlash)
			}
			if request != tt.wantRequest {
				t.Errorf("parseSlashCommand() = %#v, want %#v", request, tt.wantRequest)
			}
		})
	}
}

func TestMatchingSlashCommandsUsesCommandPrefixOnly(t *testing.T) {
	t.Parallel()

	commands := slashCommandCatalog([]SlashCommand{
		{Name: "compact", Description: "Compact Session"},
		{Name: "checkout", Description: "Move leaf"},
	})

	matches := matchingSlashCommands(commands, "/co")
	if len(matches) != 1 || matches[0].Name != "compact" {
		t.Fatalf("matchingSlashCommands() = %#v, want compact", matches)
	}
	if matches := matchingSlashCommands(commands, "/checkout "); len(matches) != 0 {
		t.Fatalf("argument input matches = %#v, want none", matches)
	}
}

func TestSlashCommandHelpIncludesUsageAndDescription(t *testing.T) {
	t.Parallel()

	help := slashCommandHelp([]SlashCommand{{
		Name:         "checkout",
		Description:  "Move the active leaf",
		ArgumentHint: "<entry|root>",
	}})
	for _, want := range []string{
		"/checkout <entry|root>",
		"Move the active leaf",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("slash command help = %q, want %q", help, want)
		}
	}
}
