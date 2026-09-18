package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

var _ interaction.SessionBrowser = (*interactiveSession)(nil)

func (s *interactiveSession) sessionDirectory() string {
	return filepath.Join(s.workspacePath, ".aice", "sessions")
}

// sessionSelection only resolves catalog tokens inside this project's store.
// Explicit paths remain a startup --session capability, not a picker input.
func (s *interactiveSession) sessionSelection(key string) (string, error) {
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, "/\\\x00") {
		return "", fmt.Errorf("app: invalid session selection")
	}
	path := filepath.Join(s.sessionDirectory(), key+".jsonl")
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("app: find session: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("app: session must be a regular file")
	}
	return path, nil
}

func (s *interactiveSession) readSelectedSession(ctx context.Context, key string) (session.Snapshot, bool, error) {
	path, err := s.sessionSelection(key)
	if err != nil {
		return session.Snapshot{}, false, err
	}
	snapshot, incomplete, err := session.Read(ctx, path)
	if err == nil && filepath.Clean(snapshot.Header.WorkingDirectory) != s.workspace.Path() {
		err = fmt.Errorf("app: session belongs to another working directory")
	}
	return snapshot, incomplete, err
}

func (s *interactiveSession) SearchSessions(ctx context.Context, query string) ([]interaction.SessionSummary, error) {
	return s.ScanSessions(ctx, query, nil)
}

func (s *interactiveSession) ScanSessions(ctx context.Context, query string, publish func([]interaction.SessionSummary) error) ([]interaction.SessionSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(s.sessionDirectory())
	if errors.Is(err, os.ErrNotExist) {
		s.catalog.prune(nil)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("app: list sessions: %w", err)
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var items []interaction.SessionSummary
	present := make(map[string]bool, len(files))
	type candidate struct {
		key      string
		modified int64
	}
	candidates := make([]candidate, 0, len(files))
	for _, file := range files {
		if file.Type().IsRegular() && strings.HasSuffix(file.Name(), ".jsonl") {
			key := strings.TrimSuffix(file.Name(), ".jsonl")
			present[key] = true
			modified := int64(0)
			if info, err := file.Info(); err == nil {
				modified = info.ModTime().UnixMilli()
			}
			candidates = append(candidates, candidate{key, modified})
		}
	}
	s.catalog.prune(present)
	// File timestamps prioritize cold discovery; displayed ordering always uses
	// validated record activity, including branch moves and compaction.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modified > candidates[j].modified })
	lastPublish := time.Now()
	for index, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := candidate.key
		item := interaction.SessionSummary{Key: key, Title: key}
		entry, err := s.catalogSession(ctx, key)
		matched := false
		if err != nil {
			item.Problem = err.Error()
			item.UpdatedAt = candidate.modified
		} else {
			if entry.empty {
				continue
			}
			item = entry.summary
			if query != "" {
				item.Snippet = ""
				for _, prose := range entry.prose {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if strings.Contains(strings.ToLower(prose.text), query) {
						item.Snippet = sessionExcerpt(prose.text, query, 160)
						matched = true
						break
					}
				}
			}
		}
		if query == "" || strings.Contains(strings.ToLower(item.Title), query) || strings.Contains(strings.ToLower(item.Key), query) || matched {
			items = append(items, item)
		}
		if publish != nil && index < len(candidates)-1 && (index == 7 || time.Since(lastPublish) >= 100*time.Millisecond) {
			sortSessionSummaries(items)
			if err := publish(append([]interaction.SessionSummary(nil), items...)); err != nil {
				return nil, err
			}
			lastPublish = time.Now()
		}
	}
	sortSessionSummaries(items)
	return items, ctx.Err()
}

func sortSessionSummaries(items []interaction.SessionSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].Key < items[j].Key
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
}

func (s *interactiveSession) PreviewSession(ctx context.Context, key, query string) (string, error) {
	entry, err := s.catalogSession(ctx, key)
	if err != nil {
		return "", err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var parts []string
	userSeen, assistantSeen := false, false
	for i := len(entry.prose) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		prose := entry.prose[i]
		if query == "" {
			if !prose.active || (prose.user && userSeen) || (!prose.user && assistantSeen) {
				continue
			}
		} else if !strings.Contains(strings.ToLower(prose.text), query) {
			continue
		}
		label := "Assistant"
		if prose.user {
			label = "You"
			userSeen = true
		} else {
			assistantSeen = true
		}
		if !prose.active {
			label += " · another branch (resume keeps the active branch)"
		}
		parts = append(parts, label+"\n"+sessionExcerpt(prose.text, query, 1200))
		if (query == "" && userSeen && assistantSeen) || len(parts) == 6 {
			break
		}
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	if len(parts) == 0 {
		parts = append(parts, "No matching conversation text on this view.")
	}
	parts = append([]string{"Last activity · " + time.UnixMilli(entry.summary.UpdatedAt).Local().Format("2006-01-02 15:04")}, parts...)
	if entry.incomplete {
		parts = append(parts, "Incomplete final record; showing the complete prefix. Repair happens only on resume.")
	}
	return strings.Join(parts, "\n\n"), nil
}

// Search conversation prose only. Tool payloads and encoded images are not
// indexed; source records are retained unchanged for actual restoration.
func sessionMessageText(message llm.AgentMessage) string {
	switch message := message.(type) {
	case llm.UserMessage:
		return userContent(message)
	case llm.AssistantMessage:
		text, _ := assistantContent(message)
		return text
	default:
		return ""
	}
}

func sessionExcerpt(text, query string, limit int) string {
	start := 0
	if query != "" {
		// Match offsets belong to the lowercased string; Unicode case mapping
		// can change byte widths, so translate through rune positions.
		lower := strings.ToLower(text)
		if index := strings.Index(lower, query); index >= 0 {
			skip := max(0, utf8.RuneCountInString(lower[:index])-40)
			for i := range text {
				if skip == 0 {
					start = i
					break
				}
				skip--
			}
		}
	}
	end, count := len(text), 0
	for i := range text[start:] {
		if count == limit {
			end = start + i
			break
		}
		count++
	}
	result := text[start:end]
	if start > 0 {
		result = "…" + result
	}
	if end < len(text) {
		result += "…"
	}
	return result
}
