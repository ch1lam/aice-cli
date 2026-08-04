//go:build !windows

package deps

import (
	"context"
	"fmt"
)

// installGitBash never runs on non-Windows hosts; it exists so Ensure compiles
// uniformly across platforms.
func installGitBash(_ context.Context, _ Options) error {
	return fmt.Errorf("Git Bash is only available on Windows")
}
