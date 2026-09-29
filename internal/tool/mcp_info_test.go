package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

type mcpInfoBackendFunc func(context.Context, string) (MCPServerInfo, error)

func (f mcpInfoBackendFunc) ServerInfo(ctx context.Context, key string) (MCPServerInfo, error) {
	return f(ctx, key)
}

func infoSource() MCPServerInfo {
	return MCPServerInfo{Service: "user:docs", Source: "user:/settings.json", Fingerprint: strings.Repeat("f", 64), Info: mcpclient.Info{Name: "fixture", Version: "1", ProtocolVersion: "2025-11-25", Tools: true, Prompts: true}}
}

func TestMCPInfoPagesRedactedInstructionsAndBindsRevision(t *testing.T) {
	source := infoSource()
	source.Secrets = []string{"secret-token"}
	source.Info.Instructions = strings.Repeat("中", 170) + "secret-token" + strings.Repeat("tail🙂", 220)
	source.Info.Name = "secret-token server"
	reader, _ := NewMCPInfo(mcpInfoBackendFunc(func(_ context.Context, key string) (MCPServerInfo, error) {
		if key != source.Service {
			t.Fatal("wrong service")
		}
		return source, nil
	}))
	var combined strings.Builder
	offset, revision := 0, ""
	for {
		args, _ := json.Marshal(MCPInfoRequest{Service: source.Service, Offset: offset, Length: 513, Revision: revision})
		result, err := reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: args})
		if err != nil || result.IsError || len(result.Content) != 2 {
			t.Fatal("read failed", err, result)
		}
		var view MCPInfoView
		if json.Unmarshal([]byte(result.Content[0].Text), &view) != nil || !view.Redacted || view.Source != source.Source || view.Fingerprint != source.Fingerprint || !strings.Contains(view.Notice, "Untrusted") || !strings.Contains(view.Notice, "unsupported") {
			t.Fatal("missing provenance/trust boundary")
		}
		if !utf8.ValidString(result.Content[1].Text) || len(result.Content[1].Text) > 513 || strings.Contains(result.Content[0].Text+result.Content[1].Text, "secret-token") {
			t.Fatal("invalid or unredacted page")
		}
		combined.WriteString(result.Content[1].Text)
		if revision != "" && view.Revision != revision {
			t.Fatal("paging changed revision")
		}
		revision = view.Revision
		if view.Complete {
			break
		}
		if view.NextOffset == nil || *view.NextOffset <= offset {
			t.Fatal("page did not advance")
		}
		offset = *view.NextOffset
	}
	if combined.String() != strings.ReplaceAll(source.Info.Instructions, "secret-token", "[credential redacted]") {
		t.Fatal("instructions lost during paging")
	}
	source.Info.Instructions += "changed"
	args, _ := json.Marshal(MCPInfoRequest{Service: source.Service, Revision: revision, Offset: 3, Length: 4})
	result, _ := reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: args})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "changed") {
		t.Fatal("mixed revisions")
	}
}

func TestMCPInfoRejectsInvalidPagesAndSafeFailures(t *testing.T) {
	source := infoSource()
	source.Info.Instructions = "中文"
	calls := 0
	reader, _ := NewMCPInfo(mcpInfoBackendFunc(func(context.Context, string) (MCPServerInfo, error) { calls++; return source, nil }))
	for _, args := range []string{`{}`, `{"service":"user:docs","offset":1}`, `{"service":"user:docs","length":8193}`, `{"service":"user:docs","length":-1}`, `{"service":"user:docs","offset":16777217,"revision":"x"}`} {
		result, _ := reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(args)})
		if !result.IsError {
			t.Fatal("invalid page accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid selector accessed backend")
	}
	for _, args := range []string{`{"service":"user:docs","length":1}`, `{"service":"user:docs","offset":1,"revision":"stale"}`} {
		result, _ := reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(args)})
		if !result.IsError {
			t.Fatal("invalid text boundary accepted")
		}
	}
	source.Info.Instructions = string([]byte{0xff})
	result, _ := reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(`{"service":"user:docs"}`)})
	if !result.IsError {
		t.Fatal("invalid UTF-8 accepted")
	}
	source.Info.Instructions = strings.Repeat("a", (16<<20)+1)
	result, _ = reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(`{"service":"user:docs"}`)})
	if !result.IsError {
		t.Fatal("oversized instructions accepted")
	}
	failing, _ := NewMCPInfo(mcpInfoBackendFunc(func(context.Context, string) (MCPServerInfo, error) {
		return MCPServerInfo{}, errors.New("private error and token")
	}))
	result, _ = failing.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(`{"service":"user:docs"}`)})
	if !result.IsError || strings.Contains(result.Content[0].Text, "private") {
		t.Fatal("backend error leaked")
	}
}

