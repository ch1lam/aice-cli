package mcpclient

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Only these host variables are inherited. Explicit configured values remain
// the caller's responsibility; provider credentials are never swept into Env.
var baseEnvironment = []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"}

type childProcess struct {
	cmd    *exec.Cmd
	stdin  *ownedPipe
	stdout *ownedPipe
	done   chan struct{}
	once   sync.Once
	err    error
}

type ownedPipe struct {
	*os.File
	once sync.Once
	err  error
}

func (p *ownedPipe) Close() error {
	p.once.Do(func() { p.err = p.File.Close() })
	return p.err
}

func openStdio(config StdioConfig, limit int, receipts *receipts) (mcp.Transport, *childProcess, error) {
	if !filepath.IsAbs(config.Executable) || !filepath.IsAbs(config.Dir) {
		return nil, nil, ErrConfig
	}
	env := make(map[string]string)
	for _, key := range baseEnvironment {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	for key, value := range config.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, nil, ErrConfig
		}
		env[key] = value
	}
	cmd := exec.Command(config.Executable, config.Args...)
	cmd.Dir = config.Dir
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	// Own the pipe endpoints directly: Wait must not wait for a descendant that
	// inherited stdout. There are no os/exec output-copy goroutines to block it.
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, ErrTransport
	}
	defer stdinRead.Close()
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		_ = stdinWrite.Close()
		return nil, nil, ErrTransport
	}
	defer stdoutWrite.Close()
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = stdinWrite.Close()
		_ = stdoutRead.Close()
		return nil, nil, ErrTransport
	}
	defer stderr.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinRead, stdoutWrite, stderr
	if err := cmd.Start(); err != nil {
		_ = stdinWrite.Close()
		_ = stdoutRead.Close()
		return nil, nil, ErrTransport
	}
	child := &childProcess{cmd: cmd, stdin: &ownedPipe{File: stdinWrite}, stdout: &ownedPipe{File: stdoutRead}, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(child.done)
	}()
	return &mcp.IOTransport{
		Reader: newFrameReader(child.stdout, limit, false, false, receipts),
		Writer: &observedWriter{WriteCloser: child.stdin, receipts: receipts},
	}, child, nil
}

func (p *childProcess) Close() error {
	p.once.Do(func() {
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-p.done:
			return
		case <-timer.C:
		}
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			p.err = ErrTransport
		}
		timer.Reset(2 * time.Second)
		select {
		case <-p.done:
		case <-timer.C:
			p.err = ErrTransport
		}
	})
	return p.err
}
