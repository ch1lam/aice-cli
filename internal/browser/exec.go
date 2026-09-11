package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (m *Manager) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	if _, err := m.Executable(); err != nil {
		return nil, err
	}
	env := m.Environment()
	// Close must not reconnect to an unavailable CDP endpoint before disconnecting.
	if len(args) > 0 && args[0] == "close" {
		env["AGENT_BROWSER_CDP"] = ""
		env["AGENT_BROWSER_AUTO_CONNECT"] = "false"
	}
	inherited := make([]string, 0, len(os.Environ())+len(env))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, owned := env[key]; !owned {
			inherited = append(inherited, entry)
		}
	}
	for key, value := range env {
		if value != "" {
			inherited = append(inherited, key+"="+value)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return m.exec(ctx, append([]string{"--session", name}, args...), inherited)
}

func (m *Manager) execute(ctx context.Context, args, env []string) ([]byte, error) {
	path, err := m.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = m.workspace
	cmd.Env = env
	cmd.WaitDelay = time.Second
	var stdout, stderr limitedBuffer
	stdout.limit = 1 << 20
	stderr.limit = 4096
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("agent-browser: %w: %s%s", errors.Join(err, ctx.Err()), stderr.String(), stdout.String())
	}
	if stdout.exceeded {
		return nil, fmt.Errorf("agent-browser response exceeds 1 MiB")
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := max(0, b.limit-b.Len())
	if n > remaining {
		b.exceeded = true
		p = p[:remaining]
	}
	_, err := b.Buffer.Write(p)
	return n, err
}
