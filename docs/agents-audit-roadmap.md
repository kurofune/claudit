# claudit — Session Audit Roadmap (Phoenix-inspired)

Captured 2026-06-07. Successor to `docs/agents-redesign.md` (the trace-viewer redesign,
all 6 phases shipped). That work made the Agents tab **one trace you can read**; this work
makes it **one trace you can interrogate**. The framing is deliberate: claudit started as a
spend report but is now a tool to *audit a whole Claude session* — what happened, in what
order, why, and where it went wrong — with cost as one dimension among many.

Build one phase at a time. Each phase below is self-contained and ordered by dependency:
later phases lean on the data introduced earlier. Backend and frontend-logic changes are
TDD (red-green-refactor) per `.claude/rules/testing.md`; UI/styling is browser-verified.

**Status:** Phases 1 ✅, 2 ✅, and 3 ✅ complete (2026-06-08). Phases 4 → 5 remain,
in dependency order.

- **Phase 1** — `aggregate.ToolKind` enum + `ToolInvocation.kind` (commit 41cc525);
  frontend colors every Feed/Tree row + drawer badge by kind, `agent` loudest
  (commit cd01cc8).
- **Phase 2** — pure `filterTrace(graph, spec)` / `specActive` + 12 jstest cases
  (commit addcff6); filter bar above the lenses (kind chips, errors toggle,
  slow/expensive thresholds, free text) with cross-lens dimming + `N matches`
  prev/next. Follow-ups deferred: the query-DSL form, and intersecting the filter
  with the playhead window.
- **Phase 3** — `AgentNode.ErrorCount` + session-level total, rolled up in
  `finalizeNode` / the session loop (commit bd92b72); pure `detectRetries(agent)`
  → `Map<"si:ti", {attempt, ofRef}>` + 4 jstest cases (commit 0637936); UI
  (commit edda57d): red error pip in the timeline gutter (scroll-independent),
  `✗N` badge on agent cards + detail head, and a drawer `↻ attempt N of M`
  affordance that links back to the first attempt. The Phase-2 `errorsOnly`
  toggle reads the same per-tool `status`.

---

## Why these five (the Phoenix borrow, distilled)

We surveyed Arize Phoenix's tracing model. Phoenix is OpenTelemetry-native — every
operation is a span with a `kind` (`LLM`/`CHAIN`/`TOOL`/`AGENT`/`RETRIEVER`), a real
`parentId`, `status`/exceptions, latency, and cumulative-vs-self token/cost rollups; the UI
is a filterable `SpansTable` → waterfall span-tree → `SpanDetails` panel, with sessions
rendered as chat threads. We can't adopt their *ingestion* (we reverse-engineer spans from
`.jsonl` after the fact, not from a live OTel exporter), and their eval/annotation platform
is out of scope. But five of their *modeling and UI conventions* map cleanly onto what we
already have, and each one advances "audit your session":

1. **Span `kind` as a first-class enum** — query behavior, not name-match strings.
2. **A filter/search over the trace** — the primary verb of auditing ("find where it broke").
3. **`status`/exception emphasis** — errors and retries as first-class, not buried.
4. **Cumulative-vs-self rollups + real `parentId`** — the blast radius of one decision.
5. **Sessions-as-conversation** — "what did I ask, how did it respond," end to end.

## What we already have (don't rebuild)

- Span tree: `AgentGraph` → `AgentSession` → `AgentNode` → `AgentStep` → `ToolInvocation`
  (`internal/agentflow/graph.go`).
- Waterfall/Gantt, Feed, Tree lenses + shared drawer + playhead (`web/view-agents.js`,
  pure math in `web/agents-logic.js`).
- Per-tool `Status` "ok"/"error" joined from `tool_result` (`internal/aggregate/sessions.go:89`).
- Per-step/per-agent cost, tokens, `DurationMs` (`graph.go:48,62,65`).
- Sub-agent detection by SourceFile + `.meta.json` (`internal/parse/parse.go:514,532`).
- Prompt→turn segmentation already computed for the Sessions tab
  (`aggregate.BuildSessionTimelines`).

