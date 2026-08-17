# CONTEXT MAP

Pointers only — what exists and where.

## State & front door

- `CURRENT.md` — live state (mode, goal, checkpoints)
- `DECISIONS.md` — settled choices + open questions
- `SITUATION_ROOM.html` — generated director brief (via the `situation-room` skill; never hand-edit)
- `AGENTS.md` / `CLAUDE.md` — how to work here; `.claude/rules/testing.md` — TDD policy

## Code

- `cmd/claudit/` — CLI entry: `report`, `diff`, `serve`, `watch` subcommands
- `internal/corpus/` — unified JSONL loader (cold load, incremental poll, mtime pre-filter)
- `internal/parse/` — JSONL line → turns; sub-agent detection via `.meta.json`
- `internal/aggregate/` — roll-ups, dedup (`ReplaySet`), session timelines, rolling totals
- `internal/agentflow/` — the Agents trace graph (`AgentGraph → AgentSession → AgentNode → AgentStep → ToolInvocation`)
- `internal/pricing/` — bundled `default.yaml` + overlay loader, date-effective rates
- `internal/render/` — report/diff HTML, tokens.css, static SPA bundling
- `internal/serve/` — web daemon: API, ETag/render cache, SSE `/events`
- `internal/watch/`, `internal/notify/`, `internal/stat/` — live TUI, desktop notify, stats
- `web/` — the SPA: `view-*.js` (DOM) + `*-logic.js` (pure, TDD'd), 16 theme CSS files
- `jstest/` — Node-runner JS unit tests; `web_embed.go` — embeds `web/` into the binary

## Docs & history

- `README.md` — user-facing; `CHANGELOG.md` — the release record (richest history source)
- `docs/agents-redesign.md` — trace-viewer redesign (shipped)
- `docs/agents-audit-roadmap.md` — Phoenix + Honeycomb programs (shipped; deferred items listed)
- `docs/filter-migration-plan.md` — historical (shipped in v1.6.0)
- `docs/design.md` — design notes; `docs/agents/` — tracker/triage/domain conventions
- Issues: GitHub Issues (`gh issue list`) — currently empty
