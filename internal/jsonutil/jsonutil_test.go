package jsonutil

import (
	"strings"
	"testing"
)

type sample struct {
	Name string `json:"name"`
}

func TestDecodeStrictDecodesSingleValue(t *testing.T) {
	t.Parallel()

	var target sample
	if err := DecodeStrict([]byte(`{"name":"ok"}`), &target); err != nil {
		t.Fatalf("DecodeStrict() error = %v", err)
	}
	if target.Name != "ok" {
		t.Errorf("DecodeStrict() name = %q, want %q", target.Name, "ok")
	}
}

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	var target sample
	if err := DecodeStrict([]byte(`{"name":"ok","extra":1}`), &target); err == nil {
		t.Fatal("DecodeStrict() error = nil, want error")
	}
}

func TestDecodeStrictRejectsMultipleValues(t *testing.T) {
	t.Parallel()

	var target sample
	err := DecodeStrict([]byte(`{"name":"ok"}{"name":"again"}`), &target)
	if err == nil {
		t.Fatal("DecodeStrict() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "multiple json values") {
		t.Errorf("DecodeStrict() error = %v, want multiple-json-values error", err)
	}
}

func TestDecodeStrictRejectsTrailingGarbage(t *testing.T) {
	t.Parallel()

	var target sample
	if err := DecodeStrict([]byte(`{"name":"ok"}x`), &target); err == nil {
		t.Fatal("DecodeStrict() error = nil, want error")
	}
}

func TestDecodeStrictRejectsEmptyInput(t *testing.T) {
	t.Parallel()

	var target sample
	if err := DecodeStrict(nil, &target); err == nil {
		t.Fatal("DecodeStrict() error = nil, want error")
	}
}
