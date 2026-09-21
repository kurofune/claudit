# Per-Bead Work Contract

This file is the canonical, agent-neutral contract for working a single bead end-to-end. It is the shared substance every Djinn adapter (Claude Code, future Codex, …) must implement. Adapter-specific guidance — which concrete command, skill, or subagent satisfies a given step — lives in that adapter's README (for Claude Code: `djinn/adapters/claude-code/README.md`), not here.

This contract has **three execution modes**. The substantive steps below are identical across all three; what differs is **how — and whether — the audit-trail artifacts persist into git history**. That persistence is NOT symmetric across modes, so a reader auditing a past run must know which mode produced it before expecting any given artifact:

- **Interactive mode** (`DJINN_SESSION` unset): the project's always-on instructions (CLAUDE.md / AGENTS.md / the beads rule file) reference this contract. Enforcement is by agent discipline + reviewer convention, not by harness rollback. The per-criterion evidence record is a **bd comment on the bead** — written with `bd comments add <bead> -f <tmpfile>`, read back with `bd comments <bead> --json` (djinn-mr56i, superseding the ruling-Q3 "local and untracked" file, djinn-nc4i4.4). The named procedures' sidecars (`.ac-quality.json`; one review sidecar per declared review loop — `.review-light.json` under every shipped formula; `.report.json`) are LOCAL per-iteration working artifacts under the gitignored `.djinn/state/<bead>/<loop-id>.iter-<N>/` dir, read by the verify step and by a human on the same machine, and never committed.
- **Single-pass autonomous mode** (`DJINN_SESSION=1`, no formula): `.djinn/PROMPT.md` wraps this contract with harness-specific bits (target-bead matching). The agent records the evidence as a bd comment on the bead (same `bd comments add` sink as interactive mode) and writes `.djinn/last-status.json`; the harness loads the status file and then the bead's NEWEST evidence comment (`status.Load` → `evidence.BeadSource.LatestEvidence` over `bd comments <id> --json`, `internal/djinn/agent/accumulator.go`), and gates the iteration on both. The evidence gate compares the report's criteria count to the bead's `acceptance_criteria` count AND the evidence comment's row count to the same number (`internal/djinn/gate/evidence.go`); the artifacts gate checks each path cited by the report AND each `implementation` path cited by a code criterion of the evidence comment against the diff (`internal/djinn/gate/artifacts.go`). A count mismatch, an unaccounted changed file, or an evidence row citing a file outside the diff rolls back the iteration (`.djinn/PROMPT.md`). A bead carrying **no** evidence comment is a documented skip, not a rejection — the record is written late in the contract and in formula mode after the gates have run — so the comment checks bind exactly when a record exists.
- **Formula autonomous mode** (`DJINN_SESSION=1` with `Cfg.FormulaPath` set — the dominant production path, `internal/djinn/session/run_formula.go`): each contract step runs as its own formula loop step, and the per-step verdicts (ac-quality, the verdict of each review loop the active formula declares — `review-light` under every shipped formula — and verify) live ONLY in the **gitignored** `.djinn/state/<bead>/<step>.iter-N/` forensics dir, with transcript and cost under `.djinn/state/<session>/<bead>/`. Formula steps are explicitly told NOT to write the evidence record or `.djinn/last-status.json` (`internal/djinn/session/formula_prompts.go` (`BuildImplementPrompt`) — "the harness owns terminal status in formula mode" — with parallel prohibitions in `BuildWriteTestsPrompt` and `BuildRefactorPrompt`). These per-step gate forensics are **EPHEMERAL**: wiped on state cleanup, never committed. The harness-owned `commit-push` step synthesizes the per-criterion record from the verify-loop's report (one row per acceptance criterion, status transcribed from the report) and RECORDS IT ON THE BEAD as a `bd comments add` comment (`internal/djinn/session/formula_evidence.go`, `emitFormulaEvidence`, djinn-id2a/djinn-mr56i). It writes no file: the former artifact lived inside the run's temp worktree and was deleted with it, so nothing of the per-criterion record survived the run. This is the single writer of the formula-mode record; the per-step prohibition above is preserved unchanged, so terminal-status ownership stays with the harness and there is no double-write. What survives a past formula run is therefore the bead itself: its evidence comment, its `bd close` reason, and the `evidence_*` state labels (`docs/verdict-state.md`).

