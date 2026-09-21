package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/evidence"
)

func testBundle(t *testing.T) *evidence.Bundle {
	t.Helper()
	source, err := evidence.NewSource("https://example.com/a", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	return &evidence.Bundle{
		Sources:     []evidence.Source{source},
		Items:       []evidence.Evidence{{SourceID: source.ID, Kind: evidence.KindDocument, Text: "doc", Format: evidence.FormatMarkdown, Acquisition: evidence.AcquisitionHTTPFetch, RetrievedAt: 3, ReturnedBytes: 3}},
		Diagnostics: evidence.Diagnostics{Warnings: []string{"w"}},
	}
}

func TestToolResultEvidenceCloneValidateAndEncode(t *testing.T) {
	t.Parallel()
	bundle := testBundle(t)
	result := ToolResult{CallID: "c1", Name: "web_fetch", Content: []ContentPart{NewTextContent("text").Part()}, Evidence: bundle}
	message, err := NewToolResultMessage(result)
	if err != nil {
		t.Fatal(err)
	}
	if message.Evidence == bundle {
		t.Fatal("message shares the caller's bundle pointer")
	}
	bundle.Items[0].Text = "changed"
	bundle.Diagnostics.Warnings[0] = "changed"
	if message.Evidence.Items[0].Text != "doc" || message.Evidence.Diagnostics.Warnings[0] != "w" {
		t.Fatal("message evidence aliases caller data")
	}

	cloned, err := CloneAgentMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	copied := cloned.(ToolResultMessage)
	copied.Evidence.Sources[0].Title = "mutated"
	copied.Evidence.Items[0].Text = "mutated"
	if message.Evidence.Sources[0].Title != "A" || message.Evidence.Items[0].Text != "doc" {
		t.Fatal("clone shares evidence with original")
	}

	encoded, err := MarshalAgentMessages([]AgentMessage{message})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"evidence"`) || strings.Contains(string(encoded), `"reported_cost"`) {
		t.Fatalf("encoded = %s", encoded)
	}
	decoded, err := UnmarshalAgentMessages(encoded)
	if err != nil {
		t.Fatal(err)
	}
	restored := decoded[0].(ToolResultMessage)
	if restored.Evidence == nil || restored.Evidence.Items[0].Text != "doc" || restored.Evidence.Sources[0].ID != message.Evidence.Sources[0].ID {
		t.Fatalf("decoded = %+v", restored.Evidence)
	}

	// Legacy records without the field decode to nil evidence.
	legacy, err := UnmarshalAgentMessages([]byte(`[{"role":"toolResult","tool_call_id":"c1","content":[{"type":"text","text":"x"}],"timestamp":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	if legacy[0].(ToolResultMessage).Evidence != nil {
		t.Fatal("legacy record acquired evidence")
	}

	// Invalid evidence is rejected at message construction.
	broken := testBundle(t)
	broken.Items[0].SourceID = "missing"
	if _, err := NewToolResultMessage(ToolResult{CallID: "c1", Content: []ContentPart{NewTextContent("x").Part()}, Evidence: broken}); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("invalid evidence accepted: %v", err)
	}
	oversized := testBundle(t)
	oversized.Items[0].Text = strings.Repeat("x", evidence.MaxBundleBytes)
	if _, err := NewToolResultMessage(ToolResult{CallID: "c1", Content: []ContentPart{NewTextContent("x").Part()}, Evidence: oversized}); err == nil {
		t.Fatal("oversized evidence accepted")
	}

	// Nested tool-result parts (assistant-side projections) clone evidence too.
	parts := cloneContentParts([]ContentPart{{Type: ContentTypeToolResult, ToolResult: &result}})
	parts[0].ToolResult.Evidence.Items[0].Text = "nested mutation"
	if result.Evidence.Items[0].Text == "nested mutation" {
		t.Fatal("nested clone aliases evidence")
	}
	var generic map[string]any
	if err := json.Unmarshal(encoded[1:len(encoded)-1], &generic); err != nil {
		t.Fatal(err)
	}
	if _, ok := generic["evidence"].(map[string]any)["sources"]; !ok {
		t.Fatalf("evidence shape = %v", generic["evidence"])
	}
}
