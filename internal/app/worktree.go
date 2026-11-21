package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// worktreeBranchPrefix names the branches that carry worktree subagents'
// changes back to the parent.
const worktreeBranchPrefix = "aice/agent-"

// maxWorktreeStatLines bounds the change summary in a worktree child's note.
const maxWorktreeStatLines = 40

// worktreeMu serializes AICE's changes to repositories' shared worktree
// list and branches; concurrent subagents start and finish independently.
var worktreeMu sync.Mutex

// worktree is a writing subagent's own git checkout of the commit its parent
// was on. The child changes files only there; when it ends, AICE commits
// what it left to a branch and removes the checkout, so its changes reach
// the main workspace only when the parent or the user applies the branch.
type worktree struct {
	repo string // the main checkout's top level
	dir  string // the worktree's top level
	// workspace is where the child works: the main workspace's place in the
	// repository, inside the worktree.
	workspace string
	base      string // the commit it started from
	branch    string
	// changed records that finish left changes on the branch.
	changed bool
}

// worktreeRoot holds worktree subagents' checkouts, outside every
// workspace; empty when the home directory is unknown.
func (a *application) worktreeRoot() string {
	home, err := a.userHome()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".aice", "worktrees")
}

// newWorktree checks out HEAD of the repository containing workspace into a
// new worktree under root, on a new branch.
func newWorktree(ctx context.Context, workspace, root string) (*worktree, error) {
	repo, err := runGit(ctx, workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("a worktree needs the workspace in a git repository: %w", err)
	}
	base, err := runGit(ctx, workspace, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return nil, errors.New("a worktree needs a commit to start from; the repository has none")
	}
	repo = filepath.FromSlash(repo)
	physical, err := hostpath.Canonical(workspace)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(repo, physical)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("workspace %s is not inside repository %s", workspace, repo)
	}
	var suffix [3]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(suffix[:])
	w := &worktree{
		repo:   repo,
		dir:    filepath.Join(root, filepath.Base(repo)+"-"+id),
		base:   base,
		branch: worktreeBranchPrefix + id,
	}
	w.workspace = filepath.Join(w.dir, rel)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create worktree directory: %w", err)
	}
	worktreeMu.Lock()
	defer worktreeMu.Unlock()
	// Forget worktrees whose directories a crash or a person removed.
	if _, err := runGit(ctx, repo, "worktree", "prune"); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, repo, "worktree", "add", "--quiet", "-b", w.branch, w.dir, base); err != nil {
		return nil, err
	}
	return w, nil
}

// reopen checks a continued child's branch out again; finish removed the
// checkout, unless it kept one it could not settle. A missing branch starts
// again from the base: finish deleted it because the child changed nothing,
// or someone deleted it, perhaps after applying it, which note tells the
// child.
func (w *worktree) reopen(ctx context.Context) (note string, err error) {
	if _, err := os.Stat(w.dir); err == nil {
		return "", nil
	}
	worktreeMu.Lock()
	defer worktreeMu.Unlock()
	if _, err := runGit(ctx, w.repo, "worktree", "prune"); err != nil {
		return "", err
	}
	if _, err := runGit(ctx, w.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+w.branch); err == nil {
		_, err := runGit(ctx, w.repo, "worktree", "add", "--quiet", w.dir, w.branch)
		return "", err
	}
	if _, err := runGit(ctx, w.repo, "worktree", "add", "--quiet", "-b", w.branch, w.dir, w.base); err != nil {
		return "", err
	}
	if w.changed {
		w.changed = false
		note = fmt.Sprintf("[AICE: your branch %s was deleted, perhaps after the agent that started you applied it, so your worktree starts again from commit %s without your earlier changes.]\n\n",
			w.branch, w.base[:12])
	}
	return note, nil
}

// dirtyWorkspace reports whether workspace has uncommitted changes a worktree child
// will not see. AICE's own directory is not part of the project.
func dirtyWorkspace(ctx context.Context, workspace string) bool {
	out, err := runGit(ctx, workspace, "status", "--porcelain", "--", ".", ":(exclude).aice")
	return err == nil && out != ""
}

