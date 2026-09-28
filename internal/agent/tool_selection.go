package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ErrToolSelection stops preparation without retrying catalog failures as model
// requests. The caller can repair the binding and explicitly start another Run.
var ErrToolSelection = errors.New("agent: cannot prepare selected tools")

const maxSelectionCandidates = 5

// ToolSchemaBudget bounds complete dynamically loaded definitions. Builtins are
// accounted for by the normal request context budget, independently of this cap.
func ToolSchemaBudget(model llm.Model) int64 {
	if model.ContextWindow <= 0 {
		return 4096
	}
	return min(8192, max(1, model.ContextWindow/20))
}

func schemaTokens(d llm.ToolDefinition) int64 {
	return llm.EstimateTextTokens(d.Name) + llm.EstimateTextTokens(d.Description) + llm.EstimateTextTokens(string(d.InputSchema))
}

func (e *runExecution) validateProposal(ctx context.Context, proposal []ToolReference) error {
	if len(proposal) == 0 {
		return nil
	}
	if e.input.Catalog == nil {
		return fmt.Errorf("tool selection requires a run catalog")
	}
	if len(proposal) > maxSelectionCandidates {
		return fmt.Errorf("a search may select at most %d tools", maxSelectionCandidates)
	}
	ids := make([]string, 0, len(proposal))
	wanted := make(map[string]string, len(proposal))
	for _, ref := range proposal {
		if strings.TrimSpace(ref.ID) == "" || ref.Revision == "" {
			return fmt.Errorf("tool selection has an empty identity or revision")
		}
		if _, exists := wanted[ref.ID]; exists {
			return fmt.Errorf("tool selection repeats ID %q", ref.ID)
		}
		wanted[ref.ID] = ref.Revision
		ids = append(ids, ref.ID)
	}
	resolved, err := e.resolveTools(ctx, ids)
	if err != nil {
		return err
	}
	for _, entry := range resolved {
		if wanted[entry.Reference.ID] != entry.Reference.Revision {
			return fmt.Errorf("tool %q changed during discovery; search again", entry.Reference.ID)
		}
		if err := e.input.Catalog.Check(ctx, entry.Reference); err != nil {
			return fmt.Errorf("tool selection invalidated: %w", err)
		}
		if schemaTokens(entry.Tool.Definition()) > ToolSchemaBudget(e.input.Model) {
			return fmt.Errorf("tool %q exceeds the complete schema budget; it was not selected", entry.Reference.ID)
		}
		delete(wanted, entry.Reference.ID)
	}
	if len(wanted) > 0 {
		return fmt.Errorf("a proposed tool is unavailable; search again")
	}
	return nil
}

// resolveTools checks the catalog boundary before definitions can reach a model.
func (e *runExecution) resolveTools(ctx context.Context, ids []string) ([]CatalogTool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	resolved, err := e.input.Catalog.Resolve(ctx, slices.Clone(ids))
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	names := make(map[string]bool, len(resolved))
	for _, entry := range resolved {
		if !allowed[entry.Reference.ID] || entry.Reference.Revision == "" || entry.Tool == nil {
			return nil, errors.New("catalog returned an unrequested, duplicate or incomplete tool")
		}
		delete(allowed, entry.Reference.ID)
		d := entry.Tool.Definition()
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("catalog tool definition: %w", err)
		}
		if _, builtin := e.loop.tools[d.Name]; builtin || names[d.Name] {
			return nil, fmt.Errorf("catalog tool name %q collides with an existing tool", d.Name)
		}
		names[d.Name] = true
	}
	return resolved, nil
}

// refreshTools runs only after a fully recorded round. Resolve produces current
// immutable versions; dispatch separately checks that those versions are live.
func (e *runExecution) refreshTools(ctx context.Context) error {
	if e.input.Catalog == nil {
		return nil
	}
	selected := slices.Clone(e.selected)
	for _, ref := range e.pendingSelection {
		selected = slices.DeleteFunc(selected, func(id string) bool { return id == ref.ID })
		selected = append(selected, ref.ID)
	}
	ids := slices.Clone(e.input.PinnedTools)
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || seen[id] {
			return fmt.Errorf("%w: empty or duplicate pinned ID", ErrToolSelection)
		}
		seen[id] = true
	}
	for _, id := range selected {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	resolved, err := e.resolveTools(ctx, ids)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrToolSelection, err)
	}
	byID := make(map[string]CatalogTool, len(resolved))
	for _, entry := range resolved {
		byID[entry.Reference.ID] = entry
	}
	budget := ToolSchemaBudget(e.input.Model)
	tools := maps.Clone(e.loop.tools)
	definitions := slices.Clone(e.loop.definitions)
	versions := make(map[string]CatalogTool, len(resolved))
	add := func(entry CatalogTool) bool {
		d := entry.Tool.Definition()
		cost := schemaTokens(d)
		if cost > budget {
			return false
		}
		d.InputSchema = slices.Clone(d.InputSchema)
		d.PromptGuidelines = slices.Clone(d.PromptGuidelines)
		budget -= cost
		tools[d.Name] = entry.Tool
		definitions = append(definitions, d)
		versions[d.Name] = entry
		return true
	}
	for _, id := range e.input.PinnedTools {
		entry, exists := byID[id]
		if !exists {
			return fmt.Errorf("%w: pinned tool %q is unavailable", ErrToolSelection, id)
		}
		if !add(entry) {
			return fmt.Errorf("%w: pinned tools exceed the complete schema budget", ErrToolSelection)
		}
		delete(byID, id)
	}
	retained := make([]string, 0, len(selected))
	omitted := false
	// Most recently selected definitions take precedence. Never cut a schema.
	for i := len(selected) - 1; i >= 0; i-- {
		id := selected[i]
		if slices.Contains(e.input.PinnedTools, id) {
			continue
		}
		entry, exists := byID[id]
		if !exists || !add(entry) {
			omitted = true
			continue
		}
		retained = append(retained, id)
	}
	slices.Reverse(retained)
	e.tools, e.definitions, e.catalogTools = tools, definitions, versions
	e.selected, e.pendingSelection = retained, nil
	e.toolNotice = "Only tools in this request's definitions are currently callable. Search results select tools for the next model round; they do not authorize execution."
	if omitted {
		e.toolNotice += " Some earlier selections are unavailable or were evicted to fit the complete schema budget; search again if needed."
	}
	return nil
}

func (e *runExecution) checkToolVersion(ctx context.Context, name string) error {
	if version, ok := e.catalogTools[name]; ok {
		if err := e.input.Catalog.Check(ctx, version.Reference); err != nil {
			return fmt.Errorf("selected tool version is no longer available; search again: %w", err)
		}
	}
	return nil
}
