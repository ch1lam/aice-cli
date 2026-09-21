package agent

import (
	"encoding/json"
	"testing"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Web results carry operational metadata (retrieval time, upstream request
// ID, duration) that must not count as progress, while a real change in the
// returned content still does.
func TestToolRoundFingerprintIgnoresEvidenceOperationalFields(t *testing.T) {
	t.Parallel()
	source, err := evidence.NewSource("https://example.com/a", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	round := func(text string, retrievedAt int64, requestID string) ModelRound {
		call := llm.ToolCall{ID: "call-" + requestID, Name: "web_search", Arguments: json.RawMessage(`{"query":"q"}`)}
		assistant := llm.AssistantMessage{Role: llm.RoleAssistant, API: "a", Provider: "p", ModelID: "m", StopReason: llm.StopReasonToolUse,
			Content: []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &call}}}
		result := llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: call.ID, ToolName: "web_search", Timestamp: retrievedAt,
			Content: []llm.ContentPart{llm.NewTextContent(text).Part()},
			Evidence: &evidence.Bundle{
				Sources:     []evidence.Source{source},
				Items:       []evidence.Evidence{{SourceID: source.ID, Kind: evidence.KindExcerpt, Text: text, Format: evidence.FormatText, Acquisition: evidence.AcquisitionSearchService, RetrievedAt: retrievedAt}},
				Diagnostics: evidence.Diagnostics{UpstreamRequestID: requestID, DurationMS: retrievedAt},
			}}
		return ModelRound{Assistant: assistant, ToolResults: []llm.ToolResultMessage{result}}
	}
	first, err := toolRoundFingerprint(round("same results", 1, "req-1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := toolRoundFingerprint(round("same results", 2, "req-2"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("timestamps and request IDs counted as progress")
	}
	changed, err := toolRoundFingerprint(round("new results", 3, "req-3"))
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("changed content not detected as progress")
	}
}
