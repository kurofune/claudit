# CURRENT

**Mode:** Steady-state maintenance. No active initiative — both roadmaps
(`docs/agents-redesign.md`, `docs/agents-audit-roadmap.md`) are fully shipped.

**Goal:** Keep claudit correct against upstream reality: pricing current with
Anthropic's published rates, the parser current with Claude Code's JSONL schema.
Ship fixes and small features as they earn their place.

**Last change:** v1.8.0 (2026-07-26) — date-effective pricing (each turn priced
at the rate in effect when it ran), Opus 5 rate card, quantified unpriced-model
warnings. Since then: docs/tooling only (AGENTS.md split, skill wiring, LSP rules).

**Assumptions:**
- The Sonnet 5 introductory→standard transition (2026-09-01) is already encoded
  as a dated rate period in the bundled table; no release is needed at the cliff.
- Fast-mode turns (Opus 5 / 4.8 `speed` field) price at the standard rate — a
  known, documented gap (README "Status and limitations").

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** After 2026-09-01 — spot-check that Sonnet 5 turns on both
sides of the cliff price correctly on the real corpus.

**Stop conditions:**
- Claude Code changes the JSONL schema → parser catch-up becomes the priority.
- Anthropic publishes new model rates → refresh `internal/pricing/default.yaml`
  and cut a release.
