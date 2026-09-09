package llm

// TruncationReason identifies why a tool omitted source content.
type TruncationReason string

const (
	TruncationRequestedLines TruncationReason = "requested_lines"
	TruncationLineLimit      TruncationReason = "line_limit"
	TruncationByteLimit      TruncationReason = "byte_limit"
	TruncationOversizedLine  TruncationReason = "oversized_line"
	TruncationMatchLimit     TruncationReason = "match_limit"
	TruncationLongLines      TruncationReason = "long_lines"
)

// ToolTruncation is value-only source metadata, retained in Session history but
// not encoded by provider adapters. An empty Reason means no reported truncation
// (including legacy results). Read counts exclude continuation notices and
// separators. Grep counts formatted output before its final notices, with no
// source total or continuation offset.
type ToolTruncation struct {
	// Grep retains simultaneous match and long-line limits even when Reason is
	// byte_limit. Zero values keep older read metadata unchanged.
	MatchLimitReached int              `json:"match_limit_reached,omitempty"`
	LinesTruncated    bool             `json:"lines_truncated,omitempty"`
	Reason            TruncationReason `json:"reason"`
	OutputLines       int              `json:"output_lines"`
	OutputBytes       int              `json:"output_bytes"`
	NextOffset        int              `json:"next_offset"`
	TotalLines        int              `json:"total_lines"`
	TotalLinesKnown   bool             `json:"total_lines_known"`
}
