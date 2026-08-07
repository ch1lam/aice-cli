package openaicompletions

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/streamcore"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestFinishRejectsUnknownContentType(t *testing.T) {
	t.Parallel()

	core := streamcore.NewStream(llm.Model{
		ID:        "kimi-k2.6",
		API:       API,
		Provider:  "opencode-go",
		MaxTokens: 4_096,
	})
	core.Parts.Partial(0, unknownPart{})
	s := &stream{core: core, toolCalls: make(map[int64]*partState)}
	if _, err := s.finish(); err == nil {
		t.Fatal("finish() error = nil, want unknown content type error")
	}
}

type unknownPart struct{}

func (unknownPart) PartialContent() (llm.ContentPart, bool) {
	return llm.ContentPart{}, true
}
