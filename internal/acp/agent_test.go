package acp

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

type permissionClient struct {
	sdk.Client
	answer   func(sdk.RequestPermissionRequest) sdk.RequestPermissionResponse
	requests chan sdk.RequestPermissionRequest
}

func (c *permissionClient) RequestPermission(_ context.Context, request sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	c.requests <- request
	return c.answer(request), nil
}

func TestGuardAskMapsOptionsAndCancelDenies(t *testing.T) {
	for name, test := range map[string]struct {
		answer func(sdk.RequestPermissionRequest) sdk.RequestPermissionResponse
		want   string
	}{
		"cancelled": {
			answer: func(sdk.RequestPermissionRequest) sdk.RequestPermissionResponse {
				return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeCancelled()}
			},
			want: "deny",
		},
		"selected": {
			answer: func(sdk.RequestPermissionRequest) sdk.RequestPermissionResponse {
				return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeSelected("allow-once")}
			},
			want: "allow-once",
		},
	} {
		t.Run(name, func(t *testing.T) {
			agentIn, clientOut := io.Pipe()
			clientIn, agentOut := io.Pipe()
			client := &permissionClient{answer: test.answer, requests: make(chan sdk.RequestPermissionRequest, 1)}
			sdk.NewClientSideConnection(client, clientOut, clientIn)
			a := &agent{sessionID: "s", ready: make(chan struct{})}
			a.conn = sdk.NewAgentSideConnection(a, agentOut, agentIn)
			close(a.ready)

			reply := make(chan interaction.GuardReply, 1)
			done := make(chan struct{})
			a.answerGuard(t.Context(), interaction.GuardRequest{
				ID: "call-1", ToolName: "read", Path: "/etc/hosts",
				Options: []interaction.GuardOption{{ID: "allow-once"}, {ID: "allow-run-file"}, {ID: "deny", Deny: true}},
				Reply:   reply, Done: done,
			})
			request := <-client.requests
			kinds := []sdk.PermissionOptionKind{}
			for _, option := range request.Options {
				kinds = append(kinds, option.Kind)
			}
			want := []sdk.PermissionOptionKind{sdk.PermissionOptionKindAllowOnce, sdk.PermissionOptionKindAllowAlways, sdk.PermissionOptionKindRejectOnce}
			if len(kinds) != len(want) || kinds[0] != want[0] || kinds[1] != want[1] || kinds[2] != want[2] {
				t.Fatalf("kinds = %v, want %v", kinds, want)
			}
			if request.ToolCall.ToolCallId != "call-1" {
				t.Fatalf("tool call = %q", request.ToolCall.ToolCallId)
			}
			select {
			case got := <-reply:
				if got.OptionID != test.want {
					t.Fatalf("reply = %q, want %q", got.OptionID, test.want)
				}
			case <-time.After(time.Second):
				t.Fatal("no reply")
			}
		})
	}
}

func TestRunInputAcceptsBaselineContent(t *testing.T) {
	t.Parallel()
	input, err := runInput([]sdk.ContentBlock{
		sdk.TextBlock("explain "),
		sdk.ResourceLinkBlock("main.go", "file:///work/main.go"),
		sdk.ResourceLinkBlock("docs", "https://example.com/docs"),
		sdk.ImageBlock("aGk=", "image/png"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.Prompt != "explain https://example.com/docs" || len(input.Files) != 1 || input.Files[0] != "/work/main.go" {
		t.Fatalf("input = %+v", input)
	}
	if len(input.Images) != 1 || string(input.Images[0].Data) != "hi" || input.Images[0].MIMEType != "image/png" {
		t.Fatalf("images = %+v", input.Images)
	}
	if _, err := runInput([]sdk.ContentBlock{sdk.AudioBlock("aGk=", "audio/wav")}); err == nil {
		t.Fatal("audio accepted without the capability")
	}
}

func TestStopReasonsAndToolOutcome(t *testing.T) {
	t.Parallel()
	for reason, want := range map[llm.StopReason]sdk.StopReason{
		llm.StopReasonStop:    sdk.StopReasonEndTurn,
		llm.StopReasonLength:  sdk.StopReasonMaxTokens,
		llm.StopReasonRefusal: sdk.StopReasonRefusal,
	} {
		if got := stopReason(reason); got != want {
			t.Errorf("stopReason(%s) = %s, want %s", reason, got, want)
		}
	}
	update := toolEnd(interaction.ToolDisplay{
		ID: "edit-1", Failed: true, Kind: interaction.ToolKindEdit,
		Locations: []interaction.ToolLocation{{Path: "/work/a.go"}},
		Diff:      interaction.DiffDisplay{Text: "-old\n+new"},
		Output:    interaction.ToolOutputDisplay{Text: "conflict", Available: true},
	}).ToolCallUpdate
	if update == nil || *update.Status != sdk.ToolCallStatusFailed || *update.Kind != sdk.ToolKindEdit || update.Locations[0].Path != "/work/a.go" || len(update.Content) != 2 {
		t.Fatalf("tool update = %+v", update)
	}
	if got := rawInput([]byte("not json")); fmt.Sprint(got) != "map[value:not json]" {
		t.Fatalf("rawInput = %v", got)
	}
}
