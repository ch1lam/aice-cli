package anthropic_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/anthropic"
)

func TestCatalogThinkingOnWire(t *testing.T) {
	t.Parallel()
	for _, model := range anthropic.Models() {
		for _, level := range llm.SupportedThinkingLevels(model) {
			t.Run(model.ID+"/"+string(level), func(t *testing.T) {
				t.Parallel()
				var body struct {
					Model    string `json:"model"`
					Thinking struct {
						Type   string `json:"type"`
						Budget int64  `json:"budget_tokens"`
					} `json:"thinking"`
					OutputConfig *struct {
						Effort string `json:"effort"`
					} `json:"output_config"`
				}
				client := &http.Client{Transport: apitest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.URL.String() != anthropic.BaseURL+"/v1/messages" || r.Header.Get("X-Api-Key") != "test-key" || r.Header.Get("Authorization") != "" {
						t.Errorf("unexpected endpoint or authentication")
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				})}
				p, err := anthropic.New(anthropic.Config{APIKey: "test-key", HTTPClient: client})
				if err != nil {
					t.Fatal(err)
				}
				request := apitest.MinimalRequest(model.API)
				request.Model, request.Options.Thinking = model, level
				stream, err := p.Stream(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				wantType := "adaptive"
				if model.ID == anthropic.ModelHaiku45 {
					wantType = "enabled"
				}
				if level == llm.ThinkingLevelOff {
					wantType = "disabled"
				}
				if body.Model != model.ID || body.Thinking.Type != wantType {
					t.Fatalf("unexpected request: %+v", body)
				}
				if wantType == "enabled" && body.Thinking.Budget != 1024 {
					t.Fatalf("budget = %d", body.Thinking.Budget)
				}
				if wantType == "adaptive" {
					if body.Thinking.Budget != 0 || body.OutputConfig == nil || body.OutputConfig.Effort != string(level) {
						t.Fatalf("adaptive thinking: %+v", body)
					}
				} else if body.OutputConfig != nil {
					t.Fatal("disabled/Haiku thinking must omit effort")
				}
			})
		}
	}
}

func TestAlwaysThinkingModelsRejectOffBeforeHTTP(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected HTTP request")
		return nil, io.ErrUnexpectedEOF
	})}
	p, err := anthropic.New(anthropic.Config{APIKey: "test-key", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range anthropic.Models() {
		if model.ID != anthropic.ModelOpus55 && model.ID != anthropic.ModelFable51 {
			continue
		}
		request := apitest.MinimalRequest(model.API)
		request.Model, request.Options.Thinking = model, llm.ThinkingLevelOff
		if _, err := p.Stream(t.Context(), request); err == nil || !strings.Contains(err.Error(), "does not support thinking") {
			t.Fatalf("off error = %v", err)
		}
	}
}

func TestCatalogValuesAreIndependent(t *testing.T) {
	t.Parallel()
	models := anthropic.Models()
	*models[0].ThinkingLevelMap[llm.ThinkingLevelHigh] = "changed"
	models[0].InputModalities[0] = "changed"
	fresh := anthropic.DefaultModel()
	if value, _ := fresh.ThinkingLevelMap.WireValue(llm.ThinkingLevelHigh); value != "high" || fresh.InputModalities[0] != llm.InputModalityText {
		t.Fatal("catalog mutation leaked")
	}
}
