package tool

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ErrApprovalRequired is returned when a mutating tool has no approval policy.
var ErrApprovalRequired = errors.New("tool: explicit approval is required")

// ApprovalRequest describes one validated mutating operation. Arguments may
// contain source code or commands and must not be logged without redaction.
type ApprovalRequest struct {
	Tool        string
	Description string
	Arguments   json.RawMessage
}

// Approver decides whether one mutating tool call may proceed.
type Approver interface {
	Approve(ctx context.Context, request ApprovalRequest) error
}

// ApproverFunc adapts a function into an Approver.
type ApproverFunc func(ctx context.Context, request ApprovalRequest) error

// Approve calls f.
func (f ApproverFunc) Approve(ctx context.Context, request ApprovalRequest) error {
	return f(ctx, request)
}

func requestApproval(
	ctx context.Context,
	approver Approver,
	call llm.ToolCall,
	description string,
) error {
	if approver == nil {
		return ErrApprovalRequired
	}
	request := ApprovalRequest{
		Tool:        call.Name,
		Description: description,
		Arguments:   bytesClone(call.Arguments),
	}
	return approver.Approve(ctx, request)
}

func bytesClone(value []byte) []byte {
	if value == nil {
		return nil
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}
