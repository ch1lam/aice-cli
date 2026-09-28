package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestCallCheckedRunsInsideConnectionQueue(t *testing.T) {
	fixture := &httpFixture{}
	client := openFixture(t, fixture, nil)
	denied := errors.New("test permission revoked")
	result, err := client.CallChecked(t.Context(), "echo", json.RawMessage(`{}`), func(context.Context) error {
		// The queue token is already held at the revalidation boundary. A
		// check before acquire would allow this send and fail the assertion.
		select {
		case client.gate <- struct{}{}:
			<-client.gate
			t.Error("dispatch check ran outside the serialized operation")
		default:
		}
		return denied
	})
	if !errors.Is(err, denied) || result.State != llm.ExecutionNotDispatched || fixture.calls.Load() != 0 {
		t.Fatalf("denied dispatch = %+v, %v, calls=%d", result, err, fixture.calls.Load())
	}
	// A refused callback releases the queue; it neither poisons the connection
	// nor implicitly retries the denied operation.
	result, err = client.CallChecked(t.Context(), "echo", json.RawMessage(`{}`), func(context.Context) error { return nil })
	if err != nil || result.State != llm.ExecutionReturned || fixture.calls.Load() != 1 {
		t.Fatalf("next explicit dispatch = %+v, %v", result, err)
	}
}

func TestReadResourceCheckedDoesNotDispatchAfterQueuedRefusal(t *testing.T) {
	var reads atomic.Int32
	fixture := &httpFixture{result: func(req fixtureRequest) string {
		if req.Method == "resources/read" {
			reads.Add(1)
		}
		return fixtureResult(req)
	}}
	client := openFixture(t, fixture, nil)
	denied := errors.New("permission revoked")
	result, err := client.ReadResourceChecked(t.Context(), "fixture://one", func(context.Context) error {
		select {
		case client.gate <- struct{}{}:
			<-client.gate
			t.Error("resource check ran outside queue")
		default:
		}
		return denied
	})
	if !errors.Is(err, denied) || result.State != llm.ExecutionNotDispatched || reads.Load() != 0 {
		t.Fatal("read dispatched after refusal", result, err, reads.Load())
	}
	result, err = client.ReadResourceChecked(t.Context(), "fixture://one", func(context.Context) error { return nil })
	if err != nil || result.State != llm.ExecutionReturned || reads.Load() != 1 {
		t.Fatal("explicit read failed", result, err, reads.Load())
	}
}
