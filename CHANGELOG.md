# Changelog

All notable changes to claudit are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **`claudit serve` — local web daemon.** A long-running process that watches the projects root and serves the same HTML report at `http://127.0.0.1:8787/`, re-rendering against the freshest data on demand. A background poller re-parses only files whose `(mtime, size)` changed since the last tick, so a steady-state daemon does no work when nothing is happening. Filters live in the URL query string — `?project=myrepo&last=7d`, `?since=2026-05-01&until=2026-05-15&by=week`, `?hotspots=5&sessions=20&redact=true` — so a bookmarked URL is a bookmarked filter. `--open` (default on) launches the browser; skipped automatically on headless hosts. Loopback-only bind by default; `--bind=0.0.0.0` is allowed with a warning printed at startup since the report contains prompt text and CWD paths and has no auth. Diagnostic endpoints at `/_claudit/status` (JSON snapshot vitals) and `/_claudit/healthz` (liveness probe).

  **Operational defaults (different from `claudit report`).** Serve is a dashboard, not an audit: the default view is `last=7d` and `sessions=10`, which keeps the rendered page small (~3 MB uncompressed, ~600 KB on the wire) and the render path under 2 s on large corpora. A small fixed pill at the top of the page ("Showing last 7 days · top 10 sessions · show all") makes the narrowing obvious and one click away from the full archive. Both defaults are configurable on the daemon (`--last=`, `--sessions=`) or per-request (`?last=`, `?sessions=`, `?scope=all`).

  **Silent auto-reload.** The page reloads itself every 30 s (configurable via `--reload-sec`) when new data has arrived. Reload is deferred while you're reading — when the tab is hidden, when any `<details>` is open, or when there's been mouse/keyboard/scroll activity in the last 10 s — and resumes the moment the page is idle. If a reload has been deferred for more than 5 minutes while new data has piled up, a small toast in the bottom-right offers a manual reload.

  **Performance.** Responses are gzipped when the client accepts it (≈ 25× compression for the default view, ≈ 3× for `scope=all`). A bounded LRU (default 16 entries, `--cache=N`) memoizes rendered HTML keyed on `(canonical-query, snapshot-generation)`, so the auto-reload polls and any repeat navigation between data changes serve in <10 ms. Old-generation cache entries are pruned on every insert.
- **Anomaly callouts on the trend chart.** Buckets whose cost spikes above 2× the trailing 7-bucket median (or whose cache hit ratio falls more than 20 percentage points below the same window) are now flagged inline: the corresponding dot on the trend chart is enlarged and colored coral, with a small marker label showing the multiplier or pp-gap, and the hover tooltip gains a flagged line. Markdown reports gain an `## Anomalies` section under the totals; JSON gains an `anomalies` array. Renders in all three output modes whenever there are at least eight trend buckets to baseline against.
- **Deep-link anchors** on hotspot cards and session cards in the HTML report. Each card carries a small `#` link in its summary that copies a shareable URL (`#overview/hotspot-3`, `#sessions/session-<sid>`); loading the URL opens the card and scrolls it into view. Bare anchors (`#hotspot-3`, `#session-abc`) also work and route to the right view automatically.
- **Print stylesheet.** Saving the HTML report as a PDF (Cmd-P) now produces a usable single-document copy: every `<details>` body is force-expanded, the sidebar is hidden, the panel flows full-width, dark mode is overridden with a light palette, interactive chrome (filter inputs, tooltips, copy buttons) is hidden, and each top-level section starts on a fresh page.
- **Cross-links into Sessions view.** Prompt hotspot cards and the "Top expensive prompts" table rows now carry a "view session →" button that jumps to the Sessions drill-down with the originating session card and prompt block pre-expanded. Disabled (with a tooltip) when the prompt's session falls below the `--sessions=N` cap. Cross-links survive `--redact` because the link key is computed from raw prompt text, not the displayed body.
- **Sessions drill-down view** in the HTML report. A new "Sessions" tab in the nav (between Cost and Cache) lists the top sessions by cost. Open a session to see its user prompts in order, and open a prompt to see the assistant turns it produced — with per-turn model, tokens, cost, and tool chips. Configurable via `--sessions=N` on `claudit report` (default 50; `--sessions=0` disables). Use `--redact` to replace prompt bodies with `[redacted N chars]` before sharing.
- `claudit diff --html` renders the comparison as a self-contained HTML document with side-by-side A/B bars, totals tiles with delta lines, and a new-hotspots grid. Uses the same design tokens as the main report.
- `claudit diff` now runs with no arguments, defaulting to the last 7 days vs the prior 7 days via a new `--by=week|month` flag (`--by=month` → 30d vs 30d). Equal-size rolling windows ending at midnight tonight; labels say "7 days" rather than "this week" to match the rolling math. Explicit `--a`/`--b` still wins when provided.

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
