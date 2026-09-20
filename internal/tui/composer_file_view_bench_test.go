package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func BenchmarkComposerFileView(b *testing.B) {
	for _, rows := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			m := newModel(nil, nil)
			prefix := strings.Repeat("ordinary draft line 中文\n", rows-1)
			path := "internal/中文 文件夹/configuration.go"
			label := fileReferenceLabel(path)
			m.input.SetValue(prefix + label + " plain")
			m.input.files = []composerFile{{start: utf8.RuneCountInString(prefix), end: utf8.RuneCountInString(prefix + label), path: path}}
			m.input.SetWidth(40)
			m.input.MoveToEnd()
			m.input, _ = m.input.Update(nil)
			m.input.View()
			b.ReportAllocs()
			for b.Loop() {
				m.input.View()
			}
		})
	}
}