---

## Phase 1 — Tool `kind` enum

**Goal.** Tag every tool call with a normalized category so the timeline, drawer, and
(Phase 2) filter key off a stable enum instead of matching tool-name strings. This is the
spine the other phases hang on.

**The one that matters.** `agent` (an `Agent` tool_use = a sub-agent spawn) is the
audit-critical category — it's the edge Phase 4 rolls cost up across.

**Data / contract.** Add one field to `aggregate.ToolInvocation` (`sessions.go:77`):

```go
// Kind is the normalized tool category — "agent", "exec", "read", "edit",
// "web", "skill", "command", "mcp", "todo", "other" — derived from Name by
// ToolKind. Lets the frontend filter and color by category without matching
// raw tool names. Distinct from AgentNode.Kind ("main"/"subagent").
Kind string `json:"kind"`
```

Naming note: `AgentNode.Kind` (`graph.go:41`) already means main-vs-subagent. Keep the
docstring's "distinct from" line so the two `Kind`s don't get conflated.

**Backend.** New pure classifier in `internal/aggregate` (e.g. `kind.go`):

```go
func ToolKind(name string) string // "Agent"→"agent", "Bash"→"exec",
  // "Read"/"Glob"/"Grep"/"LS"/"NotebookRead"→"read",
  // "Edit"/"Write"/"MultiEdit"/"NotebookEdit"→"edit",
  // "WebFetch"/"WebSearch"→"web", "Skill"→"skill",
  // "SlashCommand"→"command", "TodoWrite"/"TodoRead"→"todo",
  // strings.HasPrefix(name,"mcp__")→"mcp", default "other".
```

Populate `Kind` where `ToolInvocation` is built — `DistinctToolInvocations`
(`internal/aggregate`, called from `graph.go:182` and the Sessions path), so **both** the
Agents payload and Sessions drill-down get it free.

**TDD target.** `internal/aggregate/kind_test.go` — table test mapping representative names
(including `mcp__foo__bar` and an unknown) to expected kinds. Red first.

**Frontend (UI, browser-verified).** Color/icon timeline bars and feed/tree rows by `kind`;
the `agent` kind gets the most distinct treatment. No logic change beyond reading the field.

**Acceptance.** Every `ToolInvocation` in `/api/agents` and the static report carries a
non-empty `kind`; `Agent` calls read `"agent"`.

---

## Phase 2 — Filter / search over the trace

**Goal.** Turn the trace from something you *read* into something you *interrogate*. This is
the highest-value audit capability: "show only errors," "tools slower than N s," "this
sub-agent type," "anything mentioning `payload.go`."

**Design decision — frontend-first, structured chips before DSL.** Implement as a pure
predicate over the already-loaded `AgentGraph`, in `web/agents-logic.js`. Rationale: instant
(no round-trip), works in the **offline static HTML report** (which has no server), and
keeps the contract unchanged. Phoenix's query DSL (`latency_ms > 5000 and status == 'error'`)
is the eventual target — note it as a follow-up, ship chips + free-text first. Don't
silently cap matches; show the count.

**Logic (TDD).** New pure helper in `agents-logic.js`:

```js
// filterTrace(graph, spec) -> Set<refKey>  (refKeys reuse the existing
// "sid#ai" / "sid#ai.si" / "sid#ai.si:ti" scheme, agents-logic.js ~:486).
// spec: { text, kinds:[], errorsOnly, minDurationMs, minCostUSD, agentType }
//   text  → substring match over tool name/detail/input/output,
//           step thinking/text, agent description/agent_type
//   kinds → ToolInvocation.kind ∈ kinds (Phase 1)
//   errorsOnly → tool Status === "error"
//   minDurationMs → AgentStep.duration_ms ≥ threshold (stalls)
//   minCostUSD    → step/agent cost ≥ threshold
//   agentType     → AgentNode.agent_type === value
// A parent ref is included if any descendant matches (so the tree stays navigable).
```

**TDD target.** `jstest/agents-logic.test.js` — `filterTrace` returns the expected refKey
set for each spec dimension and combinations (AND semantics). Red first.

