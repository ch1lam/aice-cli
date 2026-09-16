package app

import (
	"context"

	"github.com/ch1lam/aice-cli/internal/deps"
)

// Default application fixtures never download helpers. Tests of provisioning
// inject an observer or use deps' isolated HTTP fixtures instead.
func skipTestHelperDownloads(context.Context, deps.Options) error { return nil }
