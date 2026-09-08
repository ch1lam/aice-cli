package session

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Image finds retained source content on the active branch, including messages
// removed from model context by compaction. It never exposes a sibling branch.
func (s *Store) Image(ctx context.Context, id string) (llm.ImageContent, error) {
	if s == nil {
		return llm.ImageContent{}, fmt.Errorf("session: saved image %q was not found", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for node := s.leafID; node != ""; node = s.index.parents[node] {
		if err := ctx.Err(); err != nil {
			return llm.ImageContent{}, err
		}
		entry, ok := s.index.messages[node]
		if !ok {
			continue
		}
		var content []llm.ContentPart
		switch message := entry.Message.(type) {
		case llm.UserMessage:
			content = message.Content
		case llm.ToolResultMessage:
			content = message.Content
		}
		for _, part := range content {
			if part.Image != nil && part.Image.ID == id {
				return part.Image.Clone(), nil
			}
		}
	}
	return llm.ImageContent{}, fmt.Errorf("session: saved image %q was not found on the active branch", id)
}