The substantive requirements below are mode-independent and agent-independent. Harness-only enforcement details live in `.djinn/PROMPT.md`; the mapping from each step to a concrete invocation lives in your adapter's README.

## 1. Pick and claim the task

Run `bd show <id>` for full details — read ALL fields (description, acceptance, design, notes).
Claim with BOTH flags: `bd update <id> --claim --status=in_progress`

The bead must be **in_progress** BEFORE the first line of code changes (this is the cardinal beads rule, also stated in the beads rule file your adapter loads).

## 2. Understand the intent BEFORE writing code

The bead's description tells you WHAT to build and WHY it exists.
The bead's acceptance criteria tell you what DONE looks like — this includes the **Interaction Inventory** (every hover/click/keyboard action) and the **Scalability Assumption** (data volumes it must remain usable under). Both must be satisfied for the bead to be complete.
The bead's design notes tell you WHERE to look and WHAT patterns to follow.
The bead's notes tell you how this fits into the larger plan.

Read every field. If the design field references files, READ THOSE FILES before writing any code. If the description mentions a user problem, keep that problem in mind — you're solving it, not just creating a file.

**If the bead has a parent, run `bd show <parent>` and read its description too.** Under the epic-as-brief convention (`/create-bead`, "Children of an epic"), the parent epic carries the shared design brief — Diagnostic Question, Reference Standard, architecture context, plan pointer — that child beads deliberately do not duplicate. A child bead read without its parent's brief is missing half its intent.

**Read `.djinn/SCARS.md`** — a short list of UX failure patterns we have shipped before. Every item there is an implicit acceptance criterion for any UI work. Treat each scar as something to catch this iteration, not the next one.

### 2a. AC-quality gate — required before §3

