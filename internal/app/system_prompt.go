package app

import (
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
)

// buildDefaultSystemPrompt assembles the built-in system prompt following Pi's
// layout: an intro, the available tools with one-line snippets, usage
// guidelines collected from the tools, and the working directory. A custom
// SYSTEM.md replaces this whole prompt, so only the built-in default carries
// the tool list.
func buildDefaultSystemPrompt(tools []agent.Tool, cwd string) string {
	snippets := make([]string, 0, len(tools))
	guidelines := make([]string, 0, len(tools)+3)
	guidelineSet := make(map[string]struct{})
	addGuideline := func(guideline string) {
		guideline = strings.TrimSpace(guideline)
		if guideline == "" {
			return
		}
		if _, exists := guidelineSet[guideline]; exists {
			return
		}
		guidelineSet[guideline] = struct{}{}
		guidelines = append(guidelines, guideline)
	}

	hasBash := false
	hasGrep := false
	hasFind := false
	hasLS := false
	for _, current := range tools {
		definition := current.Definition()
		switch definition.Name {
		case "bash":
			hasBash = true
		case "grep":
			hasGrep = true
		case "find":
			hasFind = true
		case "ls":
			hasLS = true
		}
		if definition.PromptSnippet != "" {
			snippets = append(snippets, "- "+definition.Name+": "+definition.PromptSnippet)
		}
		for _, guideline := range definition.PromptGuidelines {
			addGuideline(guideline)
		}
	}
	if hasBash && !hasGrep && !hasFind && !hasLS {
		addGuideline("Use bash for file operations like ls, rg, find")
	}
	addGuideline("Be concise in your responses")
	addGuideline("Show file paths clearly when working with files")

	toolsList := "(none)"
	if len(snippets) > 0 {
		toolsList = strings.Join(snippets, "\n")
	}
	promptCWD := strings.ReplaceAll(cwd, "\\", "/")

	return fmt.Sprintf(`%s

Available tools:
%s

Guidelines:
%s

Current working directory: %s`,
		defaultSystemPrompt,
		toolsList,
		strings.Join(guidelines, "\n"),
		promptCWD,
	)
}
