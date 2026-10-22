package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// bindImageReader replaces the read tool in every given set with one reader
// that can resolve saved images. Settings rebuilds compose from the base set,
// so an interactive Session must bind both its base and published tools.
func bindImageReader(workspace *tool.Workspace, options tool.ReadOptions, toolSets ...[]agent.Tool) error {
	reader, err := tool.NewRead(workspace, options)
	if err != nil {
		return err
	}
	for _, tools := range toolSets {
		for index, current := range tools {
			if current.Definition().Name == "read" {
				tools[index] = reader
			}
		}
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
