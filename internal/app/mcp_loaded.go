package app

import (
	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

// mcpLoadedTools is a display projection of the Loop's latest request, not a
// second selected tool set. It cannot resolve, execute or authorize a tool.
type mcpLoadedTools struct {
	counts map[string]int
}

func (s *interactiveSession) beginMCPLoadedTools(catalog *mcpCatalog) (func([]agent.ToolReference), func()) {
	state := &mcpLoadedTools{}
	s.stateMu.Lock()
	s.mcpLoaded = state
	s.stateMu.Unlock()
	observe := func(refs []agent.ToolReference) {
		counts := make(map[string]int)
		if catalog != nil {
			catalog.mu.RLock()
			for _, ref := range refs {
				if entry, ok := catalog.entries[ref.ID]; ok {
					counts[entry.service]++
				}
			}
			catalog.mu.RUnlock()
		}
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		if s.mcpLoaded == state {
			state.counts = counts
		}
	}
	clear := func() {
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		if s.mcpLoaded == state {
			s.mcpLoaded = nil
		}
	}
	return observe, clear
}

// No discovery, generation check, Guard request or transport I/O is needed to
// report what was in the request. Revocation still blocks dispatch immediately;
// the next request replaces this snapshot after the Loop filters stale tools.
func (s *interactiveSession) applyMCPLoadedTools(services []interaction.MCPService) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range services {
		services[i].RunActive = s.mcpLoaded != nil
		services[i].LoadedTools = 0
		if s.mcpLoaded != nil {
			services[i].LoadedTools = s.mcpLoaded.counts[services[i].Key]
		}
	}
}
