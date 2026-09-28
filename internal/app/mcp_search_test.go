package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestMCPCatalogLexicalRetrieval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, query, wanted, other string
	}{
		{"word boundary", "cat", "Inspect a cat", "Concatenate text"},
		{"Chinese sentence", "请帮我读取客户资料", "读取客户资料和联系方式", "读取订单和发票"},
		{"English plural", "Read the replies", "Read a reply", "Read a report"},
		{"English derivation", "connect", "Connection details", "Connector installation"},
		{"create synonym", "add inventory item", "Create an inventory item", "Update an inventory item"},
		{"search synonym", "locate sensor", "Search for a sensor", "Read a sensor"},
		{"read synonym", "show invoice", "Read an invoice", "Create an invoice"},
		{"delete synonym", "remove receipt", "Delete a receipt", "Archive a receipt"},
		{"update stays distinct", "update inventory item", "Update an inventory item", "Create an inventory item"},
		{"archive stays distinct", "archive receipt", "Archive a receipt", "Delete a receipt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("wanted", tc.wanted), catalogFixtureTool("other", tc.other)}}
			gate, err := guard.New("", guard.Config{})
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := newMCPCatalog(catalogTestConfig("fixture"), map[string]mcpCatalogConnection{"fixture": client}, gate)
			if err != nil {
				t.Fatal(err)
			}
			defer catalog.Close()
			result, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Query: tc.query, Limit: 5})
			if err != nil || len(result.Selected) == 0 || result.Selected[0].ID != mcpToolID("fixture", "wanted") {
				t.Fatalf("retrieval=%+v err=%v", result, err)
			}
			if tc.name == "word boundary" && len(result.Selected) != 1 {
				t.Fatal("substring was treated as a whole word")
			}
		})
	}
}

func TestMCPCatalogRankingIgnoresRepetitionAndMapOrder(t *testing.T) {
	t.Parallel()
	client := &mcpCatalogFixture{}
	for _, name := range []string{"z", "a", "b", "c", "d", "e"} {
		client.items = append(client.items, catalogFixtureTool(name, "Read service logs and query errors"))
	}
	client.items = append(client.items, catalogFixtureTool("repeat", strings.Repeat("Read service logs and query errors ", 20)))
	gate, err := guard.New("", guard.Config{})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := newMCPCatalog(catalogTestConfig("fixture"), map[string]mcpCatalogConnection{"fixture": client}, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	want := []string{"fixture/tool/a", "fixture/tool/b", "fixture/tool/c", "fixture/tool/d", "fixture/tool/e"}
	for i := range 20 {
		query := "read service query logs errors"
		if i%2 == 1 {
			query = "errors logs query service read get show read"
		}
		result, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Query: query, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, entry := range result.Entries {
			got = append(got, entry.ID)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unstable or repetition-biased ranking: %v", got)
		}
	}
}

func TestMCPCatalogFullToolNamePrecedesProse(t *testing.T) {
	t.Parallel()
	client := &mcpCatalogFixture{items: []mcpclient.Tool{
		catalogFixtureTool("list_apps", "List applications and their windows"),
		catalogFixtureTool("list_windows", "Enumerate open windows"),
	}}
	gate, err := guard.New("", guard.Config{})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := newMCPCatalog(catalogTestConfig("fixture"), map[string]mcpCatalogConnection{"fixture": client}, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	result, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Query: " LIST_WINDOWS ", Limit: 1})
	if err != nil || len(result.Selected) != 1 || result.Selected[0].ID != "fixture/tool/list_windows" {
		t.Fatal("full tool name lost to neighboring description", result, err)
	}
}
