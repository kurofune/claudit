# claudit

Go CLI that audits Claude Code sessions from the `.jsonl` logs under `~/.claude/projects/` — what the agents did (Agents trace view) and what it cost. Outputs `--json` and `--html` (default).

## Testing policy (all agents)

All backend and logic code follows Kent Beck's TDD — red (failing test first), green (minimal code to pass), refactor. This is not optional. The full policy, including the frontend carve-outs and the no-implicit-override clause, is in `.claude/rules/testing.md`; agents that don't auto-load that directory should read it before writing code.
