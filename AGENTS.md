# claudit

Go tool that audits coding-agent session logs (Claude Code, Codex) — what the agents did (Agents trace view) and what it cost — as a local web app (`claudit serve`, bare `claudit`) plus the `claudit watch` TUI.

## Agent skills

### Issue tracker

Issues are beads in the local bd DB; file with `bd create`, acceptance criteria via `--acceptance`. The bd workflow comes from `bd prime` (SessionStart hook).

### Triage labels

Default five-role vocabulary as plain bd labels (advisory only). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root, created
lazily. See `docs/agents/domain.md`.

### UI rules

UI work: every `docs/SCARS.md` item is an acceptance criterion.
