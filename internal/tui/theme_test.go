package tui

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
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
		{name: "vermilion", got: vermilionHex, want: "#C1272D"},
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
		{name: "brand", style: brandStyle, want: vermilionHex},
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

func TestThemeAppliesLayeredBackgrounds(t *testing.T) {
	t.Parallel()

	styles := []struct {
		name  string
		style lipgloss.Style
		want  string
	}{
		{name: "user", style: userStyle, want: panelBlackHex},
		{name: "thinking", style: thinkingStyle, want: panelBlackHex},
		{name: "focused composer", style: composerFocusedStyle, want: panelBlackHex},
		{name: "blurred composer", style: composerBlurredStyle, want: panelBlackHex},
	}

	for _, tt := range styles {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertColor(t, tt.style.GetBackground(), lipgloss.Color(tt.want))
		})
	}

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

func TestModelUsesGoldCursor(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	styles := current.input.Styles()
	assertColor(t, styles.Cursor.Color, lipgloss.Color(goldHex))
	assertColor(t, styles.Focused.CursorLine.GetBackground(), lipgloss.Color(panelBlackHex))

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
