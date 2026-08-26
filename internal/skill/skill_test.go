package skill

import "testing"

func TestSourceString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source Source
		want   string
	}{
		{source: SourceBuiltin, want: "builtin"},
		{source: SourceUser, want: "user"},
		{source: SourceProject, want: "project"},
		{source: Source(""), want: "unknown"},
		{source: Source("other"), want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want+" for "+string(tt.source), func(t *testing.T) {
			t.Parallel()
			if got := tt.source.String(); got != tt.want {
				t.Fatalf("Source(%q).String() = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

func TestCatalogZeroValue(t *testing.T) {
	t.Parallel()

	var catalog Catalog
	if skills := catalog.Skills(); len(skills) != 0 {
		t.Fatalf("Skills() = %#v, want empty", skills)
	}
	if _, ok := catalog.Lookup("x"); ok {
		t.Fatal("Lookup on zero Catalog = true, want false")
	}
}