Before writing any implementation code, invoke the AC-quality review procedure (per your adapter's documentation) with the bead id you claimed in §1. This is the interactive-mode mirror of the autonomous formula's `ac-quality-loop` step (`core/iter.formula.toml`, the embedded default; consumers may override at `.djinn/iter.formula.toml`) — it catches under-specified acceptance criteria BEFORE you write code against them, when the cheapest fix (tightening one or two AC lines) is cheaper than every downstream gate.

The procedure emits a structured sidecar with one of two verdicts:

- **`CONCRETE`** — every criterion's cheapest literal implementation would not embarrass the author. Proceed to §3.
- **`UNDERSPEC`** — at least one criterion admits a skin-deep minimum output. The sidecar lists `findings[]` with the embarrassing criterion text, the cheapest output it would admit, and a `rewrite_hint` for the tightening. Do NOT proceed to §3.

When the verdict is `UNDERSPEC`, you have two valid paths and no others:

1. **Tighten the bead's acceptance_criteria.** Apply each `rewrite_hint` via `bd update <bead_id> --acceptance="<rewritten criteria>"` — preserve unflagged criteria in their original order so the segment count still aligns with the evidence comment you record in §6. Re-invoke the AC-quality review until verdict is `CONCRETE`. Then proceed to §3.
2. **Escalate.** If you cannot tighten the criteria without exceeding the bead's scope, the bead is fundamentally under-spec'd and must be triaged by the planner. Stop work, report the verdict and the `findings[]` to the user, and do NOT proceed to §3. Filing a follow-up bead is appropriate when the under-specification stems from a missing capability you cannot author; otherwise the planner re-opens this bead and refiles it with tightened ACs. In formula mode there is no user to stop for: emit the `UNDERSPEC` verdict with its `findings[]` and let the harness own the bead's terminal status.

**The gate does NOT silently skip.** Proceeding to §3 with a known `UNDERSPEC` verdict is a contract violation symmetric with skipping the §5 code review or the §7 acceptance verification. The on-disk sidecar the procedure emits is the audit trail; your prose self-assessment ("the ACs look fine to me") is not. This applies to every bead — chores, documentation, tiny refactors, and large features alike.

Plumbing beads (single API endpoint, DB migration, type definition) are held to a structural bar — the procedure knows this and is more lenient with their criteria. Feature beads (user-visible capabilities) are held to the full Capability Gate plus the concrete-verification rule from the bead-authoring procedure.

## 3. Implement with depth, commit incrementally

Build the complete feature described in the acceptance criteria. Check each acceptance criterion explicitly — if it says "stats shown as progress bars," you build progress bars, not raw numbers. If it says "clickable links that navigate," you wire the navigation, not just render text.

**Commit after each meaningful milestone** (e.g., after each TDD green phase, after wiring data, after styling). Do not wait until everything is perfect to make your first commit. Incremental commits protect your work — but only against the failure classes the harness preserves, not all of them. Commit early because doing so is the only way to *qualify* for preservation: nothing the harness preserves is uncommitted, and the coherence gate can only roll the tree back to a building commit if a building commit exists. The precise guarantee, by failure class:

| Failure class | What the harness does to your commits | Where it's decided |
|---|---|---|
| **Agent timeout** (`ErrAgentTimeout`) | **Preserved**, coherence-gated (best-effort). If `HEAD` builds, every commit at-or-after `pre_sha` survives untouched. If `HEAD` does not build, the tree is reset to the newest building commit in `pre_sha..HEAD` (floored at `pre_sha`); if no candidate builds — or `git rev-list`/every reset fails — the tree may be left at the original `HEAD`. The bead is reopened, never hard-reset to `pre_sha` by this path. The gate exists because preserving an unbuildable `HEAD` would strand the next iteration on a build failure before the agent writes a line — it is a resumability contract, not just a safety check. | `preserveAndReopen` → `coherentResetTarget` |
| **Transient transport error** (`ErrAgentTransient`, e.g. a blinking API) | **Preserved**, same coherence-gated path as timeout. The work is presumed valid; the server, not the code, failed. | `preserveAndReopen` |
| **Every other non-verdict invocation failure** — a spent usage window (`usage_limit`), an auth rejection (`auth`), a malformed stream (`ErrAgentMalformed`), a non-zero adapter exit (`ErrAgentExit`), and a regression gate that could not RUN (missing binary / cancelled ctx / fired deadline) | **Preserved**, same coherence-gated path as timeout, with the failure class named in the bd reopen reason. None of these looked at the code, so none of them may discard it (djinn-oot7a). | `preservableFailureClass` → `preserveAndReopen` |
| **Verify-loop / quality exhaustion** | **Per the step's `on_exhausted`**: `leave_and_reopen` preserves the tree (reopens the bead, no git reset); `soft_fail` preserves the tree and moves the bead to bd `deferred` with the `needs-operator` label and a reason naming the loop and its last verdict (djinn-oot7a — it previously left the bead `in_progress`, i.e. stranded outside `bd ready`); `hard_reset` resets `--hard` to `pre_sha` (commits do **not** survive). Production verify-loop config selects `leave_and_reopen`. | exhaustion dispatch in `runOneFormula` / `classifyIterOutcome` |
| **Verify-red / verify-green TDD-discipline failure** | **Not preserved** — `git reset --hard pre_sha`. A timeout that wraps a discipline sentinel is still treated as a quality signal and resets, because the sentinel judges the *quality* of the code the agent wrote (the red/green TDD contract), which is independent of whether the step ran to completion. The dispatch checks these sentinels before the timeout/transient class for exactly this reason. | `verifyRedRollbackGate` / `verifyGreenRollbackGate` |
| **Genuine `formula_error`** (a harness fault with no agent work behind it — an unknown step function, a malformed formula) | **Not preserved** — `git reset --hard pre_sha`. This is the destructive catch-all, now narrowed to the classes above escaping it. | generic rollback in `runOneFormula` |

The dispatch order and the run-bead forensic reason for each class live in `classifyIterOutcome` (`internal/djinn/session/iter_beads.go`); the routing that produces them is in `runOneFormula` (`internal/djinn/session/run_formula.go`). When in doubt, commit anyway: committing never *reduces* your protection, and for the preserved classes it is the difference between surviving and starting over.

**The `commit-push` step commits whatever you left uncommitted**, before its gates run, excluding the harness-owned `.djinn/` paths and anything git ignores (`commitWorkTree`, `internal/djinn/session/commit_push_sweep.go`). A clean tree produces no commit. This is a safety net for the fix steps, whose prompts do not instruct a commit — it is not a licence to skip your own incremental commits: the net only catches the accept path, and every preserving disposition above still rolls back to a *commit*, not to your uncommitted tree.

**Run `go test -short ./...`; never run `-tags=slow` inside a formula run — CI owns the slow tier.** The slow tier under `cmd/summoner` and `internal/djinn/session` spawns and signals real djinn/summoner processes; running it from inside a worker is recursion with signals and takes the worker down with it (djinn-np4m4.9: the test returned exit 137 and its agent exited 143 a second later).

**The bead is not done until every acceptance criterion is met.** A skeleton that compiles is not done. A component that renders but shows no meaningful data is not done.

## 4. Verify in browser (required if you changed web/ files)

Behavior verification comes BEFORE code review. There is no point polishing code that doesn't work.

**Use your adapter's browser-automation procedure.** Invoke it so that snapshots and other artifacts land in the canonical artifact directory your adapter specifies (your adapter's README documents where that is and how to avoid scattering redundant artifact dirs across the tree). The §6 evidence comment expects snapshot paths under that directory.

