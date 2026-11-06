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

## 4. Failures that mean something (after dogfooding, 2026-11-06)

The landed list flagged 641 of 1,429 sessions (45%, $3,402 of $4,171) as
"ended with unfixed failed commands": `failed` counts every errored call
with no later identical success, so a `grep` that found nothing, an `ls` on
a missing path or a `ps | rg` counts. All 8 Worth opening rows were the
costliest sessions with any failure; compaction and top-10% never showed.

Measured on the same corpus (summary endpoint, every session):
- Last run of each check (a command segment that starts `go test|build|vet`,
  `npm test`/`npm run test|build|lint|check`, `make`, `pytest`, `cargo
  test|build|check`, `node --test`, `tsc`, `git commit|push`) failed: 52
  sessions (4%, $242). Mostly `go test` (32), `go build` (8).
- An edit (Edit, Write, MultiEdit, apply_patch) that errored and never
  succeeded with the same input: 4 sessions ($312).
- Either: 56 sessions (4%, $554).
- A substring match is wrong: `ps -A … | rg 'go test'` matched 86 times;
  match the start of a `&&`/`;`/`||`/newline segment after `cd`/env prefixes.

Change: rank and count by these two facts instead of `failed`.
- Top sentence: "K ended with a failing check (go test, build, commit…)."
- Worth opening reason 1: "Last `<check>` failed" (the check's first word
  pair) linking to that turn; reason 2: "N edits failed and never applied".
- The filter chip and the session page's failure block lead with the
  failing check and failed edits; the other unretried errors stay listed
  under a collapsed "N other commands errored and were not retried".
- Titles: `<pasted_content …>` tags are stripped and the first line of the
  pasted text is the title (11 sessions).

**Landed 2026-11-06, code at 25df683 (claudit-me6).** `claudit serve --port
18787` built at 25df683, on this machine's corpus, the list's default window
(the last 7 days, no query):

```sh
curl -s http://127.0.0.1:18787/_claudit/api/sessions | jq -c 'def fc: (.failing_checks // []) | length > 0; def usd(f): [.sessions[] | select(f) | .cost_usd] | add // 0 | . * 100 | round / 100; {total: (.sessions | length), usd: usd(true), failed: ([.sessions[] | select(.failed > 0)] | length), checks: ([.sessions[] | select(fc)] | length), checks_usd: usd(fc), edits: ([.sessions[] | select(.failed_edits > 0)] | length), edits_usd: usd(.failed_edits > 0), either: ([.sessions[] | select(fc or .failed_edits > 0)] | length), either_usd: usd(fc or .failed_edits > 0), by_check: ([.sessions[] | (.failing_checks // [])[] | .check] | group_by(.) | map({key: .[0], value: length}) | sort_by(-.value) | from_entries)}'
```

- 1,447 sessions, $4,208; `failed` > 0 in 650 (45%, unchanged rule).
- A non-empty `failing_checks`: 81 sessions ($173). Per label: `go test` 54,
  `go build` 11, `make` 6, `npm test` 5, `go vet` 3, `npm run check` 3,
  `node --test` 2, `git commit` 1.
- `failed_edits` > 0: 4 sessions ($312), as measured above.
- Either: 85 sessions (5.9%, $485), under the 10% bar.
- 81 is above the 52 measured above; the difference is not reconciled
  (the window moved a day and the corpus is live). Titles: no session title
  carries `pasted_content` (§4 counted 11).

**Remeasured 2026-11-06, code at b1d5b6e (claudit-me6),** after check
recognition stopped splitting at separators inside quotes or after a
backslash (`rg 'a; go test' .` is no check). The same query against
`claudit serve --port 18787` built at b1d5b6e:

- 1,457 sessions, $4,218; `failed` > 0 in 653 (45%).
- A non-empty `failing_checks`: 82 sessions ($174). Per label: `go test` 54,
  `go build` 11, `make` 6, `npm test` 5, `go vet` 3, `node --test` 3,
  `npm run check` 3, `git commit` 1.
- `failed_edits` > 0: 4 sessions ($312).
- Either: 86 sessions (5.9%, $486), under the 10% bar.
- The quote-aware split changed no session: a build at 13594f9 (before it),
  served on port 18788 at the same moment, gave the same 82 sessions with
  identical `failing_checks` (check, command, turn). The 81 → 82 move is
  the live corpus. Every entry carries an `input_key`, which the session
  page's failure block now matches calls by.

**Remeasured 2026-11-06, code at 22521a0 (claudit-me6),** after a `#` that
begins a word started a comment through its line (`# don't skip checks`
newline `go test ./...` is a `go test` again; the quote-aware split had
read the apostrophe as an open quote). Same query, `claudit serve` built at
22521a0 on port 18797 beside a build at c29e5cd (before it) on 18798, both
at the same moment:

- 1,461 sessions, $4,223; `failed` > 0 in 654 (45%).
- A non-empty `failing_checks`: 81 sessions ($173). Per label: `go test` 54,
  `go build` 11, `make` 6, `npm test` 5, `go vet` 3, `npm run check` 3,
  `node --test` 2, `git commit` 1.
- `failed_edits` > 0: 4 sessions ($312). Either: 85 sessions (5.8%, $485).
- Comment handling changed no session: the two builds gave identical
  `failing_checks` per session. The 82 → 81 move is the live corpus.

