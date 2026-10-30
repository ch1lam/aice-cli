package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// replayCase is one recorded model decision to rerun: the assistant message
// Message of the Session at Session (relative to the sessions root), and the
// rules its replacement reply must satisfy.
type replayCase struct {
	ID            string       `json:"id"`
	Session       string       `json:"session"`
	SessionSHA256 string       `json:"session_sha256"`
	Message       string       `json:"message"`
	Source        string       `json:"source"`
	Note          string       `json:"note"`
	Expect        []replayRule `json:"expect"`
}

// replayRule checks one property of a reply. call and no_call match tool
// calls by name (any tool when Tool is empty) and by Pattern against the
// call's JSON arguments with HTML escapes undone; text and no_text
// match Pattern against visible text.
type replayRule struct {
	Kind    string `json:"kind"`
	Tool    string `json:"tool,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

func loadReplayCases(path string) ([]replayCase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var cases []replayCase
	seen := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		var c replayCase
		if err := decoder.Decode(&c); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("%s:%d: duplicate case %q", path, line, c.ID)
		}
		seen[c.ID] = true
		cases = append(cases, c)
	}
	return cases, scanner.Err()
}

func (c replayCase) validate() error {
	if c.ID == "" || c.Session == "" || c.Message == "" || len(c.SessionSHA256) != 64 || len(c.Expect) == 0 {
		return errors.New("a case needs id, session, session_sha256, message and expect")
	}
	for _, rule := range c.Expect {
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return fmt.Errorf("case %s: %w", c.ID, err)
		}
		switch rule.Kind {
		case "call", "no_call":
		case "text", "no_text":
			if rule.Pattern == "" || rule.Tool != "" {
				return fmt.Errorf("case %s: %s rules take only a pattern", c.ID, rule.Kind)
			}
		default:
			return fmt.Errorf("case %s: unknown rule kind %q", c.ID, rule.Kind)
		}
	}
	return nil
}

// request rebuilds the recorded request after checking the Session is the
// recorded file.
func (c replayCase) request(ctx context.Context, root string) (llm.Request, error) {
	path := filepath.Join(root, filepath.FromSlash(c.Session))
	data, err := os.ReadFile(path)
	if err != nil {
		return llm.Request{}, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != c.SessionSHA256 {
		return llm.Request{}, fmt.Errorf("%s does not match the recorded SHA-256", path)
	}
	snapshot, _, err := session.Read(ctx, path)
	if err != nil {
		return llm.Request{}, err
	}
	return session.RebuildRequest(snapshot, c.Message)
}

// judgeReplay returns the rules a reply violates.
func judgeReplay(reply llm.AssistantMessage, rules []replayRule) []string {
	var calls []llm.ToolCall
	for _, part := range reply.Content {
		if part.ToolCall != nil {
			calls = append(calls, *part.ToolCall)
		}
	}
	text := visibleText(reply.Content, "\n")
	var failures []string
	for _, rule := range rules {
		pattern := regexp.MustCompile(rule.Pattern)
		matched := false
		switch rule.Kind {
		case "call", "no_call":
			for _, call := range calls {
				if (rule.Tool == "" || call.Name == rule.Tool) && pattern.MatchString(argumentsText(call.Arguments)) {
					matched = true
				}
			}
		default:
			matched = pattern.MatchString(text)
		}
		if matched == strings.HasPrefix(rule.Kind, "no_") {
			failures = append(failures, fmt.Sprintf("%s tool=%q pattern=%q", rule.Kind, rule.Tool, rule.Pattern))
		}
	}
	return failures
}

// argumentsText spells arguments the way rules are written: encoding/json
// stores <, > and & as \u003c, \u003e and \u0026, which a pattern should not
// have to anticipate. Key order is kept.
func argumentsText(arguments []byte) string {
	return htmlEscapes.Replace(string(arguments))
}

var htmlEscapes = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&")

// acpRequestParts are the system prompt and tool definitions this build sends
// from `aice acp --approve` in a fresh workspace.
type acpRequestParts struct {
	systemPrompt string
	tools        []llm.ToolDefinition
}

func currentACPRequestParts(ctx context.Context, a *application, workspace string) (parts acpRequestParts, returnErr error) {
	approve := true
	configured, err := a.loadRunModel(ctx, workspace, &approve, nil)
	if err != nil {
		return acpRequestParts{}, err
	}
	environment, err := a.prepareRunEnvironment(ctx, workspace, &approve, nil, false, configured, runFrontend{asks: true})
	if err != nil {
		return acpRequestParts{}, err
	}
	defer environment.web.closeBackend()
	defer func() { returnErr = errors.Join(returnErr, environment.mcp.Close()) }()
	parts.systemPrompt = environment.systemPrompt
	for _, current := range environment.tools {
		parts.tools = append(parts.tools, current.Definition())
	}
	return parts, nil
}

// promptWorkspaceMarker introduces the workspace line that ends the built-in
// base prompt; project instructions and the Skills catalog follow it.
const promptWorkspaceMarker = "\n\nCurrent working directory: "

// withCurrentPrompt replaces the recorded built-in base prompt, up to its
// workspace line, with this build's. The recorded workspace line and the
// project-specific sections after it are kept.
func withCurrentPrompt(request llm.Request, current acpRequestParts) (llm.Request, error) {
	recorded := strings.Index(request.SystemPrompt, promptWorkspaceMarker)
	base := strings.Index(current.systemPrompt, promptWorkspaceMarker)
	if recorded < 0 || base < 0 {
		return llm.Request{}, errors.New("the recorded system prompt is not built on the built-in base")
	}
	request.SystemPrompt = current.systemPrompt[:base] + request.SystemPrompt[recorded:]
	return refit(request)
}

// withCurrentTools offers this build's ACP tool set instead of the recorded
// one, so added, removed and changed tools all take effect. Tools that came
// from the recorded run's configuration, such as MCP tools, are not offered.
func withCurrentTools(request llm.Request, current acpRequestParts) (llm.Request, error) {
	request.Tools = slices.Clone(current.tools)
	return refit(request)
}

// withModel sends the request to model instead of the recorded one.
func withModel(request llm.Request, model llm.Model) (llm.Request, error) {
	if request.Model.Provider == model.Provider && request.Model.ID == model.ID {
		return request, nil
	}
	request.Model = model
	request.Options.Thinking = llm.ClampThinkingLevel(model, request.Options.Thinking)
	request.Options.MaxTokens = 0
	return refit(request)
}

func refit(request llm.Request) (llm.Request, error) {
	fitted, estimate, ok := llm.FitOutputTokens(request)
	if !ok {
		return llm.Request{}, fmt.Errorf("a context of %d tokens leaves no output budget", estimate)
	}
	return fitted, nil
}

// completeOnce makes one model call and returns its final message.
func completeOnce(ctx context.Context, model llm.Streamer, request llm.Request) (llm.AssistantMessage, error) {
	stream, err := model.Stream(ctx, request)
	if err != nil {
		return llm.AssistantMessage{}, err
	}
	defer stream.Close()
	var final *llm.AssistantMessage
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return llm.AssistantMessage{}, err
		}
		if (event.Type == llm.EventTypeDone || event.Type == llm.EventTypeError) && event.Message != nil {
			final = event.Message
		}
	}
	if final == nil {
		return llm.AssistantMessage{}, errors.New("the model stream ended without a final message")
	}
	if final.StopReason == llm.StopReasonError || final.StopReason == llm.StopReasonAborted {
		return *final, fmt.Errorf("model request failed: %s", final.ErrorMessage)
	}
	return *final, nil
}

func TestReplayCasesLoad(t *testing.T) {
	cases, err := loadReplayCases(filepath.Join("..", "..", "evals", "replay", "cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no replay cases")
	}
}

func TestLoadReplayCasesRejectsInvalidCases(t *testing.T) {
	t.Parallel()
	valid := `{"id":"a","session":"s.jsonl","session_sha256":"` + strings.Repeat("0", 64) + `","message":"m","expect":[{"kind":"call","tool":"bash"}]}`
	for name, data := range map[string]string{
		"unknown field": strings.Replace(valid, `"id"`, `"extra":1,"id"`, 1),
		"no rules":      strings.Replace(valid, `[{"kind":"call","tool":"bash"}]`, `[]`, 1),
		"unknown kind":  strings.Replace(valid, `"call"`, `"maybe"`, 1),
		"bad pattern":   strings.Replace(valid, `"tool":"bash"`, `"pattern":"("`, 1),
		"text tool":     strings.Replace(valid, `"kind":"call","tool":"bash"`, `"kind":"text","tool":"bash","pattern":"x"`, 1),
		"short digest":  strings.Replace(valid, strings.Repeat("0", 64), "00", 1),
		"duplicate":     valid + "\n" + valid,
	} {
		path := filepath.Join(t.TempDir(), "cases.jsonl")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadReplayCases(path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestJudgeReplay(t *testing.T) {
	t.Parallel()
	reply := llm.AssistantMessage{Content: []llm.ContentPart{
		{Type: llm.ContentTypeText, Text: "Writing the page."},
		{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{Name: "write", Arguments: []byte(`{"path":"/var/www/html/index.html","content":"\u003chtml>Welcome</html>"}`)}},
	}}
	for _, tc := range []struct {
		rule replayRule
		pass bool
	}{
		{replayRule{Kind: "call", Pattern: `index\.html`}, true},
		{replayRule{Kind: "call", Tool: "bash", Pattern: `index\.html`}, false},
		{replayRule{Kind: "no_call", Pattern: `index\.html.*<html`}, false},
		{replayRule{Kind: "no_call", Tool: "bash"}, true},
		{replayRule{Kind: "text", Pattern: `(?i)writing`}, true},
		{replayRule{Kind: "no_text", Pattern: `page`}, false},
	} {
		if got := len(judgeReplay(reply, []replayRule{tc.rule})) == 0; got != tc.pass {
			t.Errorf("rule %+v passed = %v, want %v", tc.rule, got, tc.pass)
		}
	}
}

// TestReplayCurrentPartsReproduceSameBuild checks the replay override on a
// Session this build records through ACP: replacing the prompt and tools with
// the current build's must reproduce the recorded request exactly.
func TestReplayCurrentPartsReproduceSameBuild(t *testing.T) {
	t.Parallel()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeAppFile(t, workspace, "notes.txt", "inside notes")
	model := &acpScriptModel{outside: filepath.Join(workspace, "notes.txt")}
	server := startACPServer(t, workspace, model, &acpTranscript{}, nil)
	created, err := server.conn.NewSession(t.Context(), sdk.NewSessionRequest{Cwd: workspace, McpServers: []sdk.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.conn.Prompt(t.Context(), sdk.PromptRequest{SessionId: created.SessionId, Prompt: []sdk.ContentBlock{sdk.TextBlock("inspect")}}); err != nil {
		t.Fatal(err)
	}
	if err := server.stop(); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := session.Read(t.Context(), filepath.Join(workspace, ".aice", "sessions", string(created.SessionId)+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var assistant string
	for _, entry := range snapshot.Messages {
		if _, ok := entry.Message.(llm.AssistantMessage); ok {
			assistant = entry.ID
			break
		}
	}
	recorded, err := session.RebuildRequest(snapshot, assistant)
	if err != nil {
		t.Fatal(err)
	}

	configuration := trustTestConfig(trustTestPaths(t))
	a := &application{dependencies: dependencies{
		loadConfig:    func(config.LoadOptions) (config.Config, error) { return configuration, nil },
		providers:     defaultProviders(),
		ensureHelpers: skipTestHelperDownloads,
		userHomeDir:   isolatedUserHome(t),
	}}
	current, err := currentACPRequestParts(t.Context(), a, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(current.systemPrompt, workspace) {
		t.Fatal("the current prompt must come from a different workspace")
	}
	replayed, err := withCurrentPrompt(recorded, current)
	if err == nil {
		replayed, err = withCurrentTools(replayed, current)
	}
	if err != nil {
		t.Fatal(err)
	}
	if replayed.SystemPrompt != recorded.SystemPrompt {
		t.Fatalf("current prompt differs\n got: %q\nwant: %q", replayed.SystemPrompt, recorded.SystemPrompt)
	}
	got, _ := json.Marshal(replayed.Tools)
	want, _ := json.Marshal(recorded.Tools)
	if string(got) != string(want) || !reflect.DeepEqual(replayed.Options, recorded.Options) {
		t.Fatalf("current tools differ\n got: %s\nwant: %s", got, want)
	}
}
