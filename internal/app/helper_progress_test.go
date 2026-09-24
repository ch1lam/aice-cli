package app

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/charmbracelet/x/ansi"
)

func TestHelperProgress(t *testing.T) {
	for _, name := range []string{"terminal", "redirected"} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			p := newHelperProgressPrinter(&output)
			p.terminal = name == "terminal"
			for _, count := range []int64{0, 100} {
				if err := p.Report(deps.Progress{Helper: "agent-browser", Version: "0.37.1", Downloaded: count, Total: 100}); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			got := ansi.Strip(output.String())
			if strings.Count(got, "downloading agent-browser") != 1 {
				t.Fatal(got)
			}
			if p.terminal && !strings.Contains(got, "100%") {
				t.Fatal(got)
			}
			if !p.terminal && strings.ContainsAny(got, "\r%") {
				t.Fatal(got)
			}
		})
	}
}

func TestUnknownSizeProgressEndsBeforeInstallLog(t *testing.T) {
	var output bytes.Buffer
	p := newHelperProgressPrinter(&output)
	p.terminal = true
	if err := p.Report(deps.Progress{Helper: "agent-browser", Downloaded: 1024}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write([]byte("installed\n")); err != nil {
		t.Fatal(err)
	}
	if got := ansi.Strip(output.String()); !strings.Contains(got, "MiB\ninstalled\n") {
		t.Fatal(got)
	}
}

func TestDownloadProgressAnimatesAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		p := newDownloadProgressPrinter(&output)
		p.terminal = true
		defer p.Close()
		if err := p.Draw(80, 100); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()
		frames := strings.Split(ansi.Strip(output.String()), "\r")
		if len(frames) < 3 {
			t.Fatalf("no intermediate frames: %q", frames)
		}
		var percent int
		fields := strings.Fields(frames[len(frames)-1])
		if _, err := fmt.Sscanf(fields[len(fields)-1], "%d%%", &percent); err != nil {
			t.Fatal(err)
		}
		if percent <= 0 || percent >= 80 {
			t.Fatalf("no smooth transition: %d%%", percent)
		}
		// Frequent callbacks must not starve the animation or lose its latest target.
		for count := int64(81); count <= 90; count++ {
			if err := p.Draw(count, 100); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if !strings.HasSuffix(ansi.Strip(output.String()), "90%") {
			t.Fatal(output.String())
		}
		settled := output.String()
		time.Sleep(time.Second)
		synctest.Wait()
		if output.String() != settled {
			t.Fatal("settled animation keeps writing")
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
		closed := output.String()
		time.Sleep(time.Second)
		synctest.Wait()
		if output.String() != closed {
			t.Fatal("animation wrote after Close")
		}
		if strings.Contains(ansi.Strip(closed), "100%") {
			t.Fatal("partial download marked complete")
		}
	})
}

func TestHelperProgressFlushesBeforeNextDownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		p := newHelperProgressPrinter(&output)
		p.terminal = true
		defer p.Close()
		for _, event := range []deps.Progress{
			{Helper: "first", Downloaded: 40, Total: 100},
			{Helper: "second", Downloaded: 0, Total: 100},
			{Helper: "second", Downloaded: 100, Total: 100},
		} {
			if err := p.Report(event); err != nil {
				t.Fatal(err)
			}
		}
		got := ansi.Strip(output.String())
		if !strings.Contains(got, "40%\ndownloading second") || !strings.HasSuffix(got, "100%\n") {
			t.Fatal(got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if ansi.Strip(output.String()) != got {
			t.Fatal("completed download keeps writing")
		}
	})
}

type progressFailWriter struct {
	remaining int
	err       error
}

func (w *progressFailWriter) Write(data []byte) (int, error) {
	if w.remaining == 0 {
		return 0, w.err
	}
	w.remaining--
	return len(data), nil
}

func TestDownloadProgressReturnsAnimationWriteError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		want := errors.New("terminal closed")
		output := &progressFailWriter{remaining: 1, err: want}
		p := newDownloadProgressPrinter(output)
		p.terminal = true
		if err := p.Draw(50, 100); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if err := p.Draw(60, 100); !errors.Is(err, want) {
			t.Fatalf("Draw error = %v", err)
		}
		if err := p.Close(); !errors.Is(err, want) {
			t.Fatalf("Close error = %v", err)
		}
	})
}
