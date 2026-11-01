# DECISIONS

Settled choices and why. On settled questions this file beats every other doc;
a contradicting doc is a cleanup target. Supersede, don't delete.

## Decided

| Decision | Why | Evidence |
|---|---|---|
| claudit is a **session auditor**, not a spend report | The vision is auditing what agents *did* (errors, retries, blast radius), with cost one dimension among many | v1.6.0 reframe; `docs/agents-audit-roadmap.md` preamble |
| TDD (Kent Beck red-green-refactor) for all backend and frontend-logic code; browser verification for UI | Written knowing it would sometimes feel slow; no implicit override | `~/.agents/AGENTS.md` § Testing Policy (repo stub removed 2026-09-20) |
| **Superseded 2.0.0 →** *claudit is a local daemon + TUI*, below. ~~The Agents view is **serve-only** — no static-report parity~~ | Live data, on-demand I/O fetches, and payload size don't fit a one-shot file | `docs/agents-audit-roadmap.md` conventions; README "Status and limitations" (pre-2.0.0) |
| claudit is a **local daemon + TUI** (`serve`, `watch`); **no file outputs** | The Agents view, the product's centre, never fit a one-shot file (row above); the v2 plan cut the file outputs rather than keep two surfaces | CHANGELOG 2.0.0; `cmd/claudit/main.go` `topLevelUsage` |
| Per-view **local filters**; the global filter bar is removed | The global bar had no wiring and falsely advertised filtering everything | v1.6.0 changelog; `docs/filter-migration-plan.md` |
| Spend **dedups by `message.id`** via a deterministic `ReplaySet`; every drill-down must reconcile to the headline | Resumed/forked sessions replay turns into new files; per-file counting inflated totals (−53M tokens / −291 turns on the real corpus) | v1.7.0 changelog |
| **Date-effective pricing**: each turn priced at the rate in effect when it ran (`rates:` periods, inclusive UTC `until`) | One flat number cannot serve both historical and future turns across a rate change | v1.8.0 changelog; the motivating case (Sonnet 5 cliff) was later cancelled — see log 2026-09-01 |
| Fast-mode pricing reads `message.usage.speed`; only `"fast"` selects an optional flat `fast:` rate, otherwise standard date-effective rates apply | Fast turns need per-turn pricing; overlays still replace whole model entries | [Pricing selection](internal/pricing/pricing.go); [rate tests](internal/pricing/pricing_test.go); claudit-5wb |
| Pricing loader is **overlay-style**: bundled defaults always load; `~/.config/claudit/prices.yaml` overlays per-model, replacing an entry entirely | The old copy-on-first-run pinned users to their first install's prices forever | v1.4.3 changelog |
| Date filters resolve in **local time** at midnight | UTC boundaries shifted windows for non-UTC users and left serve internally inconsistent | v1.4.0 changelog |
| **Desktop-only** — no responsive/mobile pass | Dogfooding tool for a desktop workflow | Owner ruling (memory: no-mobile-pass) |
| **Superseded 2.0.0 →** *Issue tracker is beads*, below. ~~Issue tracker is **GitHub Issues** via `gh`; five-role triage labels~~ | Wired 2026-08-15 | `docs/agents/issue-tracker.md` (deleted in 42ee087); commit 131618a |
| Issue tracker is **beads** (`bd`); see AGENTS.md. Five-role triage labels stay, as bd labels | Issues live in the local bd DB with the work; the GitHub tracker doc was deleted | AGENTS.md § Agent skills / Issue tracker; `docs/agents/triage-labels.md`; commit 42ee087 |
| A deleted cwd **joins a repo only on filesystem evidence** (its nearest surviving ancestor is in that repo); otherwise it is a `missing` bucket named after its own basename — never reassigned to a repo by a similar name | A name match guesses: `the-pit-9hw-fast` reads like a the-pit worktree, but nothing on disk proves it, and a guess would misattribute spend to a repo. Cost: removed worktrees stay one row each, so the real corpus shows 637 by-project rows, not the under-60 claudit-apd expected | `resolveDir` in `internal/project/project.go`; `TestResolve_MissingIgnoresSimilarlyNamedRepos` in `internal/project/project_test.go`; log 2026-10-31 (claudit-80r) |
| **Rejected** (2026-07-03 audit — do not re-propose): Sankey diagram, cost treemap/icicle, config file, incremental/streaming aggregation, framework/bundler migration, multi-session stacked Gantt | Each evaluated against the trace-audit vision and rejected (e.g. Sankey answers a flow question, not a trace question) | 2026-07 principal-engineer audit (memory: audit-2026-07-rejected-ideas) |