**UI (browser-verified).** A filter bar above the lenses: kind chips, an "errors only"
toggle, duration/cost threshold inputs, a free-text box. Non-matching rows dim across **all**
lenses (Feed/Tree/Timeline); show "N matches" with prev/next to step the selection through
hits. Plays with the playhead (filter the visible window).

**Acceptance.** Typing a filename narrows every lens to steps that touched it; "errors only"
+ a kind chip compose; the same filter works in the static HTML report with no network.

---

## Phase 3 — Error & retry surfacing

**Goal.** Make failures and recoveries first-class — the core "where did it go wrong"
question. We have per-tool `Status` but never surface error *patterns* or retries.

**Part A — error counts (backend, TDD).** Roll up error counts so cards/timeline/filter can
heat-map them. Add to `AgentNode` (`graph.go:40`):

```go
ErrorCount int `json:"error_count"` // tool calls with Status=="error" in this agent
```

Sum into a session-level total on `AgentSession` too. Compute in `finalizeNode`
(`graph.go:244`) / the session rollup loop (`graph.go:189`).

- **TDD target.** `internal/agentflow/graph_test.go` — a snapshot with one errored and one
  ok tool yields `ErrorCount==1` on the node and the session total.

**Part B — retry detection (frontend logic, TDD).** Retries aren't in the source data;
derive them. Pure helper in `agents-logic.js`:

```js
// detectRetries(agent) -> Map<refKey,{attempt:int, ofRef:string}>
// Walk an agent's tool calls in time order; group by (kind, name, detail).
// When a call with that key follows an earlier *errored* call of the same key,
// mark it attempt 2,3,… linked back to the first (ofRef).
```

Kept frontend to match the redesign's "derivations live in agents-logic.js" split and avoid
bloating the contract.

- **TDD target.** `jstest/agents-logic.test.js` — error-then-same-call ⇒ attempt 2 linked
  to the first; success-then-same-call ⇒ no retry.

**UI (browser-verified).** Red error pip on timeline bars and an "N errors" badge on agent
cards; in the drawer, group a retry chain with a ↻ "attempt 2 of …" affordance. Wire the
Phase-2 `errorsOnly` toggle to this data.

**Acceptance.** An agent that retried a failed `Bash` shows the retry chain in the drawer and
contributes to its card's error badge; `errorsOnly` jumps to it.

---

## Phase 4 — Exact parent link + cumulative rollups

**Goal.** Tie each sub-agent to the **exact** tool call that spawned it, then roll the
sub-agent's cost/tokens/duration/errors up onto that call — the "blast radius of one
decision" view. Today parentage is *inferred* from SourceFile + start-time ordering
(`graph.go:151,210`); we can make it exact.

**Key finding (verified 2026-06-07).** The sub-agent's sibling `agent-<id>.meta.json` carries
a `toolUseId` field that resolves exactly to the `Agent` tool_use in the parent session file.
Example: `{"agentType":"review-triage","description":"…","toolUseId":"toolu_01N5q2…"}` →
that `toolu_…` is a `name:"Agent"` tool_use in `<sessionId>.jsonl`. `ReadSubagentMeta`
(`parse.go:532`) currently parses only `agentType`/`description` and discards `toolUseId`.

**Step 1 — capture the id (backend, TDD).** Extend `parse.SubagentMeta` (`parse.go:525`) and
`ReadSubagentMeta` with `ToolUseID string` (`json:"toolUseId"`).

- **TDD target.** `internal/parse/parse_test.go` — a meta.json with `toolUseId` round-trips
  into `SubagentMeta.ToolUseID`.

**Step 2 — link + roll up (backend, TDD).** In `agentflow`:

- Carry `ToolUseID` onto the subagent node: add `ParentToolUseID string` to `AgentNode` —
  set from meta in `finalizeNode` (`graph.go:270`).
