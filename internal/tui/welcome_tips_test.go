package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestWelcomeTipTypesHoldsAndErases(t *testing.T) {
	t.Parallel()

	// Include a wide character: animation steps must never split UTF-8 bytes.
	tip := welcomeTip{text: []rune("Go 金")}
	now := time.Unix(100, 0)
	for _, want := range []string{"G", "Go", "Go ", "Go 金"} {
		tip.advance(now)
		got := ansi.Strip(tip.view(80))
		if !utf8.ValidString(got) || !strings.Contains(got, want) {
			t.Fatalf("typed tip = %q, want prefix %q", got, want)
		}
		if got := string(tip.text[:tip.visible]); got != want {
			t.Fatalf("visible text = %q, want %q", got, want)
		}
		now = now.Add(welcomeTipTypeInterval)
	}
	completed := now.Add(-welcomeTipTypeInterval)
	full := tip.view(80)
	if strings.Contains(full, "▏") {
		t.Fatal("holding tip should hide the drawn caret")
	}
	for _, elapsed := range []time.Duration{time.Second, 6999 * time.Millisecond, 7*time.Second - 1} {
		tip.advance(completed.Add(elapsed))
		if tip.view(80) != full {
			t.Fatalf("tip changed before seven seconds, at %s", elapsed)
		}
	}
	now = completed.Add(7 * time.Second)
	for _, want := range []string{"Go ", "Go", "G", ""} {
		tip.advance(now)
		if got := string(tip.text[:tip.visible]); got != want {
			t.Fatalf("erasing text = %q, want %q", got, want)
		}
		now = now.Add(welcomeTipEraseInterval)
	}
	previous := tip.index
	tip.advance(now)
	if tip.index == previous || tip.visible != 0 {
		t.Fatal("after erasing, the next tip must differ and start empty")
	}
	tip.advance(now.Add(welcomeTipTypeInterval))
	if tip.visible != 1 {
		t.Fatal("next tip did not begin typing")
	}
}

func TestWelcomeTipsNeverRepeatConsecutively(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for _, text := range welcomeTips {
		if strings.TrimSpace(text) == "" || seen[text] {
			t.Fatalf("empty or duplicate catalog tip: %q", text)
		}
		seen[text] = true
	}
	for index, text := range welcomeTips {
		tip := welcomeTip{index: index, text: []rune(text)}
		for range 100 {
			previous := string(tip.text)
			tip.selectNext()
			if string(tip.text) == previous {
				t.Fatalf("consecutive tips repeated: %q", previous)
			}
		}
	}
}

func TestWelcomeTipCentersEachSentence(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"Short tip.", welcomeTips[12], "A wide 金 character."} {
		t.Run(text, func(t *testing.T) {
			for _, width := range []int{24, 80, 146} {
				tip := welcomeTip{text: []rune(text), visible: len([]rune(text)), phase: welcomeTipHolding}
				full := ansi.Strip(tip.view(width))
				for _, line := range strings.Split(full, "\n") {
					trimmed := strings.TrimSpace(line)
					left := len(line) - len(strings.TrimLeft(line, " "))
					right := width - left - lipgloss.Width(trimmed)
					if left-right < -1 || left-right > 1 {
						t.Fatalf("width %d: sentence line is not centered: %q (%d/%d)", width, line, left, right)
					}
				}
				firstLine := strings.Split(full, "\n")[0]
				left := len(firstLine) - len(strings.TrimLeft(firstLine, " "))
				for _, phase := range []welcomeTipPhase{welcomeTipTyping, welcomeTipErasing} {
					tip.visible = 1
					tip.phase = phase
					partial := ansi.Strip(tip.view(width))
					if got := strings.Index(partial, string(tip.text[0])); got != left {
						t.Fatalf("animation moved the sentence from column %d to %d", left, got)
					}
				}
			}
		})
	}
}

