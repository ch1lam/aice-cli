package streamcore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestResolveMaxTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request llm.Request
		want    int64
		wantErr string
	}{
		{
			name:    "option wins",
			request: llm.Request{Options: llm.StreamOptions{MaxTokens: 10}, Model: llm.Model{MaxTokens: 99}},
			want:    10,
		},
		{
			name:    "model default",
			request: llm.Request{Model: llm.Model{MaxTokens: 32}},
			want:    32,
		},
		{
			name:    "non-positive is rejected",
			request: llm.Request{Model: llm.Model{MaxTokens: 0}},
			wantErr: "max tokens must be positive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolveMaxTokens(test.request)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("ResolveMaxTokens() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveMaxTokens() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("ResolveMaxTokens() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestValidateTemperature(t *testing.T) {
	t.Parallel()

	if err := ValidateTemperature(nil); err != nil {
		t.Fatalf("nil temperature error = %v", err)
	}
	ok := 1.5
	if err := ValidateTemperature(&ok); err != nil {
		t.Fatalf("1.5 error = %v", err)
	}
	high := 2.1
	if err := ValidateTemperature(&high); err == nil {
		t.Fatal("2.1 error = nil, want rejection")
	}
}

func TestDecodeToolSchemas(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object"}`)
	got, err := DecodeToolSchemas([]llm.ToolDefinition{
		{Name: "read", InputSchema: schema},
	})
	if err != nil {
		t.Fatalf("DecodeToolSchemas() error = %v", err)
	}
	if len(got) != 1 || got[0]["type"] != "object" {
		t.Fatalf("DecodeToolSchemas() = %#v", got)
	}

	_, err = DecodeToolSchemas([]llm.ToolDefinition{{Name: "", InputSchema: schema}})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("empty name error = %v", err)
	}
}

func TestImageDataURL(t *testing.T) {
	t.Parallel()

	got := ImageDataURL(llm.ImageContent{MIMEType: "image/png", Data: []byte("hi")})
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("ImageDataURL() = %q", got)
	}
}
