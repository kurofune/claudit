# CURRENT

**Mode:** Steady-state maintenance. No active initiative — both roadmaps
(`docs/agents-redesign.md`, `docs/agents-audit-roadmap.md`) are fully shipped.

**Goal:** Keep claudit correct against upstream reality: pricing current with
Anthropic's published rates, the parser current with Claude Code's JSONL schema.
Ship fixes and small features as they earn their place.

**Last change:** 2026-10-24 (unreleased on main) — Haiku 5.5 added with its >100K-token prompt tier; Sonnet 5.5 cache reads $0.20 → $0.10 from 2026-10-07; rest of the pricing page re-checked, unchanged.

**Assumptions:**
- Sonnet 5.5 is the first bundled model with rate history (`rates:` until 2026-10-06); Haiku 5.5 is the first with a prompt-length tier (`long_prompt:`). Users on v1.9.1 or earlier price Haiku 5.5 at $0 and over-report Sonnet 5.5 cache reads.

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** Cut v1.9.2 with the two pricing fixes. Next trigger is upstream: new Anthropic
rates, or a Claude Code JSONL schema change.

**Stop conditions:**
- Claude Code changes the JSONL schema → parser catch-up becomes the priority.
- Anthropic publishes new model rates → refresh `internal/pricing/default.yaml`
  and cut a release.