Open the affected page(s) and walk the bead's acceptance criteria mechanically:

1. **Read `acceptance_criteria` before clicking.** Each hover/click/keyboard/filter/navigation action in that field becomes a `criteria` entry in your evidence comment (§6). That comment is the audit trail that proves you covered every interaction — no separate in-reply list required. Note the exact field name — it is `acceptance_criteria`, not `acceptance`.
2. **Execute each criterion in order, frugally.** Take **one snapshot per navigation** (page change), not after every click or hover. For per-criterion assertions, prefer a targeted CSS-selector evaluation returning a compact string (e.g., `.metadata-grid` text content) over pulling a full aria-tree snapshot. State the observed outcome in one line. Confirm it matches the criterion. If the outcome doesn't match, fix the code and restart the walkthrough from item 1.
3. **Exercise the Scalability Assumption at the specific volume the bead names.** The bead's `acceptance_criteria` field names a concrete data volume (e.g., "remains readable with 500 unique ticks and 2000 out-of-tick traces"). Reproduce that exact volume before declaring done. If seed data isn't available at that scale, modify a fixture, add a temporary seed call, or mock the API response — do NOT skip and do NOT test at a smaller volume. Confirm no overlap, truncation, clipped interactions, or unreadable density at the named scale.
4. **Check empty / loading / error states.** What does it look like with zero data? Mid-fetch? After a failed request? These are near-universal bugs. Verify all three or state explicitly that they don't apply.
5. **Check the console for NEW errors using a baseline comparison.** First, navigate to a control route (home `/` or an unrelated inspector route) and capture the console — record the error count. Then navigate to the changed page and capture the console again. Only errors that appear on the changed page but NOT the baseline count as new and must be fixed. Do NOT treat "1 error on the page" as acceptable without establishing what was already there.

Any acceptance_criteria interaction not verified = not done. Any broken layout or affordance at realistic data scale = not done. Any new console error = not done. Fix and re-verify before proceeding.

## 5. Code review

**Invoke the review procedure(s) the ACTIVE formula's review loop(s) declare.** This obligation is formula-relative — it is NOT a fixed implementation-plus-tests pair. Read the step graph of the formula the bead is being worked under (`djinn formula describe <path> --json`) and, for each review loop it declares, invoke the procedure named by that loop's body child. Concretely, across the shipped formulas:

