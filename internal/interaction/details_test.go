package interaction

import "testing"

func TestDetailsTextRendersEachLineKind(t *testing.T) {
	t.Parallel()
	got := DetailsText([]Detail{
		{Key: "Service", Value: "docs", Tone: ToneTitle},
		{Heading: "Connection"},
		{Indent: 2, Key: "State", Value: "ready", Tone: ToneGood},
		{},
		{Text: "plain"},
	})
	if want := "Service: docs\nConnection\n  State: ready\n\nplain"; got != want {
		t.Fatalf("DetailsText() = %q, want %q", got, want)
	}
}
