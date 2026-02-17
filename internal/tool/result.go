package tool

// Result is the strict output envelope for tool execution.
type Result struct {
	OK             bool           `json:"ok"`
	ExitCode       int            `json:"exit_code"`
	Stdout         string         `json:"stdout"`
	Stderr         string         `json:"stderr"`
	TruncatedLines bool           `json:"truncated_lines"`
	TruncatedBytes bool           `json:"truncated_bytes"`
	NextPageCursor string         `json:"next_page_cursor,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
}
