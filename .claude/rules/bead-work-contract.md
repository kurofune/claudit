# Per-Bead Work Contract

Always-on rules for working one bead. The steps themselves live in the formula (`djinn formula describe --embedded iter --json`; shapes in `core/SHAPE-REGISTRY.md`), `/wish`, and the skills each step names; this page holds only the rules they share. Every formula runs claim → implement → review-loop → regression-loop → commit-push.

## 1. Work a bead only through /wish or a drain

Interactive: `/wish <id>`. Autonomous (`DJINN_SESSION=1`): the summoner drain. Never hand-drive the steps. Under the harness, the harness owns terminal status, the evidence record, and every push; no step waits for a person.

## 2. Claim before code; read the whole bead

- `bd update <id> --claim --status=in_progress` before the first line of code changes.
- Lifecycle: open → in_progress → closed. Never skip to closed.
- `bd show <id>`: read every field, then the parent epic's description (it carries the shared brief). Read every file the design field names before writing code.
- Acceptance-criteria quality is settled at filing: author every bead through `/create-bead`.

## 3. Implement to every criterion; commit incrementally

- Build each acceptance criterion in full. A skeleton that compiles is not done.
- When every criterion already holds at HEAD, commit nothing and report already-done; the harness closes the bead.
- Commit after each milestone. The harness preserves commits, never an uncommitted tree (failure classes: `classifyIterOutcome`, `internal/djinn/session/iter_beads.go`).
- Backend code and frontend logic: TDD. In a formula run use `go test -short ./...`; CI owns the `-tags=slow` tier.
- UI work: every `.djinn/SCARS.md` item is an implicit acceptance criterion.

## 4. Browser-verify web/ changes

Changed `web/` files: walk every acceptance criterion in a browser with the adapter's browser procedure (artifacts in `.playwright-cli/`), at the data volume the Scalability Assumption names, through empty, loading and error states, with no console errors beyond a baseline route's.

## 5. Review

Run the review the active formula declares — `review` (`core/skills/review/SKILL.body.md`) — as the named procedure; a self-assessment or a custom review prompt does not count. Fix every BLOCKER and IMPORTANT; never act on counter-findings. Follow-up filing and its P2 floor: the review skill.

## 6. Evidence on the bead

The per-criterion record is a `djinn-evidence` JSON comment on the bead (`bd comments add <id> -f <tmpfile>`), one row per acceptance-criteria line — never a file, never `--notes`. Under every formula, commit-push writes it, and the `evidence_*` labels, from review's sidecar `criteria[]`. Schema and lifetimes: `docs/verdict-state.md`.

## 7. Stay in scope

Adjacent work is out of scope: file it through `/create-bead`, stating why it matters, and move on.

## 8. Commit, then push git and Dolt together

Interactive: after `bd close`, run `bd dolt push` with the `git push` (repos without Dolt auto-commit run `bd dolt commit` first), so code and bead state land together. Under `DJINN_SESSION=1` the harness pushes both (`commitClose`); never push by hand.
