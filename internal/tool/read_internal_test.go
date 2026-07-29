package tool

import (
	"context"
	"errors"
	"io"
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

	_, err := readTextPage(ctx, reader, 1, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readTextPage() error = %v, want context.Canceled", err)
	}
}
