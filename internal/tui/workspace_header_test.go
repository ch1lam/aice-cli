package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func workspaceMouse(t *testing.T, m model) tea.Mouse {
	t.Helper()
	for y, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if index := strings.Index(row, "AICE  "); index >= 0 {
			return tea.Mouse{X: ansi.StringWidth(row[:index+6]), Y: y, Button: tea.MouseLeft}
		}
	}
	t.Fatal("workspace header is missing")
	return tea.Mouse{}
}

func TestWorkspaceHeaderHoverCopyAndOpen(t *testing.T) {
	for _, width := range []int{40, 100} {
		m := newModel(nil, nil)
		m.workingDirectory = filepath.Join(t.TempDir(), "目录 with spaces #?%")
		m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
		mouse := workspaceMouse(t, m)
		before := m.View().Content
		idleHeader := m.headerView(m.layoutWidth())
		m = updateModel(t, m, tea.MouseMotionMsg(mouse))
		hover := m.View().Content
		if hover == before || ansi.Strip(hover) != ansi.Strip(before) {
			t.Fatal("path hover must highlight without changing text or geometry")
		}
		for _, header := range []string{idleHeader, m.headerView(m.layoutWidth())} {
			if strings.Contains(header, "\x1b]8;") || strings.Contains(header, "\x9d8;") {
				t.Fatal("workspace header must not emit terminal-decorated hyperlinks")
			}
		}
		m = updateModel(t, m, tea.MouseClickMsg(mouse))
		updated, cmd := m.Update(tea.MouseReleaseMsg(mouse))
		m = updated.(model)
		if cmd == nil || !m.copyNotice {
			t.Fatal("path click did not copy with confirmation")
		}
		copied := fmt.Sprint(cmd().(tea.BatchMsg)[0]())
		if copied != m.workingDirectory {
			t.Fatalf("copied %q, want absolute path %q", copied, m.workingDirectory)
		}
		m.copyNotice = false
		opened := ""
		m.openDirectory = func(path string) tea.Cmd {
			opened = path
			return func() tea.Msg { return directoryOpenedMsg{} }
		}
		mouse.Mod = tea.ModCtrl
		if runtime.GOOS == "darwin" {
			mouse.Mod = tea.ModSuper
		}
		m = updateModel(t, m, tea.MouseClickMsg(mouse))
		updated, cmd = m.Update(tea.MouseReleaseMsg(mouse))
		m = updated.(model)
		if opened != m.workingDirectory || cmd == nil || m.copyNotice {
			t.Fatal("modified click must open the complete path without copying")
		}
	}
}

func TestWorkspacePressCancellationAndHitBounds(t *testing.T) {
	for _, cancel := range []tea.Msg{
		tea.MouseMotionMsg{X: 0, Y: 0, Button: tea.MouseLeft},
		tea.MouseWheelMsg{Button: tea.MouseWheelDown},
		tea.WindowSizeMsg{Width: 80, Height: 24},
		tea.BlurMsg{},
		guardRequestMsg{req: &interaction.GuardRequest{Reply: make(chan interaction.GuardReply, 1)}},
	} {
		m := newModel(nil, nil)
		m.workingDirectory = t.TempDir()
		m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
		mouse := workspaceMouse(t, m)
		m = updateModel(t, m, tea.MouseClickMsg(mouse))
		m = updateModel(t, m, cancel)
		updated, cmd := m.Update(tea.MouseReleaseMsg(mouse))
		if cmd != nil || updated.(model).copyNotice {
			t.Fatalf("%T did not cancel the pending path click", cancel)
		}
	}
	m := newModel(nil, nil)
	m.workingDirectory = t.TempDir()
	m.contextUsage = DisplayContext{Window: 1000000, Known: true}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for y, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		for _, text := range []string{"AICE", "READY", "0.00%"} {
			if index := strings.Index(row, text); index >= 0 && m.workspaceContains(tea.Mouse{X: ansi.StringWidth(row[:index]), Y: y}) {
				t.Fatalf("%s is incorrectly clickable as a directory", text)
			}
		}
	}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 24, Height: 24})
	for x := range 24 {
		if m.workspaceContains(tea.Mouse{X: x, Y: m.verticalPadding()}) {
			t.Fatal("hidden path retained a hit target")
		}
	}
}

func TestWorkspacePlatformActions(t *testing.T) {
	for _, platform := range []string{"darwin", "windows", "linux"} {
		for _, mod := range []tea.KeyMod{0, tea.ModShift, tea.ModAlt, tea.ModCtrl, tea.ModSuper, tea.ModMeta} {
			want := mod == tea.ModCtrl
			if platform == "darwin" {
				want = mod == tea.ModSuper || mod == tea.ModMeta
			}
			if workspaceOpenModifier(mod, platform) != want {
				t.Fatalf("incorrect open modifier %v for %s", mod, platform)
			}
		}
	}
	path := "/tmp/目录 with spaces #?%"
	for _, test := range []struct {
		platform, name string
		args           []string
	}{
		{"darwin", "open", []string{path}},
		{"windows", "rundll32.exe", []string{"url.dll,FileProtocolHandler", directoryURI(path)}},
		{"linux", "xdg-open", []string{path}},
	} {
		name, args := directoryOpenArgs(path, test.platform)
		if name != test.name || !reflect.DeepEqual(args, test.args) {
			t.Fatalf("%s command = %s %q", test.platform, name, args)
		}
	}
	uri, err := url.Parse(directoryURI(filepath.FromSlash(path)))
	if err != nil || uri.Path != path+"/" || uri.Fragment != "" || uri.RawQuery != "" {
		t.Fatalf("file link lost special path characters: %v, %v", uri, err)
	}
	if directoryURI("/") != "file:///" {
		t.Fatal("root directory link is malformed")
	}
}

func TestDirectoryOpenerUsesArgumentsAndReportsFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX helper fixture; Windows command arguments are covered separately")
	}
	dir := t.TempDir()
	name, _ := directoryOpenArgs("", runtime.GOOS)
	helper := filepath.Join(dir, name)
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$AICE_TEST_OPEN_RESULT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(dir, "result")
	t.Setenv("PATH", dir)
	t.Setenv("AICE_TEST_OPEN_RESULT", resultPath)
	path := filepath.Join(dir, "space ; $(not-a-command) 中文")
	result := openDirectoryCommand(t.Context(), path)().(directoryOpenedMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil || string(data) != path {
		t.Fatalf("opener did not receive exact path: %q, %v", data, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result := openDirectoryCommand(ctx, path)().(directoryOpenedMsg); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancelled opener = %v", result.err)
	}
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	result = openDirectoryCommand(t.Context(), path)().(directoryOpenedMsg)
	m := newModel(nil, nil)
	m = updateModel(t, m, result)
	if result.err == nil || !strings.Contains(m.inputNotice, "file manager") {
		t.Fatal("failed opener did not show an actionable notice")
	}
}
