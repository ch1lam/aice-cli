package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
)

const storeVersion = 1

// Entry is one stored decision for a canonical directory path.
type Entry struct {
	Path     string
	Decision Decision
}

// Update is one decision to record or clear. A DecisionUnknown update removes
// the entry, matching Pi's null decision.
type Update struct {
	Path     string
	Decision Decision
}

// normalizeKey canonicalizes a path for use as a store key. Windows paths are
// case-insensitive, so keys are lowercased there to keep Lookup and SetMany
// consistent regardless of the case callers pass in.
func normalizeKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// Store reads and writes the global project trust store. Its format is
// versioned and strictly validated; a corrupt store fails closed instead of
// silently trusting projects.
type Store struct {
	path string
}

// NewStore returns a Store backed by the given trust.json path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path returns the trust store file path.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Lookup returns the nearest stored decision for path, walking parents, and
// whether any entry matched.
func (s *Store) Lookup(path string) (Entry, bool, error) {
	if s == nil {
		return Entry{}, false, fmt.Errorf("trust: store is required")
	}
	release, err := acquireLock(lockPath(s.path))
	if err != nil {
		return Entry{}, false, err
	}
	defer release()

	data, err := readStore(s.path)
	if err != nil {
		return Entry{}, false, err
	}
	path = normalizeKey(path)
	for {
		if decision, ok := data[path]; ok {
			return Entry{Path: path, Decision: decision}, true, nil
		}
		parent, ok := ParentPath(path)
		if !ok {
			return Entry{}, false, nil
		}
		path = parent
	}
}

// Set records one decision for path. DecisionUnknown removes the entry.
func (s *Store) Set(path string, decision Decision) error {
	return s.SetMany([]Update{{Path: path, Decision: decision}})
}

// SetMany applies several updates atomically under one lock. A DecisionUnknown
// update removes its entry.
func (s *Store) SetMany(updates []Update) error {
	if s == nil {
		return fmt.Errorf("trust: store is required")
	}
	release, err := acquireLock(lockPath(s.path))
	if err != nil {
		return err
	}
	defer release()

	data, err := readStore(s.path)
	if err != nil {
		return err
	}
	for _, update := range updates {
		key := normalizeKey(update.Path)
		if update.Decision == DecisionUnknown {
			delete(data, key)
			continue
		}
		data[key] = update.Decision
	}
	return writeStore(s.path, data)
}

type storeFile struct {
	Version  int              `json:"version"`
	Projects map[string]*bool `json:"projects"`
}

func lockPath(path string) string {
	return path + ".lock"
}

func readStore(path string) (map[string]Decision, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Decision{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("trust: read store %s: %w", path, err)
	}
	var file storeFile
	if err := jsonutil.DecodeStrict(data, &file); err != nil {
		return nil, fmt.Errorf("trust: decode store %s: %w", path, err)
	}
	if file.Version != storeVersion {
		return nil, fmt.Errorf(
			"trust: store %s has unsupported version %d",
			path,
			file.Version,
		)
	}
	projects := make(map[string]Decision, len(file.Projects))
	for key, value := range file.Projects {
		if value == nil {
			return nil, fmt.Errorf(
				"trust: store %s: null decision for %q",
				path,
				key,
			)
		}
		projects[key] = decisionFromBool(*value)
	}
	return projects, nil
}

func writeStore(path string, data map[string]Decision) error {
	file := storeFile{
		Version:  storeVersion,
		Projects: make(map[string]*bool, len(data)),
	}
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := data[key] == DecisionTrusted
		file.Projects[key] = &value
	}
	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("trust: encode store: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("trust: create store directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("trust: create store temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("trust: set store permissions: %w", err)
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("trust: write store temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("trust: sync store temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("trust: close store temp file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("trust: replace store %s: %w", path, err)
	}
	return nil
}
