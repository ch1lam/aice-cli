package tool

import (
	"io"
	"strings"
	"sync"
)

const bashTruncationMarker = "\n[output truncated]\n"

// bashOutputWriter retains the start and most recent end of a byte stream.
// Storage never exceeds limit, even when a single Write contains huge output.
// UTF-8 is repaired only when rendering so split writes preserve whole runes.
type bashOutputWriter struct {
	mu        sync.Mutex
	limit     int
	head      []byte
	tail      []byte
	tailLen   int
	tailNext  int
	truncated bool
}

func newBashOutputWriter(limit int) *bashOutputWriter {
	limit = max(0, limit)
	headSize := limit / 2
	return &bashOutputWriter{
		limit: limit,
		head:  make([]byte, 0, headSize),
		tail:  make([]byte, limit-headSize),
	}
}

func (w *bashOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(data)
	headCount := min(len(data), cap(w.head)-len(w.head))
	w.head = append(w.head, data[:headCount]...)
	data = data[headCount:]
	if len(data) > len(w.tail)-w.tailLen {
		w.truncated = true
	}
	if len(data) == 0 || len(w.tail) == 0 {
		return n, nil
	}
	if len(data) >= len(w.tail) {
		copy(w.tail, data[len(data)-len(w.tail):])
		w.tailLen = len(w.tail)
		w.tailNext = 0
		return n, nil
	}
	first := copy(w.tail[w.tailNext:], data)
	copy(w.tail, data[first:])
	w.tailNext = (w.tailNext + len(data)) % len(w.tail)
	w.tailLen = min(len(w.tail), w.tailLen+len(data))
	return n, nil
}

func (w *bashOutputWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	tail := make([]byte, w.tailLen)
	start := 0
	if w.tailLen == len(w.tail) {
		start = w.tailNext
	}
	first := copy(tail, w.tail[start:])
	copy(tail[first:], w.tail[:start])
	if !w.truncated {
		return strings.ToValidUTF8(string(w.head)+string(tail), "")
	}
	if w.limit < len(bashTruncationMarker) {
		return bashTruncationMarker[:w.limit]
	}
	available := w.limit - len(bashTruncationMarker)
	headCount := available / 2
	tailCount := available - headCount
	return strings.ToValidUTF8(string(w.head[:headCount]), "") +
		bashTruncationMarker +
		strings.ToValidUTF8(string(tail[len(tail)-tailCount:]), "")
}

var _ io.Writer = (*bashOutputWriter)(nil)