| Active formula | Review loop(s) declared | What §5 requires |
|---|---|---|
| `core/iter.formula.toml` (the default) | `review-light-loop` | ONE single-lens review pass |
| every variant that extends it (`extends = ["iter"]`) | `review-light-loop` (inherited via `extends`) | ONE single-lens review pass |

Every shipped formula declares exactly ONE review loop since the djinn-y8thy retier: every variant that extends the default is an override file that patches knobs and restates no step, so it inherits the default's review surface unchanged. Run the surface the formula declares, neither heavier nor lighter — running two 5-panel orchestrations where the formula declares one single-lens pass reinstates roughly a dozen review agents the default chain exists to cut. The `/5-panel-review-*` skills still exist for INTERACTIVE use, but no formula dispatches them, and reaching for one on your own judgement is not §5 compliance. In interactive mode the active formula is the one your adapter's orchestrator resolves for the bead (its `formula:<name>` label, else the embedded default); when no formula is in play at all, the embedded default's review surface is the baseline. A consumer formula that declares NO review loop produces knowingly unreviewed code — an explicit routing decision recorded when the bead was labeled, never a licence to skip review under a formula that does declare one.

**Review loops ratchet.** From the second pass onward a review is INCREMENTAL, not a fresh sample of the whole diff (djinn-np4m4.6). The reviewer receives the previous iteration's promoted findings plus `git diff <that review's SHA>..HEAD`, and its sidecar answers both: a `prior_findings` entry marking each earlier finding `resolved` or `unresolved`, and an `origin` of `regression` or `latent` on every new one. The loop's check counts unresolved prior findings plus new regressions — its **variant** — at a threshold that tightens per iteration: IMPORTANT on the first pass, BLOCKER after, with IMPORTANT readmitted only when the last fix caused it. A variant of zero passes the loop; a variant that stops shrinking takes the graceful-exhaustion rescue immediately instead of spending the remaining budget. A new **latent IMPORTANT** is filed as a P2 follow-up bead rather than handed to the fix step — the reference standard is a human review round, where the second reviewer checks the fixes plus the delta and nobody blocks round three on something they could have raised in round one. A BLOCKER blocks under either origin; the ratchet never applies to it.

Whichever procedures apply, each must run as **the named procedure** per your adapter's documentation — not via an ad-hoc subagent (via your agent's subagent mechanism) running a custom review prompt — so the on-disk review artifact is produced as the audit trail. Address any `BLOCKER` or `IMPORTANT` findings. `NIT` findings may be deferred. Counter-findings (`kind="counter"` rows in the review's triage JSON) are anti-recommendations — do not act on them, including not codifying their rationale as comments or doc notes.

**File deferred findings as follow-up beads — but only at or above the priority floor.** Before declaring complete, if any review you ran surfaced findings you are deferring, file a follow-up bead per item with label `review-follow-up` and a `discovered-from` dep pointing back to the iteration's bead ID. Title = the reviewer's suggestion, verbatim or lightly summarized; description = the reviewer's rationale. Deferred does not mean forgotten.

The configured **priority floor** (default **P2**, operator ruling D1, 2026-09-03, `DECISIONS.md`) is the filing threshold. Priority numbering is "lower number = higher urgency" (P0 critical … P4 backlog), so a finding is *below the floor* when its target priority is numerically greater than the floor (e.g. a P3 or P4 item under a P2 floor). **The review's triage pass grades each NIT's filing priority per-finding** (the Wave 3 meta-triage on a 5-panel review; the single-lens reviewer's own triage sidecar on `review-light` — both emit the same sidecar shape) — aggressive default **P4**, covering pure polish / cosmetic / test-micro-tightening ("extract a constant", "use `bytes.Cut`", "strengthen this assertion") AND absent-verification findings (an untested error path, a named-scenario coverage gap, a drift trap between two sources — real as report content, but their fix is "add a test/comment/sync", not a behavior change; graded P3 they buried the backlog, closed as a class 2026-08-03). **P3** when the NIT names a **deferred behavior defect** — diff code that produces observably wrong runtime behavior (wrong output, panic, false verdict, resource leak) which is being deferred rather than fixed this iteration. **P2 only** when that deferred defect is severe enough that recording it in the review artifact is not enough — it has to be carried on the backlog until someone fixes it. **A NIT graded P3 or P4 is below the P2 floor and is explicitly not filed** — it stays recorded in the review artifact for human reading, but does NOT become a backlog bead. A `review-follow-up` bead has to *earn* P2: the deferred behavior defect is severe enough that the backlog, not the review artifact, is the right place for it. This is the deliberate brake on the low-value follow-up pile — the P4 floor never braked, because the grader stamped the polish class at P3 anyway; at P2 the honest grade and the filing threshold finally disagree in the direction that drops noise. Both filing paths honor the grade per-finding — the autonomous `file_nits` filer (`internal/djinn/beads/filer.go`, reading the per-finding `priority` on the triage sidecar) and the interactive orchestrator's filing path (the review-runner subagent on a 5-panel review body; the orchestrator itself on a single-lens `review-light` body, which has no runner). The floor is the same `MinPriorityOrDefault` config the autonomous `file_nits` step honors (`internal/djinn/formula/spec.go`), tunable per formula without a code change; the prose threshold here must track it.

**This applies to every bead whose active formula declares a review loop** — chores, documentation, tiny refactors, and large features alike. There is no size exemption; the only exemption is the formula-level one named above. Inline self-assessment in your prose ("reviewed the diff and it looks clean") is NOT a substitute. Neither is a subagent running a custom review prompt: context *isolation* is fine — adapters deliberately dispatch the named procedure inside a subagent — but the subagent must invoke the named procedure, not paraphrase its rubric.

## 6. Record the evidence on the bead

Before declaring complete you MUST record the per-criterion evidence **as a comment on the bead** — typed JSON with one `criteria` entry per acceptance criterion, attached with `bd comments add <bead_id> -f <tmpfile>`. The comment is the mechanical audit trail that converts self-reported completion into a checkable claim, and it lives where it outlives the run: a file under `.djinn/` died with the worktree and took the record with it (djinn-mr56i, superseding ruling Q3's local-and-untracked file, djinn-nc4i4.4). `bd comments` is append-only, so one comment per iteration accumulates rather than clobbering — never use `bd update --notes` for this. In formula mode you do NOT record it: the harness's `commit-push` step synthesizes the comment from the verify report.

**You MUST:**

- [ ] Write the JSON to a temp file and attach it with `bd comments add <bead_id> -f <tmpfile>`. Do NOT print JSON to stdout, do NOT wrap it in a markdown fence — the audit reads the bead's comment thread, not your transcript.
- [ ] The `bead` field and the bead you comment on MUST both equal the bead you claimed in §1.
- [ ] One `criteria` entry per `acceptance_criteria` segment. Entry count MUST equal segment count.
- [ ] Each criterion's `status` MUST be exactly `pass`, `partial`, or `fail` (lowercase).
- [ ] Set `schema` to `"djinn-evidence"`, `run` to this attempt's identity (the harness session id in autonomous mode; a stable session label in interactive mode), and `iter` to the integer iteration number. These top-level fields replace the provenance header.

Readers recognize JSON comments by `schema: "djinn-evidence"`; legacy YAML comments with the `# djinn evidence — run <run-id> · iter <n>` header remain readable. Code selects the newest evidence comment in comment order. When verifying a particular attempt, match its `run` and `iter` and treat other attempts as history.