// finish commits what the child left to its branch and removes the
// worktree. It runs after the child stopped for any reason, including
// cancellation, and returns the note that tells the parent where the
// changes are. A worktree it could not settle stays for a person to recover.
func (w *worktree) finish(ctx context.Context, description string) string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	tip, err := w.commit(ctx, description)
	worktreeMu.Lock()
	defer worktreeMu.Unlock()
	if err != nil {
		return fmt.Sprintf("The subagent's worktree %s (branch %s) was kept because AICE could not commit its changes: %v",
			w.dir, w.branch, err)
	}
	if _, err := runGit(ctx, w.repo, "worktree", "remove", "--force", w.dir); err != nil {
		return fmt.Sprintf("The subagent's changes are on branch %s, but its worktree %s could not be removed: %v", w.branch, w.dir, err)
	}
	w.changed = tip != w.base
	if !w.changed {
		if _, err := runGit(ctx, w.repo, "branch", "--quiet", "-D", w.branch); err != nil {
			return fmt.Sprintf("The subagent changed nothing; its empty branch %s could not be deleted: %v", w.branch, err)
		}
		return "The subagent changed nothing in its worktree."
	}
	stat, err := runGit(ctx, w.repo, "diff", "--stat=100", w.base, w.branch)
	if err != nil {
		stat = err.Error()
	}
	if lines := strings.Split(stat, "\n"); len(lines) > maxWorktreeStatLines {
		stat = strings.Join(append(lines[:maxWorktreeStatLines-1], fmt.Sprintf(" ... %d more lines", len(lines)-maxWorktreeStatLines+1)), "\n")
	}
	repo, diff := shellWord(w.repo), "diff "+w.base[:12]+" "+w.branch
	return fmt.Sprintf("The subagent's changes are on branch %s, starting from commit %s; nothing was applied to the workspace:\n%s\n"+
		"Review: git -C %s %s\n"+
		"Apply: git -C %s %s | git -C %s apply --3way (applied files are staged; resolve any conflict markers)\n"+
		"Delete the branch once it is no longer needed: git -C %s branch -D %s",
		w.branch, w.base[:12], stat, repo, diff, repo, diff, repo, repo, w.branch)
}

