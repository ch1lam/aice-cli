package tui

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestInkPalette(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "ink black", got: inkBlackHex, want: "#0D0B0A"},
		{name: "panel black", got: panelBlackHex, want: "#1B1613"},
		{name: "separator", got: separatorHex, want: "#332921"},
		{name: "primary text", got: primaryTextHex, want: "#F2E9D8"},
		{name: "muted text", got: mutedTextHex, want: "#8F8477"},
		{name: "sunset", got: sunsetHex, want: "#FF6B6B"},
		{name: "gold", got: goldHex, want: "#C9A063"},
		{name: "success", got: successHex, want: "#7A9471"},
		{name: "warning", got: warningHex, want: "#D98C3D"},
		{name: "error", got: errorHex, want: "#A8383D"},
		{name: "information", got: informationHex, want: "#5B8A9E"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("palette color = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestThemeMapsSemanticColors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		style lipgloss.Style
		want  string
	}{
		{name: "brand", style: brandStyle, want: sunsetHex},
		{name: "primary text", style: bodyStyle, want: primaryTextHex},
		{name: "secondary detail", style: labelStyle, want: goldHex},
		{name: "information", style: infoStyle, want: informationHex},
		{name: "error", style: errorStyle, want: errorHex},
		{name: "warning", style: noticeStyle, want: warningHex},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertColor(t, tt.style.GetForeground(), lipgloss.Color(tt.want))
		})
	}
}

func TestSlashCommandSelectionUsesAccentWithoutBackground(t *testing.T) {
	t.Parallel()

	assertColor(
		t,
		slashCommandSelectedStyle.GetForeground(),
		lipgloss.Color(sunsetHex),
	)
	assertNoColor(t, slashCommandSelectedStyle.GetBackground())
}

func TestThemeAppliesLayeredBackgrounds(t *testing.T) {
	t.Parallel()

	assertColor(
		t,
		thinkingStyle.GetBackground(),
		lipgloss.Color(panelBlackHex),
	)

	markdown := inkMarkdownStyle()
	if markdown.CodeBlock.Chroma == nil {
		t.Fatal("markdown code block syntax colors are not configured")
	}
	for _, background := range []*string{
		markdown.H1.BackgroundColor,
		markdown.Code.BackgroundColor,
		markdown.CodeBlock.Chroma.Background.BackgroundColor,
	} {
		if background == nil || *background != panelBlackHex {
			t.Errorf("markdown panel background = %v, want %q", background, panelBlackHex)
		}
	}
}

func TestMarkdownTaskListUsesEmojiMarkers(t *testing.T) {
	t.Parallel()

	style := inkMarkdownStyle()
	if style.Task.Ticked != "✅ " {
		t.Errorf("completed task marker = %q, want %q", style.Task.Ticked, "✅ ")
	}
	if style.Task.Unticked != "⏳ " {
		t.Errorf("incomplete task marker = %q, want %q", style.Task.Unticked, "⏳ ")
	}

	rendered := ansi.Strip(renderMarkdown(
		"- [x] Finished task\n- [ ] Outstanding task",
		80,
	))
	for _, marker := range []string{"✅ Finished task", "⏳ Outstanding task"} {
		if !strings.Contains(rendered, marker) {
			t.Errorf("rendered task list = %q, want marker %q", rendered, marker)
		}
	}
}

func TestUserStyleUsesGoldRailAndMutedTextWithoutBackground(t *testing.T) {
	t.Parallel()

	if !userStyle.GetBorderLeft() {
		t.Fatal("user style does not render its left rail")
	}
	assertColor(
		t,
		userStyle.GetBorderLeftForeground(),
		lipgloss.Color(goldHex),
	)
	assertColor(
		t,
		userStyle.GetForeground(),
		lipgloss.Color(mutedTextHex),
	)
	assertNoColor(t, userStyle.GetBackground())
}

func TestPendingSteerUsesInformationColor(t *testing.T) {
	t.Parallel()

	for _, style := range []lipgloss.Style{
		pendingSteerLabelStyle,
		pendingSteerStyle,
	} {
		assertColor(
			t,
			style.GetForeground(),
			lipgloss.Color(informationHex),
		)
	}
	if !pendingSteerStyle.GetBorderLeft() {
		t.Fatal("pending steer style does not render its left rail")
	}
	assertColor(
		t,
		pendingSteerStyle.GetBorderLeftForeground(),
		lipgloss.Color(informationHex),
	)
}

func TestComposerUsesScreenBackground(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	inputStyles := current.input.Styles()
	styles := []struct {
		name  string
		style lipgloss.Style
	}{
		{name: "focused composer", style: composerFocusedStyle},
		{name: "blurred composer", style: composerBlurredStyle},
		{name: "focused base", style: inputStyles.Focused.Base},
		{name: "focused text", style: inputStyles.Focused.Text},
		{name: "focused cursor line", style: inputStyles.Focused.CursorLine},
		{name: "focused prompt", style: inputStyles.Focused.Prompt},
		{name: "focused placeholder", style: inputStyles.Focused.Placeholder},
		{name: "blurred base", style: inputStyles.Blurred.Base},
		{name: "blurred text", style: inputStyles.Blurred.Text},
		{name: "blurred cursor line", style: inputStyles.Blurred.CursorLine},
		{name: "blurred prompt", style: inputStyles.Blurred.Prompt},
		{name: "blurred placeholder", style: inputStyles.Blurred.Placeholder},
	}

	for _, tt := range styles {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertNoColor(t, tt.style.GetBackground())
		})
	}
}

func TestModelUsesGoldCursor(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	styles := current.input.Styles()
	assertColor(t, styles.Cursor.Color, lipgloss.Color(goldHex))

	view := current.View()
	assertColor(t, view.BackgroundColor, lipgloss.Color(inkBlackHex))
	assertColor(t, view.ForegroundColor, lipgloss.Color(primaryTextHex))
}

func assertColor(t *testing.T, got, want color.Color) {
	t.Helper()

	gotRed, gotGreen, gotBlue, gotAlpha := got.RGBA()
	wantRed, wantGreen, wantBlue, wantAlpha := want.RGBA()
	if gotRed != wantRed || gotGreen != wantGreen || gotBlue != wantBlue || gotAlpha != wantAlpha {
		t.Errorf(
			"color = rgba(%d, %d, %d, %d), want rgba(%d, %d, %d, %d)",
			gotRed,
			gotGreen,
			gotBlue,
			gotAlpha,
			wantRed,
			wantGreen,
			wantBlue,
			wantAlpha,
		)
	}
}

func assertNoColor(t *testing.T, got color.Color) {
	t.Helper()

	if _, ok := got.(lipgloss.NoColor); !ok {
		t.Errorf("color = %T(%v), want lipgloss.NoColor", got, got)
	}
}
