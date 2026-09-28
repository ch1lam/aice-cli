package mcpclient

import (
	"bufio"
	"bytes"
	"io"
)

// frameReader bounds frames before the SDK can allocate an unbounded JSON
// value. It forwards original bytes; observing them does not implement MCP.
type frameReader struct {
	source   io.ReadCloser
	reader   *bufio.Reader
	limit    int
	sse      bool
	jsonBody bool
	receipts *receipts
	pending  []byte
	done     bool
}

func newFrameReader(source io.ReadCloser, limit int, sse, jsonBody bool, receipts *receipts) *frameReader {
	return &frameReader{source: source, reader: bufio.NewReader(source), limit: limit,
		sse: sse, jsonBody: jsonBody, receipts: receipts}
}

func (r *frameReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.done {
			return 0, io.EOF
		}
		frame, err := r.frame()
		if err != nil {
			if err == ErrLimit {
				r.receipts.limit()
			}
			return 0, err
		}
		r.pending = frame
		if r.sse {
			var data []byte
			for line := range bytes.SplitSeq(frame, []byte{'\n'}) {
				line = bytes.TrimSuffix(line, []byte{'\r'})
				if rest, ok := bytes.CutPrefix(line, []byte("data:")); ok {
					rest = bytes.TrimPrefix(rest, []byte{' '})
					data = append(data, rest...)
					data = append(data, '\n')
				}
			}
			r.receipts.observe(data, false)
		} else {
			r.receipts.observe(frame, false)
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *frameReader) frame() ([]byte, error) {
	if r.jsonBody {
		data, err := io.ReadAll(io.LimitReader(r.reader, int64(r.limit)+1))
		r.done = true
		if len(data) > r.limit {
			return nil, ErrLimit
		}
		return data, err
	}
	var frame []byte
	lineStart := 0
	for {
		chunk, err := r.reader.ReadSlice('\n')
		if len(frame)+len(chunk) > r.limit {
			return nil, ErrLimit
		}
		frame = append(frame, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			r.done = true
			if err == io.EOF && len(frame) > 0 {
				return frame, nil
			}
			return nil, err
		}
		line := bytes.TrimRight(frame[lineStart:], "\r\n")
		if !r.sse || len(line) == 0 {
			return frame, nil
		}
		lineStart = len(frame)
	}
}

func (r *frameReader) Close() error { return r.source.Close() }

type observedWriter struct {
	io.WriteCloser
	receipts *receipts
}

func (w *observedWriter) Write(p []byte) (int, error) {
	w.receipts.observe(p, true)
	return w.WriteCloser.Write(p)
}
