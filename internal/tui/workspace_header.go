package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m model) workspaceRect() screenRect {
	if m.guardPending != nil || m.reading != nil || strings.TrimSpace(m.workingDirectory) == "" {
		return screenRect{}
	}
	layout := m.headerLayout(m.layoutWidth())
	header := m.screenLayout().header
	return screenRect{header.x + layout.workspaceX, header.y, lipgloss.Width(layout.workspace), 1}
}

func (m model) workspaceContains(mouse tea.Mouse) bool { return m.workspaceRect().contains(mouse) }

func (m model) workspaceHeaderView(layout headerLayout) string {
	if strings.TrimSpace(m.workingDirectory) == "" {
		return mutedStyle.Render(layout.workspace)
	}
	style := mutedStyle
	if m.pointer.known && !m.selection.active &&
		m.workspaceContains(tea.Mouse{X: m.pointer.x, Y: m.pointer.y}) {
		style = pathStyle
	}
	// Keep the path plain: OSC 8 can add an always-visible terminal underline.
	// Terminals can still detect paths themselves when their link modifier is held.
	return style.Render(layout.workspace)
}

func directoryURI(path string) string {
	path = filepath.ToSlash(path)
	link := url.URL{Scheme: "file"}
	if strings.HasPrefix(path, "//") {
		link.Host, path, _ = strings.Cut(strings.TrimPrefix(path, "//"), "/")
	}
	link.Path = "/" + strings.Trim(path, "/")
	if link.Path != "/" {
		link.Path += "/"
	}
	return link.String()
}

func workspaceOpenModifier(mod tea.KeyMod, platform string) bool {
	if platform == "darwin" {
		return mod.Contains(tea.ModSuper) || mod.Contains(tea.ModMeta)
	}
	return mod.Contains(tea.ModCtrl)
}

func (m *model) activateWorkspace(mod tea.KeyMod) tea.Cmd {
	path, err := filepath.Abs(m.workingDirectory)
	if err != nil {
		return func() tea.Msg { return directoryOpenedMsg{err: fmt.Errorf("resolve workspace path: %w", err)} }
	}
	if workspaceOpenModifier(mod, runtime.GOOS) {
		if m.openDirectory != nil {
			return m.openDirectory(path)
		}
		return func() tea.Msg { return directoryOpenedMsg{err: errors.New("directory opener is unavailable")} }
	}
	return m.copyText(path)
}

type directoryOpenedMsg struct{ err error }

// The helper belongs to the TUI lifetime and never interpolates a shell command.
func openDirectoryCommand(ctx context.Context, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := ctx.Err(); err != nil {
			return directoryOpenedMsg{err: err}
		}
		name, args := directoryOpenArgs(path, runtime.GOOS)
		command := exec.CommandContext(ctx, name, args...)
		command.WaitDelay = time.Second
		err := command.Run()
		if err != nil {
			err = fmt.Errorf("could not open workspace in the file manager: %w", err)
		}
		return directoryOpenedMsg{err: err}
	}
}

func directoryOpenArgs(path, platform string) (string, []string) {
	switch platform {
	case "darwin":
		return "open", []string{path}
	case "windows":
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", directoryURI(path)}
	default:
		return "xdg-open", []string{path}
	}
}
