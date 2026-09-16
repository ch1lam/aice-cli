package tool

import (
	"strings"
	"testing"
)

func BenchmarkReadTextPage(b *testing.B) {
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{name: "short_100", source: strings.Repeat("line-0001\n", 100)},
		{name: "short_2000", source: strings.Repeat("line-0001\n", 2000)},
		{name: "long_line", source: strings.Repeat("x", 40*1024) + "\n"},
		{name: "oversized_line", source: strings.Repeat("x", 60*1024)},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.source)))
			for b.Loop() {
				_, _, err := readTextPage(ctx, strings.NewReader(fixture.source), 1, defaultReadLines, "notes.txt", false)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
