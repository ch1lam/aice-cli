package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

const (
	maxTargets         = 64
	maxElements        = 200
	maxObservationText = 96 * 1024
)

type windowIdentity struct {
	PID      int    `json:"pid"`
	WindowID uint64 `json:"window_id"`
}

type Window struct {
	Ref      string `json:"target_ref"`
	PID      int    `json:"pid"`
	WindowID uint64 `json:"window_id"`
	App      string `json:"app"`
	Title    string `json:"title"`
}

type Discovery struct {
	Apps       []Application `json:"apps,omitempty"`
	Windows    []Window      `json:"windows"`
	Truncated  bool          `json:"truncated"`
	Diagnostic string        `json:"diagnostic,omitempty"`
}

// Windows returns only window metadata, never AX contents or screenshots.
// References come from native results and are scoped to this run/generation.
func (r *Run) Windows(ctx context.Context, query string, limit int) (Discovery, error) {
	if len(query) > 256 || limit < 1 || limit > maxTargets {
		return Discovery{}, errors.New("desktop: window query must be <=256 bytes and limit 1..64")
	}
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return Discovery{}, err
	}
	defer release()
	return r.windowsLocked(ctx, query, limit, nil)
}

func (r *Run) windowsLocked(ctx context.Context, query string, limit int, matchingPIDs map[int]bool) (Discovery, error) {
	if err := r.ensureLocked(ctx); err != nil {
		return Discovery{}, err
	}
	reply, err := r.callLocked(ctx, "list_windows", map[string]any{})
	if err != nil {
		return Discovery{}, err
	}
	if reply.IsError {
		return Discovery{}, errors.New("desktop: window discovery unavailable")
	}
	var wire struct {
		Windows []struct {
			windowIdentity
			App   string `json:"app_name"`
			Title string `json:"title"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(reply.Structured, &wire); err != nil || wire.Windows == nil {
		return Discovery{}, errors.New("desktop: invalid window discovery response")
	}
	// Refresh replaces this run's discovery. It never silently rebinds an old
	// reference to a process/window whose numeric identity might have been reused.
	clear(r.targets)
	r.clearObservationsLocked()
	result := Discovery{Windows: []Window{}}
	query = strings.ToLower(strings.TrimSpace(query))
	for _, window := range wire.Windows {
		matches := (query == "" && len(matchingPIDs) == 0) || matchingPIDs[window.PID] || (query != "" && strings.Contains(strings.ToLower(window.App+" "+window.Title), query))
		if window.PID <= 0 || window.WindowID == 0 || !matches {
			continue
		}
		if len(result.Windows) == limit {
			result.Truncated = true
			break
		}
		ref := "window-" + rand.Text()
		r.targets[ref] = window.windowIdentity
		result.Windows = append(result.Windows, Window{Ref: ref, PID: window.PID, WindowID: window.WindowID, App: boundedText(window.App, 256), Title: boundedText(window.Title, 1024)})
	}
	return result, nil
}

type ObserveRequest struct {
	TargetRef  string `json:"target_ref"`
	Screenshot bool   `json:"screenshot"`
	Query      string `json:"query,omitempty"`
}

type Element struct {
	Token    string `json:"element_token,omitempty"`
	Role     string `json:"role"`
	Label    string `json:"label,omitempty"`
	Value    string `json:"value,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Selected *bool  `json:"selected,omitempty"`
}

type Observation struct {
	Ref         string            `json:"observation_ref"`
	TargetRef   string            `json:"target_ref"`
	Elements    []Element         `json:"elements"`
	Complete    bool              `json:"elements_complete"`
	Truncated   bool              `json:"projection_truncated"`
	Degraded    bool              `json:"degraded"`
	Diagnostic  string            `json:"diagnostic,omitempty"`
	ImageWidth  int               `json:"image_width,omitempty"`
	ImageHeight int               `json:"image_height,omitempty"`
	Image       *llm.ImageContent `json:"-"`
}

type observationBinding struct {
	target                                   windowIdentity
	targetRef                                string
	generation                               uint64
	snapshot, capture                        string
	tokens                                   map[string]struct{}
	width, height, sourceWidth, sourceHeight int
}

func (r *Run) Observe(ctx context.Context, request ObserveRequest) (Observation, error) {
	if len(request.Query) > 256 {
		return Observation{}, errors.New("desktop: observation query exceeds 256 bytes")
	}
	if request.Screenshot && !r.options.Images {
		return Observation{}, errors.New("desktop: current model does not accept images; request a semantic observation")
	}
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return Observation{}, err
	}
	defer release()
	return r.observeLocked(ctx, request)
}

func (r *Run) observeLocked(ctx context.Context, request ObserveRequest) (Observation, error) {
	target, exists := r.targets[request.TargetRef]
	if !exists {
		return Observation{}, errors.New("desktop: stale target; discover windows again")
	}
	if err := r.ensureLocked(ctx); err != nil {
		return Observation{}, err
	}
	// Invalidate before capture: even a partial/failed native observation may
	// have replaced the Driver's per-window snapshot.
	delete(r.manager.latest, target)
	for ref, previous := range r.observations {
		if previous.target == target {
			delete(r.observations, ref)
		}
	}
	reply, err := r.callLocked(ctx, "get_window_state", map[string]any{
		"session": r.id, "pid": target.PID, "window_id": target.WindowID,
		"include_screenshot": request.Screenshot, "include_accessibility_tree": true,
		"max_elements": maxElements, "max_depth": 15, "max_image_dimension": 1600,
		"timeout_ms": 5000, "query": request.Query,
	})
	if err != nil {
		if request.Screenshot {
			r.manager.recordCapture(false)
		}
		return Observation{}, err
	}
	result, err := r.bindObservation(ctx, request.TargetRef, target, request.Screenshot, reply)
	if request.Screenshot {
		r.manager.recordCapture(err == nil && result.Image != nil)
	}
	return result, err
}

func (r *Run) bindObservation(ctx context.Context, targetRef string, target windowIdentity, screenshot bool, reply Reply) (Observation, error) {
	var wire struct {
		windowIdentity
		Snapshot   string    `json:"snapshot_id"`
		Capture    string    `json:"capture_id"`
		Elements   []Element `json:"elements"`
		Complete   bool      `json:"elements_complete"`
		Degraded   bool      `json:"degraded"`
		Reason     string    `json:"degraded_reason"`
		Width      int       `json:"screenshot_width"`
		Height     int       `json:"screenshot_height"`
		FrameValid bool      `json:"screenshot_frame_valid"`
	}
	if err := json.Unmarshal(reply.Structured, &wire); err != nil || wire.windowIdentity != target {
		return Observation{}, errors.New("desktop: observation did not establish the exact requested window")
	}
	ref := "observation-" + rand.Text()
	result := Observation{Ref: ref, TargetRef: targetRef, Complete: wire.Complete && !reply.IsError, Degraded: wire.Degraded || reply.IsError, Diagnostic: boundedText(wire.Reason, 2048), Elements: []Element{}}
	binding := observationBinding{target: target, targetRef: targetRef, generation: r.manager.Status().Generation, snapshot: wire.Snapshot, tokens: make(map[string]struct{})}
	textBytes := 0
	for _, element := range wire.Elements {
		textBytes += len(element.Token) + len(element.Role) + len(element.Label) + len(element.Value)
		if len(result.Elements) == maxElements || textBytes > maxObservationText {
			result.Truncated = true
			result.Complete = false
			break
		}
		// Keep only actual opaque tokens; never derive one from display indices.
		if wire.Snapshot != "" && element.Token != "" && len(element.Token) <= 512 {
			binding.tokens[element.Token] = struct{}{}
		} else {
			element.Token = ""
		}
		result.Elements = append(result.Elements, element)
	}
	if len(reply.Images) > 1 {
		return Observation{}, errors.New("desktop: observation exceeds one-image result budget")
	}
	if screenshot && len(reply.Images) == 0 {
		result.Degraded = true
		result.Diagnostic = "Screenshot was requested but unavailable; semantic references may still be used"
	}
	if len(reply.Images) == 1 && r.options.Images && screenshot {
		part := reply.Images[0]
		prepared, err := media.Prepare(ctx, llm.ImageContent{Data: part.Data, MIMEType: part.MIMEType, Source: "Computer Use window observation"}, nil)
		if err != nil {
			result.Degraded = true
			result.Diagnostic = "Screenshot unavailable: invalid image; semantic references may still be used"
		} else {
			result.Image, result.ImageWidth, result.ImageHeight = &prepared, prepared.Width, prepared.Height
			width, height := prepared.Width, prepared.Height
			if prepared.Original != nil {
				width, height = prepared.Original.Width, prepared.Original.Height
			}
			if wire.FrameValid && wire.Capture != "" && wire.Width == width && wire.Height == height {
				binding.capture = wire.Capture
				binding.width, binding.height = prepared.Width, prepared.Height
				binding.sourceWidth, binding.sourceHeight = width, height
			} else {
				result.Degraded = true
				result.Diagnostic = "Image has no verified capture mapping; use semantic references only"
			}
		}
	}
	if reply.IsError && result.Diagnostic == "" {
		result.Diagnostic = "Driver returned a partial observation"
	}
	r.observations[ref] = binding
	r.manager.latest[target] = ref
	return result, nil
}

func (r *Run) observationLocked(ref string) (observationBinding, error) {
	binding, ok := r.observations[ref]
	if !ok || r.closed.Load() || r.ctx.Err() != nil || !r.manager.Status().Connected || binding.generation != r.manager.Status().Generation || r.manager.latest[binding.target] != ref {
		return observationBinding{}, errors.New("desktop: stale observation; observe the target again before acting")
	}
	return binding, nil
}

func (r *Run) clearObservationsLocked() {
	for ref, binding := range r.observations {
		if r.manager.latest[binding.target] == ref {
			delete(r.manager.latest, binding.target)
		}
	}
	clear(r.observations)
}

// pixel reverses only AICE's image transformation. Cua reverses its own
// screenshot downscale/Retina conversion; window/screen offsets are never added.
func (b observationBinding) pixel(x, y float64) (float64, float64, error) {
	if b.capture == "" || b.width <= 0 || b.height <= 0 || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || x < 0 || y < 0 || x >= float64(b.width) || y >= float64(b.height) {
		return 0, 0, fmt.Errorf("desktop: pixel must be inside a current, bound screenshot")
	}
	return x * float64(b.sourceWidth) / float64(b.width), y * float64(b.sourceHeight) / float64(b.height), nil
}

func boundedText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "") + "…"
}
