package tool

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const maxSkillResources = 50

// SkillEntry is the minimum skill data the activation tool needs.
// Callers map from their skill catalog; this package does not import
// internal/skill.
type SkillEntry struct {
	Name string
	Dir  string
	Body string
}

// Skill loads a named Agent Skill's already-parsed instructions.
type Skill struct {
	byName map[string]SkillEntry
	names  []string
}

// NewSkill constructs a skill activation tool. Duplicate names keep the last
// entry; empty names are skipped. Builtin skills have an empty Dir.
func NewSkill(entries []SkillEntry) *Skill {
	byName := make(map[string]SkillEntry, len(entries))
	for _, entry := range entries {
		if entry.Name == "" {
			continue
		}
		byName[entry.Name] = entry
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)
	return &Skill{byName: byName, names: names}
}

// Definition returns the model-facing skill contract. The name parameter is
// constrained to the constructed skill set so the model cannot invent names.
func (s *Skill) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "skill",
		Description: "Load the full instructions for a named Agent Skill. " +
			"Call this when the current task matches a skill description from " +
			"the available skills list in the system prompt.",
		InputSchema:   skillInputSchema(s.names),
		PromptSnippet: "Load Agent Skill instructions on demand",
		PromptGuidelines: []string{
			"When a task matches a skill description in the available skills list, call skill with that name before proceeding.",
		},
	}
}

// Execute returns the named skill's body, wrapped so the model can distinguish
// skill instructions from conversation content. Directory listing is best
// effort and omitted when Dir is empty or unreadable.
func (s *Skill) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	type arguments struct {
		Name string `json:"name"`
	}
	args, err := decodeArguments[arguments](ctx, call, "skill")
	if err != nil {
		return llm.ToolResult{}, err
	}
	entry, ok := s.byName[args.Name]
	if !ok {
		return textResult(call, fmt.Sprintf("unknown skill %q", args.Name), true), nil
	}

	var resources []string
	var truncated bool
	includeResources := false
	if entry.Dir != "" {
		listed, wasTruncated, listErr := listSkillResources(ctx, entry.Dir)
		if listErr != nil {
			if errors.Is(listErr, context.Canceled) || errors.Is(listErr, context.DeadlineExceeded) {
				return llm.ToolResult{}, listErr
			}
		} else {
			resources = listed
			truncated = wasTruncated
			includeResources = true
		}
	}
	return textResult(call, formatSkillContent(entry, resources, truncated, includeResources), false), nil
}

func skillInputSchema(names []string) json.RawMessage {
	if names == nil {
		names = []string{}
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the Agent Skill to load",
				"enum":        names,
			},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return jsonSchema(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`)
	}
	return raw
}

func formatSkillContent(entry SkillEntry, resources []string, truncated, includeResources bool) string {
	var builder strings.Builder
	builder.WriteString("<skill_content name=\"")
	builder.WriteString(xmlEscape(entry.Name))
	builder.WriteString("\">\n")
	builder.WriteString(entry.Body)
	if entry.Body != "" && !strings.HasSuffix(entry.Body, "\n") {
		builder.WriteByte('\n')
	}
	if entry.Dir != "" {
		builder.WriteByte('\n')
		builder.WriteString("Skill directory: ")
		builder.WriteString(entry.Dir)
		builder.WriteByte('\n')
		builder.WriteString("Relative paths in this skill are relative to the skill directory.\n")
		if includeResources {
			builder.WriteString("<skill_resources")
			if truncated {
				builder.WriteString(` truncated="true"`)
			}
			builder.WriteString(">\n")
			for _, resource := range resources {
				builder.WriteString("  <file>")
				builder.WriteString(xmlEscape(resource))
				builder.WriteString("</file>\n")
			}
			builder.WriteString("</skill_resources>\n")
		}
	}
	builder.WriteString("</skill_content>")
	return builder.String()
}

func listSkillResources(ctx context.Context, dir string) ([]string, bool, error) {
	files := []string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" {
			return nil
		}
		files = append(files, rel)
		if len(files) > maxSkillResources {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	slices.Sort(files)
	if len(files) <= maxSkillResources {
		return files, false, nil
	}
	return files[:maxSkillResources], true, nil
}

func xmlEscape(value string) string {
	var builder strings.Builder
	if err := xml.EscapeText(&builder, []byte(value)); err != nil {
		return value
	}
	return builder.String()
}
