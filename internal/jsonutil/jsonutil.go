// Package jsonutil provides strict JSON decoding shared by AICE storage
// and input loaders.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeStrict decodes exactly one JSON value from data into target,
// rejecting unknown fields and any trailing value after the first.
func DecodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("jsonutil: decode: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("jsonutil: multiple json values")
		}
		return fmt.Errorf("jsonutil: decode trailing json: %w", err)
	}
	return nil
}
