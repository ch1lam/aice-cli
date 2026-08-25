package streamcore

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

type staticPartial struct {
	content llm.ContentPart
	ok      bool
}

func (p staticPartial) PartialContent() (llm.ContentPart, bool) {
	return p.content, p.ok
}

func TestPartRegistryLifecycle(t *testing.T) {
	t.Parallel()

	text := llm.NewTextContent("hello").Part()
	later := llm.NewTextContent("later").Part()
	thinking := llm.NewThinkingContent("reason", "sig").Part()

	tests := []struct {
		name  string
		apply func(*PartRegistry)
		want  []llm.ContentPart
		has   map[int]bool
	}{
		{
			name: "complete records a finished part",
			apply: func(r *PartRegistry) {
				r.Complete(0, text)
			},
			want: []llm.ContentPart{text},
			has:  map[int]bool{0: true, 1: false},
		},
		{
			name: "partial is included when PartialContent ok",
			apply: func(r *PartRegistry) {
				r.Partial(0, staticPartial{content: text, ok: true})
			},
			want: []llm.ContentPart{text},
			has:  map[int]bool{0: true},
		},
		{
			name: "partial with ok=false is omitted from snapshot",
			apply: func(r *PartRegistry) {
				r.Partial(0, staticPartial{content: text, ok: false})
			},
			want: []llm.ContentPart{},
			has:  map[int]bool{0: true},
		},
		{
			name: "complete replaces a partial at the same index",
			apply: func(r *PartRegistry) {
				r.Partial(0, staticPartial{content: text, ok: true})
				r.Complete(0, later)
			},
			want: []llm.ContentPart{later},
		},
		{
			name: "duplicate complete keeps the last write",
			apply: func(r *PartRegistry) {
				r.Complete(0, text)
				r.Complete(0, later)
			},
			want: []llm.ContentPart{later},
		},
		{
			name: "duplicate partial keeps the last write",
			apply: func(r *PartRegistry) {
				r.Partial(0, staticPartial{content: text, ok: true})
				r.Partial(0, staticPartial{content: later, ok: true})
			},
			want: []llm.ContentPart{later},
		},
		{
			name: "out-of-order indexes snapshot in index order",
			apply: func(r *PartRegistry) {
				r.Complete(2, thinking)
				r.Complete(0, text)
				r.Partial(1, staticPartial{content: later, ok: true})
			},
			want: []llm.ContentPart{text, later, thinking},
		},
		{
			name: "sparse indexes omit gaps",
			apply: func(r *PartRegistry) {
				r.Complete(0, text)
				r.Complete(3, later)
			},
			want: []llm.ContentPart{text, later},
			has:  map[int]bool{0: true, 1: false, 3: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			registry := NewPartRegistry()
			test.apply(registry)
			got := registry.Snapshot()
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Snapshot() = %#v, want %#v", got, test.want)
			}
			for index, wantHas := range test.has {
				if gotHas := registry.Has(index); gotHas != wantHas {
					t.Fatalf("Has(%d) = %v, want %v", index, gotHas, wantHas)
				}
			}
		})
	}
}

func TestPartialHelpers(t *testing.T) {
	t.Parallel()

	text := PartialText("hello", "sig")
	if text.Type != llm.ContentTypeText || text.Text != "hello" || text.Signature != "sig" {
		t.Fatalf("PartialText() = %#v", text)
	}

	thinking := PartialThinking("reason", "sig", true)
	if thinking.Type != llm.ContentTypeThinking || !thinking.Redacted {
		t.Fatalf("PartialThinking() = %#v", thinking)
	}

	valid, ok := PartialToolCall(llm.ToolCall{ID: "1", Name: "read"}, `{"path":"a.go"}`, nil)
	if !ok || valid.ToolCall == nil || string(valid.ToolCall.Arguments) != `{"path":"a.go"}` {
		t.Fatalf("PartialToolCall(valid) = %#v ok=%v", valid, ok)
	}

	invalid, ok := PartialToolCall(llm.ToolCall{ID: "1", Name: "read"}, `{`, nil)
	if ok || invalid.Type != "" {
		t.Fatalf("PartialToolCall(invalid) = %#v ok=%v, want omitted", invalid, ok)
	}
}

func TestToolCallArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		streamed string
		initial  []byte
		want     string
	}{
		{name: "empty becomes object", want: "{}"},
		{name: "streamed wins", streamed: `{"a":1}`, initial: []byte(`{"a":0}`), want: `{"a":1}`},
		{name: "initial used when streamed empty", initial: []byte(`{"a":0}`), want: `{"a":0}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := ToolCallArguments(test.streamed, test.initial)
			if string(got) != test.want {
				t.Fatalf("ToolCallArguments() = %s, want %s", got, test.want)
			}
			if !json.Valid(got) {
				t.Fatalf("ToolCallArguments() produced invalid json %s", got)
			}
		})
	}
}

func TestFinishToolCall(t *testing.T) {
	t.Parallel()

	call, err := FinishToolCall(llm.ToolCall{ID: "1", Name: "read"}, `{"path":"a.go"}`, nil)
	if err != nil {
		t.Fatalf("FinishToolCall() error = %v", err)
	}
	if string(call.Arguments) != `{"path":"a.go"}` {
		t.Fatalf("FinishToolCall() arguments = %s", call.Arguments)
	}

	_, err = FinishToolCall(llm.ToolCall{ID: "1", Name: "read"}, `{`, nil)
	if err == nil {
		t.Fatal("FinishToolCall(invalid) error = nil")
	}
}

func TestProjectThinkingToText(t *testing.T) {
	t.Parallel()

	input := []llm.ContentPart{
		llm.NewTextContent("visible").Part(),
		llm.NewThinkingContent("keep", "").Part(),
		func() llm.ContentPart {
			part := llm.NewThinkingContent("secret", "").Part()
			part.Redacted = true
			return part
		}(),
		llm.NewThinkingContent("   ", "").Part(),
	}
	got := ProjectThinkingToText(input)
	want := []llm.ContentPart{
		llm.NewTextContent("visible").Part(),
		llm.NewTextContent("keep").Part(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProjectThinkingToText() = %#v, want %#v", got, want)
	}
}
