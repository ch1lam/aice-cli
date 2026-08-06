package trust

import (
	"path/filepath"
	"testing"
)

func protectedSnapshot() Snapshot {
	return Snapshot{
		Resources: []Resource{{Name: "SYSTEM.md", Path: "/workspace/.aice/SYSTEM.md"}},
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func TestResolveOverrideWinsWithoutResources(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	resolution, err := store.Resolve(ResolveOptions{
		CWD:      "/workspace",
		Override: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Decision != DecisionUntrusted || resolution.Source != SourceOverride {
		t.Errorf("Resolve() = %#v, want untrusted override", resolution)
	}
}

func TestResolveOverrideIgnoresStore(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.Set("/workspace", DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	resolution, err := store.Resolve(ResolveOptions{
		CWD:      "/workspace",
		Snapshot: protectedSnapshot(),
		Override: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Decision != DecisionUntrusted || resolution.Source != SourceOverride {
		t.Errorf("Resolve() = %#v, want untrusted override", resolution)
	}
}

func TestResolveWithoutResourcesIsTrusted(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	asked := false
	resolution, err := store.Resolve(ResolveOptions{
		CWD: "/workspace",
		AskUI: func(cwd string) (Choice, error) {
			asked = true
			return Choice{Decision: DecisionUntrusted}, nil
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Decision != DecisionTrusted {
		t.Errorf("Resolve().Decision = %v, want trusted", resolution.Decision)
	}
	if asked {
		t.Error("AskUI called without protected resources")
	}
}

func TestResolveStoredDecisionWinsOverPolicy(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.Set("/workspace", DecisionUntrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	resolution, err := store.Resolve(ResolveOptions{
		CWD:      "/workspace",
		Snapshot: protectedSnapshot(),
		Policy:   DefaultAlways,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Decision != DecisionUntrusted || resolution.Source != SourceStore {
		t.Errorf("Resolve() = %#v, want stored untrusted", resolution)
	}
	if resolution.Entry.Path != filepath.Clean("/workspace") {
		t.Errorf("Resolve().Entry.Path = %q, want /workspace", resolution.Entry.Path)
	}
}

func TestResolvePolicyAlways(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	resolution, err := store.Resolve(ResolveOptions{
		CWD:      "/workspace",
		Snapshot: protectedSnapshot(),
		Policy:   DefaultAlways,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Decision != DecisionTrusted || resolution.Source != SourcePolicy {
		t.Errorf("Resolve() = %#v, want trusted policy", resolution)
	}
}

func TestResolvePolicyNever(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	resolution, err := store.Resolve(ResolveOptions{
		CWD:      "/workspace",
		Snapshot: protectedSnapshot(),
		Policy:   DefaultNever,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Decision != DecisionUntrusted || resolution.Source != SourcePolicy {
		t.Errorf("Resolve() = %#v, want untrusted policy", resolution)
	}
}

func TestResolveAskWithoutUIIsUntrusted(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	resolution, err := store.Resolve(ResolveOptions{
		CWD:      "/workspace",
		Snapshot: protectedSnapshot(),
		Policy:   DefaultAsk,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Decision != DecisionUntrusted || resolution.Source != SourcePolicy {
		t.Errorf("Resolve() = %#v, want untrusted policy", resolution)
	}
}

func TestResolveEmptyPolicyDefaultsToAsk(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	resolution, err := store.Resolve(ResolveOptions{
		CWD:      "/workspace",
		Snapshot: protectedSnapshot(),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Decision != DecisionUntrusted {
		t.Errorf("Resolve().Decision = %v, want untrusted", resolution.Decision)
	}
}

func TestResolveAskWithUIUsesChoice(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	selected := Choice{
		Label:    "Trust",
		Decision: DecisionTrusted,
		Updates:  []Update{{Path: "/workspace", Decision: DecisionTrusted}},
	}
	var gotCWD string
	resolution, err := store.Resolve(ResolveOptions{
		CWD:      "/workspace",
		Snapshot: protectedSnapshot(),
		Policy:   DefaultAsk,
		AskUI: func(cwd string) (Choice, error) {
			gotCWD = cwd
			return selected, nil
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if gotCWD != "/workspace" {
		t.Errorf("AskUI cwd = %q, want /workspace", gotCWD)
	}
	if resolution.Decision != DecisionTrusted ||
		resolution.Source != SourceInteractive ||
		!resolution.Prompted {
		t.Errorf("Resolve() = %#v, want trusted interactive prompt", resolution)
	}
	if resolution.Choice.Label != "Trust" {
		t.Errorf("Resolve().Choice.Label = %q, want Trust", resolution.Choice.Label)
	}
}

func TestResolveRequiresWorkspace(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.Resolve(ResolveOptions{}); err == nil {
		t.Fatal("Resolve() error = nil, want error")
	}
}

func TestChoicesIncludeParentAndSessionOnly(t *testing.T) {
	t.Parallel()

	parentPath, ok := ParentPath("/workspace")
	if !ok {
		t.Fatal("workspace has no parent path")
	}
	choices := Choices("/workspace")
	want := []struct {
		label   string
		trusted bool
		persist bool
	}{
		{label: "Trust", trusted: true, persist: true},
		{label: "Trust parent folder (" + parentPath + ")", trusted: true, persist: true},
		{label: "Trust (this session only)", trusted: true, persist: false},
		{label: "Do not trust", trusted: false, persist: true},
		{label: "Do not trust (this session only)", trusted: false, persist: false},
	}
	if len(choices) != len(want) {
		t.Fatalf("Choices() = %d options, want %d", len(choices), len(want))
	}
	for index, choice := range choices {
		if choice.Label != want[index].label {
			t.Errorf("Choices()[%d].Label = %q, want %q", index, choice.Label, want[index].label)
		}
		if (choice.Decision == DecisionTrusted) != want[index].trusted {
			t.Errorf("Choices()[%d] decision = %v, want trusted=%v", index, choice.Decision, want[index].trusted)
		}
		if (len(choice.Updates) > 0) != want[index].persist {
			t.Errorf("Choices()[%d] persist = %v, want %v", index, len(choice.Updates) > 0, want[index].persist)
		}
	}
}

func TestChoicesParentClearsExactEntry(t *testing.T) {
	t.Parallel()

	parentPath, ok := ParentPath("/workspace")
	if !ok {
		t.Fatal("workspace has no parent path")
	}
	choices := Choices("/workspace")
	if len(choices) < 2 {
		t.Fatalf("Choices() = %d options, want at least 2", len(choices))
	}
	parent := choices[1]
	if parent.Decision != DecisionTrusted {
		t.Fatalf("parent choice decision = %v, want trusted", parent.Decision)
	}
	updates := map[string]Decision{}
	for _, update := range parent.Updates {
		updates[update.Path] = update.Decision
	}
	if updates["/workspace"] != DecisionUnknown {
		t.Errorf("parent choice does not clear /workspace entry: %#v", parent.Updates)
	}
	if updates[parentPath] != DecisionTrusted {
		t.Errorf("parent choice does not trust %q: %#v", parentPath, parent.Updates)
	}
}
