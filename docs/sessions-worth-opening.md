# Sessions: say what to open, and open it fast

Operator-approved 2026-11-05 after a council review (Astra, Fable) of a
rejected "kinds of work" landing plan. Three beads, in order.

## Why

The operator, after using the v3 Sessions surface: "I don't even know what
I'm looking at. What am I supposed to do with this? There's no guidance.
It's just huge chunks of text. It also feels slow." A newcomer would be
lost too.

The page must answer **"Which session should I open, and why?"** with a
literal, counted reason and a link to the evidence. No LLM: every sentence
is a template over counted facts.

## Rejected, and why (do not rebuild)

- "3× a typical run" flags: cost tracks turns (r = 0.97 on implement runs);
  p90 is already 4.6× the median, so the rule flags the top tenth by
  construction.
- A failure-rate column per family: contradicts outcomes (failed > 0 on 40%
  of implement runs while 143 of 177 outcomes say they committed).
- Repeated-work / one-off tiles, "kinds of work" landing page, invented short
  labels ("Implement step (Djinn)"): prompt families are first-line matches,
  not kinds of work, and a label needs an LLM.
- Working-time medians from a 5-minute gap rule: an estimate, not a count.
- Hiding the last message: it is the session's outcome.

## Measured facts (7-day window, ~1,500 sessions)

- Implement runs with a failed command: median $2.78 (n=70); without: $0.65.
- `failed` (internal/agentflow/index.go:174-209) counts errored calls whose
  exact input never later succeeded — "unfixed failed commands".
- Triage family mixes models: Fable 102 runs, Opus 79.
- Story cold load 9–10 s, warm 0.3 s; Overview 2.7–6 s, once >70 s; server
  ~170% CPU while live logs write; many `write api response failed … i/o
  timeout section=api-sessions-story`.

## 1. Speed (first; blocks the others)

Cause: `sessionStory` and `filterAggregate` memoize on `snap.Generation`
(internal/serve/api_sessions.go:227-255), which bumps on every poll while
any live log writes, so each story request rebuilds the session and the
whole-corpus Aggregator for `ContextCostByTurnWhere`. The header
(web/session-header.js:41-65) fetches `?whole=1`, the entire session.

Mechanism:
- Compute a session's per-turn context cost from that session's own turns
  and results (plus its compact boundaries), not from the corpus Aggregator.
- Key the story memo on the session's own source files (path + size +
  mtime), so a change to another session's log does not evict it.
- Index rows for sessions whose files did not change are reused across
  generations.
- The header reads a small summary (the fields it renders) instead of the
  whole session.

Budgets, measured while a live session is writing, at the real corpus and
at 3× volume: session open (header + first story page) < 1 s cold; Overview
< 2 s; no `i/o timeout` write errors.

## 2. The list says what to open

On `#agents` (web/sessions-list.js, sessions-list-logic.js):
- One top sentence from counts: "N sessions in <window>, $X API-equivalent.
  K ended with unfixed failed commands ($Y)." Reads sensibly with 20
  hand-typed sessions and no families.
- A "Worth opening" block, at most ~8 rows, each with one literal reason,
  ranked in this order:
  1. "N failed commands never fixed" (failed > 0), costliest first.
  2. "Context compacted N times" (Claude Code compact boundaries).
  3. "In the most expensive 10% of N runs of this prompt" — only families
     with ≥ 5 runs.
  4. "Most expensive session this week" — the single top one, labelled as
     exactly that.
  Each links to the session, and for failures to the failing command's turn.
- Rows: a short quote of the request (wrapper stripped, first line) as the
  title; "Last message:" line visible; dollars say API-equivalent where
  Codex is included.
- Family headers show runs, total, median cost and model mix; each family
  starts collapsed to its 3 rows most worth opening, with "Show all N".
- Empty, loading and error states; keyboard reachable.

## 3. The session page leads with how it ended

On `#agents/<id>` (web/session-header.js, session-header-logic.js):
- First: the last message, then unfixed failed commands (name + one-line
  input, link to the turn), then cost/time tiles.
- Comparison line only when the family has ≥ 5 runs: "$8.28 — longest/most
  expensive 10% of 177 runs of this prompt (median $1.06; 102 Fable, 79
  Opus)". Otherwise no line.
- Prompt wrapper text stays collapsed behind a disclosure.

## After

The operator uses the reworked list and session page before anything
larger is planned.
