package llm

// TruncationReason identifies why a text read omitted source content.
type TruncationReason string

const (
	TruncationRequestedLines TruncationReason = "requested_lines"
	TruncationLineLimit      TruncationReason = "line_limit"
	TruncationByteLimit      TruncationReason = "byte_limit"
	TruncationOversizedLine  TruncationReason = "oversized_line"
)

// ToolTruncation is value-only source metadata, retained in Session history but
// not encoded by provider adapters. An empty Reason means no reported truncation
// (including legacy results). Counts exclude continuation notices and separators.
type ToolTruncation struct {
	Reason          TruncationReason `json:"reason"`
	OutputLines     int              `json:"output_lines"`
	OutputBytes     int              `json:"output_bytes"`
	NextOffset      int              `json:"next_offset"`
	TotalLines      int              `json:"total_lines"`
	TotalLinesKnown bool             `json:"total_lines_known"`
}
