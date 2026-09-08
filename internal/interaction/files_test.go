package interaction

import (
	"reflect"
	"testing"
)

func TestFileReferencesAndCompletionShareSyntax(t *testing.T) {
	t.Parallel()
	text := "read @src/main.go @\"images/a b.png\" @'中文 图.jpg' x@y.com @@literal `@code`\n```go\n@hidden\n```\n@last"
	want := []string{"src/main.go", "images/a b.png", "中文 图.jpg", "last"}
	if got := FileReferences(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("refs = %#v", got)
	}
	for _, path := range []string{"hello world.png", `a\b"c.png`, "中文.md"} {
		if got := FileReferences(QuoteFileReference(path)); !reflect.DeepEqual(got, []string{path}) {
			t.Fatalf("quote roundtrip = %#v", got)
		}
	}
	refs := ScanFileReferences("看 @\"未完成 路径")
	if len(refs) != 1 || refs[0].Complete || refs[0].Path != "未完成 路径" || refs[0].Start != 2 {
		t.Fatalf("incomplete = %#v", refs)
	}
}
