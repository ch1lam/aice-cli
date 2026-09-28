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

func TestToolRoundFingerprintIgnoresStructuredSourceEncoding(t *testing.T) {
	t.Parallel()
	round := func(source string) ModelRound {
		call := llm.ToolCall{ID: "call", Name: "inspect", Arguments: json.RawMessage(`{}`)}
		return ModelRound{
			Assistant:   llm.AssistantMessage{Role: llm.RoleAssistant, API: "a", Provider: "p", ModelID: "m", StopReason: llm.StopReasonToolUse, Content: []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &call}}},
			ToolResults: []llm.ToolResultMessage{{Role: llm.RoleToolResult, ToolCallID: call.ID, ToolName: call.Name, Timestamp: 1, Details: &llm.ToolResultDetails{State: llm.ExecutionReturned, StructuredContent: json.RawMessage(source)}}},
		}
	}
	baseline, err := toolRoundFingerprint(round(`{"n":9007199254740993,"label":"<tag>"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source string
		changed      bool
	}{
		{"whitespace", " { \"n\": 9007199254740993, \"label\": \"<tag>\" }\n", false},
		{"HTML escaping", `{"n":9007199254740993,"label":"\u003ctag\u003e"}`, false},
		{"different large number", `{"n":9007199254740994,"label":"<tag>"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toolRoundFingerprint(round(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if (got != baseline) != tc.changed {
				t.Fatal("storage encoding changed progress detection")
			}
		})
	}
}
