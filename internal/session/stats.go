package session

import (
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Stats are process metrics of one Session file across every recorded branch,
// so they count all billed work, including abandoned branches and retries.
type Stats struct {
	SessionID string `json:"session_id"`
	// Builds and Models list each distinct value in first-use order.
	Builds []string  `json:"builds"`
	Models []string  `json:"models"`
	Usage  llm.Usage `json:"usage"`
	// UserMessages counts prompts, steering and follow-ups.
	UserMessages int `json:"user_messages"`
	// ModelRequests counts assistant messages; FailedRequests are those that
	// ended in an error or abort, whether a retry followed or the run stopped.
	ModelRequests  int `json:"model_requests"`
	FailedRequests int `json:"failed_requests"`
	// LastStopReason is the stop reason of the last assistant message.
	LastStopReason llm.StopReason `json:"last_stop_reason,omitempty"`
	ToolCalls      int            `json:"tool_calls"`
	ToolErrors     int            `json:"tool_errors"`
	// ToolsNotDispatched were refused before execution, for example by a
	// Guard denial; ToolsUnknown have an unknown outcome after recovery.
	ToolsNotDispatched int            `json:"tools_not_dispatched"`
	ToolsUnknown       int            `json:"tools_unknown"`
	Tools              map[string]int `json:"tools"`
	// Actions counts the Guard's normalized actions by kind, and by kind and
	// operation for file actions ("file:read", "file:write").
	Actions     map[string]int `json:"actions"`
	Compactions int            `json:"compactions"`
	// DurationMS spans the first and last record creation times.
	DurationMS int64 `json:"duration_ms"`
}

// ComputeStats derives Stats from a Snapshot without reading anything else.
func ComputeStats(snapshot Snapshot) Stats {
	stats := Stats{
		SessionID:   snapshot.Header.ID,
		Builds:      []string{},
		Models:      []string{},
		Usage:       TotalUsage(snapshot),
		Tools:       map[string]int{},
		Actions:     map[string]int{},
		Compactions: len(snapshot.Compactions),
	}
	addDistinct := func(values *[]string, value string) {
		if value != "" && !slices.Contains(*values, value) {
			*values = append(*values, value)
		}
	}
	for _, record := range snapshot.RunConfigs {
		addDistinct(&stats.Builds, record.Build)
	}
	var first, last int64
	span := func(createdAt int64) {
		if createdAt <= 0 {
			return
		}
		if first == 0 || createdAt < first {
			first = createdAt
		}
		last = max(last, createdAt)
	}
	for _, compaction := range snapshot.Compactions {
		span(compaction.CreatedAt)
	}
	for _, entry := range snapshot.Messages {
		span(entry.CreatedAt)
		switch message := entry.Message.(type) {
		case llm.UserMessage:
			stats.UserMessages++
		case llm.AssistantMessage:
			stats.ModelRequests++
			addDistinct(&stats.Models, string(message.Provider)+"/"+message.ModelID)
			if message.StopReason == llm.StopReasonError || message.StopReason == llm.StopReasonAborted {
				stats.FailedRequests++
			}
			stats.LastStopReason = message.StopReason
		case llm.ToolResultMessage:
			stats.ToolCalls++
			stats.Tools[message.ToolName]++
			if message.IsError {
				stats.ToolErrors++
			}
			if message.Details != nil {
				switch message.Details.State {
				case llm.ExecutionNotDispatched:
					stats.ToolsNotDispatched++
				case llm.ExecutionUnknown:
					stats.ToolsUnknown++
				}
			}
			for _, action := range message.Actions {
				key := action.Kind
				if action.Op != "" {
					key += ":" + action.Op
				}
				stats.Actions[key]++
			}
		}
	}
	stats.DurationMS = last - first
	return stats
}
