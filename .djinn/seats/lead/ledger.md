# Ledger

## 2026-11-07 — follow-ups

Worked 4 of 6 pending (claudit-ovu, claudit-xef wait for the next run). Checked at HEAD c89104d.

- claudit-00t (from claudit-0xy): closed, already fixed. `/agents/full?turn=` sends Codex turns to `codex.FindTurnText` (internal/serve/api_agents.go:151), landed in 1ab98bd.
- claudit-ksa (from claudit-cn5): still reproduces (web/charts.js:606-607 takes 24h off month_end and reads UTC parts). Re-authored as claudit-ait, parent claudit-hty, then closed.
- claudit-7dh (from claudit-7lt): still reproduces (searchEntries skips nested child rows). Re-authored as claudit-c8f, parent claudit-fw1, then closed.
- claudit-6uw (from claudit-9yx): still reproduces (pageGroup ignores the focused row). Re-authored as claudit-b30, parent claudit-fw1, then closed.

All three new beads are filed `proposed` (bug, P2, from:seat:lead, web), not seat-approved, because the charter's ## Self-approval is still [TODO]. They go to triage. Their parent epics are closed; bd accepted them as children anyway.

## 2026-11-07 01:03 — triage

Decided 5 of 5 listed, oldest first, at HEAD 086137a. The lead's vote and reason came first in each case.

- claudit-cko: operator (2-1-0). Lead voted go and flagged changes-how-the-workshop-decides, because it rewrites how the planner applies the operator-approved label that triage reads. The contradiction is real at planner duties.toml:32-50.
- claudit-df0: operator (2-1-0). Lead voted go and flagged changes-how-the-workshop-decides, because it rewrites the lead charter's epic-closing rule. The contradiction is real at lead charter:43-46 against gardener tend-epics.
- claudit-ait: go (3-0-0). Reproduces at web/charts.js:606-607.
- claudit-c8f: go (3-0-0). Reproduces: searchEntries indexes group items only.
- claudit-b30: go (3-0-0). Reproduces: pageGroup has no focus awareness.

cko and df0 both edit .djinn/seats/. If either one is approved, it becomes a lead attended edit plus the matching change to djinn's seat template, not drain work.

## 2026-11-07 — follow-ups

Worked 2 of 2 pending at HEAD c58e8c7. That clears the queue.

- claudit-ovu (from claudit-yu4): still reproduces. `worthALook`'s `resolved` runs flatMap and indexOf over the agent's tools for each cascade signal (web/session-header-logic.js:382-383). Re-authored as claudit-zpb (parent claudit-fw1), then closed.
- claudit-xef (from claudit-hts): still reproduces. `execCommands` runs `execCall` over the raw source while `execFunction` scans `jsCode(src)` (internal/parse/codex/codex.go:873 against :662). Re-authored as claudit-3gm (parent claudit-lft), then closed.

Both new beads are filed `proposed` (bug, P2, from:seat:lead), because ## Self-approval is still [TODO]. They go to triage.

## 2026-11-07 01:14 — triage

Decided 2 of 2 listed, oldest first, at HEAD 8a48a17. The lead voted go on both, with no hard-rule flag, after confirming each claim in the code.

- claudit-3gm: go (3-0-0). Reproduces: execCommands (codex.go:873) matches execCall over raw src, while execFunction (:662) scans jsCode(src).
- claudit-zpb: go (3-0-0). Reproduces: resolved runs flatMap plus indexOf for each error-cascade signal (web/session-header-logic.js:382-383).
