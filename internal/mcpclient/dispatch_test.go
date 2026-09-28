package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
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
