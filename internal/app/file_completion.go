package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const maximumSearchEntries = 20000

// CompleteFiles searches names only. Completion never prompts for or grants
// access, follows no directory symlinks, and stops at a fixed traversal budget.
func (s *interactiveSession) CompleteFiles(ctx context.Context, query string) ([]interaction.FileCompletion, error) {
	if s.workspace == nil {
		return nil, nil
	}
	root := s.workspace.PhysicalPath()
	query = filepath.ToSlash(query)
	prefix, needle := "", query
	recursive := true
	if cut := strings.LastIndex(query, "/"); cut >= 0 {
		candidate := hostpath.ExpandTilde(filepath.FromSlash(query[:cut+1]))
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			root = candidate
			prefix = query[:cut+1]
			needle = query[cut+1:]
			recursive = false
		} else if filepath.IsAbs(filepath.FromSlash(query)) || strings.HasPrefix(query, "~") || strings.HasPrefix(query, "../") {
			return nil, nil
		}
	}
	if !s.completionPathAllowed(ctx, root) {
		return nil, nil
	}
	type match struct {
		item  interaction.FileCompletion
		score int
	}
	var matches []match
	visited := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			return nil
		}
		if path == root {
			return nil
		}
		visited++
		if visited > maximumSearchEntries {
			return fs.SkipAll
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".aice" || entry.Name() == "node_modules" || entry.Name() == "vendor") {
			return fs.SkipDir
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if score, ok := fileMatchScore(relative, needle); ok {
			name := prefix + relative
			if entry.IsDir() {
				name += "/"
			}
			matches = append(matches, match{interaction.FileCompletion{Path: name, Directory: entry.IsDir()}, score})
		}
		if entry.IsDir() && !recursive {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].item.Path < matches[j].item.Path
	})
	var result []interaction.FileCompletion
	for _, m := range matches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		full := hostpath.ExpandTilde(filepath.FromSlash(m.item.Path))
		if !filepath.IsAbs(full) {
			full = filepath.Join(s.workspace.PhysicalPath(), full)
		}
		if s.completionPathAllowed(ctx, full) {
			result = append(result, m.item)
			if len(result) == 8 {
				break
			}
		}
	}
	return result, nil
}

func (s *interactiveSession) completionPathAllowed(ctx context.Context, path string) bool {
	args, _ := json.Marshal(map[string]string{"path": path})
	result, err := s.guardAdapter.Check(ctx, llm.ToolCall{Name: "read", Arguments: args})
	return err == nil && result.Decision == agent.GuardAllow
}

// fileMatchScore prefers basename prefixes, then path prefixes, then ordered
// subsequences. Shorter, contiguous matches sort ahead of scattered characters.
func fileMatchScore(path, query string) (int, bool) {
	path, query = strings.ToLower(path), strings.ToLower(query)
	if query == "" {
		return len([]rune(path)), true
	}
	if strings.HasPrefix(filepath.Base(path), query) {
		return len([]rune(path)), true
	}
	if strings.HasPrefix(path, query) {
		return 100 + len([]rune(path)), true
	}
	q := []rune(query)
	position, last, penalty := 0, -1, 0
	for i, r := range []rune(path) {
		if r != q[position] {
			continue
		}
		if last >= 0 {
			penalty += i - last - 1
		}
		last = i
		position++
		if position == len(q) {
			return 1000 + penalty*10 + len([]rune(path)), true
		}
	}
	return 0, false
}

var _ interaction.FileCompleter = (*interactiveSession)(nil)
