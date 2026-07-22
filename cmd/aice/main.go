package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/ch1lam/aice-cli/internal/app"
	"github.com/ch1lam/aice-cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, output, errorOutput io.Writer) int {
	command, err := app.NewCommand()
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, "aice:", err)
		return cli.ExitCode(err)
	}

	command.SetArgs(args)
	command.SetOut(output)
	command.SetErr(errorOutput)
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(errorOutput, "aice:", err)
		return cli.ExitCode(err)
	}
	return 0
}
