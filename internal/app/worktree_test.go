package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// gitRepo creates a repository with one commit holding files.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repo := t.TempDir()
	for name, content := range files {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, repo, "init", "--quiet")
	// Windows runners default to core.autocrlf=true, which would check out
	// committed LF content as CRLF in worktrees.
	gitRun(t, repo, "config", "core.autocrlf", "false")
	gitRun(t, repo, "add", "--all")
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "--message", "init")
	return repo
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(t.Context(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A worktree starts from HEAD at the workspace's place in the repository;
// finishing commits what the child left to its branch, removes the
// checkout, and names a command that applies only the child's changes.
func TestWorktreeCarriesChildChangesOnABranch(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t, map[string]string{"sub/a.txt": "base\n", "b.txt": "base\n"})
	workspace := filepath.Join(repo, "sub")
	if err := os.MkdirAll(filepath.Join(workspace, ".aice", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".aice", "sessions", "s.jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirtyWorkspace(t.Context(), workspace) {
		t.Fatal("AICE's own files made the workspace dirty")
	}
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("parent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !dirtyWorkspace(t.Context(), workspace) {
		t.Fatal("an uncommitted change was not reported")
	}

	tree, err := newWorktree(t.Context(), workspace, filepath.Join(t.TempDir(), "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(tree.workspace, "a.txt")); err != nil || string(data) != "base\n" {
		t.Fatalf("worktree a.txt = %q, %v; want the committed content", data, err)
	}
	if err := os.WriteFile(filepath.Join(tree.workspace, "c.txt"), []byte("child\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	note := tree.finish(t.Context(), "add c")
	if !strings.Contains(note, "changes are on branch "+tree.branch) || !strings.Contains(note, "sub/c.txt") {
		t.Fatalf("note = %q", note)
	}
	if _, err := os.Stat(tree.dir); !os.IsNotExist(err) {
		t.Fatalf("worktree directory after finish: %v", err)
	}
	if got := gitRun(t, repo, "show", tree.branch+":sub/c.txt"); got != "child" {
		t.Fatalf("branch c.txt = %q", got)
	}

	apply := regexp.MustCompile(`(?m)^Apply: (.+) \(applied files`).FindStringSubmatch(note)
	if apply == nil {
		t.Fatalf("no apply command in %q", note)
	}
	shell, err := deps.FindNativeBash(deps.DefaultOptions())
	if err != nil {
		t.Skipf("native Bash unavailable: %v", err)
	}
	if out, err := exec.Command(shell, "-c", apply[1]).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", apply[1], err, out)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "c.txt")); err != nil || string(data) != "child\n" {
		t.Fatalf("applied c.txt = %q, %v", data, err)
	}
	if data, _ := os.ReadFile(filepath.Join(workspace, "a.txt")); string(data) != "parent\n" {
		t.Fatalf("apply changed the parent's own edit: a.txt = %q", data)
	}
}

// A child that changed nothing leaves neither a branch nor a checkout.
func TestWorktreeWithoutChangesLeavesNothing(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t, map[string]string{"a.txt": "base\n"})
	tree, err := newWorktree(t.Context(), repo, filepath.Join(t.TempDir(), "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	if note := tree.finish(t.Context(), "look"); note != "The subagent changed nothing in its worktree." {
		t.Fatalf("note = %q", note)
	}
	if branches := gitRun(t, repo, "branch", "--list", worktreeBranchPrefix+"*"); branches != "" {
		t.Fatalf("branches left = %q", branches)
	}
	if list := gitRun(t, repo, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Fatalf("worktrees left = %q", list)
	}
}

// A continued child works on its branch again: reopening checks out what
// it left, and the next finish adds to the same branch from the same base.
// A child that changed nothing starts again from the base, and one whose
// branch was deleted is told it does.
func TestWorktreeReopensForAContinuedChild(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t, map[string]string{"a.txt": "base\n"})
	tree, err := newWorktree(t.Context(), repo, filepath.Join(t.TempDir(), "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	if note, err := tree.reopen(t.Context()); err != nil || note != "" {
		t.Fatalf("reopening an open worktree: %q, %v", note, err)
	}
	tree.finish(t.Context(), "look")
	if note, err := tree.reopen(t.Context()); err != nil || note != "" {
		t.Fatalf("reopening after an empty turn: %q, %v", note, err)
	}
	if err := os.WriteFile(filepath.Join(tree.dir, "b.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree.finish(t.Context(), "first")
	if note, err := tree.reopen(t.Context()); err != nil || note != "" {
		t.Fatalf("reopening its branch: %q, %v", note, err)
	}
	if data, err := os.ReadFile(filepath.Join(tree.dir, "b.txt")); err != nil || string(data) != "one\n" {
		t.Fatalf("reopened b.txt = %q, %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(tree.dir, "c.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if note := tree.finish(t.Context(), "second"); !strings.Contains(note, "starting from commit "+tree.base[:12]) {
		t.Fatalf("note = %q", note)
	}
	if got := gitRun(t, repo, "diff", "--name-only", tree.base, tree.branch); got != "b.txt\nc.txt" {
		t.Fatalf("branch changes = %q", got)
	}
	// The parent applied the branch and deleted it: the child is told.
	gitRun(t, repo, "branch", "-D", tree.branch)
	note, err := tree.reopen(t.Context())
	if err != nil || !strings.Contains(note, "was deleted") {
		t.Fatalf("reopening a deleted branch: %q, %v", note, err)
	}
	if _, err := os.Stat(filepath.Join(tree.dir, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("b.txt after restarting from the base: %v", err)
	}
}

// Finishing also runs after cancellation and keeps a child's own commits.
func TestWorktreeFinishAfterCancellationKeepsChildCommits(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t, map[string]string{"a.txt": "base\n"})
	tree, err := newWorktree(t.Context(), repo, filepath.Join(t.TempDir(), "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.dir, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, tree.dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "--all", "--message", "child")
	if err := os.WriteFile(filepath.Join(tree.dir, "new.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if note := tree.finish(ctx, "stopped"); !strings.Contains(note, "changes are on branch") {
		t.Fatalf("note = %q", note)
	}
	if got := gitRun(t, repo, "diff", "--name-only", tree.base, tree.branch); got != "a.txt\nnew.txt" {
		t.Fatalf("branch changes = %q", got)
	}
}

func TestWorktreeNeedsACommit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	plain := t.TempDir()
	if _, err := newWorktree(t.Context(), plain, t.TempDir()); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("plain directory error = %v", err)
	}
	gitRun(t, plain, "init", "--quiet")
	if _, err := newWorktree(t.Context(), plain, t.TempDir()); err == nil || !strings.Contains(err.Error(), "commit to start from") {
		t.Fatalf("empty repository error = %v", err)
	}
}

// A worktree child may read the main workspace but neither write there nor
// run commands that reach into it; its own worktree and artifact directory
// are writable, and other outside paths follow the parent's policy.
func TestWorktreeGuardKeepsChildOutOfMainWorkspace(t *testing.T) {
	t.Parallel()
	main := gitRepo(t, map[string]string{"a.txt": "base\n"})
	main, _ = filepath.EvalSymlinks(main)
	_, parent, err := newExecutionGuard(main, guard.Config{Rules: guard.Rules{Deny: []string{"write(secret/**)"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := newWorktree(t.Context(), main, filepath.Join(t.TempDir(), "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tree.finish(context.Background(), "test") })
	tree.workspace, _ = filepath.EvalSymlinks(tree.workspace)
	tree.dir = tree.workspace
	artifacts := filepath.Join(main, ".aice", "agents", "bg-000001")
	adapter, err := parent.rebase(tree.workspace, main)
	if err != nil {
		t.Fatal(err)
	}
	adapter.inner.AllowPathSession(artifacts, true, true)
	gate := newWorktreeGuard(adapter, tree, main, artifacts)

	outside := filepath.Join(t.TempDir(), "x.txt")
	// Bash reads backslashes as escapes, so commands name Windows paths with
	// forward slashes.
	mainArg := filepath.ToSlash(main)
	for _, test := range []struct {
		name, tool string
		args       map[string]any
		want       agent.GuardDecision
		reason     string
	}{
		{"own file", "write", map[string]any{"path": "a.txt", "content": "x"}, agent.GuardAllow, ""},
		{"read main", "read", map[string]any{"path": filepath.Join(main, "a.txt")}, agent.GuardAllow, ""},
		{"write main", "write", map[string]any{"path": filepath.Join(main, "a.txt"), "content": "x"}, agent.GuardDeny, "in the main workspace"},
		{"edit main", "edit", map[string]any{"path": filepath.Join(main, "a.txt"), "old_string": "base", "new_string": "x"}, agent.GuardDeny, "in the main workspace"},
		{"artifact", "write", map[string]any{"path": filepath.Join(artifacts, "r.txt"), "content": "x"}, agent.GuardAllow, ""},
		{"rule follows", "write", map[string]any{"path": "secret/k", "content": "x"}, agent.GuardDeny, ""},
		{"elsewhere", "write", map[string]any{"path": outside, "content": "x"}, agent.GuardAsk, ""},
		{"git -C main", "bash", map[string]any{"command": "git -C " + mainArg + " status", "description": "d"}, agent.GuardDeny, "run commands there"},
		{"cd main", "bash", map[string]any{"command": "cd " + mainArg + " && ls", "description": "d"}, agent.GuardDeny, "run commands there"},
		{"GIT_DIR", "bash", map[string]any{"command": "GIT_DIR=/x/.git git log", "description": "d"}, agent.GuardDeny, "GIT_DIR"},
		{"own command", "bash", map[string]any{"command": "git status", "description": "d"}, agent.GuardAllow, ""},
	} {
		data, _ := json.Marshal(test.args)
		res, err := gate.Check(t.Context(), llm.ToolCall{ID: "c", Name: test.tool, Arguments: data})
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if res.Decision != test.want || !strings.Contains(res.Reason, test.reason) {
			t.Errorf("%s: decision %v %q, want %v %q", test.name, res.Decision, res.Reason, test.want, test.reason)
		}
	}
}

// A background general subagent in a git repository works in its own
// worktree by default: the parent learns it cannot see uncommitted changes,
// the child's writes and commands stay out of the main workspace, and its
// changes come back as a branch while the workspace keeps the parent's edit.
func TestApplicationPrintBackgroundWriterUsesWorktree(t *testing.T) {
	t.Parallel()
	workspace := gitRepo(t, map[string]string{"a.txt": "base\n"})
	workspace, _ = filepath.EvalSymlinks(workspace)
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("parent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	model := &worktreeSubagentModel{main: workspace}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) {
			return config.Config{APIKeys: map[string]string{"deepseek": "test-key"}}, nil
		},
		newModel:    func(config.Config) (agent.Model, error) { return model, nil },
		userHomeDir: func() (string, error) { return home, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs([]string{"--workspace", workspace, "--print", "change a.txt"})
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if output.String() != "parent done\n" {
		t.Fatalf("output = %q", output.String())
	}
	if !strings.Contains(model.started, "own git worktree") || !strings.Contains(model.started, "uncommitted changes") {
		t.Fatalf("start result = %q", model.started)
	}
	for i, want := range []string{"", "in the main workspace", "run commands there", ""} {
		if got := model.results[i]; want != "" && !strings.Contains(got, want) || want == "" && strings.Contains(got, "denied") {
			t.Errorf("child result %d = %q, want %q", i, got, want)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(workspace, "a.txt")); string(data) != "parent\n" {
		t.Fatalf("main a.txt = %q", data)
	}
	branch := regexp.MustCompile(`branch (aice/agent-[0-9a-f]{6})`).FindStringSubmatch(model.reported)
	if branch == nil || !strings.Contains(model.reported, "artifacts: ") {
		t.Fatalf("report = %q", model.reported)
	}
	if got := gitRun(t, workspace, "show", branch[1]+":a.txt"); got != "child" {
		t.Fatalf("branch a.txt = %q", got)
	}
	if list := gitRun(t, workspace, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Fatalf("worktrees left = %q", list)
	}
}

type worktreeSubagentModel struct {
	main string

	mu       sync.Mutex
	parent   int
	child    int
	id       string
	started  string
	results  []string
	reported string
}

func (m *worktreeSubagentModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(request.Tools) == 0 {
		return textEventStream(request.Model, "Title", llm.Usage{}, llm.StopReasonStop), nil
	}
	call := func(id, name string, arguments any) llm.Stream {
		data, _ := json.Marshal(arguments)
		return toolCallEventStream(request.Model, llm.ToolCall{ID: id, Name: name, Arguments: data})
	}
	last, _ := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
	if strings.Contains(request.SystemPrompt, "# Subagent") {
		m.child++
		if m.child > 1 {
			m.results = append(m.results, last.Content[0].Text)
		}
		artifacts := artifactDirPattern.FindStringSubmatch(request.SystemPrompt)
		switch m.child {
		case 1:
			return call("own", "write", map[string]any{"path": "a.txt", "content": "child"}), nil
		case 2:
			return call("main", "write", map[string]any{"path": filepath.Join(m.main, "a.txt"), "content": "child"}), nil
		case 3:
			return call("git", "bash", map[string]any{"command": "git -C " + filepath.ToSlash(m.main) + " add a.txt", "description": "stage"}), nil
		case 4:
			return call("artifact", "write", map[string]any{"path": filepath.Join(strings.TrimRight(artifacts[1], ".,"), "r.txt"), "content": "ok"}), nil
		}
		return textEventStream(request.Model, "changed a.txt", llm.Usage{}, llm.StopReasonStop), nil
	}
	m.parent++
	switch m.parent {
	case 1:
		return call("start", "agent", map[string]any{"description": "change", "prompt": "change a.txt", "run_in_background": true}), nil
	case 2:
		m.started = last.Content[0].Text
		m.id = regexp.MustCompile(`bg-[0-9a-f]{6}`).FindString(m.started)
		return call("wait", "task_output", map[string]any{"task_ids": []string{m.id}, "wait": 30}), nil
	}
	m.reported = last.Content[len(last.Content)-1].Text
	return textEventStream(request.Model, "parent done", llm.Usage{}, llm.StopReasonStop), nil
}
