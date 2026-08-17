package tool

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type cancelingReader struct {
	cancel context.CancelFunc
	data   []byte
	read   bool
}

func (r *cancelingReader) Read(buffer []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	read := copy(buffer, r.data)
	r.cancel()
	return read, nil
}

func TestReadTextPageStopsWhenReadCancelsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	reader := &cancelingReader{
		cancel: cancel,
		data:   []byte("one\ntwo\n"),
	}

	_, err := readTextPage(ctx, reader, 1, 1, "", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readTextPage() error = %v, want context.Canceled", err)
	}
}

func TestCountRemainingLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		data   string
		budget int
		count  int
		capped bool
	}{
		{name: "counts to EOF", data: "a\nb\nc\n", budget: 1024 * 1024, count: 3},
		{name: "empty reader", data: "", budget: 1024 * 1024, count: 0},
		{name: "no trailing newline", data: "a\nb", budget: 1024 * 1024, count: 2},
		{name: "budget cut short", data: "a\nb\nc\n", budget: 2, count: 1, capped: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := bufio.NewReader(strings.NewReader(test.data))
			count, capped, err := countRemainingLines(t.Context(), reader, test.budget)
			if err != nil {
				t.Fatalf("countRemainingLines() error = %v", err)
			}
			if count != test.count || capped != test.capped {
				t.Fatalf(
					"countRemainingLines() = (%d, %t), want (%d, %t)",
					count, capped, test.count, test.capped,
				)
			}
		})
	}
}

func TestCountRemainingLinesStopsWhenReadCancelsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	reader := bufio.NewReader(&cancelingReader{
		cancel: cancel,
		data:   []byte("one\ntwo\nthree\n"),
	})

	if _, _, err := countRemainingLines(ctx, reader, 1024*1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("countRemainingLines() error = %v, want context.Canceled", err)
	}
}

func TestCountRemainingLinesRejectsBinary(t *testing.T) {
	t.Parallel()
	reader := bufio.NewReader(strings.NewReader("ok\nbad\x00\n"))
	if _, _, err := countRemainingLines(t.Context(), reader, 1024*1024); !errors.Is(err, errBinaryContent) {
		t.Fatalf("countRemainingLines() error = %v, want errBinaryContent", err)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain", value: "large.txt", want: "'large.txt'"},
		{name: "apostrophe", value: "it's.txt", want: "'it'\\''s.txt'"},
		{name: "spaces", value: "my file.txt", want: "'my file.txt'"},
		{name: "empty", value: "", want: "''"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := shellQuote(test.value); got != test.want {
				t.Fatalf("shellQuote() = %q, want %q", got, test.want)
			}
		})
	}
}
