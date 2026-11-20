package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/hostpath"
)

// ruleFileClaim denies a write to a file another writer is changing.
const ruleFileClaim = "agent.file_claim"

// mainWriter is the main agent and the synchronous subagents it waits for.
const mainWriter = "the main agent"

type writerKey struct{}

// withWriter marks ctx as a background writer's, named by its task ID.
func withWriter(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, writerKey{}, id)
}

func writerFrom(ctx context.Context) string {
	if id, ok := ctx.Value(writerKey{}).(string); ok {
		return id
	}
	return mainWriter
}

// writeClaims keeps concurrent writers off each other's files: the first
// writer to change a file holds it until that writer ends. Only writes the
// Guard can see are claimed; a shell command whose writes it cannot name is
// not, which only physical isolation would cover. Without a background
// writer nothing runs concurrently, so the main agent claims nothing.
type writeClaims struct {
	mu      sync.Mutex
	writers map[string]bool   // running background writers
	owners  map[string]string // path key → writer
}

// begin registers a background writer before it runs.
func (c *writeClaims) begin(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writers == nil {
		c.writers, c.owners = map[string]bool{}, map[string]string{}
	}
	c.writers[id] = true
}

// end releases a background writer's files; the main agent's claims end
// with the last background writer.
func (c *writeClaims) end(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.writers, id)
	for path, owner := range c.owners {
		if owner == id || len(c.writers) == 0 {
			delete(c.owners, path)
		}
	}
}

// conflict reports a path another writer holds. The caller holds mu.
func (c *writeClaims) conflictLocked(writer string, keys []string) (string, string) {
	for _, key := range keys {
		if owner, ok := c.owners[key]; ok && owner != writer {
			return key, owner
		}
	}
	return "", ""
}

// claim records keys for writer just before it writes, rechecking for a
// claim made since the Guard decided.
func (c *writeClaims) claim(writer string, keys []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if path, owner := c.conflictLocked(writer, keys); owner != "" {
		return claimError(path, owner)
	}
	if len(c.writers) == 0 {
		return nil
	}
	for _, key := range keys {
		c.owners[key] = writer
	}
	return nil
}

// apply denies a permitted write to a file another writer holds and makes
// the call claim its files when it is about to execute.
func (c *writeClaims) apply(ctx context.Context, workspace string, res agent.GuardResult) agent.GuardResult {
	if res.Decision == agent.GuardDeny {
		return res
	}
	var keys []string
	for _, action := range res.Actions {
		if action.Kind == "file" && action.Op == "write" {
			if key, ok := claimKey(workspace, action.Path); ok {
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 {
		return res
	}
	writer := writerFrom(ctx)
	c.mu.Lock()
	path, owner := c.conflictLocked(writer, keys)
	c.mu.Unlock()
	if owner != "" {
		return agent.GuardResult{Decision: agent.GuardDeny, RuleID: ruleFileClaim, Actions: res.Actions,
			Reason: claimError(path, owner).Error()}
	}
	prior := res.Revalidate
	res.Revalidate = func(ctx context.Context) error {
		if prior != nil {
			if err := prior(ctx); err != nil {
				return err
			}
		}
		return c.claim(writer, keys)
	}
	return res
}

func claimError(path, owner string) error {
	return fmt.Errorf("%s is being changed by %s, which runs concurrently; leave it alone, "+
		"or wait for that writer to finish (task_output) or stop it (task_kill)", hostpath.HomeDisplay(path), owner)
}

// claimKey names the file a write action targets: absolute, symlinks in its
// existing part resolved. Paths the shell would expand are not claimed.
func claimKey(workspace, path string) (string, bool) {
	if path == "" || strings.ContainsAny(path, "$`*?[") {
		return "", false
	}
	path = hostpath.ExpandTilde(path)
	if !filepath.IsAbs(path) {
		if workspace == "" {
			return "", false
		}
		path = filepath.Join(workspace, path)
	}
	path = filepath.Clean(path)
	// Resolve the deepest existing ancestor so a new file and a link to it
	// agree with the file once it exists.
	rest := ""
	for dir := path; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err == nil {
			if physical, err := filepath.EvalSymlinks(dir); err == nil {
				path = filepath.Join(physical, rest)
			}
			break
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
	return hostpath.Key(path), true
}
