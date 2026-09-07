package openairesponses_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
)

func TestAdapterHonorsProviderDefaultOutputLimit(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		omit     bool
		explicit int64
		want     float64
	}{
		{name: "provider default", omit: true},
		{name: "explicit override", omit: true, explicit: 1024, want: 1024},
		{name: "model default", want: 131072},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bodies := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				bodies <- body
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer server.Close()
			adapter, err := openairesponses.New(openairesponses.Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			request := apitest.MinimalRequest(openairesponses.API)
			request.Model.MaxTokens = 131072
			request.Model.OmitMaxTokensByDefault = test.omit
			request.Options.MaxTokens = test.explicit
			stream, err := adapter.Stream(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			body := <-bodies
			got, present := body["max_output_tokens"]
			if test.want == 0 {
				if present {
					t.Fatalf("provider default overridden by max_output_tokens=%v", got)
				}
			} else if got != test.want {
				t.Fatalf("max_output_tokens=%v, want %v", got, test.want)
			}
		})
	}
}