**Remeasured 2026-11-06, code at b115c3e (claudit-b0r),** after a check
counts only when the call's exit is that check's. A Bash call exits with
its last command's status, so `checkKind` names a check only when it is the
call's final segment: trailing `echo`, `printf`, `true` and `:` after `&&`
are skipped, and a piped final segment (`go test | tail`) is no check. A
`<<` opens a here-document only outside quotes and comments. A Codex exec
script that calls more than one `tools.*` function (parallel
`exec_command`s, or one plus a `write_stdin` poll of another session) fails
when any of them does, so it runs no check (`parse.ToolUse.Concurrent`).
A call that is no check neither fails nor clears one. Same query,
`claudit serve` built at b115c3e on port 18811 beside a build at ad970bf
(its parent) on 18812, both read at 13:48:17 PST:

| | ad970bf (parent) | b115c3e (change) |
|---|---|---|
| sessions | 1,540 ($4,288) | 1,540 ($4,288) |
| non-empty `failing_checks` | 81 sessions ($173) | 7 sessions ($15), 91% fewer |
| per label | `go test` 54, `go build` 11, `make` 6, `npm test` 5, `go vet` 3, `npm run check` 3, `node --test` 2, `git commit` 1 | `go vet` 3, `go build` 2, `go test` 2 |
| by provider | Claude Code 17, Codex 64 | Claude Code 6, Codex 1 |
| either (check or `failed_edits`) | 86 sessions ($485) | 12 sessions ($327) |

The final-segment rule alone (2f57f6c, same moment) left 42 sessions; 36
were Codex exec scripts whose joined commands put the check last though
another parallel command or poll errored (`cat CONTEXT_MAP.md` beside a
`go test`, a `write_stdin` poll printing another run's FAIL). The
`Concurrent` rule removed those. It costs recall: a check that really
failed inside a multi-tool Codex script is no longer flagged.

Hand check of every remaining flag (7, fewer than 20). Each call traced to
its raw log by session and `input_key` (Codex) or turn (Claude Code):

| # | session | label | final command | output | verdict |
|---|---|---|---|---|---|
| 1 | 5c6e9a64 | go build | `go build ./... && echo built` | `family.go:34:120: too few values in struct literal` | true |
| 2 | a4324674 | go vet | `GOOS=windows go vet … && echo win-ok` | `undefined: syscall.Flock` | true |
| 3 | 01a1996e | go test | `go test ./internal/readcache … -count=1` (one exec_command) | exit 1, `go: creating work dir: … operation not permitted` | true (sandbox, not code) |
| 4 | a456734f | go vet | `go build ./cmd/djinn && go vet ./cmd/djinn ./internal/djinn/ops/room` | exit 137, `Sent redeclared in this block` | false: the printed failure is the `go build` before `&&`, so vet never ran |
| 5 | cfe42aa0 | go build | `CGO_ENABLED=0 GOOS=darwin … go build … && echo darwin-ok` | exit 1, `gozstd … undefined: CDict`; no `-ok` printed | true |
| 6 | b98f2e54 | go test | `go test -short ./internal/aggregate/ -run PathTools` | `FAIL … [build failed]` | true |
| 7 | 24b9adcb | go vet | `go vet ./internal/aggregate/ ./internal/agentflow/` | `vet: … "context" imported and not used` | true |

6 of 7 are true: the final command is the check and its output shows its
failure. Row 4 is false: the failure is the `go build` earlier in the `&&`
chain, which stopped before `go vet` ran; the final-segment rule labelled
it by the last check. (Corrected 2026-11-06; first written as "true, with a
gap".)

**Remeasured 2026-11-06, code at 801bc52 (claudit-b0r),** after a check
counts only when the call's exit is surely its own. An errored call whose
check an earlier command that can fail gates with `&&` (anything but
`echo`, `printf`, `true`, `:`; `cd` counts) may have stopped before the
check, so it neither fails nor clears it; a passing call whose check `||`
gates may never have run it, so it clears nothing. A piped final segment
is no check even after `&&` (`go test && echo ok | false`). A tool input
cut at the parser's 2,000-rune cap runs no check, since its trailing
commands are gone. Same query, `claudit serve` built at 801bc52 on port
18821 beside a build at 2ce4ec4 (its parent, code as b115c3e) on 18822,
both read at 13:54:46 PST:

| | 2ce4ec4 (parent) | 801bc52 (change) |
|---|---|---|
| sessions | 1,547 ($4,296) | 1,547 ($4,296) |
| non-empty `failing_checks` | 7 sessions ($15) | 5 sessions ($10) |
| per label | `go vet` 3, `go build` 2, `go test` 2 | `go test` 2, `go vet` 2, `go build` 1 |
| by provider | Claude Code 6, Codex 1 | Claude Code 4, Codex 1 |
| either (check or `failed_edits`) | 12 sessions ($327) | 10 sessions ($322) |

Two sessions left, both rows of the hand check above:
- a456734f (row 4, false): `go build … && go vet …` errored; gated by the
  build, so no longer labelled `go vet`. The fix this change was for.
- 5c6e9a64 (row 1, true): `sed -i … && sed -n … && go build ./... && echo
  built`; its output is a real build error, but `sed` could have failed
  first, so the conservative rule drops it. A recall cost of one true flag;
  reading the tool output would be needed to keep it.

The 5 left are rows 2, 3, 5, 6 and 7 above, each re-read in its raw log:
the check is the final command and no failable `&&` gates it (row 5's
`go build` follows a `;`, row 7's `go vet` a `;`, row 6's follows a closed
here-document on its own line). All 5 are true.
