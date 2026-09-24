package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/tui"
)

// downloadProgressPrinter owns one transient line and its animation worker.
// Callers serialize Draw/Close; mu protects state and output shared with animate.
// Close stops and joins the worker before any subsequent stage can write output.
type downloadProgressPrinter struct {
	output           io.Writer
	terminal, active bool
	lastDraw         time.Time
	stop, done, wake chan struct{}

	mu        sync.Mutex
	bar       progress.Model
	fraction  float64
	knownSize bool
	err       error
}

func newDownloadProgressPrinter(output io.Writer) *downloadProgressPrinter {
	terminal := false
	if file, ok := output.(*os.File); ok && os.Getenv("TERM") != "dumb" {
		if info, err := file.Stat(); err == nil {
			terminal = info.Mode()&os.ModeCharDevice != 0
		}
	}
	return &downloadProgressPrinter{output: output, terminal: terminal, bar: tui.NewDownloadProgress(32)}
}

func (p *downloadProgressPrinter) Close() error {
	p.stopAnimation()
	if !p.active {
		return p.err
	}
	p.active = false
	if p.knownSize && p.err == nil {
		p.render(p.bar.ViewAs(p.fraction))
	}
	_, err := fmt.Fprintln(p.output)
	p.err = errors.Join(p.err, err)
	p.bar = tui.NewDownloadProgress(32)
	return p.err
}

func (p *downloadProgressPrinter) Draw(downloaded, total int64) error {
	// Redirected output remains a compact log, without cursor controls or bars.
	if !p.terminal {
		return nil
	}
	complete := total > 0 && downloaded >= total
	if total <= 0 || complete {
		p.stopAnimation()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.knownSize = total > 0
	if total <= 0 {
		if p.active && time.Since(p.lastDraw) < 80*time.Millisecond {
			return nil
		}
		p.lastDraw = time.Now()
		p.active = true
		p.render(fmt.Sprintf("downloaded %.1f MiB", float64(downloaded)/(1<<20)))
		return p.err
	}
	p.fraction = max(0.0, min(1.0, float64(downloaded)/float64(total)))
	if complete {
		p.render(p.bar.ViewAs(p.fraction))
	} else if !p.active {
		p.render(p.bar.View())
	}
	p.active = true
	if complete || p.err != nil {
		return p.err
	}
	if p.stop == nil {
		p.stop, p.done, p.wake = make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
		go p.animate()
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return nil
}

func (p *downloadProgressPrinter) stopAnimation() {
	if p.stop == nil {
		return
	}
	close(p.stop)
	<-p.done
	p.stop, p.done, p.wake = nil, nil, nil
}

func (p *downloadProgressPrinter) animate() {
	defer close(p.done)
	var command tea.Cmd
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		p.mu.Lock()
		if p.fraction != p.bar.Percent() {
			command = p.bar.SetPercent(p.fraction)
		}
		p.mu.Unlock()
		if command == nil {
			select {
			case <-p.stop:
				return
			case <-p.wake:
				continue
			}
		}
		// A Bubbles frame command waits one animation frame (1/60 second).
		// No lock is held while it waits; download callbacks only set a target.
		message := command()
		p.mu.Lock()
		p.bar, command = p.bar.Update(message)
		p.render(p.bar.View())
		failed := p.err != nil
		p.mu.Unlock()
		if failed {
			return
		}
	}
}

// render runs under mu, or after the animation worker has been joined.
func (p *downloadProgressPrinter) render(view string) {
	_, p.err = fmt.Fprintf(p.output, "\r\x1b[2K%s", view)
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

func (p *helperProgressPrinter) Write(data []byte) (int, error) {
	if err := p.Close(); err != nil {
		return 0, err
	}
	return p.output.Write(data)
}
