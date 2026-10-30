//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// TestReplayEval reruns the recorded decisions in evals/replay/cases.jsonl
// with the configured provider and model. It makes paid requests, so it needs
// the integration tag and an explicit opt-in:
//
//	AICE_REPLAY_EVAL=1 AICE_PROVIDER=opencode-go AICE_MODEL=deepseek-v4.1-flash \
//	  go test -tags=integration ./internal/app -run TestReplayEval -v -timeout 30m
//
// Sessions are read from AICE_REPLAY_SESSIONS (default: the repository's
// jobs directory). AICE_REPLAY_PROMPT and AICE_REPLAY_TOOLS choose recorded
// (default) or current, this build's `aice acp` system prompt base and tool
// set. AICE_REPLAY_RUNS repeats each case (default 3) and AICE_REPLAY_FILTER
// limits cases by ID substring. The summary is written to
// evals/replay/results/. Configuration comes from the environment only.
func TestReplayEval(t *testing.T) {
	if os.Getenv("AICE_REPLAY_EVAL") != "1" {
		t.Skip("set AICE_REPLAY_EVAL=1 to run the paid replay evaluation")
	}
	repository := filepath.Join("..", "..")
	cases, err := loadReplayCases(filepath.Join(repository, "evals", "replay", "cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if filter := os.Getenv("AICE_REPLAY_FILTER"); filter != "" {
		cases = slices.DeleteFunc(cases, func(c replayCase) bool { return !strings.Contains(c.ID, filter) })
	}
	root := os.Getenv("AICE_REPLAY_SESSIONS")
	if root == "" {
		root = filepath.Join(repository, "jobs")
	}
	mode := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			return "recorded"
		}
		if value != "recorded" && value != "current" {
			t.Fatalf("%s must be recorded or current", name)
		}
		return value
	}
	promptMode, toolsMode := mode("AICE_REPLAY_PROMPT"), mode("AICE_REPLAY_TOOLS")
	runs := 3
	if n, err := strconv.Atoi(os.Getenv("AICE_REPLAY_RUNS")); err == nil && n > 0 {
		runs = n
	}

	// Settings files would make the current parts differ from an evaluation
	// run, so read configuration from the environment in an empty home.
	t.Setenv("HOME", t.TempDir())
	providers := defaultProviders()
	a := &application{dependencies: dependencies{
		loadConfig:    config.Load,
		providers:     providers,
		newModel:      func(c config.Config) (llm.Streamer, error) { return modelForConfiguration(providers, c) },
		ensureHelpers: skipTestHelperDownloads,
		userHomeDir:   isolatedUserHome(t),
	}}
	configured, err := a.loadConfiguredModel(config.LoadOptions{Environment: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.initializeConfiguredModel(&configured); err != nil {
		t.Fatal(err)
	}
	current, err := currentACPRequestParts(t.Context(), a, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	summary := replaySummary{
		Model: string(configured.model.Provider) + "/" + configured.model.ID, Build: testedBuild(t),
		Prompt: promptMode, Tools: toolsMode, Runs: runs,
	}
	for _, c := range cases {
		result := replayCaseResult{ID: c.ID}
		request, err := c.request(t.Context(), root)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		if err := c.recordedReplyFails(t, root); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		if promptMode == "current" {
			request, err = withCurrentPrompt(request, current)
		}
		if err == nil && toolsMode == "current" {
			request, err = withCurrentTools(request, current)
		}
		if err == nil {
			request, err = withModel(request, configured.model)
		}
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		for run := range runs {
			reply, err := completeOnce(t.Context(), configured.service, request)
			summary.Usage = llm.AddUsage(summary.Usage, reply.Usage)
			if err != nil {
				result.Errors++
				t.Logf("%s run %d: %v", c.ID, run+1, err)
				continue
			}
			failures := judgeReplay(reply, c.Expect)
			if len(failures) == 0 {
				result.Passed++
			} else {
				result.Failures = append(result.Failures, strings.Join(failures, "; "))
			}
			t.Logf("%s run %d: %d rule failures", c.ID, run+1, len(failures))
		}
		summary.Passed += result.Passed
		summary.Total += runs
		summary.Cases = append(summary.Cases, result)
	}
	t.Logf("passed %d of %d replies (%s, prompt %s, tools %s)", summary.Passed, summary.Total, summary.Model, promptMode, toolsMode)
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repository, "evals", "replay", "results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := strings.NewReplacer("/", "_", " ", "_").Replace(fmt.Sprintf("%s@%s-prompt_%s-tools_%s.json", summary.Model, summary.Build, promptMode, toolsMode))
	if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testedBuild names the checkout under test like buildinfo.Build does for a
// binary; test binaries carry no VCS stamp.
func testedBuild(t *testing.T) string {
	revision, err := exec.Command("git", "rev-parse", "--short=12", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	build := buildinfo.Version + "+" + strings.TrimSpace(string(revision))
	// Earlier results do not change the build under test.
	if status, err := exec.Command("git", "status", "--porcelain", "--", ":/", ":(top,exclude)evals/replay/results").Output(); err != nil || len(status) > 0 {
		build += "-dirty"
	}
	return build
}

// recordedReplyFails checks that the case captures a failure: the recorded
// reply itself must violate a rule.
func (c replayCase) recordedReplyFails(t *testing.T, root string) error {
	snapshot, _, err := session.Read(t.Context(), filepath.Join(root, filepath.FromSlash(c.Session)))
	if err != nil {
		return err
	}
	for _, entry := range snapshot.Messages {
		if entry.ID != c.Message {
			continue
		}
		reply, ok := entry.Message.(llm.AssistantMessage)
		if !ok {
			return fmt.Errorf("message %s is not an assistant message", c.Message)
		}
		if len(judgeReplay(reply, c.Expect)) == 0 {
			return fmt.Errorf("the recorded reply already satisfies every rule")
		}
		return nil
	}
	return fmt.Errorf("message %s is missing", c.Message)
}

type replaySummary struct {
	Model  string             `json:"model"`
	Build  string             `json:"build"`
	Prompt string             `json:"prompt"`
	Tools  string             `json:"tools"`
	Runs   int                `json:"runs"`
	Passed int                `json:"passed"`
	Total  int                `json:"total"`
	Usage  llm.Usage          `json:"usage"`
	Cases  []replayCaseResult `json:"cases"`
}

type replayCaseResult struct {
	ID     string `json:"id"`
	Passed int    `json:"passed"`
	// Errors are failed model calls; they count as not passed.
	Errors   int      `json:"errors,omitempty"`
	Failures []string `json:"failures,omitempty"`
}
