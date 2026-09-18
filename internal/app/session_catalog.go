package app

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

const sessionCatalogEntries = 256
const sessionCatalogBytes = 32 << 20

// Only derived prose is retained, never tool payloads or writable snapshots.
// Entries are immutable after publication; the mutex covers LRU bookkeeping,
// never replay or filesystem I/O. The interactive application owns its lifetime.
type sessionCatalog struct {
	mu      sync.Mutex
	entries map[string]*sessionCatalogEntry
	bytes   int
	clock   uint64
}

type sessionCatalogEntry struct {
	info              os.FileInfo
	summary           interaction.SessionSummary
	prose             []sessionProse
	incomplete, empty bool
	bytes             int
	used              uint64
}

type sessionProse struct {
	id, text     string
	user, active bool
}

func sameSessionFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime() == b.ModTime()
}

func (c *sessionCatalog) get(key string, info os.FileInfo) *sessionCatalogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return nil
	}
	if !sameSessionFile(entry.info, info) {
		c.bytes -= entry.bytes
		delete(c.entries, key)
		return nil
	}
	c.clock++
	entry.used = c.clock
	return entry
}

func (c *sessionCatalog) put(key string, entry *sessionCatalogEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.entries[key]; old != nil {
		c.bytes -= old.bytes
		delete(c.entries, key)
	}
	if entry.bytes > sessionCatalogBytes {
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]*sessionCatalogEntry)
	}
	for len(c.entries) >= sessionCatalogEntries || c.bytes+entry.bytes > sessionCatalogBytes {
		oldest := ""
		var used uint64 = ^uint64(0)
		for k, e := range c.entries {
			if e.used < used {
				oldest, used = k, e.used
			}
		}
		c.bytes -= c.entries[oldest].bytes
		delete(c.entries, oldest)
	}
	c.clock++
	entry.used = c.clock
	c.entries[key] = entry
	c.bytes += entry.bytes
}

func (c *sessionCatalog) prune(present map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if !present[key] {
			c.bytes -= entry.bytes
			delete(c.entries, key)
		}
	}
}

func (s *interactiveSession) catalogSession(ctx context.Context, key string) (*sessionCatalogEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.sessionSelection(key)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if entry := s.catalog.get(key, info); entry != nil {
		return entry, nil
	}
	snapshot, incomplete, err := s.readSelectedSession(ctx, key)
	if err != nil {
		return nil, err
	}
	branch, err := session.ActiveBranch(snapshot)
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(branch))
	for _, node := range branch {
		active[node.ID] = true
	}
	entry := &sessionCatalogEntry{info: info, incomplete: incomplete,
		empty:   len(snapshot.Messages) == 0 && len(snapshot.Compactions) == 0,
		summary: interaction.SessionSummary{Key: key, ID: snapshot.Header.ID, UpdatedAt: snapshot.Header.CreatedAt}}
	// New and old sessions share one rule: use the latest title, falling back
	// to the first user question only when the title is absent or empty.
	for _, title := range snapshot.Titles {
		entry.summary.Title = title.Title
		entry.summary.UpdatedAt = max(entry.summary.UpdatedAt, title.CreatedAt)
	}
	for _, message := range snapshot.Messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry.summary.UpdatedAt = max(entry.summary.UpdatedAt, message.CreatedAt)
		text := sessionMessageText(message.Message)
		if text == "" {
			continue
		}
		_, user := message.Message.(llm.UserMessage)
		if user && entry.summary.Title == "" {
			entry.summary.Title = sessionExcerpt(strings.Join(strings.Fields(sessionExcerpt(text, "", 200)), " "), "", 70)
		}
		entry.prose = append(entry.prose, sessionProse{id: message.ID, text: text, user: user, active: active[message.ID]})
		if active[message.ID] {
			entry.summary.Snippet = sessionExcerpt(text, "", 160)
		}
		entry.bytes += len(text) + len(message.ID) + 64
	}
	for _, c := range snapshot.Compactions {
		entry.summary.UpdatedAt = max(entry.summary.UpdatedAt, c.CreatedAt)
	}
	for _, l := range snapshot.LeafMoves {
		entry.summary.UpdatedAt = max(entry.summary.UpdatedAt, l.CreatedAt)
	}
	if entry.summary.Title == "" {
		entry.summary.Title = key
	}
	entry.bytes += len(key) + len(entry.summary.ID) + len(entry.summary.Title) + len(entry.summary.Snippet) + 512
	// A concurrent append/replacement must never label an old prefix as fresh.
	after, err := os.Lstat(path)
	if err == nil && sameSessionFile(info, after) {
		s.catalog.put(key, entry)
	}
	return entry, ctx.Err()
}
