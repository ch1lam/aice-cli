package tool

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestBashOutputPreservesBytesAcrossWrites(t *testing.T) {
	text := "start 界🙂 end"
	for split := 0; split <= len(text); split++ {
		writer := newBashOutputWriter(len(text))
		writer.Write([]byte(text[:split]))
		writer.Write([]byte(text[split:]))
		if got := writer.String(); got != text {
			t.Fatalf("split %d: got %q, want %q", split, got, text)
		}
	}
}

func TestBashOutputKeepsHeadAndTailWithBoundedStorage(t *testing.T) {
	const limit = 128
	input := []byte("HEAD:" + strings.Repeat("middle", 10000) + ":TAIL")
	for _, chunk := range []int{1, 7, 65, len(input)} {
		writer := newBashOutputWriter(limit)
		for offset := 0; offset < len(input); offset += chunk {
			data := input[offset:min(offset+chunk, len(input))]
			if n, err := writer.Write(data); n != len(data) || err != nil {
				t.Fatalf("Write = %d, %v", n, err)
			}
		}
		got := writer.String()
		if len(got) != limit || !strings.HasPrefix(got, "HEAD:") || !strings.HasSuffix(got, ":TAIL") || !strings.Contains(got, bashTruncationMarker) {
			t.Fatalf("chunk %d: output = %q", chunk, got)
		}
		if cap(writer.head)+cap(writer.tail) > limit {
			t.Fatalf("storage exceeds limit: %d", cap(writer.head)+cap(writer.tail))
		}
		input[len(input)-1] = '!'
		if writer.String() != got {
			t.Fatal("retained output aliases caller bytes")
		}
		input[len(input)-1] = 'L'
	}
}

func TestBashOutputUTF8AndSmallLimits(t *testing.T) {
	input := []byte(strings.Repeat("界🙂", 100))
	for limit := 0; limit < 100; limit++ {
		writer := newBashOutputWriter(limit)
		for _, b := range input {
			writer.Write([]byte{b})
		}
		got := writer.String()
		if len(got) > limit || !utf8.ValidString(got) {
			t.Fatalf("limit %d: invalid output %q", limit, got)
		}
	}
	writer := newBashOutputWriter(100)
	writer.Write([]byte{'a', 0xff, 'b', 0xe7})
	if got := writer.String(); got != "ab" {
		t.Fatalf("malformed UTF-8 output = %q", got)
	}
}

func TestBashOutputConcurrentWritesAndSnapshots(t *testing.T) {
	writer := newBashOutputWriter(1024)
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Go(func() {
			for j := 0; j < 1000; j++ {
				writer.Write(bytes.Repeat([]byte("x"), 17))
				if got := writer.String(); len(got) > 1024 || !utf8.ValidString(got) {
					t.Errorf("invalid concurrent snapshot: %d bytes", len(got))
				}
			}
		})
	}
	group.Wait()
}
