package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func localSlashCommands() []interaction.Command {
	return []interaction.Command{
		{
			Name:        "help",
			Description: "Show available slash commands",
		},
		{
			Name:        "clear",
			Description: "Clear the visible transcript without changing Session history",
		},
		{
			Name:        "quit",
			Description: "Quit AICE",
		},
		{
			Name:        "desktop",
			Description: "Open Computer Use in Settings",
		},
	}
}

func slashCommandCatalog(external []interaction.Command) []interaction.Command {
	local := localSlashCommands()
	commands := make(
		[]interaction.Command,
		0,
		len(local)+len(external),
	)
	seen := make(map[string]struct{}, cap(commands))
	appendCommand := func(command interaction.Command) {
		command.Name = strings.ToLower(strings.TrimPrefix(
			strings.TrimSpace(command.Name),
			"/",
		))
		command.Description = strings.TrimSpace(command.Description)
		command.ArgumentHint = strings.TrimSpace(command.ArgumentHint)
		command.SecretPrompt = strings.TrimSpace(command.SecretPrompt)
		command.Menu = normalizeSlashCommandMenu(command.Menu)
		if command.Name == "" ||
			strings.ContainsAny(command.Name, "/ \t\r\n") {
			return
		}
		if _, exists := seen[command.Name]; exists {
			return
		}
		seen[command.Name] = struct{}{}
		commands = append(commands, command)
	}
	for _, command := range local {
		appendCommand(command)
	}
	for _, command := range external {
		appendCommand(command)
	}
	return commands
}

func normalizeSlashCommandMenu(menu *interaction.CommandMenu) *interaction.CommandMenu {
	if menu == nil {
		return nil
	}

	normalized := &interaction.CommandMenu{
		Title: strings.TrimSpace(menu.Title),
	}
	for _, option := range menu.Options {
		option.Label = strings.TrimSpace(option.Label)
		option.Description = strings.TrimSpace(option.Description)
		option.Arguments = strings.TrimSpace(option.Arguments)
		option.Menu = normalizeSlashCommandMenu(option.Menu)
		if option.Label == "" {
			continue
		}
		if option.Menu == nil && option.Arguments == "" {
			continue
		}
		normalized.Options = append(normalized.Options, option)
	}
	if normalized.Title == "" || len(normalized.Options) == 0 {
		return nil
	}
	return normalized
}

func parseSlashCommand(value string) (interaction.CommandRequest, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/") {
		return interaction.CommandRequest{}, false
	}
	body := strings.TrimPrefix(value, "/")
	nameEnd := strings.IndexAny(body, " \t\r\n")
	if nameEnd < 0 {
		return interaction.CommandRequest{
			Name: strings.ToLower(body),
		}, true
	}
	return interaction.CommandRequest{
		Name:      strings.ToLower(body[:nameEnd]),
		Arguments: strings.TrimSpace(body[nameEnd:]),
	}, true
}

func findSlashCommand(
	commands []interaction.Command,
	name string,
) (interaction.Command, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return interaction.Command{}, false
}

func matchingSlashCommands(
	commands []interaction.Command,
	value string,
) []interaction.Command {
	value = strings.TrimLeft(value, " \t")
	if !strings.HasPrefix(value, "/") {
		return nil
	}
	query := strings.TrimPrefix(value, "/")
	if strings.ContainsAny(query, " \t\r\n") {
		return nil
	}
	query = strings.ToLower(query)

	type match struct {
		command interaction.Command
		score   int
	}
	var ranked []match
	for _, command := range commands {
		score, _ := fuzzyMatch(command.Name, query)
		if score < 0 && command.SkillName != "" {
			score, _ = fuzzyMatch(command.Description, query)
		}
		if score >= 0 {
			ranked = append(ranked, match{command: command, score: score})
		}
	}
	slices.SortStableFunc(ranked, func(a, b match) int {
		if query == "" && (a.command.SkillName == "") != (b.command.SkillName == "") {
			if a.command.SkillName == "" {
				return -1
			}
			return 1
		}
		return b.score - a.score
	})
	matches := make([]interaction.Command, len(ranked))
	for index, hit := range ranked {
		matches[index] = hit.command
	}
	return matches
}

func slashCommandUsage(command interaction.Command) string {
	usage := "/" + command.Name
	if command.ArgumentHint != "" {
		usage += " " + command.ArgumentHint
	}
	return usage
}

func slashCommandHelp(commands []interaction.Command) string {
	lines := make([]string, 0, len(commands)+1)
	lines = append(lines, "Available slash commands")
	for _, command := range commands {
		lines = append(
			lines,
			fmt.Sprintf(
				"%-28s %s",
				slashCommandUsage(command),
				command.Description,
			),
		)
	}
	return strings.Join(lines, "\n")
}
