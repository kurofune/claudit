# claudit — Agents View Redesign

Captured 2026-06-07. A living roadmap for turning the Agents tab (Mission Control /
Inspector / Flow) from three disconnected views into **one trace viewer you can audit**.
Update the **Status** line and **Progress log** as each phase lands. Build one phase at a
time — each phase section below is self-contained so it can be picked up cold without
re-reading the others.

**Status:** Phases 1 ✅, 2 ✅ & 3 ✅ complete (tests green, browser-verified). Decisions locked (see below).

---

## Why we're doing this

The user's complaint, precisely diagnosed against the code:

- **"main read .md — read *what*?"** — a **UI gap, not a data gap**. Every tool event
  already carries `input` / `output` / `status` (`agents-logic.js` `buildEventFeed`;
  backed by `ToolInvocation.Input/Output/Status`, `internal/aggregate/sessions.go:84`).
  Mission Control buries the filename in a hover `title=` and makes the row unclickable
  (`web/view-agents.js:284`).
- **The "1/12 · Sonnet 4-6 · 5¢ · and nothing" turn** — a **real data gap**. `parse.Turn`
  (`internal/parse/parse.go:63`) keeps `ToolUses` but has no assistant-text field, so a
  turn that only reasoned (no tool) has nothing stored to show. The other example (the
  expandable Bash `git show`) had a tool, hence had I/O. That asymmetry *is* the bug.
- **Opaque session IDs** — project is already captured (`session.cwd`, we compute
  `baseName(cwd)`); it's just rendered as a subtitle instead of the headline.
- **Flow graph has no time** — it uses a static centered grid (`buildFlowLayout`). The
  Gantt/swimlane math already exists and is unit-tested: `packLanes` / `laneCount` /
  `makeTimeScale` / `agentBar` (`web/agents-logic.js:47-109`). The temporal view is
  half-built in the helper layer already.

**The through-line:** Mission Control / Inspector / Flow aren't three features — they're
three *lenses* on one trace that's missing a shared, clickable detail panel.

## Decisions (locked 2026-06-07)

1. **Structure — unify into one trace + drawer.** Feed / Tree / Timeline are lenses over
   ONE selection model + ONE persistent detail drawer. Click anything in any lens → same
   drawer. No dead/unclickable rows.
2. **Capture reasoning text — yes.** Thread assistant text through
   parse → aggregate → agentflow → step (`--redact`-aware). Makes every turn auditable.
3. **Audit depth — snippet + load-full on demand.** Bounded snippet by default; a "show
   full" toggle reads untruncated I/O / reasoning from the JSONL on disk. Serve-mode only;
   the static HTML report falls back to the snippet (no disk at view time).
4. **Sequencing — expert's call** (the phase order below).

## Research basis

Surveyed LangSmith, Langfuse, Arize Phoenix, AgentOps, Helicone, Braintrust (LLM trace
viewers) and Honeycomb / Jaeger / Chrome DevTools (distributed-tracing waterfalls). They
converge hard:

- **Master-detail with a persistent detail panel; every node clickable; selection shared.**
- **Reasoning is just a typed span whose output is text** — never special-cased into a dead
  row.
- **Label by project/repo/task; demote the opaque id** to copyable metadata.
- **For order + concurrency, use a horizontal Gantt/waterfall on a real time axis** — one
  row per agent, indented by spawn hierarchy, overlap = concurrent, bar width = duration,
  click a bar → detail. Horizontal beats vertical: overlap reads instantly and width gives
  duration-to-scale for free.
- **Live = the same component growing**: "now" pinned to the right edge, bars grow, new
  lanes fade in, auto-follow **only while at the live edge** (the #1 live-UX trap is
  auto-scroll yanking the view while you inspect history).

