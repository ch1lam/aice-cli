package tui

import (
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

type SlashCommand = interaction.Command
type SlashCommandMenu = interaction.CommandMenu
type SlashCommandOption = interaction.CommandOption
type SlashCommandRequest = interaction.CommandRequest
type SlashCommandRunner = interaction.CommandRunner

func localSlashCommands() []SlashCommand {
	return []SlashCommand{
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
	}
}

func slashCommandCatalog(external []SlashCommand) []SlashCommand {
	local := localSlashCommands()
	commands := make(
		[]SlashCommand,
		0,
		len(local)+len(external),
	)
	seen := make(map[string]struct{}, cap(commands))
	appendCommand := func(command SlashCommand) {
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

func normalizeSlashCommandMenu(menu *SlashCommandMenu) *SlashCommandMenu {
	if menu == nil {
		return nil
	}

	normalized := &SlashCommandMenu{
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

func parseSlashCommand(value string) (SlashCommandRequest, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/") {
		return SlashCommandRequest{}, false
	}
	body := strings.TrimPrefix(value, "/")
	nameEnd := strings.IndexAny(body, " \t\r\n")
	if nameEnd < 0 {
		return SlashCommandRequest{
			Name: strings.ToLower(body),
		}, true
	}
	return SlashCommandRequest{
		Name:      strings.ToLower(body[:nameEnd]),
		Arguments: strings.TrimSpace(body[nameEnd:]),
	}, true
}

func findSlashCommand(
	commands []SlashCommand,
	name string,
) (SlashCommand, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return SlashCommand{}, false
}

func matchingSlashCommands(
	commands []SlashCommand,
	value string,
) []SlashCommand {
	value = strings.TrimLeft(value, " \t")
	if !strings.HasPrefix(value, "/") {
		return nil
	}
	query := strings.TrimPrefix(value, "/")
	if strings.ContainsAny(query, " \t\r\n") {
		return nil
	}
	query = strings.ToLower(query)

	matches := make([]SlashCommand, 0, len(commands))
	for _, command := range commands {
		if strings.HasPrefix(command.Name, query) {
			matches = append(matches, command)
		}
	}
	return matches
}

func slashCommandUsage(command SlashCommand) string {
	usage := "/" + command.Name
	if command.ArgumentHint != "" {
		usage += " " + command.ArgumentHint
	}
	return usage
}

func slashCommandHelp(commands []SlashCommand) string {
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