- Build a `map[toolUseID]*AgentNode` for a session's children, then attach a rollup to the
  spawning `ToolInvocation` (the `Agent` call whose `ID` == child's `ParentToolUseID`). Add
  to `aggregate.ToolInvocation`:

```go
// Spawned is the rolled-up cost of the sub-agent this Agent call launched —
// nil for non-Agent calls. Surfaces one decision's full blast radius.
Spawned *SpawnRollup `json:"spawned,omitempty"`
// SpawnRollup: AgentRef string; CostUSD float64; Tokens Tokens;
//              DurationMs int64; ErrorCount int
```

`AgentSession.CostUSD` is already the cumulative sum (`graph.go:199`) — that's the "root
cumulative." The new bit is *per-spawn* attribution + the exact reverse link.

- **TDD target.** `graph_test.go` — parent turn with `Agent` tool_use id X + a subagent whose
  `meta.toolUseId==X` ⇒ that subagent's `ParentToolUseID==X`, and the parent step's `Agent`
  `ToolInvocation.Spawned` carries the child's cost/tokens/errors.

**UI (browser-verified).** Tree lens nests each sub-agent under the exact step/tool that
spawned it (not a flat children list). The spawning `Agent` row shows "+ $X · N tools · M
errors across sub-agent" inline; the drawer for an `Agent` call links to the child agent.

**Acceptance.** Clicking the `Agent` tool call jumps to its sub-agent; the call's row shows
the sub-agent's total cost; session totals are unchanged (rollup is attribution, not
double-count).

---

## Phase 5 — Sessions-as-conversation lens

**Goal.** A fourth lens answering "what did I actually ask, and how did it respond, across
the whole session" — Phoenix's chat-thread session view. The data exists
(`aggregate.BuildSessionTimelines` walks `parentUuid` chains from turn → originating
`UserMessage`); we surface it as a lens sharing the same selection/drawer.

**Data / contract (backend, TDD).** Add prompt segmentation to the agents payload so the lens
needs no second endpoint. On `AgentSession`:

```go
// Prompts segments the main agent's timeline by user prompt, in order, so the
// Conversation lens can interleave "what was asked" with the turns it produced.
Prompts []PromptMarker `json:"prompts"`
// PromptMarker: UUID string; Text string (truncated, --redact-aware);
//               Timestamp time.Time; FirstStepIndex int (into Main.Steps)
```

Reuse the `parentUuid`-walk logic already proven in `BuildSessionTimelines` (extract a shared
helper rather than duplicating the chain walk). Honor `Options.Redact` (mirror `graph.go:170`).

- **TDD target.** `graph_test.go` — a session with two prompts and N turns yields two
  `PromptMarker`s in order with correct `FirstStepIndex` boundaries; redaction replaces text.

**UI (browser-verified).** New "Conversation" lens: user-prompt bubbles interleaved with
assistant turn cards (model · cost · tools · thinking snippet), segmented by `PromptMarker`.
Every card clickable into the **same** shared drawer (reuse the `sid#ai.si` refKeys). Reuses
existing step-card rendering.

**Acceptance.** Prompts render in order, each followed by exactly its turns; selecting a turn
opens the shared drawer; redaction is honored; switching from another lens preserves the
selection.

---

## Sequencing rationale

- **1 before 2/3/4** — `kind` is the field the filter, retry grouping, and spawn rollup all
  key off (`agent` kind = the rollup edge).
- **2 early** — the filter is the audit verb; everything downstream is more useful once you
  can isolate a subset.
- **3 before 4** — `ErrorCount` rolls up cleanly once errors are first-class; retry data
  enriches the drawer the spawn rollup also writes to.
- **4 before 5** — the exact parent link makes the tree (and any conversation nesting) exact.
- **5 last** — biggest surface, leans on prompt segmentation and the now-exact tree.

## Out of scope (Phoenix features we are *not* borrowing)

- OTel/OpenInference ingestion — wrong model for a post-hoc `.jsonl` auditor.
- LLM-as-judge / automated evals — that's an eval platform, not a session auditor.
- Their DB + streaming pipeline (`BulkInserter`, Prometheus) — our corpus-snapshot + SSE
  design already fits our scale.
- **Annotations** (human notes on a span) — genuinely in-scope for *auditing* and worth a
  future phase, but deferred: it needs a writable store, which the static-report path lacks.
