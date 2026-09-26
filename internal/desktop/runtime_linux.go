package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
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
			transport, err := newProcessTransport(binary, endpoint)
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
	transport, err := newLinuxOwnedTransport(binary)
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

func newLinuxOwnedTransport(binary string) (*processTransport, error) {
	if !filepath.IsAbs(binary) {
		return nil, errors.New("desktop: verified absolute Linux binary required")
	}
	// There is no daemon socket, autostart entry, implicit grant or remote
	// endpoint. This exact child is reaped by the shared ownedProcess transport.
	// Standard mode comes from the fixed environment; upstream validates its
	// immutable mode and configured native policy before accepting MCP requests.
	cmd := exec.Command(binary, "mcp", "--direct")
	cmd.Dir = filepath.Dir(binary)
	cmd.Env = driverEnvironment(os.Environ())
	cmd.Stderr = io.Discard
	return &processTransport{command: cmd}, nil
}