func TestWelcomeTipAppearsBetweenLogoAndComposer(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 150, Height: 30})
	current.version = "dev"
	text := "A centered usage sentence."
	current.welcomeTip = welcomeTip{text: []rune(text), visible: len(text), phase: welcomeTipHolding}
	current.refreshViewport(false)
	content := ansi.Strip(current.View().Content)
	assertTextOrder(t, content, "⣤⣶⣾", "dev", text, defaultPlaceholder, "? shortcuts")
	if strings.Contains(content, "● Tip") || strings.Contains(current.footerView(146), text) {
		t.Fatal("tip still has a prefix or appears below the composer")
	}
	if got, want := current.View().Cursor.Position.Y, paintedMouse(t, current, defaultPlaceholder).Y; got != want {
		t.Fatalf("input cursor row = %d, want %d", got, want)
	}
}

func TestWelcomeTipsKeepLayoutStable(t *testing.T) {
	t.Parallel()

	for _, size := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "wide", width: 150, height: 30},
		{name: "normal", width: 80, height: 24},
		{name: "narrow", width: 28, height: 24},
	} {
		t.Run(size.name, func(t *testing.T) {
			t.Parallel()
			current := newModel(make(chan runRequest), make(chan struct{}))
			current = updateModel(t, current, tea.WindowSizeMsg{Width: size.width, Height: size.height})
			cursor := *current.View().Cursor
			for _, text := range welcomeTips {
				current.welcomeTip = welcomeTip{text: []rune(text)}
				for visible := 0; visible <= len(current.welcomeTip.text); visible++ {
					current.welcomeTip.visible = visible
					current.resizeLayout()
					current.refreshViewport(false)
					view := current.View()
					if *view.Cursor != cursor {
						t.Fatal("typing a tip moved the composer cursor")
					}
					if lipgloss.Width(view.Content) > size.width || lipgloss.Height(view.Content) > size.height {
						t.Fatal("tip animation overflows the terminal")
					}
				}
			}
		})
	}
}

func TestWelcomeTipsLifecycle(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	now := time.Unix(100, 0)
	generation := current.welcomeAnimation.generation
	current = updateModel(t, current, welcomeTickMsg{generation: generation, at: now})
	if current.welcomeTip.visible != 1 {
		t.Fatal("welcome tick did not start the tip")
	}
	current = updateModel(t, current, welcomeTickMsg{generation: generation + 1, at: now.Add(time.Second)})
	if current.welcomeTip.visible != 1 {
		t.Fatal("stale tick advanced the tip")
	}
	for _, state := range []string{"running", "transcript", "side", "short"} {
		t.Run(state, func(t *testing.T) {
			hidden := current
			switch state {
			case "running":
				hidden.running = true
			case "transcript":
				hidden.entries = []transcriptEntry{{kind: entryNotice, text: "history"}}
			case "side":
				hidden.side.isVisible = true
			case "short":
				hidden.height = 10
			}
			hidden = updateModel(t, hidden, welcomeTickMsg{generation: generation, at: now.Add(time.Second)})
			if hidden.welcomeTipsView() != "" {
				t.Fatal("tip rendered outside the welcome screen")
			}
			if hidden.welcomeTip.visible != current.welcomeTip.visible {
				t.Fatal("hidden tip advanced")
			}
		})
	}
	previous := current.welcomeTip.index
	current.entries = []transcriptEntry{{kind: entryNotice, text: "history"}}
	current = updateModel(t, current, welcomeTickMsg{generation: generation, at: now.Add(time.Second)})
	current.input.SetValue("/clear")
	current = updateModel(t, current, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !current.welcomeTipsVisible() || current.welcomeTip.index == previous || current.welcomeTip.visible != 0 {
		t.Fatal("/clear should restore the welcome screen with a different, empty tip")
	}
	current = updateModel(t, current, welcomeTickMsg{generation: current.welcomeAnimation.generation, at: now.Add(2 * time.Second)})
	if current.welcomeTip.visible != 1 {
		t.Fatal("/clear did not resume typing")
	}
}
