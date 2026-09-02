# CURRENT

**Mode:** Steady-state maintenance. No active initiative — both roadmaps
(`docs/agents-redesign.md`, `docs/agents-audit-roadmap.md`) are fully shipped.

**Goal:** Keep claudit correct against upstream reality: pricing current with
Anthropic's published rates, the parser current with Claude Code's JSONL schema.
Ship fixes and small features as they earn their place.

**Last change:** 2026-09-01 pricing refresh (unreleased) — Fable 5.1 / Mythos 5.1
rate cards added (cache hits at 0.025x input, not 0.1x); Sonnet 5's 2026-09-01
increase was cancelled upstream, so its dated rate period is removed and it is a
flat $2 / $10. Before that: v1.8.0 (2026-07-26) date-effective pricing, Opus 5.

**Assumptions:**
- No bundled model needs rate history right now; the mechanism stays because the
  next real rate change will. An un-upgraded claudit over-reports Sonnet 5 spend
  from 2026-09-01 by 50%, so this refresh wants a release.
- Fast-mode turns (Opus 5 / 4.8 `speed` field) price at the standard rate — a
  known, documented gap (README "Status and limitations").

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** Cut a release carrying the 2026-09-01 pricing refresh.

**Stop conditions:**
- Claude Code changes the JSONL schema → parser catch-up becomes the priority.
- Anthropic publishes new model rates → refresh `internal/pricing/default.yaml`
  and cut a release.
