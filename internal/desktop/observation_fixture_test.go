package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

// Synthetic observation views and coordinate metadata belong to the test
// scenario, never to the production Run or Manager.
const (
	maxElements        = 200
	maxObservationText = 96 * 1024
)

type Discovery struct {
	Apps       []Application `json:"apps,omitempty"`
	Windows    []Window      `json:"windows"`
	Truncated  bool          `json:"truncated"`
	Diagnostic string        `json:"diagnostic,omitempty"`
}

type Application struct {
	Ref      string `json:"app_ref,omitempty"`
	Name     string `json:"name"`
	BundleID string `json:"bundle_id,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Running  bool   `json:"running"`
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
	capture                                  string
	width, height, sourceWidth, sourceHeight int
}

func (r *fixtureRun) bindObservation(ctx context.Context, targetRef string, target windowIdentity, screenshot bool, reply Reply) (Observation, error) {
	var wire struct {
		windowIdentity
		Snapshot     string          `json:"snapshot_id"`
		Capture      string          `json:"capture_id"`
		Elements     []Element       `json:"elements"`
		Complete     bool            `json:"elements_complete"`
		Degraded     bool            `json:"degraded"`
		Reason       string          `json:"degraded_reason"`
		Width        int             `json:"screenshot_width"`
		Height       int             `json:"screenshot_height"`
		FrameValid   *bool           `json:"screenshot_frame_valid"`
		CaptureError json.RawMessage `json:"screenshot_error"`
	}
	if err := json.Unmarshal(reply.Structured, &wire); err != nil || wire.windowIdentity != target {
		return Observation{}, errors.New("desktop: observation did not establish the exact requested window")
	}
	ref := "observation-" + rand.Text()
	result := Observation{Ref: ref, TargetRef: targetRef, Complete: wire.Complete && !reply.IsError, Degraded: wire.Degraded || reply.IsError, Diagnostic: boundedText(wire.Reason, 2048), Elements: []Element{}}
	// Linux's complete AT-SPI walk still projects only actionable nodes into
	// elements. Missing text cannot establish absence of a passive label.
	if r.manager.platform == "linux" {
		result.Complete = false
	}
	binding := observationBinding{target: target, targetRef: targetRef}
	textBytes := 0
	for _, element := range wire.Elements {
		textBytes += len(element.Token) + len(element.Role) + len(element.Label) + len(element.Value)
		if len(result.Elements) == maxElements || textBytes > maxObservationText {
			result.Truncated = true
			result.Complete = false
			break
		}
		// Keep only actual opaque tokens; never derive one from display indices.
		if wire.Snapshot == "" || len(element.Token) > 512 {
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
			// Linux 0.29.1 publishes this field only for capture failure. On its
			// admitted X11 route a capture ID binds the exact window drawable;
			// macOS still requires its explicit positive frame validation.
			frameValid := wire.FrameValid != nil && *wire.FrameValid
			if r.manager.platform == "linux" {
				frameValid = (wire.FrameValid == nil || *wire.FrameValid) && !reply.IsError && len(wire.CaptureError) == 0
			}
			if frameValid && wire.Capture != "" && wire.Width == width && wire.Height == height {
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
	return result, nil
}
