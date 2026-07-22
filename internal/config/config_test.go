package config_test

import (
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestLoadEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  map[string]string
		want    config.Config
		wantErr string
	}{
		{
			name:    "missing API key",
			values:  map[string]string{},
			wantErr: config.EnvDeepSeekAPIKey,
		},
		{
			name: "blank API key",
			values: map[string]string{
				config.EnvDeepSeekAPIKey: "  ",
			},
			wantErr: config.EnvDeepSeekAPIKey,
		},
		{
			name: "official endpoint",
			values: map[string]string{
				config.EnvDeepSeekAPIKey: " test-key ",
			},
			want: config.Config{DeepSeekAPIKey: "test-key"},
		},
		{
			name: "custom endpoint",
			values: map[string]string{
				config.EnvDeepSeekAPIKey:  "test-key",
				config.EnvDeepSeekBaseURL: " https://deepseek.example/anthropic ",
			},
			want: config.Config{
				DeepSeekAPIKey:  "test-key",
				DeepSeekBaseURL: "https://deepseek.example/anthropic",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := config.LoadEnv(func(key string) (string, bool) {
				value, exists := tt.values[key]
				return value, exists
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadEnv() error = %v, want text %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadEnv() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadEnv() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestLoadEnvRejectsNilLookup(t *testing.T) {
	t.Parallel()

	_, err := config.LoadEnv(nil)
	if err == nil || !strings.Contains(err.Error(), "lookup is required") {
		t.Fatalf("LoadEnv() error = %v, want missing lookup error", err)
	}
}
