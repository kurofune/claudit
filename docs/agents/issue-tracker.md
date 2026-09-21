# Issue tracker: bd (beads)

Issues and PRDs for this repo live in the local bd (beads) database. Use the
`bd` CLI for all operations. Verified against bd 1.1.2.

## Conventions

- **Work tickets** are type `task`, `feature`, or `bug`. Non-work types:
  `epic` (containers/maps), `decision` (ADR-shaped questions), `spike`
  (timeboxed investigations).
- **Acceptance criteria go in the structured field, always**:
  `bd create ... --acceptance "..."` (or `bd update <id> --acceptance`).
  Body-only markdown checkboxes are INVISIBLE to djinn's gates, which read the
  `acceptance_criteria` JSON field. Never publish a work ticket whose ACs live
  only in the body.
- **Create**: `bd create --type <type> --title "..." --body "..." --acceptance "..."`.
  Use a heredoc for multi-line bodies.
- **Read**: `bd show <id> --json`; comments via `bd comments <id>`.
- **List**: `bd list` with `--label` / `--type` / `--status` filters;
  `bd children <id> --pretty` for a parent's children.
- **Comment**: `bd comment <id> "..."`.
- **Labels**: `--labels` on create; `bd label add/remove <id> <label>`.
- **Dependencies**: `bd dep add <ticket> --blocked-by <blocker>`.
- **Close**: `bd close <id> --reason "..."`.
- **Triage vocabulary** is plain bd labels: `needs-triage`, `needs-info`,
  `ready-for-agent`, `ready-for-human`, `wontfix`.
- **AI-triage disclaimer comments** belong only on public trackers. A private
  bd DB has one audience — the dev — so skip the disclaimer boilerplate.

## When a skill says "publish to the issue tracker"

Create beads with `bd create`, acceptance criteria in `--acceptance`.

- **Where the repo mandates a bead-authoring path** (e.g. `/create-bead` in
  djinn-governed repos), use it instead of raw `bd create` — raw creation
  bypasses the repo's AC-quality gate. The `--acceptance` semantics are
  unchanged: ACs still land in the structured field.
- Publish in dependency order (blockers first), then wire edges in a second
  pass: `bd dep add <ticket> --blocked-by <blocker>`.
- A spec from `/to-spec` is **one bead**.
- Tickets from `/to-tickets` are **one bead each**, parented to the spec bead
  via `bd update <ticket> --parent <spec>`.

## When a skill says "fetch the relevant ticket"

Run `bd show <id> --json`, plus `bd comments <id>` for the discussion.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a single bead with **child** beads as
tickets.

- **Map**: an `epic` bead labelled `wayfinder:map`, holding the
  Destination / Notes / Decisions-so-far / Fog body.
  `bd create --type epic --labels wayfinder:map ...`
- **Child ticket**: a bead parented to the map (`bd update <id> --parent
  <map>`; list with `bd children <map> --pretty`). Labels: `wayfinder:<type>`
  (`research`/`prototype`/`grilling`/`task`).
- **Ticket-type mapping**: grilling and prototype tickets are bd type
  `decision`; research tickets are bd type `spike`; task tickets are `task`.
  HITL task tickets are created **pre-assigned** to the driving dev — an
  assigned bead stays off `bd ready`, so no agent drains it.
- **Blocking**: bd's native dependencies —
  `bd dep add <ticket> --blocked-by <blocker>`. A ticket is unblocked when
  every blocker is closed.
- **Frontier query**: `bd ready` (open + unblocked, drops claimed) **minus
  epics** — `bd ready` lists epics, so filter `"issue_type": "epic"` out of
  `bd ready --json` before picking. First in map order wins.
- **Claim**: `bd update <id> --claim` — atomic; the session's first write.
- **Resolve**: post the answer as a comment (`bd comment <id> "<answer>"`),
  then `bd close <id> --reason "<gist>"`, then append a context pointer to the
  map's Decisions-so-far.

## Deference and implementation

Where a repo carries its own bead-work contract (djinn-governed repos), **that
contract outranks this doc** — this doc covers publish/fetch/wayfinding
mechanics only.

Implementing a bead:

- **djinn-governed repos**: `/wish <bead-id>` — never a manual build.
- **Non-djinn repos**: the manual spine — `tdd-canon` at the agreed seams →
  typecheck regularly, run the full suite once → review → commit per the
  repo's convention.
