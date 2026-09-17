package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const wslProbeTimeout = 5 * time.Second

func newWSLBash(ctx context.Context, workspace *Workspace, candidates []string, nativeErr error) (*Bash, error) {
	probeCtx, cancel := context.WithTimeout(ctx, wslProbeTimeout)
	defer cancel()
	errs := []error{nativeErr}
	for _, shell := range candidates {
		if err := probeCtx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := probeWSLBash(probeCtx, shell, workspace.Path()); err != nil {
			errs = append(errs, fmt.Errorf("WSL Bash %q unavailable: %w", shell, err))
			continue
		}
		return &Bash{workspace: workspace, shellPath: shell, wsl: true}, nil
	}
	return nil, fmt.Errorf("tool: find usable bash executable: %w", errors.Join(errs...))
}

// A WSL launcher can exist without a working distribution. Probe the same stdin
// transport used for commands, including access to the Windows workspace.
func probeWSLBash(ctx context.Context, shell, cwd string) error {
	const marker = "aice-wsl-bash-ready"
	script := "test -n \"$BASH_VERSION\" || exit 1\n" +
		"command -v bash >/dev/null || exit 1\n" +
		wslWorkingDirectory(cwd) + "printf '" + marker + "'\n"
	command := exec.CommandContext(ctx, shell, "-s")
	command.Dir = cwd
	command.Stdin = strings.NewReader(script)
	output := newBoundedWriter(1024)
	command.Stdout, command.Stderr = output, output
	configureProcess(command)
	cleanup, err := startProcessTree(command)
	if err != nil {
		return err
	}
	err = command.Wait()
	cleanup()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("probe: %w: %s", err, output.String())
	}
	if strings.TrimSpace(output.String()) != marker {
		return fmt.Errorf("probe did not confirm Bash and workspace access")
	}
	return nil
}

func quoteBash(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func wslWorkingDirectory(cwd string) string {
	return "aice_workspace=$(wslpath -u " + quoteBash(cwd) + ") || exit 1\n" +
		"cd -- \"$aice_workspace\" || exit 1\n"
}

// runWSLCommand keeps stdin open as a lifetime channel. Closing it on cancellation
// lets a Linux-side watcher kill the command's process group; killing only the
// Windows launcher can leave Linux descendants alive. The host job is a bounded
// fallback for a stuck launcher. No distribution-wide shutdown is performed.
func runWSLCommand(ctx context.Context, shell, cwd, script string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer input.Close()
	defer writer.Close()
	// Cancellation first closes the lifetime channel, allowing Linux cleanup;
	// the callback below terminates the host process tree after a short grace.
	command := exec.CommandContext(context.WithoutCancel(ctx), shell, "-s")
	command.Dir, command.Stdin = cwd, input
	command.Stdout, command.Stderr = output, output
	configureProcess(command)
	cleanup, err := startProcessTree(command)
	if err != nil {
		return err
	}
	defer cleanup()

	finished := make(chan struct{})
	cancelled := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(cancelled)
		_ = writer.Close()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-finished:
		case <-timer.C:
			_ = command.Cancel()
		}
	})
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, wslCommandScript(script))
		written <- err
	}()
	runErr := command.Wait()
	close(finished)
	_ = writer.Close()
	if !stopCancel() {
		<-cancelled
	}
	writeErr := <-written
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if runErr != nil {
		return runErr
	}
	return writeErr
}

func wslCommandScript(script string) string {
	// Parse the whole compound command before executing it, leaving stdin for
	// the watcher. Job control gives the user command its own process group.
	return `{
exec 9<&0
set -m
bash --noprofile --norc -c ` + quoteBash("exec 9<&-\n"+script) + ` </dev/null &
aice_command_pid=$!
(
  IFS= read -r -u 9 aice_lifetime
  kill -KILL -- "-$aice_command_pid" 2>/dev/null
) </dev/null >/dev/null 2>&1 &
aice_watch_pid=$!
wait "$aice_command_pid"
aice_status=$?
kill -KILL -- "-$aice_command_pid" 2>/dev/null
kill "$aice_watch_pid" 2>/dev/null
wait "$aice_watch_pid" 2>/dev/null
exit "$aice_status"
}
`
}
