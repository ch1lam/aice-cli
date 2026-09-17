package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(s.sessionDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("app: list sessions: %w", err)
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var items []interaction.SessionSummary
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !file.Type().IsRegular() || !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		key := strings.TrimSuffix(file.Name(), ".jsonl")
		item := interaction.SessionSummary{Key: key, Title: key}
		snapshot, _, err := s.readSelectedSession(ctx, key)
		if err != nil {
			item.Problem = err.Error()
			if info, statErr := file.Info(); statErr == nil {
				item.UpdatedAt = info.ModTime().UnixMilli()
			}
		} else {
			if len(snapshot.Messages) == 0 && len(snapshot.Compactions) == 0 {
				continue
			}
			item.ID = snapshot.Header.ID
			item.UpdatedAt = snapshot.Header.CreatedAt
			titled := false
			for _, entry := range snapshot.Messages {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				item.UpdatedAt = max(item.UpdatedAt, entry.CreatedAt)
				text := sessionMessageText(entry.Message)
				if _, user := entry.Message.(llm.UserMessage); user && !titled && text != "" {
					item.Title = sessionExcerpt(strings.Join(strings.Fields(text), " "), "", 70)
					titled = true
				}
				if query != "" && item.Snippet == "" && strings.Contains(strings.ToLower(text), query) {
					item.Snippet = sessionExcerpt(text, query, 160)
				}
			}
			for _, entry := range snapshot.Compactions {
				item.UpdatedAt = max(item.UpdatedAt, entry.CreatedAt)
			}
			for _, entry := range snapshot.LeafMoves {
				item.UpdatedAt = max(item.UpdatedAt, entry.CreatedAt)
			}
		}
		if query == "" || strings.Contains(strings.ToLower(item.Title), query) ||
			strings.Contains(strings.ToLower(item.Key), query) || item.Snippet != "" {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].Key < items[j].Key
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items, ctx.Err()
}

func (s *interactiveSession) PreviewSession(ctx context.Context, key, query string) (string, error) {
	snapshot, incomplete, err := s.readSelectedSession(ctx, key)
	if err != nil {
		return "", err
	}
	branch, err := session.ActiveBranch(snapshot)
	if err != nil {
		return "", err
	}
	active := make(map[string]bool, len(branch))
	for _, node := range branch {
		active[node.ID] = true
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var parts []string
	for _, entry := range snapshot.Messages {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		text := sessionMessageText(entry.Message)
		if text == "" || (query == "" && !active[entry.ID]) ||
			(query != "" && !strings.Contains(strings.ToLower(text), query)) {
			continue
		}
		label := "Assistant"
		if _, user := entry.Message.(llm.UserMessage); user {
			label = "You"
		}
		if !active[entry.ID] {
			label += " · another branch (resume keeps the active branch)"
		}
		parts = append(parts, label+"\n"+sessionExcerpt(text, query, 1200))
		if len(parts) > 6 {
			parts = parts[1:]
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "No matching conversation text on this view.")
	}
	if incomplete {
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
	runes := []rune(text)
	start := 0
	if query != "" {
		// Locate in lowercased runes: Unicode case mapping can change byte widths.
		lower := strings.ToLower(text)
		if index := strings.Index(lower, query); index >= 0 {
			start = max(0, utf8.RuneCountInString(lower[:index])-40)
		}
	}
	end := min(len(runes), start+limit)
	result := string(runes[start:end])
	if start > 0 {
		result = "…" + result
	}
	if end < len(runes) {
		result += "…"
	}
	return result
}
