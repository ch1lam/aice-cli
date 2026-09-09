package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"

	"golang.org/x/image/riff"
	"golang.org/x/image/vp8l"
	"golang.org/x/image/webp"
)

// The decoder's extended-header config describes the canvas, not necessarily
// the encoded VP8L dimensions. Check those before Decode can allocate pixels.
// Animation is rejected explicitly: x/image/webp does not decode ANMF frames.
func inspectWebP(data []byte) (image.Config, error) {
	config, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return config, err
	}
	_, chunks, err := riff.NewReader(bytes.NewReader(data))
	if err != nil {
		return config, err
	}
	for {
		id, _, payload, err := chunks.Next()
		if errors.Is(err, io.EOF) {
			return config, nil
		}
		if err != nil {
			return config, err
		}
		switch string(id[:]) {
		case "VP8X":
			var flags [1]byte
			if _, err := io.ReadFull(payload, flags[:]); err != nil {
				return config, err
			}
			if flags[0]&2 != 0 {
				return config, fmt.Errorf("animated WebP is not supported by the decoder")
			}
		case "ANIM", "ANMF":
			return config, fmt.Errorf("animated WebP is not supported by the decoder")
		case "VP8L":
			frame, err := vp8l.DecodeConfig(payload)
			if err != nil {
				return config, err
			}
			if frame.Width != config.Width || frame.Height != config.Height {
				return config, fmt.Errorf("WebP frame dimensions %dx%d do not match canvas %dx%d", frame.Width, frame.Height, config.Width, config.Height)
			}
		}
	}
}