**Required schema:**

```json
{
  "schema": "djinn-evidence",
  "run": "session-example",
  "iter": 1,
  "bead": "phoenix-abcd",
  "title": "Customer detail dossier",
  "description": "Optional top-level prose. Use for scope notes (e.g., \"§4 browser verification\ndoes not apply\"), immutability caveats, or cross-refs. Omit when not needed.\n",
  "criteria": [
    {
      "text": "Activity shown as chronological timeline",
      "status": "pass",
      "non_code": false,
      "implementation": [
        "web/src/lib/components/CustomerDossier.svelte:42"
      ],
      "verification": "playwright-cli click 'Customer 87'",
      "evidence": {
        "snapshots": [
          ".playwright-cli/page-2026-04-19T00-00-00Z.yml"
        ],
        "observations": [
          "12 events rendered, latest at top"
        ]
      },
      "follow_up": []
    },
    {
      "text": "All active tags shown as styled badges",
      "status": "pass",
      "non_code": false,
      "implementation": [
        "web/src/lib/components/TagBadge.svelte:1"
      ],
      "verification": "playwright-cli eval '.tag-badge'",
      "evidence": {
        "observations": [
          "3 badges visible: VIP, Refund, NPS-10"
        ]
      }
    },
    {
      "text": "Integration tests cover the happy path",
      "status": "partial",
      "non_code": false,
      "implementation": [
        "web/src/lib/components/CustomerDossier.test.ts:14"
      ],
      "verification": "npx vitest run src/lib/components/CustomerDossier.test.ts",
      "evidence": {
        "observations": [
          "6 of 8 cases covered; edge-case snapshot pending"
        ]
      },
      "follow_up": [
        "phoenix-xyz1"
      ]
    },
    {
      "text": "Backend feature — no UI affordances to inventory",
      "status": "pass",
      "non_code": true,
      "reason": "Pure data-layer change; no hover/click/keyboard interactions exist",
      "verification": "N/A — non-code criterion"
    }
  ]
}
```

