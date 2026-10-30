package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// acpScriptModel reads a workspace file, then a file outside the workspace
// (a Guard ask), then answers.
type acpScriptModel struct {
	mu      sync.Mutex
	outside string
	turns   int
}

func (m *acpScriptModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns++
	switch m.turns % 3 {
	case 1:
		return toolCallEventStream(request.Model, llm.ToolCall{ID: "inside", Name: "read", Arguments: []byte(`{"path":"notes.txt"}`)}), nil
	case 2:
		args, _ := json.Marshal(map[string]string{"path": m.outside})
		return toolCallEventStream(request.Model, llm.ToolCall{ID: "outside", Name: "read", Arguments: args}), nil
	}
	cost := &llm.Cost{Total: 0.25}
	return textEventStream(request.Model, "done", llm.Usage{InputTokens: 10, CacheReadTokens: 5, OutputTokens: 2, TotalTokens: 17, Cost: cost}, llm.StopReasonStop), nil
}

// acpTestClient records updates and permission requests, and answers each
// permission with the option of kind choose.
type acpTestClient struct {
	mu          sync.Mutex
	choose      sdk.PermissionOptionKind
	updates     []sdk.SessionUpdate
	permissions []sdk.RequestPermissionRequest
}

func (c *acpTestClient) SessionUpdate(_ context.Context, params sdk.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, params.Update)
	return nil
}

func (c *acpTestClient) RequestPermission(_ context.Context, params sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permissions = append(c.permissions, params)
	for _, option := range params.Options {
		if option.Kind == c.choose {
			return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		}
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeCancelled()}, nil
}

func (c *acpTestClient) snapshot() ([]sdk.SessionUpdate, []sdk.RequestPermissionRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sdk.SessionUpdate(nil), c.updates...), append([]sdk.RequestPermissionRequest(nil), c.permissions...)
}

func (*acpTestClient) ReadTextFile(context.Context, sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	return sdk.ReadTextFileResponse{}, errors.New("unsupported")
}

func (*acpTestClient) WriteTextFile(context.Context, sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	return sdk.WriteTextFileResponse{}, errors.New("unsupported")
}

func (*acpTestClient) CreateTerminal(context.Context, sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	return sdk.CreateTerminalResponse{}, errors.New("unsupported")
}

func (*acpTestClient) KillTerminal(context.Context, sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	return sdk.KillTerminalResponse{}, errors.New("unsupported")
}

func (*acpTestClient) TerminalOutput(context.Context, sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	return sdk.TerminalOutputResponse{}, errors.New("unsupported")
}

func (*acpTestClient) ReleaseTerminal(context.Context, sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	return sdk.ReleaseTerminalResponse{}, errors.New("unsupported")
}

func (*acpTestClient) WaitForTerminalExit(context.Context, sdk.WaitForTerminalExitRequest) (sdk.WaitForTerminalExitResponse, error) {
	return sdk.WaitForTerminalExitResponse{}, errors.New("unsupported")
}

// acpTranscript records every JSON-RPC line in both directions.
type acpTranscript struct {
	mu    sync.Mutex
	lines []string
}

func (r *acpTranscript) tee(direction string, reader io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1<<20), 16<<20)
		for scanner.Scan() {
			line := scanner.Text()
			r.mu.Lock()
			r.lines = append(r.lines, direction+" "+line)
			r.mu.Unlock()
			if _, err := pw.Write([]byte(line + "\n")); err != nil {
				return
			}
		}
		pw.CloseWithError(scanner.Err())
	}()
	return pr
}

// acpServer runs `aice acp` on pipes and connects an SDK client to it.
type acpServer struct {
	client *acpTestClient
	conn   *sdk.ClientSideConnection
	stop   func() error
}

func startACPServer(t *testing.T, workspace string, model llm.Streamer, record *acpTranscript, configure func(*config.Config)) *acpServer {
	t.Helper()
	paths := trustTestPaths(t)
	configuration := trustTestConfig(paths)
	if configure != nil {
		configure(&configuration)
	}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) { return configuration, nil },
		newModel:   func(config.Config) (agent.Model, error) { return model, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	toAgentReader, toAgentWriter := io.Pipe()
	fromAgentReader, fromAgentWriter := io.Pipe()
	command.SetArgs([]string{"acp", "--workspace", workspace})
	command.SetIn(record.tee("->", toAgentReader))
	command.SetOut(fromAgentWriter)
	command.SetErr(io.Discard)
	done := make(chan error, 1)
	go func() {
		err := command.ExecuteContext(context.Background())
		fromAgentWriter.CloseWithError(err)
		done <- err
	}()
	client := &acpTestClient{choose: sdk.PermissionOptionKindAllowOnce}
	conn := sdk.NewClientSideConnection(client, toAgentWriter, record.tee("<-", fromAgentReader))
	server := &acpServer{client: client, conn: conn, stop: sync.OnceValue(func() error {
		toAgentWriter.Close()
		return <-done
	})}
	t.Cleanup(func() { _ = server.stop() })
	if _, err := conn.Initialize(t.Context(), sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersionNumber}); err != nil {
		t.Fatal(err)
	}
	return server
}

