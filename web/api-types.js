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
 * Mirrors internal/agentflow/graph.go:92 (agentflow.AgentStep).
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
 * Mirrors internal/agentflow/graph.go:69 (agentflow.AgentNode).
 * @typedef {Object} AgentNode
 * @property {string} kind              "main" | "subagent"
 * @property {string} agent_type        empty for the main agent
 * @property {string} description       empty for the main agent
 * @property {string} [parent_tool_use_id]  omitempty — the spawning Agent tool_use id
 * @property {string} started_at        RFC3339
 * @property {string} ended_at          RFC3339
 * @property {number} cost_usd
 * @property {Tokens} tokens
 * @property {string} [status]          "running" marks a live agent; no payload sends it today
 * @property {number} error_count
 * @property {AgentStep[]|null} steps   nil slice marshals to null
 */

/**
 * One user prompt anchored to where its turns begin in Main.Steps.
 * Mirrors internal/agentflow/graph.go:59 (agentflow.PromptMarker).
 * @typedef {Object} PromptMarker
 * @property {string} uuid              "" marks orphan turns with no resolvable prompt
 * @property {string} text
 * @property {string} timestamp         RFC3339
 * @property {number} first_step_index
 */

/**
 * One session's agent tree: the main agent plus the sub-agents it spawned.
 * Mirrors internal/agentflow/graph.go:23 (agentflow.AgentSession).
 * @typedef {Object} AgentSession
 * @property {string} session_id
 * @property {string} cwd
 * @property {string} [project]         resolved project name (git repo root's basename, or a fallback bucket)
 * @property {string} entrypoint        "cli" | "sdk-cli" | ""
 * @property {string} provider          "claude-code" | "codex"
 * @property {string} started_at        RFC3339
 * @property {string} ended_at          RFC3339
 * @property {number} cost_usd
 * @property {number} error_count
 * @property {AgentNode|null} main      *AgentNode — null when the session has no main-file turns
 * @property {AgentNode[]|null} children nil slice marshals to null
 * @property {PromptMarker[]|null} prompts nil slice marshals to null
 */

/**
 * The session graph detectSignals reads: session-header-logic's sessionOf
 * builds a one-session graph from a whole story.
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

/**
 * One row of /_claudit/api/sessions — a session in the filter window.
 * Mirrors internal/agentflow/index.go (agentflow.SessionIndexRow).
 * @typedef {Object} SessionIndexRow
 * @property {string} session_id
 * @property {string} family_key        exact-match group key; "(no prompt)" when the session has no textual prompt
 * @property {string} remainder         what the key blanked, joined by " · " (e.g. a bead id); may be ""
 * @property {string} title             first line of the first real prompt, ≤200 runes
 * @property {string} outcome           first line of the main agent's last message, ≤200 runes
 * @property {string} project
 * @property {string} provider          "claude-code" | "codex"
 * @property {string} model             the model that ran the most turns
 * @property {number} cost_usd
 * @property {number} duration_ms
 * @property {number} turns
 * @property {number} commands          tool calls
 * @property {number} failed            errored tool calls whose exact input never later succeeded
 * @property {string} first_failed_turn uuid of the earliest turn holding one of those calls; "" when failed is 0
 * @property {FailingCheck[]|null} failing_checks one per check kind whose last run in the session errored, in the order of those runs; a check run again and green is not failing
 * @property {number} failed_edits      Edit, Write, MultiEdit and apply_patch calls that errored and never later succeeded with the identical input: edits that never applied
 * @property {number} compactions       context compaction boundaries in the session's files
 * @property {string} last_activity     RFC3339
 * @property {boolean} active           last activity within the last minute
 * @property {string} started_at        RFC3339
 */

/**
 * A check whose last run in a session errored.
 * Mirrors internal/agentflow/index.go (agentflow.FailingCheck).
 * @typedef {Object} FailingCheck
 * @property {string} check    the check's label, as checkKind names it ("go test")
 * @property {string} command  the first line of that last run's command, cut to 120 runes
 * @property {string} turn     uuid of the turn that ran it
 */

/**
 * /_claudit/api/sessions payload, newest last_activity first.
 * Mirrors internal/agentflow/index.go (agentflow.SessionIndex).
 * @typedef {Object} SessionIndex
 * @property {SessionIndexRow[]} sessions
 */

/**
 * A tool call in a story step: ToolInvocation plus the split-off Codex exit
 * code, the whole input's identity (input_key, "" for no input; two calls
 * ran the same input exactly when name and input_key match) and the whole
 * input when `input` is a cut snippet (input_full, omitted when whole). A
 * story lists each call on its own row, so count is 1.
 * Mirrors internal/agentflow/story.go (agentflow.StoryTool).
 * @typedef {ToolInvocation & {exit_code: number|null, input_key: string, input_full?: string}} StoryTool
 */

/**
 * A story step: every AgentStep field, with StoryTool tools. uuid is the
 * turn uuid /agents/full?turn= takes; context_cost_usd is the write cost of
 * the tool results the turn carried into context (what it added).
 * Mirrors internal/agentflow/story.go (agentflow.StoryStep).
 * @typedef {Omit<AgentStep, 'tools'> & {tools: StoryTool[], context_cost_usd: number}} StoryStep
 */

/**
 * One user prompt of a story and how many main-agent turns it drove.
 * Mirrors internal/agentflow/story.go (agentflow.StoryPrompt).
 * @typedef {Object} StoryPrompt
 * @property {string} uuid              "" marks turns with no resolvable prompt
 * @property {string} text              whole, read back from the session log
 * @property {string} timestamp         RFC3339
 * @property {number} turns
 */

/**
 * Mirrors internal/agentflow/story.go (agentflow.StoryPageInfo).
 * @typedef {Object} StoryPageInfo
 * @property {number} prompt_index
 * @property {number} turn_offset
 * @property {number} turn_limit
 * @property {boolean} has_more
 */

/**
 * A sub-agent started within the page's span, with its own steps.
 * Mirrors internal/agentflow/story.go (agentflow.StoryChild).
 * @typedef {Object} StoryChild
 * @property {string} kind              "subagent" | "main" (a further rollout sharing the session id)
 * @property {string} agent_type
 * @property {string} description
 * @property {string} parent_tool_use_id  "" when the sub-agent's meta does not name it
 * @property {string} started_at        RFC3339
 * @property {string} ended_at          RFC3339
 * @property {number} cost_usd
 * @property {number} error_count
 * @property {StoryStep[]} steps
 */

/**
 * /_claudit/api/sessions/story payload — one page of one session's story.
 * Mirrors internal/agentflow/story.go (agentflow.StoryPage).
 * @typedef {Object} SessionStoryPage
 * @property {string} session_id
 * @property {StoryPrompt[]} prompts
 * @property {StoryPageInfo} page
 * @property {StoryStep[]} steps
 * @property {StoryChild[]} children
 * @property {{replays: number, outside_filter: number}} hidden
 * @property {string[]} source_files    every session log the session's turns came from
 */

/**
 * /_claudit/api/sessions/summary payload — what the session header reads:
 * the session's whole index row plus every step of the main agent and its
 * sub-agents with their numbers and calls but none of their text. Steps
 * carry no text or thinking; tools carry no output or input_full, and each
 * input is its first line, at most 120 runes.
 * Mirrors internal/agentflow/story.go (agentflow.SessionSummary).
 * @typedef {SessionIndexRow & {steps: StoryStep[], children: StoryChild[],
 *   source_files: string[]}} SessionSummary
 */

export {};