// commit records the worktree's state on the branch and returns its commit.
// The child may have committed or switched branches itself; the branch
// follows whatever it left checked out.
func (w *worktree) commit(ctx context.Context, description string) (string, error) {
	if _, err := runGit(ctx, w.dir, "add", "--all"); err != nil {
		return "", err
	}
	status, err := runGit(ctx, w.dir, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if status != "" {
		if _, err := runGit(ctx, w.dir, "-c", "user.name=AICE", "-c", "user.email=aice@localhost", "-c", "commit.gpgSign=false",
			"commit", "--quiet", "--no-verify", "--message", "AICE subagent: "+description); err != nil {
			return "", err
		}
	}
	tip, err := runGit(ctx, w.dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	worktreeMu.Lock()
	defer worktreeMu.Unlock()
	if _, err := runGit(ctx, w.repo, "update-ref", "refs/heads/"+w.branch, tip); err != nil {
		return "", err
	}
	return tip, nil
}

// runGit runs one git command in dir and returns its trimmed output. AICE's
// own git calls run no repository hooks and never prompt, and the caller's
// environment cannot point them at another repository.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "GIT_DIR" && name != "GIT_WORK_TREE" && name != "GIT_INDEX_FILE" && name != "GIT_COMMON_DIR" {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_TERMINAL_PROMPT=0")
	var stderr strings.Builder
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			err = fmt.Errorf("%w: %s", err, detail)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

var plainShellWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellWord quotes text for a command line the parent may run.
func shellWord(text string) string {
	if plainShellWord.MatchString(text) {
		return text
	}
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// ruleWorktree denies a worktree subagent's reach into the main workspace.
const ruleWorktree = "agent.worktree"

// gitRedirect matches variables that point git at another repository
// without naming a path the Guard can check.
var gitRedirect = regexp.MustCompile(`\bGIT_(DIR|WORK_TREE|COMMON_DIR|INDEX_FILE|OBJECT_DIRECTORY)\b`)

// worktreeGuard keeps a worktree child out of the main workspace, whose
// files it has its own copies of. The file tools may read there, since the
// parent's prompt may name main-workspace paths; writes, and shell commands
// that reach into it at all, are denied, except in the child's artifact
// directory. The checks read only paths the Guard can name: a command that
// writes there through a computed path is not caught.
type worktreeGuard struct {
	inner agent.Guard
	// root is where relative paths start; main, tree and artifacts are
	// comparison keys.
	root, main, tree, artifacts string
}

func newWorktreeGuard(inner agent.Guard, tree *worktree, main, artifacts string) worktreeGuard {
	g := worktreeGuard{inner: inner, root: tree.workspace}
	g.main, _ = claimKey("", main)
	g.tree, _ = claimKey("", tree.dir)
	if artifacts != "" {
		g.artifacts, _ = claimKey("", artifacts)
	}
	return g
}

func (g worktreeGuard) Check(ctx context.Context, call llm.ToolCall) (agent.GuardResult, error) {
	res, err := g.inner.Check(ctx, call)
	if err != nil || res.Decision == agent.GuardDeny {
		return res, err
	}
	deny := func(reason string) (agent.GuardResult, error) {
		return agent.GuardResult{Decision: agent.GuardDeny, RuleID: ruleWorktree, Actions: res.Actions, Reason: reason}, nil
	}
	if call.Name == "bash" && gitRedirect.Match(call.Arguments) {
		return deny("you work in your own git worktree; do not point git at another repository with GIT_DIR or similar variables")
	}
	for _, action := range res.Actions {
		if action.Kind != "file" {
			continue
		}
		path, ok := claimKey(g.root, action.Path)
		if !ok || !hostpath.Within(g.main, path) || hostpath.Within(g.tree, path) || hostpath.Within(g.artifacts, path) {
			continue
		}
		if call.Name == "bash" {
			return deny(fmt.Sprintf("you work in your own git worktree %s; run commands there, not in the main workspace %s",
				hostpath.HomeDisplay(g.root), hostpath.HomeDisplay(g.main)))
		}
		if action.Op == "write" {
			return deny(fmt.Sprintf("%s is in the main workspace; you work in your own git worktree, so change the same path under %s",
				hostpath.HomeDisplay(path), hostpath.HomeDisplay(g.root)))
		}
	}
	return res, nil
}

// rebase returns a gate with this one's policy over a worktree child's
// workspace. readOnlyRoots may be read without asking. It starts with no
// Session grants, MCP bindings or file claims.
func (g *guardAdapter) rebase(workspace string, readOnlyRoots ...string) (*guardAdapter, error) {
	inner, err := g.inner.Rebase(workspace, readOnlyRoots...)
	if err != nil {
		return nil, err
	}
	return &guardAdapter{inner: inner, auto: g.auto.in(workspace), yolo: g.yolo}, nil
}

// worktreeTools rebinds the tools that work on files to workspace.
func worktreeTools(ctx context.Context, tools []agent.Tool, workspace *tool.Workspace) []agent.Tool {
	out := make([]agent.Tool, 0, len(tools))
	for _, current := range tools {
		name := current.Definition().Name
		var err error
		switch name {
		case "read":
			if reader, ok := current.(*tool.Read); ok {
				current, err = reader.In(workspace)
			} else {
				current, err = tool.NewRead(workspace)
			}
		case "write":
			current, err = tool.NewWrite(workspace)
		case "edit":
			current, err = tool.NewEdit(workspace)
		case "bash":
			current, err = tool.NewBash(ctx, workspace, hostBashShells(), nil)
		case "grep":
			current, err = tool.NewGrep(workspace)
		case "find":
			current, err = tool.NewFind(workspace)
		case "ls":
			current, err = tool.NewLS(workspace)
		}
		if err != nil {
			current = tool.NewUnavailable(name, err)
		}
		out = append(out, current)
	}
	return out
}