**Field reference:**

- **`schema`** *(top-level, required)*: exactly `"djinn-evidence"`.
- **`run`** *(top-level, required, string)*: this attempt's session identity.
- **`iter`** *(top-level, required, integer)*: this attempt's iteration number.

- **`bead`** *(top-level, required)*: the bead id you claimed in §1.
- **`title`** *(top-level, required)*: the bead's title.
- **`description`** *(top-level, optional)*: preamble prose for scope notes, immutability caveats, or cross-refs. Omit when not needed.
- **`criteria`** *(top-level, required, list)*: one entry per `acceptance_criteria` segment.
- **`text`** *(per criterion, required)*: verbatim text from ONE segment of the bead's `acceptance_criteria`.
- **`status`** *(per criterion, required)*: `pass` | `partial` | `fail`. A `partial` row requires a `follow_up` naming at least one bead. Never ship `fail` under a complete-declaration — that's a BLOCKED signal or keep working.
- **`non_code`** *(per criterion, optional, default false)*: set `true` for genuinely non-code criteria (research audits, data conventions, process requirements). When `true`, `reason` is REQUIRED and `implementation` may be empty.
- **`implementation`** *(per criterion, list of strings)*: `path:line` entries (e.g., `web/src/.../File.svelte:42`). Entries may mix pure `path:line` refs with prose-hybrid entries (`file.go:42 (function_name)`); a path-shape regex (`^[^ ]+:[0-9]+`) is applied to filter entries validated against `git diff` — prose-only entries are preserved for human readers but skipped by the diff-presence check.
- **`verification`** *(per criterion, recommended)*: a copy-pastable command (e.g., `playwright-cli click '...'`, `npx vitest run ...`) or a test `file:line` reference. Optional in the schema, expected by reviewers — give them something they can run, not prose.
- **`evidence.snapshots`** *(per criterion, optional, list)*: snapshot filenames (typically under `.playwright-cli/`). Each is `os.Stat`-checked under the project root; paths that escape via `..` are rejected.
- **`evidence.observations`** *(per criterion, optional, list)*: observed outcomes (titles, console output, test names). Never "looks good" or "works."
- **`reason`** *(per criterion, required when `non_code: true`)*: why no implementation exists for this criterion.
- **`follow_up`** *(per criterion, optional, list of bead ids)*: linked deferred beads. REQUIRED for `partial` rows.

