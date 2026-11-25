package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// A fork starts with the parent's conversation, system prompt, tool
// definitions and model, so its first request repeats the parent's last one
// and reuses its prompt cache. What a fresh child reads in its system prompt
// a fork reads in its first user message, before the task.

// forkPrompt opens a fork's first message.
const forkPrompt = `[AICE: forked subagent; this message is from the agent above, not the user]

Everything above is the conversation of the agent that started you, copied into this new subagent. You are its fork, working on the one task below for it: do it directly and completely within its scope, and do not continue the conversation above or carry out its other plans. If you notice something outside the task, note it in one sentence instead of doing it. You cannot start or message subagents, ask the user or run background tasks; those tools fail here. Stop once you can answer the task with evidence; do not keep exploring for completeness. No user can answer you: make reasonable assumptions, state them, and state what you could not resolve. Your final message is the only thing the agent that started you receives: start with the answer or outcome, then the evidence behind it (names, file paths with lines, commands and their results), then what you could not verify. If you changed files, list them and the checks you ran. Keep the report as short as its evidence allows; do not narrate your steps or recap code you merely read.`

// exploreForkPrompt follows for an explore fork, whose definitions still
// list the write tools.
const exploreForkPrompt = `

You change nothing: file writes, bash commands not known to be read-only and MCP tools are denied, so report what should change instead.`

// forkNoMCPPrompt follows for a fork without MCP, whose system prompt still
// describes the parent's MCP servers.
const forkNoMCPPrompt = `

MCP tools are unavailable to you here, though the system prompt describes them: report what you would need from them instead.`

// forkTask introduces the task after the fork's sections.
const forkTask = "\n\n# Task\n\n"

// inheritance is what a fork starts with: the history of the request that
// called the agent tool, and the recorded configurations its assistant
// messages name, which the fork's Session records before them.
type inheritance struct {
	messages []llm.AgentMessage
	configs  []session.RunConfig
}

// inherit takes a fork's inheritance from the calling request; store is the
// parent's Session, if any. A compaction summary becomes the user message
// every request shows for it, which a Session can record.
func inherit(ctx context.Context, store *session.Store) (inheritance, error) {
	history, err := agent.RequestHistory(ctx)
	if err != nil {
		return inheritance{}, fmt.Errorf("a fork needs the conversation of a model request: %w", err)
	}
	if history, err = projectSummaries(history); err != nil {
		return inheritance{}, err
	}
	var configs []session.RunConfig
	for _, message := range history {
		assistant, ok := message.(llm.AssistantMessage)
		if !ok || assistant.RequestConfig == "" || slices.ContainsFunc(configs, func(record session.RunConfig) bool { return record.ID == assistant.RequestConfig }) {
			continue
		}
		record, ok := store.RunConfig(assistant.RequestConfig)
		if !ok {
			return inheritance{}, fmt.Errorf("fork: the parent's Session lacks run config %q", assistant.RequestConfig)
		}
		configs = append(configs, record)
	}
	return inheritance{messages: history, configs: configs}, nil
}

// record writes the inheritance to a fork's new Session.
func (i inheritance) record(ctx context.Context, store *session.Store) error {
	for _, record := range i.configs {
		if err := store.AppendRunConfig(ctx, record); err != nil {
			return err
		}
	}
	for _, message := range i.messages {
		if err := appendSessionMessage(ctx, store, message); err != nil {
			return err
		}
	}
	return nil
}

// projectSummaries replaces compaction summaries with their projection.
func projectSummaries(history []llm.AgentMessage) ([]llm.AgentMessage, error) {
	for index, message := range history {
		if _, ok := message.(llm.CompactionSummaryMessage); !ok {
			continue
		}
		projected, err := llm.AgentMessagesToMessages([]llm.AgentMessage{message})
		if err != nil || len(projected) != 1 {
			return nil, fmt.Errorf("fork: project compaction summary: %v", err)
		}
		history[index] = projected[0]
	}
	return history, nil
}

// forkTools lists the parent's tools in the parent's order, each with the
// parent's definition. own are the tools the fork may use; any other keeps
// its definition and fails when called.
func forkTools(parent, own []agent.Tool) []agent.Tool {
	usable := make(map[string]agent.Tool, len(own))
	for _, current := range own {
		usable[current.Definition().Name] = current
	}
	tools := make([]agent.Tool, 0, len(parent))
	for _, current := range parent {
		definition := current.Definition()
		mine, ok := usable[definition.Name]
		switch {
		case !ok:
			tools = append(tools, refusedTool{definition: definition})
		case definition.Name == "bash" && !reflect.DeepEqual(mine.Definition(), definition):
			tools = append(tools, foregroundBash{Tool: mine, definition: definition})
		case !reflect.DeepEqual(mine.Definition(), definition):
			tools = append(tools, definedAs{Tool: mine, definition: definition})
		default:
			tools = append(tools, mine)
		}
	}
	return tools
}

// refusedTool is a parent tool a fork may not use.
type refusedTool struct{ definition llm.ToolDefinition }

func (t refusedTool) Definition() llm.ToolDefinition { return t.definition }

func (t refusedTool) Execute(context.Context, llm.ToolCall) (llm.ToolResult, error) {
	return llm.ToolResult{}, fmt.Errorf("tool %q is not available to a subagent; report what you need instead", t.definition.Name)
}

// definedAs runs a fork's own tool under the parent's definition of it.
type definedAs struct {
	agent.Tool
	definition llm.ToolDefinition
}

func (t definedAs) Definition() llm.ToolDefinition { return t.definition }

// foregroundBash runs the fork's foreground bash under the parent's
// definition, which offers run_in_background.
type foregroundBash struct {
	agent.Tool
	definition llm.ToolDefinition
}

func (t foregroundBash) Definition() llm.ToolDefinition { return t.definition }

func (t foregroundBash) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &arguments); err == nil {
		if raw, ok := arguments["run_in_background"]; ok {
			var background bool
			if json.Unmarshal(raw, &background) == nil && background {
				return llm.ToolResult{}, fmt.Errorf("tool %q: a subagent cannot start background tasks; run the command in the foreground", "bash")
			}
			delete(arguments, "run_in_background")
			if call.Arguments, err = json.Marshal(arguments); err != nil {
				return llm.ToolResult{}, err
			}
		}
	}
	return t.Tool.Execute(ctx, call)
}
