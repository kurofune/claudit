// @ts-check
// JSDoc typedefs for the /_claudit/api payload shapes the SPA consumes —
// the JS mirror of the Go wire contract. Each typedef cites the Go struct
// it mirrors (file:line of the type declaration) so drift is traceable:
// when a Go json tag changes, update the matching typedef here and every
// `// @ts-check`ed consumer gets re-checked against the new shape.
//
// Field names match the Go json tags EXACTLY. Fields whose Go tag carries
// `omitempty` (or that Go can emit as null — a nil slice/pointer marshals
// to null) are marked optional / |null. `time.Time` marshals to an
// RFC3339 string.
//
// This module is type-only: importers reference it exclusively inside
// JSDoc via `import('./api-types.js').X`, which TypeScript resolves
// without a runtime import — no runtime behavior, no load-order effects.

/**
 * Token tuple. Mirrors internal/aggregate/aggregate.go:22 (aggregate.Tokens).
 * NOTE: this struct has NO json tags, so it marshals under the Go field
 * names (PascalCase) — the one deliberately un-snake_cased shape on the wire.
 * @typedef {Object} Tokens
 * @property {number} InputTokens
 * @property {number} OutputTokens
 * @property {number} CacheCreate5mTokens
 * @property {number} CacheCreate1hTokens
 * @property {number} CacheReadTokens
 */

/**
 * Rolled-up cost of the sub-agent an Agent tool_use spawned.
 * Mirrors internal/aggregate/sessions.go:133 (aggregate.SpawnRollup).
 * @typedef {Object} SpawnRollup
 * @property {string} agent_ref
 * @property {number} cost_usd
 * @property {Tokens} tokens
 * @property {number} duration_ms
 * @property {number} error_count
 */

/**
 * One (deduped) tool call within a turn.
 * Mirrors internal/aggregate/sessions.go:77 (aggregate.ToolInvocation).
 * @typedef {Object} ToolInvocation
 * @property {string} [id]              omitempty — absent for older sessions without tool_use ids
 * @property {string} name
 * @property {string} detail
 * @property {string} kind              normalized ToolKind: "agent"|"exec"|"read"|"edit"|"web"|"skill"|"command"|"mcp"|"todo"|"other"
 * @property {string} input
 * @property {string} [status]          omitempty — "ok" | "error" | absent when no result joined
 * @property {string} [output]          omitempty
 * @property {number} count
 * @property {number} [output_bytes]    omitempty
 * @property {number} [output_lines]    omitempty
 * @property {number} [rows]            omitempty
 * @property {SpawnRollup} [spawned]    omitempty — only on Agent calls whose sub-agent is in the snapshot
 * @property {string} [started_at]      omitempty (*time.Time) — RFC3339; absent for older sessions
 * @property {string} [ended_at]        omitempty (*time.Time) — RFC3339; absent for older sessions
 */

/**
 * One assistant turn within an agent's timeline.
 * Mirrors internal/agentflow/graph.go:93 (agentflow.AgentStep).
 * @typedef {Object} AgentStep
 * @property {string} [uuid]            omitempty — absent for legacy lines without one
 * @property {string} timestamp         RFC3339
 * @property {string} model
 * @property {number} cost_usd
 * @property {Tokens} tokens
 * @property {number} context_tokens
 * @property {number} duration_ms
 * @property {number} gen_ms
 * @property {ToolInvocation[]|null} tools   nil slice marshals to null
 * @property {string} [thinking]        omitempty
 * @property {string} [text]            omitempty
 */

/**
 * One agent — the main session agent or a sub-agent.
 * Mirrors internal/agentflow/graph.go:65 (agentflow.AgentNode).
 * @typedef {Object} AgentNode
 * @property {string} kind              "main" | "subagent"
 * @property {string} agent_type        empty for the main agent
 * @property {string} description       empty for the main agent
 * @property {string} [parent_tool_use_id]  omitempty — the spawning Agent tool_use id
 * @property {string} started_at        RFC3339
 * @property {string} ended_at          RFC3339
 * @property {number} cost_usd
 * @property {Tokens} tokens
 * @property {string} status            "running" | "done"
 * @property {string} current_tool      only meaningful while running
 * @property {number} error_count
 * @property {AgentStep[]|null} steps   nil slice marshals to null
 */

/**
 * One user prompt anchored to where its turns begin in Main.Steps.
 * Mirrors internal/agentflow/graph.go:55 (agentflow.PromptMarker).
 * @typedef {Object} PromptMarker
 * @property {string} uuid              "" marks orphan turns with no resolvable prompt
 * @property {string} text
 * @property {string} timestamp         RFC3339
 * @property {number} first_step_index
 */

/**
 * One session's agent tree: the main agent plus the sub-agents it spawned.
 * Mirrors internal/agentflow/graph.go:26 (agentflow.AgentSession).
 * @typedef {Object} AgentSession
 * @property {string} session_id
 * @property {string} cwd
 * @property {string} entrypoint        "cli" | "sdk-cli" | ""
 * @property {string} started_at        RFC3339
 * @property {string} ended_at          RFC3339
 * @property {number} cost_usd
 * @property {number} error_count
 * @property {AgentNode|null} main      *AgentNode — null when the session has no main-file turns
 * @property {AgentNode[]|null} children nil slice marshals to null
 * @property {PromptMarker[]|null} prompts nil slice marshals to null
 */

/**
 * Top-level /_claudit/api/agents payload.
 * Mirrors internal/agentflow/graph.go:20 (agentflow.AgentGraph).
 * @typedef {Object} AgentGraph
 * @property {AgentSession[]|null} sessions nil slice (empty snapshot) marshals to null
 */

/**
 * /_claudit/api/agents/full?session=&tool= response — untruncated I/O for one tool_use.
 * Mirrors internal/parse/detail.go:185 (parse.ToolUseDetail).
 * @typedef {Object} ToolUseDetail
 * @property {string} id
 * @property {string} name
 * @property {string} input             untruncated, same representation as ToolUse.Input
 * @property {string} output            untruncated tool_result text
 * @property {string} status            "ok" | "error" | "" when no result line
 */

/**
 * /_claudit/api/agents/full?session=&turn= response — untruncated thinking/text for one turn.
 * Mirrors internal/parse/detail.go:265 (parse.TurnTextDetail).
 * @typedef {Object} TurnTextDetail
 * @property {string} thinking
 * @property {string} text
 */

export {};
