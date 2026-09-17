package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/config"
)

// Captured before TestMain isolates HOME. The build needs the same module and
// build caches used by go test; the AICE child receives a separate allowlist.
var processBuildEnvironment = os.Environ()

func TestBinaryPrint(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "aice")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	build.Env = append(processBuildEnvironment, "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build aice: %v\n%s", err, output)
	}

	t.Run("text stdout and successful exit", func(t *testing.T) {
		server := apitest.NewSSEServer(t, []string{
			`{"id":"answer","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"binary "}}]}`,
			`{"id":"answer","model":"test-model","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
			`[DONE]`,
		})
		defer server.Close()
		stdout, stderr := runBinaryPrint(t, binary, server.URL, 0)
		if stdout != "binary answer\n" {
			t.Fatalf("stdout = %q", stdout)
		}
		if !strings.Contains(stderr, "aice: total") {
			t.Fatalf("missing diagnostics on stderr: %q", stderr)
		}
	})

	t.Run("provider error exits nonzero", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"offline fixture rejected request","type":"authentication_error"}}`)
		}))
		defer server.Close()
		stdout, stderr := runBinaryPrint(t, binary, server.URL, 1)
		if stdout != "" || !strings.Contains(stderr, "offline fixture rejected request") {
			t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
		}
		if requests.Load() != 1 {
			t.Fatalf("provider requests=%d, want one non-retryable failure", requests.Load())
		}
	})

	t.Run("usage error exits two", func(t *testing.T) {
		stdout, stderr := runBinaryPrint(t, binary, "http://127.0.0.1:0", 2, "--output-format=invalid")
		if stdout != "" || !strings.Contains(stderr, "unsupported output format") {
			t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
		}
	})

	t.Run("yolo preserves secret file deny", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/chat/completions" || r.Method != http.MethodPost {
				t.Errorf("unexpected provider route: %s %s", r.Method, r.URL.Path)
				http.Error(w, "unexpected route", http.StatusBadRequest)
				return
			}
			var body struct {
				Messages []struct {
					Role       string          `json:"role"`
					Content    json.RawMessage `json:"content"`
					ToolCallID string          `json:"tool_call_id"`
				} `json:"messages"`
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			if bytes.Contains(raw, []byte("fixture-secret-value")) || bytes.Contains(raw, []byte("untrusted-project-instruction")) {
				t.Error("provider received protected content")
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Error(err)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			switch requests.Add(1) {
			case 1:
				_, _ = io.WriteString(w, "data: "+`{"id":"read","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"secret-read","type":"function","function":{"name":"read","arguments":"{\"path\":\".env\"}"}},{"index":1,"id":"public-read","type":"function","function":{"name":"read","arguments":"{\"path\":\"public.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			case 2:
				found, readPublic := false, false
				for _, message := range body.Messages {
					if message.Role == "tool" && message.ToolCallID == "public-read" {
						readPublic = bytes.Contains(message.Content, []byte("fixture-public-value"))
					}
					if message.Role == "tool" && message.ToolCallID == "secret-read" {
						var content string
						if err := json.Unmarshal(message.Content, &content); err != nil {
							t.Error(err)
							continue
						}
						if !strings.Contains(content, "not allowed") || !strings.Contains(content, "secrets") {
							t.Errorf("tool result = %q, want hard deny", content)
						}
						found = true
					}
				}
				if !found || !readPublic {
					t.Errorf("continuation: secret denial=%v public read=%v", found, readPublic)
				}
				_, _ = io.WriteString(w, "data: "+`{"id":"done","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"access denied"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
			default:
				t.Error("unexpected extra model request")
			}
		}))
		defer server.Close()
		stdout, _ := runBinaryPrint(t, binary, server.URL, 0, "--yolo", "--output-format=json")
		var denied, ended, readPublic bool
		decoder := json.NewDecoder(strings.NewReader(stdout))
		for {
			var event struct {
				Type       string `json:"type"`
				ToolCallID string `json:"tool_call_id"`
				IsError    bool   `json:"is_error"`
				Result     string `json:"result"`
				Error      string `json:"error"`
			}
			err := decoder.Decode(&event)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("invalid NDJSON: %v\n%s", err, stdout)
			}
			if event.Type == "tool_execution_end" && event.ToolCallID == "secret-read" {
				denied = event.IsError && strings.Contains(event.Result, "not allowed") && strings.Contains(event.Result, "secrets")
			}
			if event.Type == "tool_execution_end" && event.ToolCallID == "public-read" {
				readPublic = !event.IsError && strings.Contains(event.Result, "fixture-public-value")
			}
			if event.Type == "agent_end" {
				ended = event.Error == ""
			}
		}
		if !denied || !ended || !readPublic || requests.Load() != 2 {
			t.Fatalf("denied=%v ended=%v public read=%v requests=%d\n%s", denied, ended, readPublic, requests.Load(), stdout)
		}
	})
}

func runBinaryPrint(t *testing.T, binary, endpoint string, wantExit int, extra ...string) (string, string) {
	t.Helper()
	root := t.TempDir()
	home, workspace, temp := filepath.Join(root, "home"), filepath.Join(root, "workspace"), filepath.Join(root, "tmp")
	for _, path := range []string{home, temp, filepath.Join(workspace, ".aice")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		".env":                "fixture-secret-value",
		"public.txt":          "fixture-public-value",
		"AGENTS.md":           "untrusted-project-instruction",
		".aice/settings.json": "{invalid project settings must not be loaded",
	} {
		if err := os.WriteFile(filepath.Join(workspace, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	args := []string{"--print", "inspect the fixture", "--provider=custom", "--model=test-model", "--thinking=off", "--no-approve", "--no-dep-install", "--no-update-check"}
	command := exec.CommandContext(ctx, binary, append(args, extra...)...)
	command.Dir = workspace
	command.Env = []string{
		"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home, "XDG_CONFIG_HOME=" + home,
		"TMPDIR=" + temp, "TMP=" + temp, "TEMP=" + temp,
		config.EnvCustomAPIKey + "=fixture-key", config.EnvCustomBaseURL + "=" + endpoint,
	}
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(key); ok {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("aice watchdog expired: %v\n%s", ctx.Err(), stderr.String())
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("start aice: %v", err)
		}
		code = exit.ExitCode()
	}
	if code != wantExit {
		t.Fatalf("exit=%d want=%d\nstdout=%s\nstderr=%s", code, wantExit, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "fixture-secret-value") {
		t.Fatal("secret content leaked to process output")
	}
	return stdout.String(), stderr.String()
}