## Open questions (NOT settled)

- **Deferred Phase-2 follow-ups** (drafted in the roadmap, never scheduled):
  query-DSL filter form; intersecting the trace filter with the playhead window.
- **Annotations phase** — explicitly deferred in `docs/agents-audit-roadmap.md`.

## Log

| Date | Change |
|---|---|
| 2026-08-16 | File scaffolded (Project State Protocol adopted). Rows above reconstructed from CHANGELOG.md, docs/, and session memory — each row links its primary source. |
| 2026-09-01 | Pricing refreshed against the live page. Added Fable 5.1 / Mythos 5.1 (+`[1m]`) at $10 / $50 with **cache hits at $0.25/MTok** — a 0.025x multiplier, the first model card that breaks the 0.1x cache-read ratio. Removed Sonnet 5's dated rate period: Anthropic cancelled the 2026-09-01 increase to $3 / $15 and made the introductory $2 / $10 standard, so an un-upgraded claudit over-reports Sonnet 5 spend by 50% from that date. Date-effective pricing stays — no bundled model needs it today. Evidence: https://platform.claude.com/docs/en/about-claude/pricing (fetched 2026-09-01); `internal/pricing/default.yaml`. |
| 2026-09-23 | v1.8.2: added Opus 5.5 (+`[1m]`) at $4 / $20, writes $5 / $8, **cache hits $0.20/MTok** — 0.05x input, the second card off the 0.1x ratio. Before this every Opus 5.5 turn priced at $0 (exact-id lookup, `internal/pricing/pricing.go:92`). All other rates re-checked, unchanged. Evidence: https://platform.claude.com/docs/en/about-claude/pricing (fetched 2026-09-23); `internal/pricing/default.yaml`; claudit-dkv. |
| 2026-09-24 | Settled the former fast-mode open question: parse usage speed and price fast turns using each model's optional `fast:` block, including cache rates. Added Opus 5.5 / 5 / 4.8 and their `[1m]` variants. Standard-rate periods remain standard-only; absent fast blocks fall back to standard pricing. Evidence: [pricing tests](internal/pricing/pricing_test.go), [JSONL integration test](internal/aggregate/aggregate_test.go), [bundled rates](internal/pricing/default.yaml); claudit-5wb. |
| 2026-10-31 | v2.0.0: superseded "The Agents view is serve-only" with "claudit is a local daemon + TUI (serve, watch); no file outputs" — report, diff, `--redact` and the static SPA mode are removed. Superseded "Issue tracker is GitHub Issues via gh" with "Issue tracker is beads (bd); see AGENTS.md" — the GitHub tracker doc was deleted in 42ee087 and AGENTS.md names beads. Evidence: CHANGELOG 2.0.0; `cmd/claudit/main.go`; AGENTS.md; claudit-swk. |
| 2026-10-31 | Real-corpus by-project count, re-measured with the name-based reassignment removed: `claudit serve --open=false`, `GET /_claudit/api/cost?scope=all \| jq '.by_project\|length'` returns **1540** at 19f22b0 (before claudit-apd) and **637** at 653aadf (635 with `--codex-root=`). The 637 rows are 13 repos, 1 plain dir and 621 `missing` buckets. The cwds behind those buckets are deleted: 572 removed summoner worktrees under `$TMPDIR/summoner-worktrees-*` (434 buckets named `summoner-*`) and 256 removed the-pit worktrees under `~/Projects/OutcastGames/` (81 `the-pit*` buckets). They make up $4,165 of the $10,142 total. The under-60 expectation assumed deleted worktrees fold into their repo; they keep their own buckets because the missing-path rule (row above) is kept. Supersedes the 1498→46 measurement, which used the removed heuristic. Evidence: claudit-80r; claudit-apd. |
