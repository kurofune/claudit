# DECISIONS

Settled choices and why. On settled questions this file beats every other doc;
a contradicting doc is a cleanup target. Supersede, don't delete.

## Decided

| Decision | Why | Evidence |
|---|---|---|
| claudit is a **session auditor**, not a spend report | The vision is auditing what agents *did* (errors, retries, blast radius), with cost one dimension among many | v1.6.0 reframe; `docs/agents-audit-roadmap.md` preamble |
| TDD (Kent Beck red-green-refactor) for all backend and frontend-logic code; browser verification for UI | Written knowing it would sometimes feel slow; no implicit override | `.claude/rules/testing.md` |
| The Agents view is **serve-only** — no static-report parity | Live data, on-demand I/O fetches, and payload size don't fit a one-shot file | `docs/agents-audit-roadmap.md` conventions; README "Status and limitations" |
| Per-view **local filters**; the global filter bar is removed | The global bar had no wiring and falsely advertised filtering everything | v1.6.0 changelog; `docs/filter-migration-plan.md` |
| Spend **dedups by `message.id`** via a deterministic `ReplaySet`; every drill-down must reconcile to the headline | Resumed/forked sessions replay turns into new files; per-file counting inflated totals (−53M tokens / −291 turns on the real corpus) | v1.7.0 changelog |
| **Date-effective pricing**: each turn priced at the rate in effect when it ran (`rates:` periods, inclusive UTC `until`) | One flat number cannot serve both historical and future turns across a rate change | v1.8.0 changelog; the motivating case (Sonnet 5 cliff) was later cancelled — see log 2026-09-01 |
| Pricing loader is **overlay-style**: bundled defaults always load; `~/.config/claudit/prices.yaml` overlays per-model, replacing an entry entirely | The old copy-on-first-run pinned users to their first install's prices forever | v1.4.3 changelog |
| Date filters resolve in **local time** at midnight | UTC boundaries shifted windows for non-UTC users and left serve internally inconsistent | v1.4.0 changelog |
| **Desktop-only** — no responsive/mobile pass | Dogfooding tool for a desktop workflow | Owner ruling (memory: no-mobile-pass) |
| Issue tracker is **GitHub Issues** via `gh`; five-role triage labels | Wired 2026-08-15 | `docs/agents/issue-tracker.md`; commit 131618a |
| **Rejected** (2026-07-03 audit — do not re-propose): Sankey diagram, cost treemap/icicle, config file, incremental/streaming aggregation, framework/bundler migration, multi-session stacked Gantt | Each evaluated against the trace-audit vision and rejected (e.g. Sankey answers a flow question, not a trace question) | 2026-07 principal-engineer audit (memory: audit-2026-07-rejected-ideas) |

## Open questions (NOT settled)

- **Fast-mode pricing.** Opus 5 / 4.8 fast mode bills $10/$50 per-turn via the
  transcript's `speed` field, which claudit doesn't read; fast turns price at
  standard rates. Worth reading the field, or leave to per-user overlay?
- **Deferred Phase-2 follow-ups** (drafted in the roadmap, never scheduled):
  query-DSL filter form; intersecting the trace filter with the playhead window.
- **Annotations phase** — explicitly deferred in `docs/agents-audit-roadmap.md`.

## Log

| Date | Change |
|---|---|
| 2026-08-16 | File scaffolded (Project State Protocol adopted). Rows above reconstructed from CHANGELOG.md, docs/, and session memory — each row links its primary source. |
| 2026-09-01 | Pricing refreshed against the live page. Added Fable 5.1 / Mythos 5.1 (+`[1m]`) at $10 / $50 with **cache hits at $0.25/MTok** — a 0.025x multiplier, the first model card that breaks the 0.1x cache-read ratio. Removed Sonnet 5's dated rate period: Anthropic cancelled the 2026-09-01 increase to $3 / $15 and made the introductory $2 / $10 standard, so an un-upgraded claudit over-reports Sonnet 5 spend by 50% from that date. Date-effective pricing stays — no bundled model needs it today. Evidence: https://platform.claude.com/docs/en/about-claude/pricing (fetched 2026-09-01); `internal/pricing/default.yaml`. |
