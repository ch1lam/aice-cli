package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type routingModel struct {
	recordingModel
	ids []string
}

func (m *routingModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	m.ids = append(m.ids, llm.SessionID(ctx))
	return m.recordingModel.Stream(ctx, request)
}

func TestConversationRoutingLifecycle(t *testing.T) {
	t.Parallel()
	model := &routingModel{recordingModel: recordingModel{response: "answer"}}
	h := newSideHarness(t, func() (agent.Model, error) { return model, nil })
	workspace, err := tool.NewWorkspace(h.workspace)
	if err != nil {
		t.Fatal(err)
	}
	h.session.workspace = workspace
	for range 2 {
		if err := runInteractive(t.Context(), h.session, "hello", nil); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := h.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	want := snapshot.Header.ID
	if model.ids[0] != want || model.ids[1] != want {
		t.Fatalf("main IDs = %v, want %q", model.ids, want)
	}
	for range 2 {
		_, side, err := h.session.CreateSideThread("side")
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := runSide(t, side, "side"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if model.ids[2] == "" || model.ids[2] == want || model.ids[2] != model.ids[3] ||
		model.ids[4] == "" || model.ids[4] == model.ids[2] || model.ids[4] != model.ids[5] {
		t.Fatalf("side IDs are not isolated and stable: %v", model.ids)
	}
	if _, err := h.session.slashNew(t.Context(), interaction.CommandRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := runInteractive(t.Context(), h.session, "new", nil); err != nil {
		t.Fatal(err)
	}
	if model.ids[6] == "" || model.ids[6] == want {
		t.Fatalf("new Session reused identity: %v", model.ids)
	}
	if err := h.session.conversation.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.Open(t.Context(), h.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	h.session.conversation.store = reopened
	if err := h.session.conversation.reloadHistory(); err != nil {
		t.Fatal(err)
	}
	if err := runInteractive(t.Context(), h.session, "resume", nil); err != nil {
		t.Fatal(err)
	}
	if model.ids[7] != want {
		t.Fatalf("reopened ID = %q, want %q", model.ids[7], want)
	}
}

func TestStatelessRoutingContextPreservesCancellation(t *testing.T) {
	t.Parallel()
	parent, cancel := context.WithCancel(t.Context())
	first, err := modelSessionContext(parent, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := modelSessionContext(parent, nil)
	if err != nil {
		t.Fatal(err)
	}
	if llm.SessionID(first) == "" || llm.SessionID(first) == llm.SessionID(second) {
		t.Fatal("stateless calls require distinct identities")
	}
	cancel()
	if first.Err() != context.Canceled {
		t.Fatal("routing context lost cancellation")
	}
}

func TestStandaloneCompactionRoutingIdentity(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	runPrintTurn(t, workspace, path, "first prompt", "first answer")
	runPrintTurn(t, workspace, path, "second prompt", "second answer")
	model := &routingModel{recordingModel: recordingModel{response: "summary"}}
	command := newCompactTestCommand(t, model, 1)
	command.SetArgs([]string{"compact", "--workspace", workspace, "--session", path})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(model.ids) != 1 || model.ids[0] != openSessionSnapshot(t, path).Header.ID {
		t.Fatalf("compaction IDs = %v", model.ids)
	}
}

func TestPrintCommandSendsOpenCodeSessionHeader(t *testing.T) {
	t.Parallel()
	ids := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("x-opencode-session")
		if id == "" {
			http.Error(w, "MissingSessionID", http.StatusBadRequest)
			return
		}
		ids <- id
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"id":"reply","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, "data: "+`{"id":"reply","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	var got []string
	for _, sessionPath := range []string{path, path, "", ""} {
		command, err := newTestCommand(t, dependencies{
			loadConfig: func() (config.Config, error) {
				return config.Config{
					Provider:        string(opencode.ProviderID),
					Model:           opencode.DefaultModel().ID,
					OpenCodeAPIKey:  "test-key",
					OpenCodeBaseURL: server.URL + "/v1",
				}, nil
			},
			newModel: func(configuration config.Config) (llm.Streamer, error) {
				return modelForConfiguration(defaultProviders(), configuration)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		output := new(bytes.Buffer)
		command.SetOut(output)
		args := []string{"--workspace", workspace, "--print", "hello"}
		if sessionPath != "" {
			args = append(args, "--session", sessionPath)
		}
		command.SetArgs(args)
		if err := command.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if output.String() != "ok\n" {
			t.Fatalf("output = %q", output.String())
		}
		got = append(got, <-ids)
	}
	if got[0] != openSessionSnapshot(t, path).Header.ID || got[0] != got[1] ||
		got[2] == got[0] || got[3] == got[2] {
		t.Fatalf("print routing IDs = %v", got)
	}
}
