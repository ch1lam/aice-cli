package openairesponses

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/apitest"
)

func TestOrderedStructuredToolResultProjection(t *testing.T) {
	t.Parallel()
	model, messages := apitest.ToolResultHistory(API)
	converted, err := inputParams(messages, model, false)
	if err != nil {
		t.Fatal(err)
	}
	apitest.CheckToolResultProjection(t, converted, messages)
}
