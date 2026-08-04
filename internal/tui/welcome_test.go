package tui

import (
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestWelcomeViewShowsAnimatedLogo(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.apiKeyConfigured = true

	welcome := current.welcomeView()
	for _, want := range []string{
		"█",                       // block logo
		welcomeTagline,            // product line under the logo
		"Work with your codebase", // welcome card
		"AVAILABLE TOOLS",
		"Type / to browse interactive commands.",
	} {
		if !strings.Contains(welcome, want) {
			t.Errorf("welcome = %q, want %q", welcome, want)
		}
	}
	logoRows := 0
	for _, line := range strings.Split(welcome, "\n") {
		if strings.Contains(line, "█") {
			logoRows++
		}
	}
	if logoRows != welcomeLogoHeight {
		t.Errorf("welcome logo rows = %d, want %d", logoRows, welcomeLogoHeight)
	}
}

func TestWelcomeViewOmitsLogoWhenNarrow(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 30, Height: 24})

	welcome := current.welcomeView()
	if strings.Contains(welcome, "█") {
		t.Errorf("welcome = %q, want no logo on a narrow terminal", welcome)
	}
	// The card text wraps on a narrow terminal, so assert a fragment that
	// survives wrapping.
	if !strings.Contains(welcome, "AVAILABLE") {
		t.Errorf("welcome = %q, want the welcome card on a narrow terminal", welcome)
	}
}

func TestWelcomeViewOmitsLogoWhenShort(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 10})

	welcome := current.welcomeView()
	if strings.Contains(welcome, "█") {
		t.Errorf("welcome = %q, want no logo on a short terminal", welcome)
	}
	if !strings.Contains(welcome, "AVAILABLE TOOLS") {
		t.Errorf("welcome = %q, want the welcome card on a short terminal", welcome)
	}
}

func TestWelcomeAnimationAdvancesThenStops(t *testing.T) {
	t.Parallel()

	animation := welcomeAnimation{running: true, generation: 1}
	animation.Update(welcomeTickMsg{generation: 1}, true)
	if animation.frame != 1 {
		t.Errorf("frame = %d, want 1", animation.frame)
	}
	animation.Update(welcomeTickMsg{generation: 1}, true)
	if animation.frame != 2 {
		t.Errorf("frame = %d, want 2", animation.frame)
	}
	if command := animation.Update(welcomeTickMsg{generation: 1}, false); command != nil {
		t.Errorf("inactive update returned command %T, want nil", command)
	}
	if animation.running {
		t.Error("animation still running after welcome became inactive")
	}
}

func TestWelcomeAnimationIgnoresStaleTicks(t *testing.T) {
	t.Parallel()

	animation := welcomeAnimation{running: true, generation: 1}
	if command := animation.Update(welcomeTickMsg{generation: 2}, true); command != nil {
		t.Errorf("stale tick returned command %T, want nil", command)
	}
	if animation.frame != 0 {
		t.Errorf("frame = %d, want 0", animation.frame)
	}
}

func TestWelcomeAnimationResumesAfterRestart(t *testing.T) {
	t.Parallel()

	animation := welcomeAnimation{}
	if command := animation.Start(); command == nil {
		t.Fatal("Start() returned no tick command")
	}
	animation.Update(welcomeTickMsg{generation: 1}, false)
	if animation.running {
		t.Fatal("animation still running after becoming inactive")
	}
	// /clear restarts the animation with a fresh generation so pre-restart
	// ticks in flight are ignored.
	if command := animation.Start(); command == nil {
		t.Fatal("restart returned no tick command")
	}
	if !animation.running {
		t.Error("animation did not resume after restart")
	}
	if animation.generation != 2 {
		t.Errorf("generation = %d, want 2", animation.generation)
	}
	if command := animation.Update(welcomeTickMsg{generation: 1}, true); command != nil {
		t.Errorf("pre-restart tick advanced the animation: %T", command)
	}
	animation.Update(welcomeTickMsg{generation: 2}, true)
	if animation.frame != 1 {
		t.Errorf("frame = %d, want 1 after restart", animation.frame)
	}
}

func TestWelcomeAnimationStopsWhenRunStarts(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	generation := current.welcomeAnimation.generation

	current.running = true
	updated, command := current.Update(welcomeTickMsg{generation: generation})
	if command != nil {
		t.Errorf("Update returned command %T, want nil", command)
	}
	if updated.(model).welcomeAnimation.running {
		t.Error("welcome animation still running after run started")
	}
}

func TestModelInitEmitsWelcomeTick(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	if command := current.Init(); command == nil {
		t.Error("Init() returned no command")
	}
	// newModel seeds the animation as already running, so a matching tick
	// advances the logo and re-renders the viewport with the new frame.
	updated, command := current.Update(
		welcomeTickMsg{generation: current.welcomeAnimation.generation},
	)
	if command == nil {
		t.Error("welcome tick did not schedule the next frame")
	}
	after := updated.(model)
	if frame := after.welcomeAnimation.frame; frame != 1 {
		t.Errorf("welcome frame = %d, want 1", frame)
	}
	if !strings.Contains(after.viewport.GetContent(), "█") {
		t.Error("welcome tick did not re-render the logo into the viewport")
	}
}

func TestWelcomeRenderLogoIsUniformAndAnimated(t *testing.T) {
	t.Parallel()

	logo := welcomeAnimation{}.renderLogo()
	lines := strings.Split(logo, "\n")
	if len(lines) != welcomeLogoHeight {
		t.Fatalf("logo lines = %d, want %d", len(lines), welcomeLogoHeight)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width != welcomeLogoWidth {
			t.Errorf(
				"logo line %d width = %d, want %d: %q",
				index,
				width,
				welcomeLogoWidth,
				line,
			)
		}
	}
	first := welcomeAnimation{frame: 0}.renderLogo()
	later := welcomeAnimation{frame: 12}.renderLogo()
	if first == later {
		t.Error("logo did not change between animation frames")
	}
}

func TestSampleWelcomeRampWrapsSeamlessly(t *testing.T) {
	t.Parallel()

	if got := sampleWelcomeRamp(0); got != welcomeRampRGB[0] {
		t.Errorf("sample(0) = %v, want first ramp stop %v", got, welcomeRampRGB[0])
	}
	if got := sampleWelcomeRamp(1.0); got != sampleWelcomeRamp(0) {
		t.Errorf("ramp is not periodic: sample(1) = %v, sample(0) = %v", got, sampleWelcomeRamp(0))
	}
	if got := sampleWelcomeRamp(-0.01); got != sampleWelcomeRamp(0.99) {
		t.Errorf("negative positions do not wrap: %v vs %v", got, sampleWelcomeRamp(0.99))
	}
}

func TestSampleWelcomeRampProducesGradient(t *testing.T) {
	t.Parallel()

	seen := map[[3]float64]bool{}
	for frame := 0; frame < 30; frame++ {
		position := math.Mod(float64(frame)*welcomeAnimationPhase, 1.0)
		color := sampleWelcomeRamp(position)
		for _, component := range color {
			if component < 0 || component > 255 {
				t.Fatalf("color component out of range: %v", color)
			}
		}
		seen[color] = true
	}
	if len(seen) < 5 {
		t.Errorf("ramp produced only %d distinct colors, want a gradient", len(seen))
	}
}

func TestWelcomeColorRoundTrip(t *testing.T) {
	t.Parallel()

	for _, hex := range welcomeRamp {
		formatted := formatHexColor(parseHexColor(hex))
		if !strings.EqualFold(formatted, hex) {
			t.Errorf("round trip %s -> %s", hex, formatted)
		}
	}
}
