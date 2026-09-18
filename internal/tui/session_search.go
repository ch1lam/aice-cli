package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

// One scanner owns the channel. Each UI result schedules only the next receive;
// cancellation releases backpressure even after the picker has disappeared.
func sessionSearchCommand(ctx context.Context, cancel context.CancelFunc, owner *sessionQueryOwner,
	browser interaction.SessionBrowser, generation uint64, query string,
) tea.Cmd {
	return func() tea.Msg {
		if !owner.begin() {
			cancel()
			return sessionSearchResult{generation: generation, err: context.Canceled}
		}
		results := make(chan sessionSearchResult, 1)
		var receive tea.Cmd
		receive = func() tea.Msg {
			select {
			case result := <-results:
				return result
			case <-ctx.Done():
				// Prefer a terminal result already published before cancellation.
				select {
				case result := <-results:
					return result
				default:
				}
				return sessionSearchResult{generation: generation, query: query, err: ctx.Err()}
			}
		}
		go func() {
			defer owner.wg.Done()
			defer cancel()
			send := func(items []interaction.SessionSummary, err error, next tea.Cmd) error {
				select {
				case results <- sessionSearchResult{generation: generation, query: query, items: items, err: err, next: next}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if query != "" {
				timer := time.NewTimer(180 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
			}
			var items []interaction.SessionSummary
			var err error
			if scanner, ok := browser.(interaction.SessionScanner); ok {
				items, err = scanner.ScanSessions(ctx, query, func(items []interaction.SessionSummary) error {
					return send(items, nil, receive)
				})
			} else {
				items, err = browser.SearchSessions(ctx, query)
			}
			_ = send(items, err, nil)
		}()
		return receive()
	}
}

func selectedSessionKey(p *sessionPicker) string {
	if item, ok := p.list.SelectedItem().(sessionListItem); ok {
		return item.Key
	}
	return ""
}

func sessionDateGroup(timestamp int64, now time.Time) string {
	local := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, local)
	activity := time.UnixMilli(timestamp).In(local)
	if !activity.Before(today) {
		return "Today"
	}
	if !activity.Before(today.AddDate(0, 0, -1)) {
		return "Yesterday"
	}
	return "Earlier"
}
