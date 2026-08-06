package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

const (
	maxProjectPromptBytes = 64 * 1024

	// Forward-slash names work on every platform; os.Root and os.Open accept
	// them on Windows.
	projectSystemPromptFile    = ".aice/SYSTEM.md"
	projectAppendPromptFile    = ".aice/APPEND_SYSTEM.md"
	projectAgentsFile          = "AGENTS.md"
	globalSystemPromptFile     = "SYSTEM.md"
	globalAppendPromptFile     = "APPEND_SYSTEM.md"
	projectAppendBoundaryLabel = "Project instructions from %s:\n"
	projectAgentsBoundaryLabel = "Project guidance from %s:\n"
)

// assembleSystemPrompt resolves the effective system prompt for one trusted
// run. The base comes from the project SYSTEM.md when trusted, then the global
// SYSTEM.md, then the built-in default. The project AGENTS.md is appended to
// the base when trusted, then APPEND_SYSTEM.md appends last using the same
// precedence so explicit instructions win. Project files are only read when
// the trust decision allows it; global user files are always eligible.
func assembleSystemPrompt(
	workspace *tool.Workspace,
	configuration config.Config,
	decision trust.Decision,
) (string, error) {
	trusted := decision == trust.DecisionTrusted
	base, err := resolvePromptBase(workspace, configuration, trusted)
	if err != nil {
		return "", err
	}
	agentsContent, agentsPath, _, err := readProjectPromptWithPath(workspace, projectAgentsFile, trusted)
	if err != nil {
		return "", err
	}
	if agentsContent != "" {
		base += "\n\n" + fmt.Sprintf(projectAgentsBoundaryLabel, agentsPath) + agentsContent
	}
	appendContent, appendPath, err := resolvePromptAppend(workspace, configuration, trusted)
	if err != nil {
		return "", err
	}
	if appendContent != "" {
		base += "\n\n" + fmt.Sprintf(projectAppendBoundaryLabel, appendPath) + appendContent
	}
	return base, nil
}

// resolvePromptBase picks the base system prompt following Pi's SYSTEM.md
// discovery: project when trusted, then global, then the built-in default.
func resolvePromptBase(
	workspace *tool.Workspace,
	configuration config.Config,
	trusted bool,
) (string, error) {
	content, ok, err := readProjectPrompt(workspace, projectSystemPromptFile, trusted)
	if err != nil {
		return "", err
	}
	if ok {
		return content, nil
	}
	content, ok, err = readGlobalPrompt(
		configuration,
		globalSystemPromptFile,
	)
	if err != nil {
		return "", err
	}
	if ok {
		return content, nil
	}
	return defaultSystemPrompt, nil
}

// resolvePromptAppend picks the appended project instructions following Pi's
// APPEND_SYSTEM.md discovery: project when trusted, then global.
func resolvePromptAppend(
	workspace *tool.Workspace,
	configuration config.Config,
	trusted bool,
) (string, string, error) {
	content, path, ok, err := readProjectPromptWithPath(workspace, projectAppendPromptFile, trusted)
	if err != nil {
		return "", "", err
	}
	if ok {
		return content, path, nil
	}
	content, path, ok, err = readGlobalPromptWithPath(configuration, globalAppendPromptFile)
	if err != nil {
		return "", "", err
	}
	if ok {
		return content, path, nil
	}
	return "", "", nil
}

// readProjectPrompt reads a workspace-rooted prompt source. It is only read
// when trusted; a missing or blank file yields present=false. A present file
// must be a regular, valid UTF-8 file at most maxProjectPromptBytes or the run
// fails closed. The os.Root handle prevents symlink escape and parent
// traversal.
func readProjectPrompt(
	workspace *tool.Workspace,
	rel string,
	trusted bool,
) (string, bool, error) {
	content, _, ok, err := readProjectPromptWithPath(workspace, rel, trusted)
	return content, ok, err
}

func readProjectPromptWithPath(
	workspace *tool.Workspace,
	rel string,
	trusted bool,
) (content, path string, present bool, err error) {
	if workspace == nil {
		return "", "", false, fmt.Errorf("app: workspace is required")
	}
	if !trusted {
		return "", "", false, nil
	}
	root, err := os.OpenRoot(workspace.PhysicalPath())
	if err != nil {
		return "", "", false, fmt.Errorf("app: open workspace root: %w", err)
	}
	defer root.Close()

	content, present, err = readPromptFile(root, rel, workspace.PhysicalPath())
	return content, filepath.Join(workspace.PhysicalPath(), rel), present, err
}

func readGlobalPrompt(
	configuration config.Config,
	name string,
) (string, bool, error) {
	content, _, present, err := readGlobalPromptWithPath(configuration, name)
	return content, present, err
}

func readGlobalPromptWithPath(
	configuration config.Config,
	name string,
) (string, string, bool, error) {
	globalDir := filepath.Dir(configuration.Paths.GlobalSettings)
	path := filepath.Join(globalDir, name)
	content, present, err := readPromptFile(nil, path, "")
	return content, path, present, err
}

// readPromptFile reads one prompt source. A rooted source is confined to its
// os.Root; an unrooted source is an absolute path (global user files). The
// display base prefixes the path in boundary labels.
func readPromptFile(
	root *os.Root,
	rel string,
	base string,
) (string, bool, error) {
	var file *os.File
	var err error
	if root != nil {
		file, err = root.Open(rel)
	} else {
		file, err = os.Open(rel)
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("app: open %s: %w", displayPath(base, rel), err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", false, fmt.Errorf("app: inspect %s: %w", displayPath(base, rel), err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf(
			"app: %s is not a regular file",
			displayPath(base, rel),
		)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProjectPromptBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("app: read %s: %w", displayPath(base, rel), err)
	}
	if len(data) > maxProjectPromptBytes {
		return "", false, fmt.Errorf(
			"app: %s exceeds %d bytes",
			displayPath(base, rel),
			maxProjectPromptBytes,
		)
	}
	if !utf8.Valid(data) {
		return "", false, fmt.Errorf(
			"app: %s is not valid UTF-8",
			displayPath(base, rel),
		)
	}
	content := strings.TrimSpace(string(data))
	return content, content != "", nil
}

func displayPath(base, rel string) string {
	if base == "" {
		return rel
	}
	return filepath.Join(base, rel)
}
