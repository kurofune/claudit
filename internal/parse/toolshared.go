package parse

import "time"

// BashPattern collapses a shell command to the drill-down key a Bash
// ToolUse's Detail carries ("git status", "go test"), so another provider's
// shell tool buckets the same way.
func BashPattern(cmd string) string { return bashPattern(cmd) }

// ToolInputSnippet bounds a tool call's input to the snippet ToolUse.Input
// retains.
func ToolInputSnippet(s string) string { return truncateRunes(s, toolInputMaxChars) }

// JoinTurnText appends one more text block to a turn's text, bounded to the
// snippet Turn.Text retains.
func JoinTurnText(text, block string) string {
	return truncateRunes(joinBlocks(text, block), turnTextMaxChars)
}

// NewToolResult builds the ToolResult for a tool call's full output text:
// a bounded Content snippet plus the size and line count of the whole text.
func NewToolResult(toolUseID string, isError bool, full string, ts time.Time) ToolResult {
	return ToolResult{
		ToolUseID:   toolUseID,
		IsError:     isError,
		Content:     truncateRunes(full, toolResultMaxChars),
		OutputBytes: len(full),
		OutputLines: countLines(full),
		Timestamp:   ts,
	}
}
