package anthropic

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/apitest"
)

func TestOrderedStructuredToolResultProjection(t *testing.T) {
	t.Parallel()
	model, messages := apitest.ToolResultHistory(API)
	converted, err := messageParams(messages, model)
	if err != nil {
		t.Fatal(err)
	}
	apitest.CheckToolResultProjection(t, converted, messages)
}
