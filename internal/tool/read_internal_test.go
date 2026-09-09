package tool

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
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

	_, _, err := readTextPage(ctx, reader, 1, 1, "", false)
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

func TestReadTruncationDescribesFinalPage(t *testing.T) {
	tests := []struct {
		name, source              string
		limit                     int
		userLimited               bool
		reason                    llm.TruncationReason
		lines, bytes, next, total int
		known                     bool
	}{
		{name: "requested", source: "one\ntwo\nthree", limit: 1, userLimited: true, reason: llm.TruncationRequestedLines, lines: 1, bytes: 4, next: 2, total: 3, known: true},
		{name: "default", source: strings.Repeat("x\n", 2001), limit: 2000, reason: llm.TruncationLineLimit, lines: 2000, bytes: 4000, next: 2001},
		{name: "byte budget", source: strings.Repeat(strings.Repeat("x", 1023)+"\n", 60), limit: 2000, reason: llm.TruncationByteLimit, lines: 49, bytes: 49 * 1024, next: 50},
		{name: "notice removes line", source: strings.Repeat("x", maxOutputBytes-1) + "\nmore\n", limit: 1, userLimited: true, reason: llm.TruncationOversizedLine, next: 1, total: 2, known: true},
		{name: "oversized", source: strings.Repeat("x", maxOutputBytes+1), limit: 2000, reason: llm.TruncationOversizedLine, next: 1},
		{name: "complete", source: "one\ntwo", limit: 2000},
		{name: "empty", limit: 2000},
		{name: "UTF-8 and CRLF", source: "界\r\né", limit: 1, userLimited: true, reason: llm.TruncationRequestedLines, lines: 1, bytes: 5, next: 2, total: 2, known: true},
		{name: "notice changes reason", source: strings.Repeat(strings.Repeat("x", 1023)+"\n", 60), limit: 50, userLimited: true, reason: llm.TruncationByteLimit, lines: 49, bytes: 49 * 1024, next: 50, total: 60, known: true},
		{name: "exact limit at EOF", source: "one", limit: 1, userLimited: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, got, err := readTextPage(t.Context(), strings.NewReader(tt.source), 1, tt.limit, "file.txt", tt.userLimited)
			if err != nil {
				t.Fatal(err)
			}
			want := llm.ToolTruncation{Reason: tt.reason, OutputLines: tt.lines, OutputBytes: tt.bytes, NextOffset: tt.next, TotalLines: tt.total, TotalLinesKnown: tt.known}
			if got != want {
				t.Fatalf("metadata = %+v, want %+v", got, want)
			}
			if len(text) > maxOutputBytes {
				t.Fatalf("output exceeds budget: %d", len(text))
			}
			if got.OutputBytes > 0 && !strings.HasPrefix(text, tt.source[:got.OutputBytes]) {
				t.Fatal("source bytes mismatch")
			}
			if got.NextOffset > 1 {
				next, _, err := readTextPage(t.Context(), strings.NewReader(tt.source), got.NextOffset, 2000, "file.txt", false)
				if err != nil {
					t.Fatal(err)
				}
				remaining := tt.source[got.OutputBytes:]
				if !strings.HasPrefix(next, remaining[:min(len(remaining), 1024)]) {
					t.Fatal("continuation skipped or duplicated source content")
				}
			}
		})
	}
}

func TestReadTruncationDoesNotScanForTotal(t *testing.T) {
	source := &io.LimitedReader{R: strings.NewReader(strings.Repeat("x\n", 100000)), N: 200000}
	_, got, err := readTextPage(t.Context(), source, 1, 2000, "file.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalLinesKnown || source.N < 200000-readBufferBytes {
		t.Fatalf("unexpected total scan: %+v, bytes left %d", got, source.N)
	}
	_, got, err = readTextPage(t.Context(), strings.NewReader("first\n"+strings.Repeat("x\n", maxReadBytes)), 1, 1, "file.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalLinesKnown || got.Reason != llm.TruncationRequestedLines {
		t.Fatalf("capped total = %+v", got)
	}
}

func TestReadTextPageDoesNotScanToEOFForDefaultPage(t *testing.T) {
	t.Parallel()
	source := strings.NewReader(strings.Repeat("line\n", 100000))
	page, _, err := readTextPage(t.Context(), source, 1, defaultReadLines, "notes.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if source.Len() == 0 {
		t.Fatal("default page scanned the entire source")
	}
	if !strings.HasSuffix(page, "[Showing lines 1-2000 (2000 line limit). Use offset=2001 to continue.]") {
		t.Fatal("default page lost its continuation notice")
	}
}
