package app

import (
	"fmt"
	"strings"
	"testing"
)

// Synthetic project history: 134 files with ~27 MiB of conversation prose.
// No credentials or private workspace files are read.
func BenchmarkSessionBrowser(b *testing.B) {
	s := browserHarness(b)
	for i := range 134 {
		store := browserFixture(b, s, fmt.Sprintf("session-%03d", i), "Explain the implementation",
			strings.Repeat("A reproducible history search and preview fixture. ", 4200), int64(i+1))
		if err := store.Close(); err != nil {
			b.Fatal(err)
		}
	}
	for _, query := range []string{"", "preview"} {
		b.Run("search/"+query, func(b *testing.B) {
			if _, err := s.SearchSessions(b.Context(), query); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := s.SearchSessions(b.Context(), query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("preview", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.PreviewSession(b.Context(), "session-133", ""); err != nil {
				b.Fatal(err)
			}
		}
	})
}
