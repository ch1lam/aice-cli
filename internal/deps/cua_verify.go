package deps

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func cuaVerifyCommand(ctx context.Context, binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	cmd.Dir = filepath.Dir(binary)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "CUA_DRIVER_RS_TELEMETRY_ENABLED=false", "CUA_DRIVER_RS_UPDATE_CHECK=false"}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "TMPDIR", "SystemRoot", "SystemDrive", "USERPROFILE", "LOCALAPPDATA", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "PATH="+filepath.Join(os.Getenv("SystemRoot"), "System32"))
	}
	var output cuaVerifyOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", errors.Join(ctx.Err(), err)
	}
	return output.String(), nil
}

type cuaVerifyOutput struct{ buffer bytes.Buffer }

func (w *cuaVerifyOutput) String() string { return w.buffer.String() }

func (w *cuaVerifyOutput) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > 8192 {
		return 0, errors.New("Cua verification output exceeds limit")
	}
	return w.buffer.Write(p)
}
