package deps

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestCuaVerificationCommandBoundsCopiedOutput(t *testing.T) {
	t.Parallel()
	_, err := cuaVerifyCommand(t.Context(), os.Args[0], "-test.run=^TestCuaVerificationOutputHelper$", "cua-output-overflow")
	if err == nil || !strings.Contains(err.Error(), "output exceeds limit") {
		t.Fatal("verification output not rejected by bound", err)
	}
}

func TestCuaVerificationOutputHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "cua-output-overflow" {
		return
	}
	_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 16*1024))
	os.Exit(0)
}
