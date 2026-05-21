# claudit

Go CLI that audits Claude Code session `.jsonl` files under `~/.claude/projects/` and reports token/cost spend. Default output is a self-contained HTML report; markdown and JSON are also supported. See `README.md` for user-facing usage.

## Go Code Intelligence

For Go symbol queries (definitions, references, callers, hover, implementations), use the `LSP` tool — gopls is wired up via the `gopls-lsp` plugin. See `.claude/rules/lsp.md` for the decision rule, the deferred-tool load step (`ToolSearch select:LSP`).

## Rules

- `testing.md` — TDD is required for backend and frontend-logic code; UI styling is exempt.
- `lsp.md` — use the `LSP` tool (gopls-backed) for Go symbol queries; grep for everything else.
- `time-estimations.md` — estimate for AI agents, not humans.
