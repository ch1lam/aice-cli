package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type streamingPickerBrowser struct {
	pickerBrowser
	stopped chan struct{}
}

func (b streamingPickerBrowser) ScanSessions(ctx context.Context, _ string, publish func([]interaction.SessionSummary) error) ([]interaction.SessionSummary, error) {
	defer close(b.stopped)
	for i := range 20 {
		if err := publish([]interaction.SessionSummary{{Key: "recent", Title: "Recent task", UpdatedAt: int64(i)}}); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func TestSessionSearchStreamCancelsBlockedPublisher(t *testing.T) {
	browser := streamingPickerBrowser{stopped: make(chan struct{})}
	search, _, _, _, shutdown := sessionBrowserCommands(t.Context(), browser)
	defer shutdown()
	command, cancel := search(1, "")
	result := command().(sessionSearchResult)
	if result.next == nil || len(result.items) != 1 {
		t.Fatal("no partial catalog")
	}
	// Leave its next command queued: shutdown must release a blocked sender.
	cancel()
	shutdown()
	select {
	case <-browser.stopped:
	default:
		t.Fatal("scanner survived shutdown")
	}
}

func TestSessionSearchArrivalPreservesSelectionAndPreview(t *testing.T) {
	m := pickerModel(t, 100, 28)
	m.sessionPicker.list.Select(2)
	m.sessionPicker.previewText = "Selected preview"
	next := func() tea.Msg { return nil }
	m = updateModel(t, m, sessionSearchResult{generation: m.sessionQueryGeneration, next: next,
		items: []interaction.SessionSummary{{Key: "new", Title: "Newer arrival"}, {Key: "two", Title: "Second conversation"}, {Key: "old", Title: "Older arrival"}}})
	if selectedSessionKey(m.sessionPicker) != "two" || !m.sessionPicker.loading || m.sessionPicker.previewText != "Selected preview" {
		t.Fatal("partial arrival disturbed selected task or preview")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.sessionPicker != nil {
		t.Fatal("loading blocked Escape")
	}
}

func TestSessionDateGroupsAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 0, 30, 0, 0, location)
	for _, test := range []struct {
		at   time.Time
		want string
	}{
		{now, "Today"},
		{time.Date(2026, 3, 8, 0, 1, 0, 0, location), "Yesterday"},
		{time.Date(2026, 3, 7, 23, 59, 0, 0, location), "Earlier"},
	} {
		if got := sessionDateGroup(test.at.UnixMilli(), now); got != test.want {
			t.Fatal(got, test.want)
		}
	}
	m := pickerModel(t, 100, 28)
	if !strings.Contains(m.sessionPickerView(), "Earlier") {
		t.Fatal("date heading missing")
	}
}
