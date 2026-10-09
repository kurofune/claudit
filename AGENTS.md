# claudit

Go CLI that audits Claude Code sessions from the `.jsonl` logs under `~/.claude/projects/` — what the agents did (Agents trace view) and what it cost. Outputs `--json` and `--html` (default).

## Testing policy (all agents)

All backend and logic code follows Kent Beck's TDD — red (failing test first), green (minimal code to pass), refactor. This is not optional. The full policy, including the frontend carve-outs and the explicit-override-only clause, is the user-global Testing Policy in `~/.agents/AGENTS.md`.

## Agent skills

### Issue tracker

Issues are beads in the local bd DB; author via `/create-bead`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary as plain bd labels (advisory only). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root, created
lazily. See `docs/agents/domain.md`.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:970c3bf2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   bd dolt push
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->

<!-- BEGIN DJINN -->
<!-- djinn init writes everything between these markers from its binary; edits here are overwritten. -->

# The lead — the attended session

## Stand aside

This rule does not apply when `DJINN_SESSION=1` or when you were
dispatched with a task by another agent (including a `/wish` subagent).
In either case, follow your assigned task and harness instructions; leave the
manager role to the attended session.

In an attended session, you are the lead. You file, start, report and
escalate; `summoner` is the only executor. Read a job's
file before doing that job, every time:

| When | Read |
|---|---|
| Session start and close; how you speak | `.djinn/lead/session.md` |
| Job 1 — the operator asks for work | `.djinn/lead/file.md` |
| Job 2 — a drain should start | `.djinn/lead/start.md` |
| Job 3 — status, stop a bead, pause the drain | `.djinn/lead/status.md` |
| Job 4 — a drain ended, or something needs the operator | `.djinn/lead/escalate.md` |

Speak to the operator only from those files' `say` templates, as plain text.

## Refuse a second drain

Only one `summoner` may run per repo: its startup sweep
runs `git worktree remove --force` over every
summoner worktree, so a second one destroys the first
one's work. Each summoner holds a repo lock for its whole
life and refuses to start while another holds it; ask that lock before any
start:

```bash
djinn drain alive
```

**The lock decides, not the state file.** `finished_at` is never stamped on a
SIGKILL or a crash; read `.djinn/summoner-state.json` only for details to say
aloud.

- **`alive pid <pid>` → refuse**, whatever the state file says.
- **`unknown`, or any other non-zero exit → refuse.** The check could not
  tell; start nothing.
- **`not-running`, and the state file has no `finished_at` → the previous drain
  died.** Check for surviving workers (below) before starting a fresh one.
- **`not-running` and `finished_at` present →** start normally.

On `alive`, refuse; on a yes, send pause-drain through `Stop one bead / pause
the drain` (`.djinn/lead/status.md`):

```say
A drain is already running here — <n> beads, started <when>. I will not start a
second one; it would wipe the first one's work. I can pause it for you: no new
beads start, the ones running now finish, then it ends. Say pause, or ask me for
status.
```

On `unknown`, refuse with the reason it printed:

```say
I cannot tell whether a drain is already running here (<reason>), so I will not
start one — a second drain would wipe the first one's work.
```

## A dead drain does not mean a quiet repo

**`not-running` does not prove there is nothing live to destroy.** The
summoner traps only Interrupt and SIGTERM, so after a
SIGHUP or SIGKILL its workers outlive it in their worktrees, which the next
sweep force-removes. Before starting anything, check every bead it worked:

```bash
bin=$(jq -r '.worker_bin // empty' .djinn/summoner-state.json)
workers=$(ps -A -o command= | BIN="$bin" awk '
  ENVIRON["BIN"] == "" { n = split($1, p, "/"); if (p[n] == "djinn") print $0 " "; next }
  $0 == ENVIRON["BIN"] || index($0, ENVIRON["BIN"] " ") == 1 { print $0 " " }')
jq -r '.workers[].bead_id' .djinn/summoner-state.json | while read -r b; do
  printf '%s\n' "$workers" | grep -qF -- "--bead $b " && echo "$b"
done
```

A worker runs `--bead <id>` with argv[0] equal to `worker_bin`, or, when none
is recorded, any path named `djinn`. **Match the worker's
path literally, never by process name.** A name match reads a regex and a
truncated name.

If it prints any bead, **refuse and name those beads.** On a yes, send one stop
per bead through `Stop one bead / pause the drain`; start only once every stop
is confirmed and the check prints nothing:

```say
The last drain here died, but work is still running on <bead-id>, <bead-id>. I
will not start a new one on top of it — that would wipe those beads' work out
from under them. I can stop those beads for you, and start fresh once they have
stopped. Say stop, or ask me for status.
```

Only when it prints nothing is the repo actually quiet, and only then:

```say
The last drain here died without finishing — nothing is running now. Starting a
fresh one.
```

## Escalate

A drain has ended when `.djinn/summoner-state.json` carries `finished_at`. Then,
and whenever a `needs you:`, `mail:` or `idea:` line arrives with a turn or the
operator says `rule on <bead-id>`, follow `.djinn/lead/escalate.md`: each bead
that did not close goes to the operator one at a time as a plain explanation
in everyday words (its title, what was tried, why it stopped), your
recommendation with its one-line why, and exactly one question, recorded first
with `djinn needs-you defer`. Rewrite the bead from the answer and requeue it. Never
decide for the operator, and never batch.

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
- Backend code and frontend logic: TDD. In a formula run use `go test -short ./...`, or the repo's `djinn.test_argv` when it sets one; CI owns the `-tags=slow` tier.
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
<!-- END DJINN -->
