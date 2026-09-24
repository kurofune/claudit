# CURRENT

**Mode:** Steady-state maintenance. No active initiative — both roadmaps
(`docs/agents-redesign.md`, `docs/agents-audit-roadmap.md`) are fully shipped.

**Goal:** Keep claudit correct against upstream reality: pricing current with
Anthropic's published rates, the parser current with Claude Code's JSONL schema.
Ship fixes and small features as they earn their place.

**Last change:** 2026-09-24 — fast-mode turns use `message.usage.speed` to
select bundled or overlay fast rates, including cache rates. Standard-rate
history remains unchanged. Previous release: v1.8.2 added Opus 5.5 pricing.

**Assumptions:**
- No bundled model needs rate history right now; the mechanism stays because the
  next real rate change will. Users on v1.8.0 or earlier over-report Sonnet 5
  spend by 50% from 2026-09-01 until they upgrade.

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** None scheduled. Next trigger is upstream: new Anthropic
rates, or a Claude Code JSONL schema change.

**Stop conditions:**
- Claude Code changes the JSONL schema → parser catch-up becomes the priority.
- Anthropic publishes new model rates → refresh `internal/pricing/default.yaml`
  and cut a release.
