<!-- djinn init writes this file from its binary; edits are overwritten. -->

## Job 2 — Start the drain

### The workshop decides first

```bash
djinn workshop status
```

- **Workshop up → never start a drain yourself.** The next tick picks the work
  up.
- **Workshop down → offer `djinn workshop up` or a one-off bounded drain.** Run
  whichever the operator picks; a one-off drain follows the rest of Job 2.
- When `djinn workshop status` exits non-zero or reports an unknown command, the
  workshop is down and `djinn workshop up` does not exist here: offer only the
  one-off drain.

```say
It is queued — the workshop picks it up on its next tick.
```

```say
The workshop is off, so nothing drains this on its own. Say workshop up and I will
start it, or say go for a one-off drain of just this work.
```

```say
Nothing drains work here on its own. Say go and I will start a one-off drain of
just this work.
```

### Refuse a second drain

Before any start, run the second-drain refusal and the dead-drain survivor
check in the lead kernel (the djinn block of `AGENTS.md`). Its refusals send
stop and pause through `Stop one bead / pause the drain`
(`.djinn/lead/status.md`).

### Guards, batch size, and budget

- `--max-retries <n>` is mandatory on every start — how many times one bead may
  be respawned (default 2). It is a stuck-bead guard, not a budget.
- `--max-beads <n>` is the batch size — how many beads this drain takes
  (default 5).
- No spending cap unless the operator asked for one.

```say
Starting the drain now: at most <max-beads> beads, <max-retries> retries per
bead. Whatever is already running finishes.
```

#### Only when the operator asked for a budget

Append `--max-cost <usd>` with their number to the start line, add
`and $<usd> total` to the start block, and record the budget in
`$seat/memory.md` until they lift it.

### The drain scope

**A start carries exactly one of `--epic` or `--bead`** — never both, and never
neither:

- `--epic <epic-id>` — the ready descendants of that epic.
- `--bead <id>,<id>` — an explicit list of bead ids, matched exactly against the
  ready set (repeatable, or comma-separated).

With neither flag the summoner drains the repo's **whole
ready set** up to `--max-beads`, which is almost never the work the operator
just agreed to. Job 1 tells you which you have: a new epic from
`/plan-to-beads` → `--epic`; beads filed under an existing epic, or a
hand-picked handful → `--bead` with exactly those ids, so the drain takes
none of that epic's other work.

### Which binary starts the drain

**Never start a drain by building the supervisor from a source path**
(`go` + `run` over `./cmd/summoner`). That path exists
only inside the djinn repo; in any other repo the very first thing the lead
does dies with `stat .../cmd/summoner: directory not
found`. Every start line below invokes the **installed**
`summoner` binary on `$PATH`
(`go install ./cmd/summoner` from a djinn checkout puts
it there).

#### In the djinn repo itself

The repo the supervisor runs in IS the repo the worker is built from, so nothing
extra is needed — one scope flag plus the guards:

```bash
summoner --epic <epic-id> --max-workers <n> --max-beads <n> --max-retries <n>
```

#### In any other repo

A consumer repo MUST pass **both** of these extra flags, and the run dies
without either one:

- `--worker-bin <path to an installed djinn binary>` —
  with the flag ABSENT the summoner "builds
  ./cmd/djinn from its repo root to a temp path at startup
  and execs that", and a consumer repo has no
  `./cmd/djinn` to build. Point it at the installed worker
  (`$HOME/go/bin/djinn`).
- `--allow-worker-drift` — the startup worker-SHA drift guard aborts "before any
  bead is claimed" when the resolved worker's build SHA "does not match repo
  HEAD". The worker was built from djinn and the repo HEAD is the CONSUMER
  repo's, so they can never match; without this flag the drain aborts before it
  claims a single bead. This flag downgrades that hard abort to one logged
  warning.

```bash
summoner --epic <epic-id> --max-workers <n> --max-beads <n> --max-retries <n> \
  --worker-bin "$HOME/go/bin/djinn" --allow-worker-drift
```

Both flags carry through the start line below. Append them whenever `$PWD` is
not a djinn checkout.

### Start it in the background

`--max-workers <n>` is the worker count: take it from the operator's request,
or **omit the flag entirely and let the summoner apply
its own default of 3**. Never invent a number. Start the drain as a
**background process** with exactly one of `--epic` or `--bead` — it writes
`.djinn/summoner-state.json` and one log file per bead, which status and the
wrap-up read:

```bash
nohup summoner --epic <epic-id> --max-workers <n> --max-beads <n> --max-retries <n> > .djinn/drain.out 2>&1 &
# …or, for a bead-list drain, the same command with the other scope flag:
nohup summoner --bead <id>,<id> --max-workers <n> --max-beads <n> --max-retries <n> > .djinn/drain.out 2>&1 &
```

When `HERDR_ENV` is `1`, run `djinn ops room --herdr` once after the start; it
finds or builds the ops room and changes nothing that is already there.
Otherwise run nothing more.

```say
The drain is running in the background. Ask me for status any time and I will
read it back. Per-bead output is in <log-dir>/<run-id>/<bead-id>.log.
```
