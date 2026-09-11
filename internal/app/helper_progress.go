package app

import (
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/bubbles/v2/progress"
	"github.com/ch1lam/aice-cli/internal/deps"
)

// downloadProgressPrinter owns one transient terminal line, without a timer loop.
type downloadProgressPrinter struct {
	output           io.Writer
	bar              progress.Model
	terminal, active bool
	lastDraw         time.Time
}

func newDownloadProgressPrinter(output io.Writer) *downloadProgressPrinter {
	terminal := false
	if file, ok := output.(*os.File); ok && os.Getenv("TERM") != "dumb" {
		if info, err := file.Stat(); err == nil {
			terminal = info.Mode()&os.ModeCharDevice != 0
		}
	}
	return &downloadProgressPrinter{output: output, terminal: terminal, bar: progress.New(progress.WithWidth(32))}
}
func (p *downloadProgressPrinter) Close() error {
	if !p.active {
		return nil
	}
	p.active = false
	_, err := fmt.Fprintln(p.output)
	return err
}
func (p *downloadProgressPrinter) Draw(downloaded, total int64) error {
	// Redirected output remains a compact log, without cursor controls or bars.
	if !p.terminal {
		return nil
	}
	complete := total > 0 && downloaded >= total
	if p.active && !complete && time.Since(p.lastDraw) < 80*time.Millisecond {
		return nil
	}
	p.lastDraw = time.Now()
	p.active = true
	if total <= 0 {
		_, err := fmt.Fprintf(p.output, "\r\x1b[2Kdownloaded %.1f MiB", float64(downloaded)/(1<<20))
		return err
	}
	fraction := min(1.0, float64(downloaded)/float64(total))
	_, err := fmt.Fprintf(p.output, "\r\x1b[2K%s", p.bar.ViewAs(fraction))
	return err
}

type helperProgressPrinter struct {
	*downloadProgressPrinter
	helper string
}

func newHelperProgressPrinter(output io.Writer) *helperProgressPrinter {
	return &helperProgressPrinter{downloadProgressPrinter: newDownloadProgressPrinter(output)}
}
func (p *helperProgressPrinter) Report(event deps.Progress) error {
	if p.helper != event.Helper {
		if err := p.Close(); err != nil {
			return err
		}
		p.helper = event.Helper
		if _, err := fmt.Fprintf(p.output, "downloading %s %s ...\n", event.Helper, event.Version); err != nil {
			return err
		}
	}
	if err := p.Draw(event.Downloaded, event.Total); err != nil {
		return err
	}
	if event.Total > 0 && event.Downloaded >= event.Total {
		return p.Close()
	}
	return nil
}