func TestMCPInfoPreviewsHaveAggregateBudget(t *testing.T) {
	response := ToolSearchResult{}
	for range 6 {
		source := infoSource()
		source.Info.Instructions = strings.Repeat("<", 3000)
		source.Source = "user:/" + strings.Repeat("x", 1800)
		response.ServerDetails = append(response.ServerDetails, source)
	}
	addMCPInfoPreviews(&response)
	data, _ := json.Marshal(response.Services)
	if len(data) > 8<<10 || len(response.Services) == 0 || len(response.Services) >= 5 || len(response.Notices) != 1 {
		t.Fatal("preview aggregate bound", len(data), len(response.Services))
	}
	serialized, _ := json.Marshal(response)
	if strings.Contains(string(serialized), "ServerDetails") || strings.Contains(string(serialized), strings.Repeat("x", 2000)) {
		t.Fatal("raw info escaped preview boundary")
	}
	for _, view := range response.Services {
		if len(view.Instructions) > 512 || view.Complete || view.NextOffset == nil {
			t.Fatal("preview lost page continuation")
		}
	}
}

func TestMCPInfoEmptyTextClippingAndSourceMismatch(t *testing.T) {
	source := infoSource()
	source.Info.Name = strings.Repeat("n", 1000)
	view, err := mcpInfoView(source, MCPInfoRequest{Length: 1024})
	if err != nil || !view.Complete || view.NextOffset != nil || view.TotalBytes != 0 || !view.MetadataClipped || len(view.Name) != 256 {
		t.Fatal("empty text or metadata clipping", err)
	}
	reader, _ := NewMCPInfo(mcpInfoBackendFunc(func(context.Context, string) (MCPServerInfo, error) { return source, nil }))
	result, _ := reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(`{"service":"project:docs"}`)})
	if !result.IsError {
		t.Fatal("mismatched info source admitted")
	}
	source.Secrets = []string{"user:docs"}
	if _, err := mcpInfoView(source, MCPInfoRequest{Length: 1024}); err == nil {
		t.Fatal("credential-bearing identity disclosed")
	}
}

func TestMCPInfoPreviewRespectsRemainingSearchBudget(t *testing.T) {
	source := infoSource()
	source.Info.Instructions = strings.Repeat("x", 1000)
	source.Source = "user:/" + strings.Repeat("p", 1800)
	response := ToolSearchResult{Entries: []ToolSearchEntry{{ID: "tool", Description: strings.Repeat("d", 30<<10)}}, ServerDetails: []MCPServerInfo{source}, Complete: true}
	addMCPInfoPreviews(&response)
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > 32<<10 || len(response.Services) != 0 || len(response.Notices) != 1 {
		t.Fatal("optional preview broke valid search size", len(encoded), err)
	}
}

func TestMCPInfoOmissionNoticeCannotOverflowSearch(t *testing.T) {
	source := infoSource()
	source.Info.Instructions = "usage"
	response := ToolSearchResult{Entries: []ToolSearchEntry{{ID: "tool"}}, ServerDetails: []MCPServerInfo{source}, Complete: true}
	base, _ := json.Marshal(response)
	response.Entries[0].Description = strings.Repeat("d", (32<<10)-len(base)-10)
	addMCPInfoPreviews(&response)
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > 32<<10 || len(response.Services) != 0 || len(response.Notices) != 0 {
		t.Fatal("notice overflowed existing search result", len(encoded), err)
	}
}

func TestMCPInfoUnsupportedAdapterIsNotConnectionFailure(t *testing.T) {
	reader, err := NewMCPInfo(mcpInfoBackendFunc(func(context.Context, string) (MCPServerInfo, error) {
		return MCPServerInfo{}, mcpclient.ErrUnsupported
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(`{"service":"managed:cua"}`)})
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "does not expose server information") || strings.Contains(result.Content[0].Text, "inspect connection approval") {
		t.Fatal("unsupported adapter misreported as connection failure", result, err)
	}
}
