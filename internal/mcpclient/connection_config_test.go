package mcpclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestExactProtocolPinIsPerConnection(t *testing.T) {
	for _, version := range []string{"2025-06-18", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			var initializes atomic.Int32
			f := &httpFixture{result: func(req fixtureRequest) string {
				if req.Method != "initialize" {
					return fixtureResult(req)
				}
				initializes.Add(1)
				var params struct {
					Protocol string `json:"protocolVersion"`
				}
				if json.Unmarshal(req.Params, &params) != nil || params.Protocol != version {
					t.Error("wrong proposed protocol", params.Protocol)
				}
				return strings.Replace(fixtureInit, "2025-11-25", version, 1)
			}, gets: make(chan string, 1)}
			c := openFixture(t, f, func(c *Config) { c.ProtocolVersion = version })
			if c.Info().ProtocolVersion != version || c.StdioPID() != 0 {
				t.Fatal("incorrect connection identity")
			}
			assertRoundTrip(t, c)
			if initializes.Load() != 1 {
				t.Fatal("connection reinitialized")
			}
		})
	}
	// No pin still uses the ordinary SDK proposal after pinned connections.
	c := openFixture(t, &httpFixture{result: func(req fixtureRequest) string {
		if req.Method == "initialize" && !strings.Contains(string(req.Params), "2025-11-25") {
			t.Error("pin leaked across connections")
		}
		return fixtureResult(req)
	}}, nil)
	if c.Info().ProtocolVersion != "2025-11-25" {
		t.Fatal("ordinary protocol changed")
	}
}

func TestExactProtocolPinRejectsMismatchAndInvalidVersion(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		(&httpFixture{}).ServeHTTP(w, r)
	}))
	defer server.Close()
	config := Config{HTTP: &HTTPConfig{Endpoint: server.URL}, ProtocolVersion: "unsupported"}
	if _, err := Open(t.Context(), config); !errors.Is(err, ErrConfig) || requests.Load() != 0 {
		t.Fatal("invalid tier reached transport", err)
	}
	config.ProtocolVersion = "2025-06-18"
	if c, err := Open(t.Context(), config); !errors.Is(err, ErrProtocol) {
		if c != nil {
			_ = c.Close()
		}
		t.Fatal("peer changed pinned tier", err)
	}
}

func TestConnectionMessageLimits(t *testing.T) {
	for _, tc := range []struct {
		supplied, want int
		invalid        bool
	}{
		{0, 16 << 20, false}, {24 << 20, 24 << 20, false}, {1024, 1024, false},
		{(24 << 20) + 1, 0, true}, {-1, 0, true},
	} {
		limits := Limits{MessageBytes: tc.supplied}
		err := normalizeLimits(&limits)
		if (err != nil) != tc.invalid || (!tc.invalid && limits.MessageBytes != tc.want) {
			t.Fatal("unexpected limit", tc, limits, err)
		}
	}
}

func TestStdioCompleteEnvironment(t *testing.T) {
	t.Setenv("COMSPEC", "must-not-inherit")
	for _, empty := range []bool{false, true} {
		config := stdioConfig(t)
		config.Stdio.ReplaceEnvironment = true
		if empty {
			config.Stdio.Env = nil
		}
		// The ordinary test helper needs a marker. Without it the test binary exits;
		// opening only the transport lets us verify the intentionally empty Env too.
		_, child, err := openStdio(*config.Stdio, defaultMessageBytes, &receipts{})
		if err != nil {
			t.Fatal(err)
		}
		env := child.cmd.Env
		if env == nil {
			t.Fatal("nil env would inherit the host environment")
		}
		for _, entry := range env {
			if strings.HasPrefix(entry, "COMSPEC=") {
				t.Fatal("base environment bypassed replacement")
			}
		}
		if empty && len(env) != 0 {
			t.Fatal("empty complete environment acquired values")
		}
		if err := child.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-child.done:
		default:
			t.Fatal("owned child not reaped")
		}
	}
	if os.Getenv("COMSPEC") != "must-not-inherit" {
		t.Fatal("parent environment changed")
	}
}
