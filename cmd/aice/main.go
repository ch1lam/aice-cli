package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ch1lam/aice-cli/internal/app"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := app.Run(ctx, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "aice:", err)
		return 1
	}

	return 0
}
