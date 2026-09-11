package app

import (
	"fmt"
	"io"

	"github.com/ch1lam/aice-cli/internal/update"
)

func printUpdateCheck(output io.Writer, result update.CheckResult) error {
	if result.Available {
		_, err := fmt.Fprintf(
			output,
			"update available: %s -> %s (run `aice update`)\n",
			result.Current,
			result.Latest,
		)
		return err
	}
	if result.Comparable {
		_, err := fmt.Fprintf(output, "aice is up to date (%s)\n", result.Current)
		return err
	}
	// The current version cannot be compared (for example a dev build), so the
	// latest release is reported without claiming the install is current.
	_, err := fmt.Fprintf(output, "latest release is %s (installed: %s; run `aice update --force` to replace this build)\n", result.Latest, result.Current)
	return err
}

func printUpdateResult(output io.Writer, result update.UpdateResult) error {
	if !result.Updated {
		_, err := fmt.Fprintf(output, "aice is up to date (%s)\n", result.Current)
		return err
	}
	if _, err := fmt.Fprintf(
		output,
		"updated aice %s -> %s\n",
		result.Current,
		result.Latest,
	); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "restart aice to use the new version\n")
	return err
}

// updateProgressPrinter adds update-specific messages to the shared byte display.
type updateProgressPrinter struct {
	*downloadProgressPrinter
	started bool
}

func newUpdateProgressPrinter(output io.Writer) *updateProgressPrinter {
	return &updateProgressPrinter{downloadProgressPrinter: newDownloadProgressPrinter(output)}
}

func (p *updateProgressPrinter) Report(event update.Progress) error {
	if event.Latest == "" {
		_, err := fmt.Fprintln(p.output, "checking for the latest AICE release...")
		return err
	}
	if event.Verifying {
		if err := p.Close(); err != nil {
			return err
		}
		_, err := fmt.Fprintln(p.output, "verifying checksum and installing...")
		return err
	}
	if !p.started {
		p.started = true
		if _, err := fmt.Fprintf(p.output, "downloading AICE %s (installed: %s)...\n", event.Latest, event.Current); err != nil {
			return err
		}
	}
	return p.Draw(event.Downloaded, event.Total)
}
