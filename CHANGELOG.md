# Changelog

All notable changes to claudit are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project adheres to [Semantic Versioning](https://semver.org/).

## [1.8.0] — 2026-07-26

This release gives the price table a **time dimension**: each turn is priced at the rate that was in effect when it ran, rather than at today's rate. That closes a silent-error class arriving on 2026-09-01, when **Claude Sonnet 5**'s introductory pricing expires — until now there was no correct value to put in the table, since one number had to serve both historical and future turns. It also adds **Claude Opus 5** (previously missing, and therefore free), and makes the unpriced-model warning report what the gap is actually costing you.

### Changed

- **`unknown_models` changed shape in JSON output — update any script that reads it.** It was an array of model-name strings; it is now an array of objects, `{"model": "…", "turns": N, "tokens": N}`, sorted by unpriced token volume descending. This affects both `claudit report --json` and the `/_claudit/api/overview` endpoint. A script reading `.unknown_models[]` should now read `.unknown_models[].model`.
- **Sonnet 5 turns dated 2026-09-01 or later price at the standard $3/$15 automatically.** The bundled entry now carries the standard rate, with the introductory $2/$10 recorded as a dated period ending 2026-08-31. Existing spend is unaffected — July and August turns still price at the introductory rate they were actually billed at — and no upgrade is needed when the cliff passes.

### Added

- **Date-effective pricing.** A model entry may now carry an optional `rates:` list of historical periods alongside its current rate:

  ```yaml
  claude-sonnet-5:
    input_per_mtok: 3.00          # the rate in effect now
    output_per_mtok: 15.00
    rates:
      - until: 2026-08-31         # inclusive, UTC — introductory pricing
        input_per_mtok: 2.00
        output_per_mtok: 10.00
  ```

  Each turn is priced by its own timestamp. `until` is inclusive through 23:59:59 UTC on that date; periods may be listed in any order (the narrowest one covering the turn wins), so there is no oldest- or newest-first convention to get wrong; and a turn with no usable timestamp falls back to the current rate. The key is optional and purely additive — existing flat `~/.config/claudit/prices.yaml` overlays keep working unchanged, and per-model replacement still swaps the whole entry, rate history included.

- **Pricing: Claude Opus 5.** `claude-opus-5` and `claude-opus-5[1m]` were absent from the bundled table, so those turns landed in `unknown_models` and contributed **nothing** to reported spend. Added at the Opus 4.8 rate card ($5 / $25 base; cache 0.50 / 6.25 / 10.00). Every other bundled rate was re-verified against the live pricing page on 2026-07-26 and none had drifted.

- **Unpriced-model warnings now quantify the gap.** Every surface that lists unpriced models — HTML, markdown, and JSON — now reports the turn count and token volume behind each one, worst first, and says plainly that those turns count as $0 and the totals are therefore understated. Three unpriced turns and thirty thousand are very different problems, and the old warning couldn't tell them apart.

- **An unpriced-model warning on stderr.** The default HTML path writes the report to stdout and previously said nothing in the terminal, so a stale price table surfaced only as totals that were quietly low. `claudit report` now prints the affected models and their volume to stderr.

### Fixed

- **Flaky `claudit watch` painter test under `-race` on Windows.** The Windows resize watcher polls `TerminalSize` on a ticker — Unix uses `SIGWINCH` and has no polling goroutine — so the test's deferred pipe close raced with that read and intermittently failed the `windows-latest` CI leg. Teardown now drains the reader to unpark the parked paint goroutine, so the painter stops the watcher before the pipe closes. Test-only; `watch` behavior is unchanged.

## [1.7.0] — 2026-07-15

This release grows the **Agents** trace viewer from a flat Gantt into a genuine trace waterfall — a session narrative strip, prompt-segmented bands, nested sub-agent rows, and a where-did-the-time-go breakdown — and corrects a class of **cost double-counting** for resumed/forked sessions so headline spend and every drill-down now reconcile exactly. It also adds **Claude Sonnet 5** and legacy pre-Opus-4.5 pricing, ships **prebuilt release binaries**, and hardens `claudit serve`'s memory footprint.

### Changed

- **Reported cost drops for anyone with resumed or forked sessions.** A resumed or forked session replays the prior transcript into a new JSONL file — same `message.id` and identical usage, but a fresh `uuid`/`sessionId` — and the old per-file dedup billed every replayed generation once per file it appeared in. Spend is now deduplicated by `message.id` at every aggregation point, so if you resume or fork sessions your totals will read **lower than before** (they were previously inflated). This is a correction, not a regression — see the three Fixed entries below for the mechanics. Single-session raw traces still show a forked session's full replayed context.

### Added

- **Timeline is now a trace waterfall.** The flat Gantt became a nested, prompt-segmented waterfall over three changes:
  - **Prompt bands.** Each prompt's turns render inside a labeled full-height band, so you can see which prompt a stretch of activity belongs to. Segmentation reuses the same boundaries as the Conversation lens, so the two always agree.
  - **Nested sub-agents.** Sub-agent rows now re-parent under the exact turn that spawned them (via `parent_tool_use_id`), with connector elbows and real depth indent — grandchildren nest one level deeper.
  - **Progressive disclosure.** Rows collapse by default (agent bar → turn spans → tool sub-spans), dropping a 13-agent / 754-tool session from ~1,300 always-on SVG nodes to ~210. Expansion state survives live repaints, and a cross-lens jump auto-discloses its target.
- **Session narrative strip.** Above each session's waterfall, one clickable row per prompt segment shows the prompt snippet, turn/tool counts, spawned-agent chips with a pass/fail glyph, duration, and cost. Clicking scrolls the waterfall to that prompt's band and flashes it. Sub-agents attribute wholly to the segment that spawned them (spawn chain walked to the root prompt, time-containment as fallback); the outcome glyph reads the same `error_count` the waterfall's error pips do, so the strip can never disagree with it.
- **Where-did-the-time-go rollup.** Each Timeline session card gains a stacked generation / tool / idle time bar (colors shared with the waterfall segments), and a new **Insights → Time** panel breaks the same split down per session (graph scope) or per agent, top-10 with an overflow note. Sub-agent waterfall rows also gain a pass/fail outcome chip.
- **Full turn reasoning and message on demand.** The detail drawer's Reasoning and Message fields get the same show-full affordance tool I/O already has — the untruncated thinking and narration stream from the session JSONL on request, and stay sticky across live repaints. Static reports degrade to the snippet-only note.
- **Pricing: Claude Sonnet 5.** `claude-sonnet-5` and `claude-sonnet-5[1m]` added at the introductory $2/$10 base rates (in effect through 2026-08-31; a comment flags the revert to $3/$15 on 2026-09-01). Verified against the pricing page on 2026-07-03.
- **Pricing: legacy pre-Opus-4.5 rates.** Sessions from before ~late 2025 were pricing at $0 because the exact-match table had no entries for the dated legacy ids. Added Opus 4.1/4 ($15/$75), Sonnet 4 ($3/$15), Haiku 3.5 ($0.80/$4), and the retired 3.x models at their last published rates (marked historical).
- **Prebuilt release binaries.** Every `v*` tag now attaches cross-compiled archives for darwin / linux / windows on amd64 / arm64 to the GitHub Release — no local `go build` required.
- **`net/http/pprof` endpoints on the serve mux.** Registered explicitly on the server mux (not `http.DefaultServeMux`); loopback-only bind is already the default and the report already exposes prompt text, so this adds no new exposure class.

### Fixed

- **Resumed/forked replays no longer double-count in the headline.** Dedup by `message.id` at each cost/token aggregation point — report, serve, diff, and the `watch` rolling-totals and `watch --all` combined panels. On the real corpus this removed exactly the duplicated volume (−53M tokens, −291 turns). Legacy single-line transcripts with an empty `message.id` can't be keyed and always count.
- **Drill-downs now reconcile with the deduped headline.** The per-session (`BuildSessionTimelines`) and Agents (`BuildAgentGraph`) views still summed straight from raw turns, so expanding either showed spend that didn't match the headline (+291 turns / +$36 on the corpus). A new deterministic `ReplaySet` — the lexicographically-smallest source file is canonical, the rest are flagged replays — is now shared by both builders; every drill-down reconciles to the penny.
- **Per-session attribution is now deterministic across views.** The headline aggregator deduped by first-`Add`-order, but snapshot turn order is non-deterministic (concurrent parse), so a forked session's cost could land on a different session in the Cache-by-session view than in the drill-downs, and shift run-to-run. The aggregator now dedups via the same `ReplaySet` the drill-downs use, making per-session/project attribution stable and consistent everywhere. (Also disclosed in the Tools guide: per-tool cost attribution overlaps by design — a multi-tool turn counts under each tool, so the column sums to more than total spend.)
- **`claudit serve` memory no longer balloons on large corpora.** Two leaks: the render cache evicted by entry-count only while its `(query, section, generation)` keys stranded every older-generation entry as permanently-unreachable dead weight — on a corpus where the Agents payload is ~186 MB per entry, a handful of stranded entries pushed RSS past 3 GB. The cache now tracks total resident bytes and evicts to a byte budget (default 256 MiB) in addition to the count cap, and each store eagerly sweeps entries from older generations. Additionally, turn thinking/text and prompt text are now capped to 2000-rune snippets in the snapshot and on the wire (the dominant share of that 186 MB payload), with the full text one request away. And serve mode now sets a 1.5 GiB soft memory limit (`debug.SetMemoryLimit`) so Go returns cached-payload headroom to the OS; an explicit `GOMEMLIMIT` is respected.
- **`--bind` values that already carry a port are rejected up front.** `--bind 127.0.0.1:8791` used to be silently composed into `127.0.0.1:8791:8787` and fail at listen time with a confusing too-many-colons error (and a false non-loopback warning). It's now validated right after flag parsing and points you at `--port`. The listen address is composed with `net.JoinHostPort`, so bare and bracketed IPv6 hosts both keep working.
- **Client-canceled API requests are no longer logged as errors.** A browser fires one request per report section and cancels them on reload/navigation; that surfaced as `context.Canceled` logged at ERROR with a 500 written to an already-closed socket. Those are now swallowed — no ERROR log, no 500.
- **SDK badge standardized.** The SDK-origin pill is pinned to the top-right of the Tree/Timeline/Conversation left session menus and dropped from the Timeline Gantt header (it was shown twice); the Insights token-composition bar is reordered to the canonical input → output → cache-write → cache-read.

## [1.6.0] — 2026-06-15

This release reframes claudit from a spend report into a **session audit**: the new **Agents** view is a full trace viewer for what your agents actually did — every turn, tool call, sub-agent, and anomaly — alongside the existing cost/token/cache accounting.

### Added

- **The Agents view — a full session trace viewer.** A new top-level tab turns the raw `.jsonl` transcript into something you can read and audit, with five lenses over one shared selection and detail drawer:
  - **Feed** — a live, newest-first stream of agent activity; currently-running agents pin to the top as sticky "live" rows and update in place every couple of seconds in `claudit serve` (no reload; your scroll, selection, and open panels stay put).
  - **Tree** — the drill-down: pick any agent and read its step-by-step tool log, with sub-agents nested under the call that spawned them, per-turn reasoning, and the exact input each tool sent and the output it got back (✓/✗). Cost and error counts roll up onto parent nodes and sessions.
  - **Timeline** — a Gantt on a real time axis: one row per agent, indented by who spawned whom, bar width = lifetime, overlap = concurrency. Bars are tiled into per-turn segments colored by tool kind, with inline duration/cost labels and cost-heat shading, idle-gap and critical-path marks, a draggable **playhead scrubber** that replays trace state at any instant T, and cursor-anchored scroll-wheel zoom.
  - **Conversation** — the prompt-by-prompt thread for a single session, with a session picker.
  - **Detail drawer** — whatever you click (agent, turn, or tool) fills a resizable right-hand panel: input, output, status, reasoning, tokens, cost, model, duration. Full tool I/O loads on demand from disk and stays sticky across live repaints.

  Backing this, the parser/aggregator now capture per-turn reasoning and model generation time (`gen_ms`), join tool outcomes and per-tool wall-clock into the trace, tag every tool call with a `ToolKind`, record per-turn context tokens and tool-I/O size, and resolve sub-agent spawns to navigable parent/child links with cost rollups and post-error retry-chain detection.

- **Insights lens — an analytical dashboard over the trace.** Each section is its own tab: **Signals**, **Tool mix**, **Cost Pareto**, **Latency**, **Errors**, **Token & context**, and **Group by** (kind/model/agent/status). A Graph / Session / Agent scope toggle re-slices every panel except the graph-wide Signals.

- **Signals — automatic anomaly detection.** The Signals tab surfaces a worst-first list of flagged anomalies — cost whales, retry storms, slow tools, error cascades, idle stalls, and runaway context growth — each click-through jumping to the offending spot on the Timeline; the Timeline also draws inline signal pips on the affected rows.

- **Per-view filter bars.** Cost, Tokens, Cache, and Tools each gained their own local filter bar, and the Agents view has a trace filter with cross-lens dimming — replacing the single global filter that silently claimed to filter everything.

- **Tokens view breakdown tabs.** Token usage now breaks down **by model / project / skill / prompt / subagents**.

- **Pricing: Fable 5 and Mythos 5 rate cards** added to the bundled defaults.

### Changed

- **Subagents moved into a Cost subtab.** The standalone Subagents view is gone; its content is now a tab under Cost. Cost and Tokens breakdown tabs were relabeled with a consistent `By …` prefix.
- **Custom tooltip popover everywhere.** Native `title=`/SVG tooltips were replaced with a single styled popover (it renders `code` spans, escapes HTML, and clamps to the viewport); wide targets anchor the tooltip to the cursor.

### Removed

- **The Sessions view** has been removed — it is superseded by the Agents trace viewer, which covers the same "what happened in this session" question with far more depth. Its now-dead modules, fetch helpers, and the legacy `#sessions/…` route were removed.
- **The global floating filter bar** was removed from the static report shell — it had no wiring and falsely advertised filtering every section. Use the per-view filters instead.

### Fixed

- **Assistant lines sharing a `message.id` now coalesce into one turn** in the parser, so a streamed multi-part assistant message is counted and displayed as a single turn instead of several.
- **Faster full-corpus load:** each JSONL line is now decoded once during the initial parse instead of repeatedly.
- **Signals click-through** reliably lands on the Timeline, and the Timeline's "+N" signal-pip overflow opens the Insights → Signals tab.

## [1.5.0] — 2026-05-30

### Added

- **The Sessions view now separates interactive from headless (SDK) runs and shows what each one did.** Headless `claude -p` / Agent-SDK sessions log to `~/.claude/projects/` exactly like interactive ones, but every card looked the same — an opaque UUID with a turn count — so a project driven by a headless harness (many short `sdk-cli` sessions) was impossible to tell apart or read. Three changes fix that: (1) the parser now reads each line's `entrypoint` and lifts it to the session, surfaced as an **All / Interactive / SDK** tab strip (with per-tab counts) plus an origin badge on every card — SDK runs get the brand accent so they pop; (2) each card shows a one-line preview of its kickoff prompt, so the list reads as "what each run was asked to do" instead of a wall of UUIDs; (3) turns that fired a `Bash`, `Agent`/`Task`, `Skill`, `SlashCommand`, or `WebFetch` call now retain a bounded snippet of the actual input (the full command, the prompt handed to a subagent, …), revealed by expanding the turn — the chip still shows the coarse bucket (`Bash · awk`), the expansion shows the real `awk '…' docs/X.md`. Input capture is capped at 2k chars per call and distinct inputs no longer collapse together; the prompt preview is redaction-aware. The origin classification, route parsing, and tab filtering are factored into a pure `web/sessions-logic.js` with jstest coverage; the legacy `#sessions/session-{id}` deep-link still opens, and new copy-links round-trip the active tab (`#sessions/{tab}/session-{id}`).
- **Single-day views now break charts down by hour instead of hiding them.** Picking the same start and end date in `claudit serve` used to collapse the trend to one daily bucket, so every chart showed "Only one day of data — chart hidden." A same-day window now switches to hourly buckets: the cost, hit-ratio, and token-volume charts run from local midnight to the current hour (gap-filling empty hours), and the per-row trend sparklines on the cost/cache/tools tabs follow suit. Bucketing happens in Go (`PeriodHour`, truncated in local time so hours line up with the user's wall clock); the overview and tokens payloads now ship the bucket granularity so the SPA labels the axis on a compact 12-hour clock (`12a, 1a, … 12p, … 11p`). An explicit `?by=` still wins, and multi-day windows are unchanged.

### Fixed

- **`--redact` no longer leaks Bash commands and subagent prompts through tool inputs.** The new per-turn tool-input capture (above) retains the full command, the prompt handed to an `Agent`/`Task` subagent, the `WebFetch` URL, etc. — but `--redact` only ever masked the prompt body, so a report generated for sharing still embedded every captured `ToolInvocation.Input` verbatim in its data payload. Redaction now applies to tool inputs too: each non-empty `Input` becomes the same `[redacted N chars]` length-echoing marker used for prompt text (empty inputs stay empty, and the coarse `Detail` bucket like `Bash · git commit` is kept — it carries no content). Distinct inputs are still deduplicated on their real value *before* redaction, so two different commands of the same length don't collapse into one row. `first_prompt` was already safe (it reads the already-redacted prompt text).
- **`claudit serve` no longer serves a stale cached section after a filter change.** The API ETag mixed only the snapshot generation and the section name, so two requests with different filters (`--project`, date window, `scope`) at the same generation produced an identical ETag. A browser holding one filter's section body would revalidate against another filter's URL, get a `304 Not Modified`, and render the wrong cached payload — e.g. an old unpriced-model warning surviving across a filter change. The canonical query string is now hashed into the ETag so distinct filters get distinct ETags; the render-cache keys already keyed on that same canonical query, so no other invalidation path shifts.
- **`claudit serve` actually auto-reloads when new data arrives.** The SSE-driven silent reload promised in the v1.3.0 changelog never worked: the toast wiring set the `hidden` attribute on the toast element, but the CSS hard-coded `display: none` and only revealed via a `.is-visible` class, so the toast never appeared — and there was no silent reload path either. The page just went stale until a manual refresh. Replaced with a real silent-auto-reload loop that watches the `/events` stream and reloads the page as soon as new data lands, deferred while the tab is hidden, while any `<details>` is open, or within 10s of mouse / keyboard / scroll / touch input. After 5 minutes of unsafe-to-reload pile-up it gives up on silent reload and surfaces the toast (now correctly shown) for manual reload. The decision logic is factored into a pure `decideReload(state)` with per-branch jstest coverage.
- **Auto-reload now floors at 15s between reloads.** Without a floor, the corpus poller's ~2s tick translated into the dashboard reloading every ~2s while any session was actively writing turns — distracting on a tab you're reading, wasteful on one you're not. The page must now have been on screen at least `MIN_RELOAD_INTERVAL_MS` (15s) before being replaced. The 5-minute pile-up toast still wins over the floor, so a pile-up-blocked tab still surfaces the toast on schedule. `claudit watch`'s ticker-tape cadence is unaffected — it has its own loop.

## [1.4.3] — 2026-05-29

### Fixed

- **Bundled pricing updates now actually reach users who have run claudit before.** `Load` previously wrote the embedded default YAML to `~/.config/claudit/prices.yaml` on first run and from then on read only that file — so any user who'd ever run claudit was permanently pinned to the prices that shipped on their first run, and bundled-pricing refreshes in later releases (e.g. the Opus 4.8 entry added in v1.4.2) silently never took effect for them. The pricing loader is now overlay-style: it always starts from the bundled defaults, and if `~/.config/claudit/prices.yaml` exists it overlays the user file's entries per-model on top. A model the user defines fully replaces the bundled entry for that name; bundled entries the user didn't touch stay intact; user entries for new models are added. The file is no longer auto-created on first run. Users with custom rates (enterprise discounts, private-preview models) keep their overrides exactly as before; everyone else now tracks every release.

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

[Unreleased]: https://github.com/kurofune/claudit/compare/v1.8.0...HEAD
[1.8.0]: https://github.com/kurofune/claudit/compare/v1.7.0...v1.8.0
[1.7.0]: https://github.com/kurofune/claudit/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/kurofune/claudit/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/kurofune/claudit/compare/v1.4.3...v1.5.0
[1.4.3]: https://github.com/kurofune/claudit/compare/v1.4.2...v1.4.3
[1.4.2]: https://github.com/kurofune/claudit/compare/v1.4.1...v1.4.2
[1.4.1]: https://github.com/kurofune/claudit/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/kurofune/claudit/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/kurofune/claudit/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/kurofune/claudit/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/kurofune/claudit/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/kurofune/claudit/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/kurofune/claudit/releases/tag/v1.0.0
