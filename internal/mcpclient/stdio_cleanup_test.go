package mcpclient

import (
	"io"
	"os"
	"testing"
	"time"
)

// Reproduce a Windows pipe Close waiting for a reader until the child exits.
// This fixture keeps the test deterministic on all native CI hosts.
type closeAfterEOFReader struct{ io.ReadCloser }

func (r closeAfterEOFReader) Close() error {
	_, _ = io.Copy(io.Discard, r.ReadCloser)
	return r.ReadCloser.Close()
}

func TestChildCleanupIncludesBlockedPipeClose(t *testing.T) {
	config := stdioConfig(t)
	config.Stdio.Env["AICE_MCP_TEST_MODE"] = "hang"
	_, child, err := openStdio(*config.Stdio, defaultMessageBytes, &receipts{})
	if err != nil {
		t.Fatal(err)
	}
	child.stdout = closeAfterEOFReader{child.stdout}
	done := make(chan error, 1)
	go func() { done <- child.Close() }()
	t.Cleanup(func() { _ = child.cmd.Process.Kill() })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pipe closure blocked child termination")
	}
	select {
	case <-child.done:
	default:
		t.Fatal("child not reaped")
	}
	if child.cmd.ProcessState == nil || child.cmd.ProcessState.Success() {
		t.Fatal("hung child was not terminated")
	}
	if err := child.Close(); err != nil {
		t.Fatal("repeated close changed outcome", err)
	}
}

func TestChildNormalCloseReapsSuccessfully(t *testing.T) {
	config := stdioConfig(t)
	config.Stdio.Env["GORACE"] = "atexit_sleep_ms=0"
	c, err := Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if c.StdioPID() != c.child.cmd.Process.Pid || c.StdioPID() == os.Getpid() {
		t.Fatal("wrong owned child identity")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.child.done:
	default:
		t.Fatal("normal child was not reaped")
	}
	if !c.child.cmd.ProcessState.Success() {
		t.Fatal("normal EOF shutdown killed the child")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
