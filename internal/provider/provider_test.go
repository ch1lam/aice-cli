package provider_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/provider/aihubmix"
	anthropicprovider "github.com/ch1lam/aice-cli/internal/provider/anthropic"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/kimi"
	"github.com/ch1lam/aice-cli/internal/provider/moonshot"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
	"github.com/ch1lam/aice-cli/internal/provider/zhipu"
)

// connect builds a credentialed service against a test endpoint.
type connect func(baseURL string, client *http.Client) (llm.Streamer, error)

// providerCase describes one built-in provider for the shared table tests.
type providerCase struct {
	descriptor provider.Provider
	// models is the package-level catalog, when the package exports one.
	models func() []llm.Model
	// credential is the descriptor's credential kind: an API key, OAuth
	// login, or none for keyless local endpoints.
	credential string
	// connect is nil for OAuth providers, whose services resolve credentials
	// per request.
	connect connect
	// baseSuffix is appended to the test server URL to form the base URL.
	baseSuffix string
	// paths is the request path each API reaches under that base URL.
	paths map[llm.API]string
	// anyModel providers accept IDs outside their catalog.
	anyModel bool
}

func providerCases() []providerCase {
	responses := map[llm.API]string{openairesponses.API: "/responses"}
	completions := map[llm.API]string{openaicompletions.API: "/chat/completions"}
	gateway := map[llm.API]string{
		anthropic.API:         "/v1/messages",
		openaicompletions.API: "/v1/chat/completions",
		openairesponses.API:   "/v1/responses",
	}
	return []providerCase{
		{
			descriptor: &openai.Provider{}, models: openai.Models, credential: "key", paths: responses,
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return openai.New(openai.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: &anthropicprovider.Provider{}, models: anthropicprovider.Models, credential: "key",
			paths: map[llm.API]string{anthropic.API: "/v1/messages"},
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return anthropicprovider.New(anthropicprovider.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: &kimi.Provider{}, models: kimi.Models, credential: "key", paths: responses,
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return kimi.New(kimi.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: &moonshot.Provider{}, models: moonshot.Models, credential: "key", paths: responses,
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return moonshot.New(moonshot.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: &deepseek.Provider{}, models: deepseek.Models, credential: "key",
			paths: map[llm.API]string{openairesponses.API: "/responses", anthropic.API: "/anthropic/v1/messages"},
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return deepseek.New(deepseek.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: &opencode.Provider{}, models: opencode.Models, credential: "key", baseSuffix: "/v1", paths: gateway,
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return opencode.New(opencode.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: &aihubmix.Provider{}, models: aihubmix.Models, credential: "key", baseSuffix: "/v1", paths: gateway,
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return aihubmix.New(aihubmix.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: &zhipu.Provider{}, credential: "key", paths: completions,
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return zhipu.New(zhipu.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: zhipu.CodingPlan(), credential: "key", paths: completions,
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return zhipu.NewCoding(zhipu.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
			},
		},
		{
			descriptor: &custom.Provider{}, models: custom.Models, credential: "none", paths: completions, anyModel: true,
			connect: func(baseURL string, client *http.Client) (llm.Streamer, error) {
				return custom.New(custom.Config{BaseURL: baseURL, HTTPClient: client})
			},
		},
		{descriptor: &codex.Provider{}, models: codex.Models, credential: "oauth"},
		{descriptor: &claudesubscription.Provider{}, models: claudesubscription.Models, credential: "oauth"},
	}
}

func TestProviderCatalogInvariants(t *testing.T) {
	t.Parallel()
	for _, tc := range providerCases() {
		id := tc.descriptor.ProviderID()
		t.Run(string(id), func(t *testing.T) {
			t.Parallel()
			d := tc.descriptor
			if strings.TrimSpace(d.Label()) == "" || strings.TrimSpace(d.MenuDescription()) == "" {
				t.Error("label and menu description are required")
			}
			models := d.Models()
			if len(models) == 0 {
				t.Fatal("catalog is empty")
			}
			if tc.models != nil && !reflect.DeepEqual(tc.models(), models) {
				t.Error("descriptor catalog differs from the package catalog")
			}
			seen := make(map[string]bool, len(models))
			for _, model := range models {
				if seen[model.ID] {
					t.Errorf("duplicate model %q", model.ID)
				}
				seen[model.ID] = true
				if model.Provider != id || model.API == "" || model.Name == "" {
					t.Errorf("model %q identity = %q/%q/%q", model.ID, model.Provider, model.API, model.Name)
				}
				if model.ContextWindow <= 0 || model.MaxTokens <= 0 || model.MaxTokens > model.ContextWindow {
					t.Errorf("model %q budgets = %d/%d", model.ID, model.ContextWindow, model.MaxTokens)
				}
				for _, level := range []llm.ThinkingLevel{
					llm.ThinkingLevelOff, llm.ThinkingLevelMinimal, llm.ThinkingLevelLow, llm.ThinkingLevelMedium,
					llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax,
				} {
					if _, ok := model.ThinkingLevelMap[level]; !ok {
						t.Errorf("model %q thinking map omits %q", model.ID, level)
					}
				}
			}
			if !containsModel(models, d.DefaultModel()) {
				t.Errorf("default model %q is not a catalog entry", d.DefaultModel().ID)
			}

			mutate := func(model *llm.Model) {
				for level, value := range model.ThinkingLevelMap {
					if value != nil {
						*value = "mutated"
					}
					model.ThinkingLevelMap[level] = llm.ThinkingValue("mutated")
				}
				if len(model.InputModalities) > 0 {
					model.InputModalities[0] = "mutated"
				}
			}
			changed := d.Models()
			for index := range changed {
				mutate(&changed[index])
			}
			changedDefault := d.DefaultModel()
			mutate(&changedDefault)
			if !reflect.DeepEqual(d.Models(), models) || !containsModel(models, d.DefaultModel()) {
				t.Error("mutating returned models changed the catalog")
			}
		})
	}
}

func containsModel(models []llm.Model, want llm.Model) bool {
	for _, model := range models {
		if reflect.DeepEqual(model, want) {
			return true
		}
	}
	return false
}

func TestProviderDescriptorCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range providerCases() {
		t.Run(string(tc.descriptor.ProviderID()), func(t *testing.T) {
			t.Parallel()
			d := tc.descriptor
			var configuration config.Config
			if got := d.Configured(configuration); got != (tc.credential == "none") {
				t.Fatalf("Configured(empty) = %v", got)
			}
			switch tc.credential {
			case "key":
				if err := d.CredentialNotConfiguredError(); err == nil || err.Error() == "" {
					t.Error("missing credential error is empty")
				}
				if _, err := d.New(configuration); err == nil || !strings.Contains(err.Error(), "API key is required") {
					t.Errorf("New(empty) error = %v, want missing API key", err)
				}
				d.ApplyAPIKey(&configuration, "test-key")
				if !d.Configured(configuration) {
					t.Error("Configured() = false after ApplyAPIKey")
				}
			case "oauth":
				if _, err := d.New(configuration); err == nil {
					t.Error("New(empty) succeeded without a login")
				}
				if _, err := d.SaveAPIKey("api-key"); err == nil {
					t.Error("OAuth provider accepted an API key")
				}
			case "none":
				if _, err := d.New(configuration); err != nil {
					t.Errorf("New(empty) error = %v, want keyless service", err)
				}
			}
		})
	}
}

func TestProvidersRouteCatalogModelsAndRejectMismatchesBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, tc := range providerCases() {
		if tc.connect == nil {
			continue
		}
		id := tc.descriptor.ProviderID()
		t.Run(string(id), func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer server.Close()
			service, err := tc.connect(server.URL+tc.baseSuffix, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			request := func(model llm.Model) llm.Request {
				return llm.Request{Model: model, Messages: []llm.Message{llm.UserMessage{
					Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
				}}}
			}

			for _, model := range tc.descriptor.Models() {
				stream, err := service.Stream(t.Context(), request(model))
				if err != nil {
					t.Fatalf("Stream(%q) error = %v", model.ID, err)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				got := paths[len(paths)-1]
				mu.Unlock()
				if want, ok := tc.paths[model.API]; !ok || got != want {
					t.Errorf("model %q path = %q, want %q", model.ID, got, want)
				}
			}

			mu.Lock()
			sent := len(paths)
			mu.Unlock()
			mismatch := func(change func(*llm.Model)) llm.Model {
				model := tc.descriptor.DefaultModel()
				change(&model)
				return model
			}
			mismatches := map[string]llm.Model{
				"provider": mismatch(func(model *llm.Model) { model.Provider = "other" }),
				"API":      mismatch(func(model *llm.Model) { model.API = "other-api" }),
			}
			if !tc.anyModel {
				mismatches["model"] = mismatch(func(model *llm.Model) { model.ID = "unknown-model" })
			}
			for name, model := range mismatches {
				_, err := service.Stream(t.Context(), request(model))
				if err == nil || !strings.HasPrefix(err.Error(), string(id)+": ") {
					t.Errorf("%s mismatch error = %v, want %s-prefixed rejection", name, err, id)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(paths) != sent {
				t.Errorf("rejected requests reached HTTP: %v", paths[sent:])
			}
		})
	}
}
