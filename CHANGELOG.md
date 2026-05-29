# Changelog

All notable changes to claudit are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project adheres to [Semantic Versioning](https://semver.org/).

## [1.4.2] — 2026-05-29

### Changed

- **Bundled pricing refreshed against the Anthropic pricing page on 2026-05-29.** Added Claude Opus 4.8 (`claude-opus-4-8` and the `[1m]` 1M-context variant) at the same $5/$25 input/output rates as Opus 4.7 / 4.6. All existing entries (Opus 4.7/4.6/4.5, Sonnet 4.6/4.5, Haiku 4.5) were re-verified and unchanged; their `# verified` tags were refreshed. Users with a custom `~/.config/claudit/prices.yaml` are unaffected.

## [1.4.1] — 2026-05-27

### Fixed

- **The sidebar date-range pill now matches the picker in `claudit serve`.** The pill label was rendered from the corpus's actual first/last turn timestamps (sliced off a UTC string), while the picker popover showed the selected window — so the two disagreed. Two symptoms: an off-by-one at the inclusive/exclusive boundary (a late-evening turn in a UTC-behind zone slid the label's end a day forward, e.g. label `→ 05-28` vs picker `05-27`), and on first open the label showed the last turn's date instead of the window end (today). The label now derives from the same `urlToRange` translation the picker uses, so they always agree. The static report still shows the corpus data span (it has no picker). Backed by the project's first JS unit tests (`jstest/`, Node's built-in runner, wired into CI).

## [1.4.0] — 2026-05-27

### Added

- **Tokens view in `claudit serve` and the static report.** A dedicated tab answering "how many tokens did I burn, and what is that number made of": the grand total broken into the four categories (input / output / cache-write / cache-read), a stacked token-volume trend over time, and a by-model breakdown. On real corpora the total is dominated by cache-read, which this view makes legible. All roll-ups (grand total, composition percentages, per-model totals) are computed server-side in `render.BuildTokens` and shipped via a new `/_claudit/api/tokens` endpoint — inlined into the static bundle for offline use — so the JS view is purely presentational. An **Overview "Total tokens" tile** also lands between Assistant turns and Cache hit ratio.
- **Token comparison in `claudit diff`** across all three outputs. A markdown `## Tokens` table, a JSON `tokens` block, and an HTML section with A/B mix-shift bars plus per-category before→after **dumbbell** rows (signed Δ and Δ%, colored with the diff's existing up/down semantics), sharing one dumbbell axis so dot positions stay comparable across categories. The diff Overview also gains a **"Total tokens" A→B tile**. Categories reuse the same composition split as the report (`BuildTokenDiff` pairs each side off the shared `tokenComposition`), so the diff's categories never drift from the report's.

### Changed

- **Unified the data layer behind every command into one `internal/corpus` package.** `report`, `diff`, `serve`, and `watch` now load session JSONL through a single loader (concurrent cold-load, incremental `(mtime, size)` polling for the long-lived consumers, and an mtime pre-filter for date-windowed one-shots) and roll it up through the same `internal/aggregate` pipeline. Previously `watch` reimplemented its own windowed scan plus a bespoke rolling-totals sum, which is exactly why its hour/today/week/month figures could diverge from `serve` and `report`. That parallel path is deleted; `watch`'s rolling panel is now `aggregate.RollingTotals` over the shared corpus, so it matches the other commands by construction (verified: `watch --all` month and `report --since=<1st>` month agree to the cent).
- **Date filters now resolve in local time.** `--since` / `--until`, `diff`'s `--a` / `--b` ranges, and serve's `?since` / `?until` are interpreted as calendar dates at midnight in your **local** time zone — consistent with `--last` and the `watch` rolling buckets, which were already local. They previously pinned the boundary to UTC, which shifted the window for non-UTC users and left `serve` internally inconsistent (`?last` was local while `?since` was UTC).

### Deprecated

- **`claudit watch --scan-days` is deprecated and ignored.** The rolling panel now reads the full corpus and refreshes on a poll, so there is no startup scan window to size — and therefore no clamp. The flag is still accepted (so existing invocations and aliases don't error) but has no effect; passing it prints a one-line deprecation notice.

### Fixed

- **`claudit watch` rolling totals no longer under-report, often dramatically.** The hour/today/week/month panel was seeded from a one-time startup scan bounded by `--scan-days` (default 30) and thereafter only updated from the session file(s) being tailed. Two failure modes followed: the **month** total was clamped whenever `--scan-days` was shorter than the elapsed part of the calendar month (e.g. `--scan-days=7` showed ~$2.5k of a ~$9.7k month), and a long-running `watch` drifted below reality as spend accrued in other projects it wasn't tailing — so the same month that `serve` reported at ~$9.9k could show as ~$4k or ~$2.5k in `watch`. The panel is now computed over the full corpus and refreshed on a 2 s poll, so `watch`'s totals track `serve` / `report` for the same window.
- **Build label pinned to the bottom of the static report sidebar.** The version + commit footer (added in v1.3.0) floated mid-sidebar in the standalone `claudit report` export; it now sticks to the sidebar bottom as it already did in `claudit serve`.

## [1.3.0] — 2026-05-23

### Added

- **Selectable theme picker in `claudit serve` — 16 palettes plus Auto.** A gear button in the sidebar footer opens a popover offering Auto + 6 light + 10 dark themes (Ayu Light, Catppuccin Latte, Gruvbox Light, One Light, PaperColor Light, Solarized Light; Catppuccin Mocha, Dracula, GitHub Dark, Gruvbox Dark, Monokai Pro, Night Owl, Nord, One Dark, Solarized Dark, Tokyo Night), alphabetized within each scheme group. Each theme is an OKLCH variable-override file (`web/theme-<slug>.css`) layered over the shared `tokens.css` design tokens; only `--accent` shifts to the theme's signature hue while the semantic accents (`--hot` red, `--accent-2` green, `--warn` amber) stay anchored to their families. The choice persists in `localStorage` and an inline `<head>` script applies it before first paint, so there's no flash of the wrong theme on reload. Auto (the default) follows the OS `prefers-color-scheme`.
- **Static `claudit report` / `claudit diff` exports inherit the theme chosen in `serve`.** Picking a theme in the running SPA writes it to `~/.config/claudit/theme` (matching the existing `~/.config/claudit/prices.yaml` convention); a subsequent `claudit report` or `claudit diff` stamps that theme onto the exported HTML and inlines just the one matching theme's CSS — no picker UI and no 16-theme catalog bloating the standalone file. Auto, or a missing/invalid slug, falls back to the OS `prefers-color-scheme` default. The export is still a single self-contained `.html`.
- **`claudit serve` is now a single-page app with lazy per-section loading.** `/` ships the sidebar chrome only; each tab fetches its data from `/_claudit/api/{overview,cost,cache,tools,subagents,sessions}` when first opened. Section responses carry ETags and revalidate against an in-process render cache, so a refresh of an unchanged section returns `304 Not Modified` in a few ms. Concurrent requests for the same canonical query collapse onto one build instead of re-parsing. Initial paint of the dashboard view is dramatically faster on large corpora because only the sidebar and Overview render up-front.
- **Live updates over Server-Sent Events** at `/events`, replacing the 30 s background poll the page used in v1.1.0. When the watcher detects a new turn the page learns immediately; the auto-reload still defers while the tab is hidden, while a `<details>` is open, or while the user is actively interacting.
- **Static report (`claudit report > report.html`) is now an SPA shell with everything inlined.** The output is still a single self-contained `.html` file, but the body is the same SPA shell `claudit serve` uses, with every `web/*.js` module embedded as `<script type="text/x-claudit-mod">`, every section's JSON inlined as `window.__claudit_static_data`, and a bootstrap that topologically sorts modules from their `import` statements and rewrites each `./X.js` reference to the dependency's blob URL. A downloaded report keeps tabs, charts, tables, session deep-links, and lazy timeline expand — fully interactive offline, no server.
- **Date-range picker popover in the sidebar brand area** (serve mode). The "claudit" subtitle becomes a button that opens a popover with two native `<input type="date">` fields and Apply / Clear / Cancel. Apply rewrites the URL to `?since=&until=&scope=all` and reloads — the same query parameters that `filter.go` already reads. The user-facing End is inclusive; we translate at the URL boundary by adding one day on Apply and subtracting one when seeding from the URL. The static report renders `#date-range` as a plain `<div>` so the module no-ops there.
- **Shimmer skeleton loaders** on lazy-loaded tab content and sidebar metric counts, so the page doesn't sit on placeholder dashes while a section's data is in flight. Resets cleanly on error so the dash returns if a request fails.
- **Build version + commit in the sidebar footer**, sourced from `runtime/debug.ReadBuildInfo`. Visible in both `claudit serve` and `claudit report`. Matches what `claudit version` prints, so a stale `go install ...@latest` is diagnosable from a screenshot of the page.
- **Month-end cost forecast on the Overview tab.** Cumulative cost-this-month chart with a projected end-of-month total based on the current run rate.
- **`claudit version` / `claudit --version`** prints the installed binary's module version and git commit. For `go install` builds the output is `claudit vX.Y.Z (commit abc1234)`; for local `go build` builds it's `claudit (devel) (commit abc1234, dirty)`. Built on `runtime/debug.ReadBuildInfo`, so no version constant to forget to bump. Closes the diagnostic gap where a stale `go install ...@latest` (served by a Go module proxy that hadn't yet indexed the new tag) silently returned the previous version with no way to tell.

### Removed

- **Legacy fat-HTML serve surface retired** (Phase 10 of the SPA cutover). The `/legacy` route, `/_claudit/data.json`, `/_claudit/status` (replaced by the SSE `/events` stream), and the 3,879-line `report.html.tmpl` template are deleted, along with their SSR helpers. The cutover at `/` shipped in the previous wave and the legacy surface has been carried for one minor release as promised in v1.1.0; new bookmarks should target `/`.

### Fixed

- **`claudit watch` no longer freezes when the terminal stops draining its pty** (Ghostty in a fully-obscured window, macOS post-sleep, etc.). The screen painter wrote frames synchronously on the event-loop goroutine, so a parked `io.WriteString` to the TTY blocked the loop, which stopped draining the bounded event channels, which blocked the per-session Tail goroutines, which stopped polling JSONLs. Diagnostic fingerprint: opening a second `claudit watch` would un-freeze the first one, because bringing the terminal to the foreground let its pty drain again. Painting now runs on a dedicated goroutine with latest-frame coalescing (a `dirty` flag plus a cap-1 wake channel), so `Render` and `Alert` are non-blocking and the event loop keeps draining no matter how slow the terminal is.
- **`claudit serve` enforces HTTP read / write / idle timeouts** on its listener so slow-loris clients can't pin connections indefinitely, and **caps inbound request body size** to prevent unbounded reads.
- **Bind-warning widened to cover IPv6 and non-loopback hosts.** Previously only IPv4 non-loopback binds triggered the "report contains prompt text and CWDs, no auth" startup warning; an `--bind=::` bind printed nothing.
- **Horizontal-bar fills scaled to `totalCost`** so the rendered bar width matches the printed percentage. Previously fills were sized against the row-max cost, so the widest row always rendered as 100% even when its share of total spend was small.
- **`claudit watch` notifier runs off the hub goroutine** so a notifier-binary stall (e.g. a hanging `osascript`) can't wedge shutdown.

## [1.2.0] — 2026-05-19

### Added

- **`claudit watch` rolling totals now include an Hour tier** alongside Today / Week / Month, so a long debug session can see per-hour burn rate at a glance without doing the arithmetic.

### Changed

- **Default report theme is now teal** (Datadog / Sentry / Honeycomb / Grafana observability-category color) instead of violet. Surfaces carry only a faint teal cast; the brand color shows up in accents — primary affordances, focus rings, the totals headline. The chart palette has been redistributed to avoid violet entirely (blue / rose / green / amber / coral); green and amber slots are preserved because `.tier-good` and `.tier-ok` rely on them semantically. The token block is now a single shared `internal/render/tokens.css` injected into both `report` and `diff` templates, so future theme swaps touch one file instead of two.

### Fixed

- **`claudit serve` no longer renders a blank page on first request.** The cache poller was launching in a goroutine and returning before its first scan completed, so the listener (and any `--open` browser tab) could race the scan and hit the empty initial snapshot. `Server.Start` now primes the cache synchronously before returning, so the listener never accepts before real data is available.
- **Rounded report tables no longer show a 1px L-sliver in the top corners of header cells.** `border-collapse: collapse` + `border-radius` is a known CSS footgun — the collapsed border becomes owned by the corner cells, which don't follow border-radius. Switched to `border-collapse: separate; border-spacing: 0` so the table's border stays on the table element where border-radius applies cleanly. Side effect: the rounded outline now wraps continuously across the top of header rows (it was previously hidden by the `th` background).

## [1.1.1] — 2026-05-17

### Fixed

- **Deep-link anchor (`#`) on hotspot and session cards now copies the shareable URL to the clipboard,** as the v1.1.0 changelog and the tooltip both promised. The click handler was never wired up in v1.1.0 — clicking the `#` updated the URL hash and scrolled the card into view (via the default `<a href>` behavior) but did not copy. The new handler covers both transports: `navigator.clipboard.writeText` on `http(s)://` (e.g. via `claudit serve`) and a `<textarea>` + `execCommand('copy')` fallback on `file://` pages where the Clipboard API is blocked. The `#` briefly flips to `✓` on success.

## [1.1.0] — 2026-05-17

### Added

- **`claudit watch` upgraded to a load-bearing live monitor.** Full-screen TUI with three stacked rounded-corner panels (TOTALS / LIVE / ALERTS) on a TTY; one-line stream fallback when piped.
  - **TOTALS panel** shows rolling today / week / month spend, pre-scanned from `~/.claude/projects/` at startup and updated incrementally as turns land.
  - **LIVE panel** shows currently-active sessions. `--all` tails every recently-modified session (last 15 min) concurrently, grouped by project, with a two-line layout: project heading (aggregate when multiple sessions) followed by indented detail row(s). Idle sessions auto-hide.
  - **ALERTS panel** surfaces budget crosses (`--budget`) and per-turn cost spikes (`--spike-threshold`, default 5× the rolling median of the prior 20 turns). Spike detection dedupes against the immediately-preceding turn so back-to-back identical-cost rows from Claude Code's wire pattern only fire once.
  - **`--notify`** sends a desktop notification on budget crosses and spikes (macOS / Linux / Windows).
  - **`--scan-days N`** (default 30) trims the rolling-totals startup scan window; smaller is faster but clamps the month total to N days. `--rolling=false` disables the startup scan entirely.
  - **Per-panel interior padding**, uppercase panel titles (TOTALS / LIVE / ALERTS), and the last-turn cell groups the tool name and per-turn cost in one parenthesized cell: `last turn: Bash (+$0.0808)`. The cost color encodes magnitude — dim under $0.05, yellow $0.05-$0.50, red ≥ $0.50.
- **`claudit serve` — local web daemon.** Long-running process that serves the HTML report at `http://127.0.0.1:8787/`, re-rendering against the freshest data on demand.
  - Background poller re-parses only files whose `(mtime, size)` changed since the last tick; idle daemons do no work.
  - Filters live in the URL query string (`?project=`, `?last=`, `?since=`/`?until=`, `?by=`, `?hotspots=`, `?sessions=`, `?redact=`), so a bookmarked URL is a bookmarked filter.
  - Loopback-only bind by default; `--bind=0.0.0.0` allowed with a startup warning (the report contains prompt text and CWD paths and has no auth).
  - `--open` (default on) launches a browser; skipped on headless hosts.
  - Diagnostic endpoints at `/_claudit/status` (JSON snapshot vitals) and `/_claudit/healthz` (liveness probe).
  - **Dashboard defaults** (different from `claudit report`): `last=7d` and `sessions=10`, keeping the page ~3 MB uncompressed / ~600 KB on the wire and the render path under 2 s on large corpora. A pill at the top of the page surfaces the narrowing with a one-click escape to the full archive. Configurable via `--last=`, `--sessions=` (daemon) or `?last=`, `?sessions=`, `?scope=all` (per-request).
  - **Silent auto-reload** every 30 s (`--reload-sec`) when new data has arrived. Deferred while the tab is hidden, while any `<details>` is open, or while there's been mouse/keyboard/scroll activity in the last 10 s. After 5 min of pile-up, a bottom-right toast offers manual reload.
  - **Performance.** Gzip when accepted (~25× for the default view, ~3× for `scope=all`). Bounded LRU (`--cache=N`, default 16) keyed on `(canonical-query, snapshot-generation)` serves repeat hits in <10 ms; old-generation entries pruned on insert.
- **Sessions drill-down view** in the HTML report. New "Sessions" tab in the nav (between Cost and Cache) listing top sessions by cost.
  - Open a session → user prompts in order; open a prompt → the assistant turns it produced, with per-turn model, tokens, cost, and tool chips.
  - `--sessions=N` on `claudit report` (default 50; `--sessions=0` disables).
  - `--redact` replaces prompt bodies with `[redacted N chars]` before sharing.
- **Cross-links into Sessions view** from prompt hotspot cards and "Top expensive prompts" table rows. "view session →" buttons jump to the Sessions drill-down with the originating session card and prompt block pre-expanded.
  - Disabled (with a tooltip) when the prompt's session falls below the `--sessions=N` cap.
  - Survives `--redact` because the link key is computed from raw prompt text, not the displayed body.
- **Deep-link anchors** on hotspot and session cards in the HTML report. Each card carries a small `#` link in its summary that copies a shareable URL (`#overview/hotspot-3`, `#sessions/session-<sid>`); loading the URL opens the card and scrolls it into view. Bare anchors (`#hotspot-3`, `#session-abc`) also route to the right view automatically.
- **Anomaly callouts on the trend chart.** Buckets whose cost spikes above 2× the trailing 7-bucket median, or whose cache hit ratio falls more than 20 pp below the same window, are flagged inline.
  - Chart dot enlarged and colored coral, with a marker label showing the multiplier or pp-gap; hover tooltip gains a flagged line.
  - Markdown reports gain an `## Anomalies` section under the totals; JSON gains an `anomalies` array.
  - Renders in all three output modes once there are ≥8 trend buckets to baseline against.
- **Print stylesheet** for the HTML report. Saving as a PDF (Cmd-P) produces a usable single-document copy: every `<details>` body is force-expanded, the sidebar is hidden, the panel flows full-width, dark mode is overridden with a light palette, interactive chrome (filter inputs, tooltips, copy buttons) is hidden, and each top-level section starts on a fresh page.
- **`claudit diff --html`** renders the comparison as a self-contained HTML document with side-by-side A/B bars, totals tiles with delta lines, and a new-hotspots grid. Uses the same design tokens as the main report.
- **`claudit diff` with no arguments** defaults to the last 7 days vs the prior 7 days via a new `--by=week|month` flag (`--by=month` → 30d vs 30d). Equal-size rolling windows ending at midnight tonight; labels say "7 days" rather than "this week" to match the rolling math. Explicit `--a`/`--b` still wins when provided.

### Changed

- **Faster startup for windowed queries.** `claudit report --since=` / `--last=`, `claudit diff`, and `claudit watch`'s rolling-totals scan now mtime-skip JSONL files whose last modification predates the query window — those files can't contain a turn newer than the cutoff, so opening them is wasted I/O. On a 7700-file `~/.claude/projects` tree, `claudit report --last=1d` drops from ~7.7s to ~0.75s (~10×); `claudit diff --by=week` from ~7.7s to ~1.1s (~7×). Unbounded `claudit report` (no `--since`/`--last`) is unchanged. Watch's rolling-totals scan also gains parallel parse via the shared GOMAXPROCS worker pool that `report` and `diff` already use.

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

[Unreleased]: https://github.com/kurofune/claudit/compare/v1.4.2...HEAD
[1.4.2]: https://github.com/kurofune/claudit/compare/v1.4.1...v1.4.2
[1.4.1]: https://github.com/kurofune/claudit/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/kurofune/claudit/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/kurofune/claudit/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/kurofune/claudit/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/kurofune/claudit/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/kurofune/claudit/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/kurofune/claudit/releases/tag/v1.0.0
