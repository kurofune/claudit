# CURRENT

**Mode:** Steady-state maintenance. No active initiative — both roadmaps
(`docs/agents-redesign.md`, `docs/agents-audit-roadmap.md`) are fully shipped.

**Goal:** Keep claudit correct against upstream reality: pricing current with
Anthropic's published rates, the parser current with Claude Code's JSONL schema.
Ship fixes and small features as they earn their place.

**Last change:** v1.8.1 (2026-09-01) — pricing refresh: Fable 5.1 / Mythos 5.1
rate cards added (cache hits at 0.025x input, not 0.1x); Sonnet 5's 2026-09-01
increase was cancelled upstream, so its dated rate period is removed and it is a
flat $2 / $10. Before that: v1.8.0 (2026-07-26) date-effective pricing, Opus 5.

**Assumptions:**
- No bundled model needs rate history right now; the mechanism stays because the
  next real rate change will. Users on v1.8.0 or earlier over-report Sonnet 5
  spend by 50% from 2026-09-01 until they upgrade.
- Fast-mode turns (Opus 5 / 4.8 `speed` field) price at the standard rate — a
  known, documented gap (README "Status and limitations").

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** None scheduled. Next trigger is upstream: new Anthropic
rates, or a Claude Code JSONL schema change.

**Stop conditions:**
- Claude Code changes the JSONL schema → parser catch-up becomes the priority.
- Anthropic publishes new model rates → refresh `internal/pricing/default.yaml`
  and cut a release.
