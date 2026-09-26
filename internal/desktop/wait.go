package desktop

import (
	"context"
	"errors"
	"strings"
	"time"
)

// WaitCondition asks for visible semantic text, not a generic visual-stability
// heuristic. A missing match in an incomplete projection remains unknown.
type WaitCondition struct {
	Text      string `json:"text"`
	TimeoutMS int    `json:"timeout_ms"`
}

func (r *Run) waitLocked(ctx context.Context, binding observationBinding, request ActRequest) (ActResult, error) {
	condition := request.Wait
	if condition == nil || strings.TrimSpace(condition.Text) == "" || len(condition.Text) > 256 || condition.TimeoutMS < 1 || condition.TimeoutMS > 10000 {
		return ActResult{}, errors.New("desktop: wait requires semantic text of 1..256 bytes and timeout_ms of 1..10000")
	}
	if request.Point != nil || request.ElementToken != "" || request.Text != "" || request.Key != "" || len(request.Keys) != 0 || request.Direction != "" || request.Amount != 0 {
		return ActResult{}, errors.New("desktop: wait contains unrelated action fields")
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(condition.TimeoutMS)*time.Millisecond)
	defer cancel()
	result := ActResult{Outcome: "returned", WaitState: "unknown"}
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if waitCtx.Err() != nil {
			break
		}
		observation, err := r.observeLocked(waitCtx, ObserveRequest{TargetRef: binding.targetRef, Query: condition.Text})
		if err != nil {
			result.Observation = nil // a failed refresh invalidates older refs too
			result.WaitState = "unknown"
			result.ObservationError = "Condition could not be checked; observe the target before continuing"
			return result, nil
		}
		result.Observation = &observation
		result.WaitState = semanticCondition(observation, condition.Text)
		if result.WaitState == "satisfied" {
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if request.Screenshot {
		if waitCtx.Err() != nil {
			result.Diagnostic = "Wait deadline reached; final state is semantic only"
			return result, nil
		}
		// Capture once at completion, then re-evaluate using that exact final
		// observation. No screenshot is taken on every polling tick.
		observation, err := r.observeLocked(waitCtx, ObserveRequest{TargetRef: binding.targetRef, Screenshot: true, Query: condition.Text})
		if err != nil {
			result.Observation = nil // the attempted refresh invalidated its refs
			result.WaitState = "unknown"
			result.ObservationError = "Final observation unavailable; the earlier condition may have changed"
			return result, nil
		}
		result.Observation = &observation
		result.WaitState = semanticCondition(observation, condition.Text)
	}
	return result, nil
}

func semanticCondition(observation Observation, text string) string {
	for _, element := range observation.Elements {
		if strings.Contains(element.Label, text) || strings.Contains(element.Value, text) {
			return "satisfied"
		}
	}
	if observation.Complete && !observation.Degraded && !observation.Truncated {
		return "unsatisfied"
	}
	return "unknown"
}
