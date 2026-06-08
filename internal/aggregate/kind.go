package aggregate

import "strings"

// ToolKind maps a raw Claude Code tool name to a normalized, low-cardinality
// category so the frontend can filter and color by behavior instead of
// matching tool-name strings. The categories are:
//
//	agent   — Agent (a sub-agent spawn; the audit-critical edge)
//	exec    — Bash
//	read    — Read, Glob, Grep, LS, NotebookRead
//	edit    — Edit, Write, MultiEdit, NotebookEdit
//	web     — WebFetch, WebSearch
//	skill   — Skill
//	command — SlashCommand
//	todo    — TodoWrite, TodoRead
//	mcp     — any mcp__* tool
//	other   — anything else (default)
//
// Distinct from AgentNode.Kind, which means main-vs-subagent ("main"/"subagent").
func ToolKind(name string) string {
	switch name {
	case "Agent":
		return "agent"
	case "Bash":
		return "exec"
	case "Read", "Glob", "Grep", "LS", "NotebookRead":
		return "read"
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "edit"
	case "WebFetch", "WebSearch":
		return "web"
	case "Skill":
		return "skill"
	case "SlashCommand":
		return "command"
	case "TodoWrite", "TodoRead":
		return "todo"
	}
	if strings.HasPrefix(name, "mcp__") {
		return "mcp"
	}
	return "other"
}
