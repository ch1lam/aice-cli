package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestMCPOwnerAsksForConnectionOncePerFingerprint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer bool
		err    error
		opened int32
		asks   int32
	}{
		{name: "allow", answer: true, opened: 1, asks: 1},
		{name: "decline is remembered", answer: false, opened: 0, asks: 1},
		{name: "cancelled prompt asks again", err: context.Canceled, opened: 0, asks: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := ownerTestConfig(t, 1)
			key := "user:service0"
			var opened, asks atomic.Int32
			o, err := newMCPOwner(c.MCP, ownerTestGuard(t), false, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
				opened.Add(1)
				return &mcpOwnedFixture{}, nil
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close()
			ctx := withMCPConnectionApprover(t.Context(), func(_ context.Context, server config.MCPServer) (bool, error) {
				asks.Add(1)
				if server.Key != key {
					t.Errorf("asked for %s", server.Key)
				}
				return tc.answer, tc.err
			})
			connection := o.Connections()[key]
			// Concurrent discovery of one service shows a single prompt.
			var work sync.WaitGroup
			for range 3 {
				work.Go(func() { _, _ = connection.Tools(ctx) })
			}
			work.Wait()
			_, _ = connection.Tools(ctx)
			if opened.Load() != tc.opened || asks.Load() != tc.asks {
				t.Fatalf("opened=%d asks=%d", opened.Load(), asks.Load())
			}
			// Without an approver (Print), an unapproved connection still fails closed.
			if tc.answer {
				return
			}
			if _, err := connection.Tools(t.Context()); err == nil || opened.Load() != 0 {
				t.Fatal("connection opened without approval")
			}
		})
	}
}

func TestMCPConnectionPromptOffersOnlyExplicitAllows(t *testing.T) {
	c := ownerTestConfig(t, 1)
	c.Paths.GlobalTrust, c.Paths.BinDir = filepath.Join(t.TempDir(), "trust.json"), t.TempDir()
	server := c.MCP.Servers["user:service0"]
	// The saved choice is last because it persists for later cases.
	for _, tc := range []struct {
		option  string
		allowed bool
		saved   bool
	}{
		{guardOptionAllowMCPConnection, true, false},
		{guardOptionDeny, false, false},
		{"allow-run-tool", false, false},
		{guardOptionAllowMCPConnectionSaved, true, true},
	} {
		requests := make(chan interaction.GuardRequest, 1)
		session := &interactiveSession{guardRequests: requests}
		go func() {
			request := <-requests
			if request.Title == "" || request.Command != server.Settings.URL || len(request.Options) != 3 {
				t.Errorf("request = %#v", request)
			}
			request.Reply <- interaction.GuardReply{OptionID: tc.option}
		}()
		allowed, err := session.approveMCPConnection(c.Paths)(t.Context(), server)
		if err != nil || allowed != tc.allowed {
			t.Fatalf("%s: allowed=%v err=%v", tc.option, allowed, err)
		}
		data, _ := os.ReadFile(c.Paths.GlobalAuth)
		if saved := bytes.Contains(data, []byte(server.Fingerprint)); saved != tc.saved {
			t.Fatalf("%s: saved=%v", tc.option, saved)
		}
	}
	if _, err := (&interactiveSession{}).approveMCPConnection(c.Paths)(t.Context(), server); err == nil {
		t.Fatal("approval without a frontend succeeded")
	}
}

// The application path: a model's first discovery asks for the connection in
// the frontend's guard channel, then the tool call asks separately. The
// connection answer is reused by the next Run without another prompt.
func TestMCPInteractiveRunAsksBeforeFirstConnection(t *testing.T) {
	endpoint, initializes, calls, _ := mcpStartupServer(t)
	c := ownerTestConfig(t, 1)
	c, err := c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": {Transport: "http", URL: endpoint}}})
	if err != nil {
		t.Fatal(err)
	}
	c = c.WithAPIKey("deepseek", "offline-fixture")
	model := &mcpStartupModel{t: t, search: true, wantRemote: true}
	var prompts []string
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil }, newModel: func(config.Config) (llm.Streamer, error) { return model, nil },
		runTUI: func(ctx context.Context, frontend interaction.Frontend, _ tui.Options) error {
			answers, stop := context.WithCancel(ctx)
			defer stop()
			go func() {
				for {
					select {
					case <-answers.Done():
						return
					case request := <-frontend.Guards.GuardRequests():
						prompts = append(prompts, request.RuleID)
						option := guardOptionAllowOnce
						if request.RuleID == guardRuleMCPConnection {
							option = guardOptionAllowMCPConnection
						}
						request.Reply <- interaction.GuardReply{OptionID: option}
					}
				}
			}()
			for range 2 {
				model.round = 0
				run, err := frontend.Runner.NewRun(ctx, interaction.RunInput{Prompt: "read fixture"}, nil)
				if err != nil {
					return err
				}
				if err := run.Run(ctx); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"--workspace", t.TempDir()})
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{guardRuleMCPConnection, "mcp.tool", "mcp.tool"}; !slices.Equal(prompts, want) {
		t.Fatalf("prompts = %v, want %v", prompts, want)
	}
	if initializes.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("initializes=%d calls=%d", initializes.Load(), calls.Load())
	}
}