**The `implementation` paths and `evidence.snapshots` are mechanically checked.** Each non-code criterion's `implementation` is validated against `git diff pre_sha..HEAD --name-only` and each `evidence.snapshots` entry against the filesystem. Citing a path you did not touch this iteration, or naming a snapshot filename that was never created, is a contract violation — in autonomous mode the iteration rolls back; in interactive mode this is a self-discipline failure caught on review. Plausible-sounding `path:line` strings that reference nothing are not a substitute — the diff and the filesystem are the audit trail.

## 7. Verify acceptance coverage

Invoke the acceptance-coverage verification procedure per your adapter's documentation — not via an ad-hoc subagent (via your agent's subagent mechanism) running a custom prompt. The procedure must run as the named procedure so the on-disk verification report is produced as the audit trail alongside the review artifacts from §5.

Provide two inputs: the `bead_id` you claimed in §1 and the diff base ref (use the harness `pre_sha` in autonomous mode; otherwise `HEAD~N` where N is this iteration's commit count). The procedure reads the §6 evidence off the bead itself with `bd comments <bead_id> --json`.

Address findings before declaring complete. Critical (Missing) → fix the implementation, re-verify in the browser if needed, and update the evidence row. Important (Partial) → either close the gap or confirm the evidence row names a filed follow-up bead. A verdict of REQUEST CHANGES means the bead is not complete; iterate until APPROVE. Nit findings may be deferred.

**This applies to every bead.** Writing "verify-acceptance: N/N Pass" in your close notes or reasoning in prose is NOT equivalent to running the procedure. The on-disk verification report is the audit trail; your self-report is not. **The verdict the procedure emits is the gate** — `REQUEST CHANGES` means the bead is not complete; disagreeing with the verdict and declaring complete anyway is not a valid path. Iterate until APPROVE, or report BLOCKED with your reasoning. In formula mode there is no BLOCKED report to make: emit the verdict and let the harness own terminal status.

## 8. Final commit and beads-DB sync

Commit any remaining changes after steps 4, 5, 6, and 7 fixes.

**There is no evidence file to commit.** The §6 evidence is a bd comment on the bead (djinn-mr56i, superseding ruling Q3's "local and untracked" file, djinn-nc4i4.4), and the named procedures' sidecars are LOCAL, per-iteration working artifacts under the gitignored `.djinn/state/` — read by the §7 verification and by a human on the same machine, never tracked or committed. Do not `git add -f` them back in. The durable, queryable record of what a bead's verification concluded is the bead's own state: its evidence comment, its `bd close` reason, and the `evidence_*` state labels (`docs/verdict-state.md`).

**When you finish a bead, sync the beads DB to its Dolt remote.** A bead's durable state — its closed status, the `evidence_*` state labels (see the repo `CLAUDE.md` "Agent-owned verdict state"), the `bd close` reason, and any notes — lives in the Dolt-backed beads DB, NOT in git. `git push` does not carry it, so a bead is not actually finished for collaborators until its Dolt state is pushed too. After `bd close`, run:

```bash
bd dolt commit -m "<bead-id>: <one-line summary>"   # commit pending bead-state changes
bd dolt push                                          # push to the configured Dolt remote
```

Pair this with the bead's `git push` so the code and the bead-state record reach the shared remotes in lockstep and never drift — a remotely-closed bead whose commit is still local is the inconsistency this prevents. (If `git push` is being held until the operator asks, hold the Dolt push the same way and run both together when you publish.)

This step is the **interactive-mode** obligation. In **single-pass autonomous** and **formula autonomous** modes the harness owns it: it already runs `bd dolt push` after closing the work bead (`commitClose`, `internal/djinn/session/session.go`), skipped only under `DryRun`. Formula/autonomous agents must NOT run `bd dolt push` by hand, exactly as they must not run `git push` — the harness owns both.

## 9. Scope discipline

Adjacent work that is out of scope: file a bead for it and move on.
Do not implement it. When filing, carry the intent forward — describe WHY the new bead matters, not just WHAT to build.
