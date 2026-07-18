// Package app wires AICE's process-level dependencies and lifecycle.
package app

import (
	"context"
	"fmt"
	"io"
)

// Run starts the current minimal AICE application.
func Run(ctx context.Context, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start application: %w", err)
	}

	if _, err := fmt.Fprintln(output, "AICE Go foundation is ready."); err != nil {
		return fmt.Errorf("write startup message: %w", err)
	}

	return nil
}