func TestACPServesSessionsThroughInteractionContracts(t *testing.T) {
	t.Parallel()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeAppFile(t, workspace, "notes.txt", "inside notes")
	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := &acpTranscript{}
	model := &acpScriptModel{outside: outside}
	server := startACPServer(t, workspace, model, record, nil)

	created, err := server.conn.NewSession(t.Context(), sdk.NewSessionRequest{Cwd: workspace, McpServers: []sdk.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	if created.Modes == nil || created.Modes.CurrentModeId != "default" {
		t.Fatalf("modes = %+v", created.Modes)
	}
	model0 := created.ConfigOptions[0].Select
	if model0 == nil || model0.Category == nil || *model0.Category != sdk.SessionConfigOptionCategoryModel || !strings.HasPrefix(string(model0.CurrentValue), "deepseek/") {
		t.Fatalf("config options = %+v", created.ConfigOptions)
	}
	if _, err := server.conn.SetSessionConfigOption(t.Context(), sdk.SetSessionConfigOptionRequest{ValueId: &sdk.SetSessionConfigOptionValueId{
		SessionId: created.SessionId, ConfigId: "model", Value: model0.CurrentValue,
	}}); err != nil {
		t.Fatalf("selecting the effective model: %v", err)
	}
	if _, err := server.conn.SetSessionConfigOption(t.Context(), sdk.SetSessionConfigOptionRequest{ValueId: &sdk.SetSessionConfigOptionValueId{
		SessionId: created.SessionId, ConfigId: "model", Value: "other/model",
	}}); err == nil {
		t.Fatal("ACP switched the model chosen by AICE settings")
	}
	if _, err := server.conn.NewSession(t.Context(), sdk.NewSessionRequest{Cwd: t.TempDir(), McpServers: []sdk.McpServer{}}); err == nil {
		t.Fatal("session outside the served workspace accepted")
	}

	response, err := server.conn.Prompt(t.Context(), sdk.PromptRequest{SessionId: created.SessionId, Prompt: []sdk.ContentBlock{sdk.TextBlock("inspect notes")}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StopReason != sdk.StopReasonEndTurn || response.Usage == nil || response.Usage.InputTokens != 15 || response.Usage.OutputTokens != 2 {
		t.Fatalf("prompt response = %+v usage %+v", response, response.Usage)
	}
	updates, permissions := server.client.snapshot()
	if len(permissions) != 1 || permissions[0].ToolCall.ToolCallId != "outside" {
		t.Fatalf("permission requests = %+v", permissions)
	}
	starts := map[sdk.ToolCallId]*sdk.SessionUpdateToolCall{}
	ends := map[sdk.ToolCallId]*sdk.SessionToolCallUpdate{}
	var text string
	var usage *sdk.SessionUsageUpdate
	for _, update := range updates {
		switch {
		case update.ToolCall != nil:
			starts[update.ToolCall.ToolCallId] = update.ToolCall
		case update.ToolCallUpdate != nil:
			ends[update.ToolCallUpdate.ToolCallId] = update.ToolCallUpdate
		case update.AgentMessageChunk != nil && update.AgentMessageChunk.Content.Text != nil:
			text += update.AgentMessageChunk.Content.Text.Text
		case update.UsageUpdate != nil:
			usage = update.UsageUpdate
		}
	}
	inside := starts["inside"]
	if inside == nil || inside.Kind != sdk.ToolKindRead || len(inside.Locations) != 1 || inside.Locations[0].Path != filepath.Join(workspace, "notes.txt") {
		t.Fatalf("inside tool_call = %+v", inside)
	}
	if raw, _ := json.Marshal(inside.RawInput); string(raw) != `{"path":"notes.txt"}` {
		t.Fatalf("rawInput = %s, want the call arguments", raw)
	}
	for _, id := range []sdk.ToolCallId{"inside", "outside"} {
		end := ends[id]
		if end == nil || end.Status == nil || *end.Status != sdk.ToolCallStatusCompleted {
			t.Fatalf("%s tool_call_update = %+v", id, end)
		}
	}
	if text != "done" || usage == nil || usage.Cost == nil || usage.Cost.Amount != 0.25 {
		t.Fatalf("text = %q usage = %+v", text, usage)
	}

	// The Session survives the process; another client loads and replays it.
	if err := server.stop(); err != nil {
		t.Fatal(err)
	}
	reloaded := startACPServer(t, workspace, model, record, nil)
	if _, err := reloaded.conn.LoadSession(t.Context(), sdk.LoadSessionRequest{SessionId: created.SessionId, Cwd: workspace, McpServers: []sdk.McpServer{}}); err != nil {
		t.Fatal(err)
	}
	replayed, _ := reloaded.client.snapshot()
	var user, answer string
	tools := 0
	for _, update := range replayed {
		switch {
		case update.UserMessageChunk != nil:
			user += update.UserMessageChunk.Content.Text.Text
		case update.AgentMessageChunk != nil:
			answer += update.AgentMessageChunk.Content.Text.Text
		case update.ToolCall != nil:
			tools++
			if update.ToolCall.Kind != sdk.ToolKindRead {
				t.Fatalf("replayed tool kind = %q", update.ToolCall.Kind)
			}
		}
	}
	if user != "inspect notes" || answer != "done" || tools != 2 {
		t.Fatalf("replay user=%q answer=%q tools=%d", user, answer, tools)
	}
	if err := reloaded.stop(); err != nil {
		t.Fatal(err)
	}
	writeACPTranscript(t, record)
}

// writeACPTranscript refreshes the golden transcript that the pinned ACP
// schema check (evals/acp) validates, when ACP_TRANSCRIPT names a file.
func writeACPTranscript(t *testing.T, record *acpTranscript) {
	t.Helper()
	path := os.Getenv("ACP_TRANSCRIPT")
	if path == "" {
		return
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	var data bytes.Buffer
	for _, line := range record.lines {
		data.WriteString(line + "\n")
	}
	if err := os.WriteFile(path, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// blockingModel holds its stream open until the run is cancelled.
type blockingModel struct{ started chan struct{} }

func (m *blockingModel) Stream(ctx context.Context, _ llm.Request) (llm.Stream, error) {
	close(m.started)
	return &blockingStream{ctx: ctx}, nil
}

type blockingStream struct{ ctx context.Context }

func (s *blockingStream) Next() (llm.Event, error) {
	<-s.ctx.Done()
	return llm.Event{}, s.ctx.Err()
}

func (*blockingStream) Close() error { return nil }

func TestACPStopReasonsAndDeniedPermission(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (string, string) {
		workspace, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		writeAppFile(t, workspace, "notes.txt", "inside notes")
		outside, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		writeAppFile(t, outside, "outside.txt", "outside notes")
		return workspace, filepath.Join(outside, "outside.txt")
	}
	prompt := func(t *testing.T, server *acpServer, workspace string) (sdk.PromptResponse, error) {
		created, err := server.conn.NewSession(t.Context(), sdk.NewSessionRequest{Cwd: workspace, McpServers: []sdk.McpServer{}})
		if err != nil {
			t.Fatal(err)
		}
		return server.conn.Prompt(t.Context(), sdk.PromptRequest{SessionId: created.SessionId, Prompt: []sdk.ContentBlock{sdk.TextBlock("inspect")}})
	}

	t.Run("turn limit", func(t *testing.T) {
		t.Parallel()
		workspace, outside := setup(t)
		server := startACPServer(t, workspace, &acpScriptModel{outside: outside}, &acpTranscript{}, func(c *config.Config) { c.MaxTurns = 1 })
		response, err := prompt(t, server, workspace)
		if err != nil || response.StopReason != sdk.StopReasonMaxTurnRequests {
			t.Fatalf("response = %+v, %v; want max_turn_requests", response, err)
		}
	})
	t.Run("denied permission", func(t *testing.T) {
		t.Parallel()
		workspace, outside := setup(t)
		server := startACPServer(t, workspace, &acpScriptModel{outside: outside}, &acpTranscript{}, nil)
		server.client.choose = sdk.PermissionOptionKindRejectOnce
		if response, err := prompt(t, server, workspace); err != nil || response.StopReason != sdk.StopReasonEndTurn {
			t.Fatalf("response = %+v, %v", response, err)
		}
		updates, _ := server.client.snapshot()
		for _, update := range updates {
			if end := update.ToolCallUpdate; end != nil && end.ToolCallId == "outside" {
				if end.Status == nil || *end.Status != sdk.ToolCallStatusFailed {
					t.Fatalf("denied tool status = %v", end.Status)
				}
				return
			}
		}
		t.Fatal("denied tool reported no outcome")
	})
	t.Run("cancel", func(t *testing.T) {
		t.Parallel()
		workspace, _ := setup(t)
		model := &blockingModel{started: make(chan struct{})}
		server := startACPServer(t, workspace, model, &acpTranscript{}, nil)
		created, err := server.conn.NewSession(t.Context(), sdk.NewSessionRequest{Cwd: workspace, McpServers: []sdk.McpServer{}})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			<-model.started
			_ = server.conn.Cancel(context.Background(), sdk.CancelNotification{SessionId: created.SessionId})
		}()
		response, err := server.conn.Prompt(t.Context(), sdk.PromptRequest{SessionId: created.SessionId, Prompt: []sdk.ContentBlock{sdk.TextBlock("wait")}})
		if err != nil || response.StopReason != sdk.StopReasonCancelled {
			t.Fatalf("response = %+v, %v; want cancelled", response, err)
		}
	})
}
