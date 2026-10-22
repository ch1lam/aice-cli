package utf8text

import "testing"

func TestPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "fits", value: "abc", limit: 3, want: "abc"},
		{name: "ascii cut", value: "abcdef", limit: 4, want: "abcd"},
		{name: "zero limit", value: "abc", limit: 0, want: ""},
		{name: "negative limit", value: "abc", limit: -1, want: ""},
		{name: "backs off split rune", value: "a界b", limit: 3, want: "a"},
		{name: "keeps whole rune", value: "a界b", limit: 4, want: "a界"},
		{name: "keeps earlier invalid byte", value: "a\xffbcd", limit: 3, want: "a\xffb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Prefix(tt.value, tt.limit); got != tt.want {
				t.Errorf("Prefix(%q, %d) = %q, want %q", tt.value, tt.limit, got, tt.want)
			}
		})
	}
}
