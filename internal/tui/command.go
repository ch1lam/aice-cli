package tui

import (
	"context"
	"fmt"
	"strings"
)

// SlashCommand describes one command exposed by the interactive TUI.
type SlashCommand struct {
	Name         string
	Description  string
	ArgumentHint string
}

// SlashCommandRequest is one parsed command invocation.
type SlashCommandRequest struct {
	Name      string
	Arguments string
}

// SlashCommandRunner executes application-owned slash commands.
type SlashCommandRunner interface {
	SlashCommands() []SlashCommand
	RunSlashCommand(
		ctx context.Context,
		request SlashCommandRequest,
	) (string, error)
}

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
