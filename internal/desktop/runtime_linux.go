package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

// Linux supports both a shared service proxy and an owned stdio runtime in the
// pinned public CLI. Reuse only a verified compatible service. Exact absence
// permits a private runtime, whose lifetime is the Manager's MCP connection.
func dialLinuxRuntime(ctx context.Context, binary, endpoint string) (driverClient, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	s, err := newLinuxInspector(binary, endpoint)
	if err != nil {
		return nil, err
	}
	if _, err := s.service.inspect(ctx); err == nil {
		s.connect = func(ctx context.Context) (driverClient, error) {
			transport, err := newProxyConfig(binary, endpoint)
			if err != nil {
				return nil, err
			}
			return connectReviewed(ctx, transport, reviewedLinuxTools)
		}
		c, facts, err := s.admit(ctx)
		if err != nil {
			return nil, err
		}
		if err := requireLinuxX11(facts); err != nil {
			return nil, errors.Join(err, c.close())
		}
		return c, nil
	} else if !serviceHasCode(err, "not_running") {
		return nil, err
	}
	transport, err := newLinuxOwnedConfig(binary)
	if err != nil {
		return nil, err
	}
	c, err := connectReviewed(ctx, transport, reviewedLinuxTools)
	if err != nil {
		return nil, err
	}
	facts, err := readLinuxRuntime(ctx, c)
	if err == nil {
		err = requireLinuxX11(facts)
	}
	if err != nil {
		return nil, errors.Join(err, c.close())
	}
	return c, nil
}

func newLinuxOwnedConfig(binary string) (mcpclient.Config, error) {
	if !filepath.IsAbs(binary) {
		return mcpclient.Config{}, errors.New("desktop: verified absolute Linux binary required")
	}
	// No daemon socket or autostart entry. The generic client owns this child;
	// the fixed environment selects standard native permission mode.
	return driverMCPConfig(binary, filepath.Dir(binary), "mcp", "--direct"), nil
}
