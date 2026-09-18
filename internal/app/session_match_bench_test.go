package app

import (
	"strings"
	"testing"
)

func BenchmarkSessionTextQuery(b *testing.B) {
	body := strings.Repeat("A reproducible history search fixture. 中文正文。 ", 4200)
	for _, test := range []struct{ name, text, query string }{
		{"early", "NEEDLE " + body, "needle"},
		{"late", body + "NEEDLE", "needle"},
		{"missing", body, "needle"},
		{"unicode", body + "İ中文", "i中文"},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.Run("lowercase", func(b *testing.B) {
				for b.Loop() {
					strings.Index(strings.ToLower(test.text), test.query)
				}
			})
			b.Run("streaming", func(b *testing.B) {
				query := newSessionTextQuery(test.query)
				for b.Loop() {
					query.index(test.text)
				}
			})
		})
	}
}
