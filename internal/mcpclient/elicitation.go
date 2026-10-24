package mcpclient

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Elicitation is one form-mode request for user input from the server. Schema
// is the requested flat object schema; the SDK validates accepted content.
type Elicitation struct {
	Message string
	Schema  json.RawMessage
}

// ElicitationAction is the user's answer: accept carries Content, decline is an
// explicit refusal and cancel means no choice was made.
type ElicitationAction string

const (
	ElicitationAccept  ElicitationAction = "accept"
	ElicitationDecline ElicitationAction = "decline"
	ElicitationCancel  ElicitationAction = "cancel"
)

type ElicitationReply struct {
	Action  ElicitationAction
	Content map[string]any
}

// ElicitFunc answers an elicitation. ctx is the operation that the server is
// processing, so it carries the caller's values and cancellation.
type ElicitFunc func(context.Context, Elicitation) (ElicitationReply, error)

// elicitationHandler serves legacy elicitation/create requests and
// 2026-07-28 input-required results through one caller-owned function. Both
// arrive while this connection's single operation holds the gate.
func (c *Client) elicitationHandler(elicit ElicitFunc) func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	return func(handlerCtx context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		ctx, active := c.activeOperation()
		if !active || request == nil || request.Params == nil || request.Params.Mode == "url" {
			return &mcp.ElicitResult{Action: string(ElicitationCancel)}, nil
		}
		schema, err := json.Marshal(request.Params.RequestedSchema)
		if err != nil {
			return nil, err
		}
		// The wait ends with either the operation or the server's request.
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(handlerCtx, cancel)
		defer stop()
		reply, err := elicit(ctx, Elicitation{Message: request.Params.Message, Schema: schema})
		if err != nil {
			return nil, err
		}
		if reply.Action != ElicitationAccept {
			reply.Content = nil
		}
		return &mcp.ElicitResult{Action: string(reply.Action), Content: reply.Content}, nil
	}
}

func (c *Client) activeOperation() (context.Context, bool) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	return c.active, c.active != nil
}

func (c *Client) setActiveOperation(ctx context.Context) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	c.active = ctx
}
