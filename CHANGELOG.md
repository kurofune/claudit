# Changelog

All notable changes to claudit are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.0.0] — 2026-05-16

Initial public release.

### Subcommands

- `report` — generate a cost/usage report from session JSONL files. HTML by default; markdown and JSON also supported.
- `diff` — compare two date ranges and report top movers across model, project, tool, and subagent dimensions.
- `watch` — tail the active session JSONL and print running cost with optional budget alerts.

### What the report covers

- Totals: turns, sessions, tokens (input / output / cache-read / 5m-cache-write / 1h-cache-write), USD cost, and the time range covered.
- Spend split by model, project (cwd), tool, and subagent — with drill-downs into Bash patterns, file extensions, grep globs, and web hosts.
- Per-prompt cost: every user prompt's downstream cost via the conversation's parent links.
- Sidechain (subagent) cost separated from main-thread cost, with per-invocation rows and per-agent-type roll-ups.
- Cache efficiency: overall hit ratio plus the worst-offender prompts and tools driving misses.
- Cost hotspots: top drivers with a copyable LLM prompt for each, so you can paste into a model and get specific advice.
- Trends: day / week / month buckets with sparklines.

### Pricing

- Per-model prices live at `~/.config/claudit/prices.yaml`. The first run writes an embedded default; override the path with `--prices`. Models missing from the YAML surface in the `unknown_models` block with zero attributed cost.

### Discovery

- Defaults to `~/.claude/projects/` for session JSONLs. Honors `CLAUDE_CONFIG_DIR` so users with dotfiles setups, sandboxed configs, or non-default-drive layouts on Windows are found automatically.

### Privacy

- Pure local processing. No network calls in the pipeline — reads `.jsonl` files from disk and a local pricing YAML, writes HTML / JSON / markdown to stdout.

### Platforms

- macOS, Linux, and Windows. CI runs the full test suite on all three. On Windows, `claudit watch`'s live status line requires a VT-capable terminal (Windows Terminal, PowerShell 7); legacy `cmd.exe` shows escape sequences literally.

[Unreleased]: https://github.com/kurofune/claudit/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/kurofune/claudit/releases/tag/v1.0.0
