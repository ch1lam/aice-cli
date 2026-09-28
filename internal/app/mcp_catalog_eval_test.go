package app

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type mcpSearchTask struct {
	Case, Service, Name, Description, Query string
}

type mcpSearchCaseResult struct {
	Case     string   `json:"case"`
	Query    string   `json:"query"`
	Expected string   `json:"expected"`
	TopFive  []string `json:"top_five"`
	Hit      bool     `json:"hit"`
	Exact    bool     `json:"exact"`
}

type mcpSearchEvaluation struct {
	Tools int                   `json:"tools"`
	Hits  int                   `json:"hits"`
	Exact int                   `json:"exact"`
	Cases []mcpSearchCaseResult `json:"cases"`
}

// This is an opt-in, deterministic retrieval evaluation, not a provider or
// task-completion test. Natural-language misses remain visible in its report;
// the plan's 95% tuning target is not replaced by successful exact-ID lookups.
func TestMCPCatalogRetrievalEvaluation(t *testing.T) {
	if os.Getenv("AICE_MCP_SEARCH_EVAL") != "1" {
		t.Skip("set AICE_MCP_SEARCH_EVAL=1 for the 50-task retrieval evaluation")
	}
	tasks := loadMCPSearchTasks(t)
	for _, size := range []int{50, 500, 2000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			clients, keys := mcpSearchFixtures(tasks, size)
			connections := make(map[string]mcpCatalogConnection)
			for key, client := range clients {
				connections[key] = client
			}
			gate, err := guard.New("", guard.Config{})
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := newMCPCatalog(catalogTestConfig(keys...), connections, gate)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(catalog.Close)
			search, err := tool.NewToolSearch(catalog)
			if err != nil {
				t.Fatal(err)
			}
			report := mcpSearchEvaluation{Tools: size}
			for _, task := range tasks {
				t.Run(task.Case, func(t *testing.T) {
					entry := mcpSearchCaseResult{Case: task.Case, Query: task.Query, Expected: mcpToolID(task.Service, task.Name)}
					args, err := json.Marshal(tool.ToolSearchRequest{Query: task.Query, Limit: 5})
					if err != nil {
						t.Fatal(err)
					}
					result, refs, err := search.SelectTools(t.Context(), llm.ToolCall{ID: task.Case, Name: "tool_search", Arguments: args})
					if err != nil || result.IsError || len(refs) > 5 {
						t.Fatalf("natural-language search failed: %+v %v", result, err)
					}
					for _, ref := range refs {
						entry.TopFive = append(entry.TopFive, ref.ID)
					}
					entry.Hit = slices.Contains(entry.TopFive, entry.Expected)
					if entry.Hit {
						report.Hits++
					}
					// A separate request, never a fallback counted as a language hit.
					args, err = json.Marshal(tool.ToolSearchRequest{IDs: []string{entry.Expected}, Limit: 1})
					if err != nil {
						t.Fatal(err)
					}
					result, refs, err = search.SelectTools(t.Context(), llm.ToolCall{ID: task.Case + "-exact", Name: "tool_search", Arguments: args})
					entry.Exact = err == nil && !result.IsError && len(refs) == 1 && refs[0].ID == entry.Expected
					if !entry.Exact {
						t.Errorf("exact ID selection failed: %+v %v", result, err)
					} else {
						report.Exact++
					}
					report.Cases = append(report.Cases, entry)
				})
			}
			if len(catalog.entries) != size {
				t.Fatalf("catalog has %d tools, want %d", len(catalog.entries), size)
			}
			for _, client := range clients {
				if client.calls.Load() != 0 {
					t.Fatal("retrieval executed a remote tool")
				}
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("MCP_SEARCH_EVAL %s", encoded)
			t.Logf("tools=%d language_top5=%d/50 exact=%d/50 target=95%%", size, report.Hits, report.Exact)
		})
	}
}

func loadMCPSearchTasks(t *testing.T) []mcpSearchTask {
	t.Helper()
	data, err := os.ReadFile("testdata/mcp-search-tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var tasks []mcpSearchTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 50 {
		t.Fatal("evaluation requires 50 fixed tasks")
	}
	seen := make(map[string]bool)
	for _, task := range tasks {
		if task.Case == "" || task.Service == "" || task.Name == "" || task.Description == "" || task.Query == "" || seen[task.Case] || seen[task.Service+"/"+task.Name] || strings.Contains(task.Query, task.Name) {
			t.Fatalf("invalid or identifier-leaking task: %+v", task)
		}
		seen[task.Case], seen[task.Service+"/"+task.Name] = true, true
	}
	return tasks
}

func mcpSearchFixtures(tasks []mcpSearchTask, size int) (map[string]*mcpCatalogFixture, []string) {
	clients := make(map[string]*mcpCatalogFixture)
	for _, task := range tasks {
		if clients[task.Service] == nil {
			clients[task.Service] = &mcpCatalogFixture{}
		}
		clients[task.Service].items = append(clients[task.Service].items, catalogFixtureTool(task.Name, task.Description))
	}
	keys := make([]string, 0, len(clients))
	for key := range clients {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	// Related administrative operations are distractors, not renamed
	// copies of the answer. The same nested corpus is used at each size.
	verbs := []string{"list", "read", "create", "update", "delete", "archive", "restore", "export", "import", "validate", "compare", "search", "approve", "schedule", "cancel"}
	objects := []string{"audit_records", "access_policies", "notification_rules", "retention_rules", "saved_filters", "report_templates", "webhook_settings", "sync_jobs", "import_jobs", "export_jobs", "usage_reports", "automation_rules", "integration_settings"}
	for i := range size - len(tasks) {
		key := keys[i%len(keys)]
		operation := i / len(keys)
		verb, object := verbs[operation%len(verbs)], objects[operation/len(verbs)]
		name := verb + "_" + object
		description := fmt.Sprintf("%s %s for the %s service. Administrative configuration and history, not domain content.", verb, strings.ReplaceAll(object, "_", " "), key)
		clients[key].items = append(clients[key].items, catalogFixtureTool(name, description))
	}
	return clients, keys
}