Strongest screenshot references: AgentOps Session Waterfall
(https://docs.agentops.ai/v1/usage/dashboard-info), Langfuse trace view
(https://langfuse.com/changelog/2025-03-19-new-trace-view), Honeycomb trace waterfall
(https://docs.honeycomb.io/reference/honeycomb-ui/query/trace-waterfall), Phoenix Agent
Graph (https://arize.com/blog/new-in-arize-ax-experiment-comparisons-better-data-visualization-and-a-dedicated-agent-graph-tab/).

## Architecture spine (shared by every phase)

- **One `selectedRef`** identifying agent / step / tool, shared across all lenses; default-
  select the root so the drawer is never empty. Survives refetch and live updates; live mode
  auto-selects the newest event *unless* the user has pinned a selection.
- **One persistent drawer** rendering the full audit payload: input, output, status (✓/✗),
  reasoning, tokens, cost, model, duration — empty sections collapse, never disappear.
- **Consistent color + icon by kind** (Read / Edit / Bash / Grep / Task / reasoning / error),
  applied identically in every lens.
- Pure logic lives in `web/agents-logic.js` (TDD'd in `jstest/agents-logic.test.js`); DOM/SVG
  stays in `web/view-agents.js`. Same split the file already documents.

## Testing obligations (per `.claude/rules/testing.md`)

- Backend (Phases 1, 5) and frontend-logic (selection model, drawer payload shaping,
  timeline geometry) are **TDD-required** — run `/tdd` in a subagent, red-green-refactor.
- Pure UI/styling is browser-verified (playwright-cli), tests after visual confirmation.

---

## Phase 1 — Capture reasoning text  *(backend, TDD)*  — **Status: ✅ complete**

**Goal.** Close the one real data gap so tool-less turns become auditable.

**Scope.**
- Add an assistant-text field to `parse.Turn` (`internal/parse/parse.go:63`); populate it
  from the assistant message's `text` content blocks.
- Thread it through `internal/aggregate` → `internal/agentflow.AgentStep` as
  `Text string json:"text,omitempty"`.
- Redaction-aware: under `--redact`, replace with the length-echoing marker like tool inputs.

**Files.** `internal/parse/parse.go` (+ testdata), `internal/aggregate/sessions.go`,
`internal/agentflow/graph.go`, respective `_test.go`.

**Done when.** A turn with no tool call carries its reasoning text through to the
`/_claudit/api/agents` payload; `--redact` masks it; tests green.

## Phase 2 — Selection spine + detail drawer + project-first  *(frontend-logic TDD + UI browser-verify)*  — **Status: ✅ complete**

**Goal.** The "I can finally drill in" win. Built directly in the unified shell so it's not
throwaway.

**Scope.**
- `selectedRef` selection model + the drawer payload shaper, in `agents-logic.js` (TDD).
- Persistent drawer renders the full audit payload from existing data + Phase 1 reasoning.
- Make every feed row / active card / tree node clickable → sets selection → repaints drawer.
  **Kill the dead row.**
- Project name leads (`baseName(cwd)`); session UUID → copyable metadata.
- Color + icon by kind; compact per-row cost·duration metric (trace doubles as spend heat-map).
- Default-select root.

**Files.** `web/agents-logic.js`, `jstest/agents-logic.test.js`, `web/view-agents.js`,
`web/app.css`.

**Done when.** Clicking any row/card/node anywhere fills one shared drawer with full detail;
no unclickable rows remain; project name is the headline; browser-verified.

## Phase 3 — Unify the IA into lenses  *(UI)*  — **Status: ✅ complete**

**Goal.** Realize the "unify" decision: lenses over one selection.

**Scope.**
- Mission Control → **Feed** lens; Inspector → **Tree** lens. Sub-tab nav becomes a lens
  switch that swaps the LEFT pane while the drawer persists.
- Selection + drawer state carry across lens switches unchanged.

**Files.** `web/view-agents.js`, `web/app.css`.

**Done when.** Switching lenses keeps the same selection + drawer; the left pane is the only
thing that changes.

## Phase 4 — Timeline (Gantt) lens  *(geometry TDD'd already; UI browser-verify)*  — **Status: not started**

**Goal.** Replace the static flow graph with a horizontal, time-axis swimlane.

**Scope.**
- New Timeline lens using existing `packLanes` / `makeTimeScale` / `agentBar`. One row per
  agent, indented by spawn hierarchy, bar = lifetime, overlap = concurrency. Bars click →
  same drawer.
- Live: "now" pinned right, bars grow, new lanes fade in, **auto-follow only at the live
  edge**, with a "● jump to now" button. Render-batched so bursts don't thrash.
- Retire `buildFlowLayout` (or keep as a deprecated peer until the timeline proves out).

**Files.** `web/agents-logic.js` (any new geometry helpers, TDD), `jstest/agents-logic.test.js`,
`web/view-agents.js`, `web/app.css`.

**Done when.** The Timeline lens shows agents on a real time axis with correct overlap; live
runs grow at the right edge without yanking the viewport; bars drill into the drawer.

## Phase 5 — Load-full-on-demand  *(backend endpoint, TDD)*  — **Status: not started**

**Goal.** True "audit everything" without bloating the default payload.

**Scope.**
- New serve endpoint to fetch untruncated input/output + full reasoning for a `tool_use` id /
  step, read from the JSONL on disk.
- Drawer "show full" toggle calls it on demand. Serve-mode only; static report falls back to
  the snippet (clearly, not silently).

**Files.** `internal/serve/api_agents.go` (or a sibling handler), `internal/serve/*_test.go`,
`web/view-agents.js`, `web/api.js`.

**Done when.** The drawer can expand any truncated field to its full on-disk content in serve
mode; static report degrades gracefully.

## Phase 6 *(optional fast-follow)* — Scrubber / playhead  *(frontend-logic TDD + UI)*  — **Status: not started**

**Goal.** "Press play and watch the graph build itself."

**Scope.**
- Draggable playhead computing "state at time T" as a **pure recompute from events ≤ T**
  (avoids the session-replay incremental-seek desync bug class). Live mode = playhead
  auto-advancing.

**Files.** `web/agents-logic.js` (state-at-T recompute, TDD), `jstest/agents-logic.test.js`,
`web/view-agents.js`, `web/app.css`.

**Done when.** Scrubbing to any T renders the timeline + drawer as of that instant; seeking
anywhere is always correct; live = the playhead at "now".

---

## Out of scope (for this redesign)

- Mobile / responsive — claudit is desktop-only; a wide timeline is fine.
- Mutating or annotating sessions — read-only audit tool.
- A flame/icicle density toggle — possible later power-user view; not now.

## Progress log

- 2026-06-07 — Roadmap captured. Research + data-model audit done; decisions locked
  (unify + drawer, capture reasoning, snippet+load-full, expert-sequenced).
- 2026-06-07 — Phase 1 done (TDD, red-green-refactor). Real sessions carry distinct
  `thinking` / `text` / `tool_use` blocks, so reasoning is captured as TWO fields
  (not concatenated): `parse.Turn.Thinking` + `.Text` via new `extractAssistantText`,
  threaded to `agentflow.AgentStep.Thinking` + `.Text` (json `omitempty`), redacted via
  newly-exported `aggregate.RedactMarker` (empty fields stay empty). 5 new tests; full
  suite green. Not yet committed — backend Phase 1 is entangled with pre-existing
  tool-outcome work in the same files; staging deferred to the user.
- 2026-06-07 — Phase 2 done (frontend-logic TDD + UI browser-verify). Built the unified
  shell: a two-column body (`.agents-body`) with the three lenses sharing ONE selection
  (`selectedRef`, a `refKey` string) and ONE persistent drawer. New pure helpers in
  `agents-logic.js` (TDD, +26 tests → 89 total): `refKey`/`parseRefKey` (agent/step/tool
  refs), `defaultRef`, `resolveRef` (stale-ref tolerant), `buildDrawerPayload` (flat
  always-present audit record; a tool inherits its parent step's reasoning/model/cost),
  `agentTokens` (reads the Go-marshalled `InputTokens…` names — **fixed a latent bug** where
  the old `totalTokens` read non-existent `t.input/…` and always showed 0), `baseName`;
  plus `buildEventFeed` tool events now carry `stepIndex`/`toolIndex`/`cost_usd`/`durationMs`.
  `view-agents.js` rewritten: every feed row / active card / tree agent·step·tool / flow
  node carries `data-ref` and is click+keyboard selectable via one delegated handler → the
  drawer repaints with the full payload (input, output, status ✓/✗, reasoning, narration,
  tokens, cost, model, duration; empty sections collapse to a dim header, never vanish). The
  dead/unclickable feed row is gone. Project name (`baseName(cwd)`) leads every header; the
  session UUID is demoted to a copy-on-click chip. Color+icon by kind via `.kind-badge`
  monograms. Compact per-row cost·duration metric on the feed. Default-selects the root.
  Browser-verified across all three lenses (selection persists across lens switches); full
  JS + Go suites green. Not yet committed — same entanglement as Phase 1; staging is the
  user's call.
- 2026-06-07 — Phase 3 done (UI/IA, browser-verified). The sub-tab nav is now a LENS
  SWITCH: **Mission Control → Feed**, **Inspector → Tree** (Flow graph kept as the Phase 4
  Timeline precursor). Renamed the user-facing labels AND the URL hash segments to match
  the lens names (`#agents/control`→`#agents/feed`, `#agents/inspector`→`#agents/tree`;
  `flow` unchanged) — the router is generic (`sub` = rest-of-hash) and the agents view
  shipped on this same redesign branch, so there were no external bookmarks to preserve.
  Internal helper/CSS names (`renderInspector`, `insp-*`) deliberately left alone to keep
  the diff IA-only and low-risk. Made the persistence **intentional**: a pure lens switch
  now passes `paintDrawer=false` to `renderActive`, so the right pane is left physically
  untouched (no flicker, no loss of drawer scroll / copy-button state) while only the left
  pane swaps; first paint + live updates still repaint it. Browser-verified with
  playwright `eval` (immune to the live-feed ref churn): selecting a sub-agent then cycling
  Feed→Flow→Tree→Feed keeps the exact same drawer (`djinn:review-triage` / sub-agent /
  matching sid) every time, while the active lens content swaps correctly. JS suite 89/89,
  Go all green. Not yet committed — staging is the user's call.
