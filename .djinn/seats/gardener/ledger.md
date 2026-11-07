# Ledger

## 2026-11-07 — tend-seats

- check: fired (DECISIONS.md and seat files touched by bf97b78, 167c2d0 and others in the last 8 days).
- filed 4dd4eee0674b -> claudit-df0 (lead charter.md:43-45 closes an epic once its children close; gardener tend-epics (charter.md:42-43, duties.toml:435-436) requires its own acceptance lines to hold first).
- filed 094e4b8d3af0 -> claudit-cko (planner duties.toml:32-34,48-50 treats every non-sweep mail as an operator idea and labels it operator-approved; the gardener's "Epic <id> has an unmet line" mail (charter.md:44, duties.toml:463-466) is not one).
- skipped: none (no garden-fp label existed for either). Past cap: none (2 of 5).
- not filed, noted: lead charter `## Self-approval` is `[TODO]` (charter.md:50); follow-ups/red-main fall back to `proposed`, so no contradiction, only a gap. The charter's tend-seats skip line omits docs/adr/, which the check includes; not a contradiction, because the charter's Inputs tie rulings to docs/adr. The planner has two mail duties, but the workshop wakes only the earliest-due one per seat (djinn internal/djinn/workshop/mail.go:177), so the planner inbox duty may never run; this is tool behaviour, not a seat line. claudit has no docs/adr/, but the tend-workshop current-budget prompt cites docs/adr/2026-10-24-finished-plans-are-deleted.md.
- memory.md sizes: gardener 9 B before the edit, 499 B after it; lead 9 B; planner 9 B (budget 16384 B each). No findings, no sweeps mailed.
- applied to gardener memory.md: added 2 standing facts (no finding removed).
- bd memories: 0.
- memory audit: claude 0 files (no memory/ directory), codex 0 files (~/.codex/memories/ is empty), 0 findings, mail none
