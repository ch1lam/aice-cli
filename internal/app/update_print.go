package app

import (
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/bubbles/v2/progress"

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

// updateProgressPrinter owns one command's transient progress line. ViewAs
// renders actual byte counts directly, without a second event loop or timer.
type updateProgressPrinter struct {
	output   io.Writer
	bar      progress.Model
	terminal bool
	active   bool
	lastDraw time.Time
	started  bool
}

func newUpdateProgressPrinter(output io.Writer) *updateProgressPrinter {
	terminal := false
	if file, ok := output.(*os.File); ok && os.Getenv("TERM") != "dumb" {
		if info, err := file.Stat(); err == nil {
			terminal = info.Mode()&os.ModeCharDevice != 0
		}
	}
	return &updateProgressPrinter{output: output, terminal: terminal, bar: progress.New(progress.WithWidth(32))}
}

func (p *updateProgressPrinter) Close() error {
	if !p.active {
		return nil
	}
	p.active = false
	_, err := fmt.Fprintln(p.output)
	return err
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
	// Redirected output remains a compact log, without cursor controls or bars.
	if !p.terminal {
		return nil
	}
	complete := event.Total > 0 && event.Downloaded >= event.Total
	if p.active && !complete && time.Since(p.lastDraw) < 80*time.Millisecond {
		return nil
	}
	p.lastDraw = time.Now()
	p.active = true
	if event.Total <= 0 {
		_, err := fmt.Fprintf(p.output, "\r\x1b[2Kdownloaded %.1f MiB", float64(event.Downloaded)/(1<<20))
		return err
	}
	fraction := min(1.0, float64(event.Downloaded)/float64(event.Total))
	_, err := fmt.Fprintf(p.output, "\r\x1b[2K%s", p.bar.ViewAs(fraction))
	return err
}
