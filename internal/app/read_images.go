package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func bindImageReader(tools []agent.Tool, workspace *tool.Workspace, options tool.ReadOptions) error {
	for index, current := range tools {
		if current.Definition().Name != "read" {
			continue
		}
		reader, err := tool.NewRead(workspace, options)
		if err != nil {
			return err
		}
		tools[index] = reader
		return nil
	}
	return nil
}

func (s *interactiveSession) lookupImage(ctx context.Context, id string) (llm.ImageContent, error) {
	s.conversation.historySyncMu.Lock()
	defer s.conversation.historySyncMu.Unlock()
	return s.conversation.store.Image(ctx, id)
}

func (s *interactiveSession) canReadImages() bool {
	return slices.Contains(s.settingsSnapshot().model.InputModalities, llm.InputModalityImage)
}

// Stateless print runs retain image sources for the invocation even when the
// model context is compacted. Durable runs resolve directly from their Store.
func imageFromMessages(messages []llm.AgentMessage, id string) (llm.ImageContent, error) {
	for _, message := range messages {
		var content []llm.ContentPart
		switch value := message.(type) {
		case llm.UserMessage:
			content = value.Content
		case llm.ToolResultMessage:
			content = value.Content
		}
		for _, part := range content {
			if part.Image != nil && part.Image.ID == id {
				return part.Image.Clone(), nil
			}
		}
	}
	return llm.ImageContent{}, fmt.Errorf("saved image %q was not found", id)
}
