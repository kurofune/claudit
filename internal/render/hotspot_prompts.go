// Hotspot prompts: per-Kind LLM prompt templates. We deliberately keep
// these as Go text/template strings (not pre-rendered) so the user gets
// the raw, copyable prompt with their own numbers substituted in.
package render

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/kurofune/claudit/internal/aggregate"
)

// preamble is the shared "what I want and don't want" preamble. Every
// kind-specific prompt builds on top of this so the user gets consistent
// advice across hotspots.
const preamble = `I'm using Claude Code (Anthropic's agentic coding CLI). I'm trying to reduce one specific cost driver in my usage. I'd like substantive, specific advice — not generic optimization tips.

Common suggestions I've already heard — these are still valid directions, but I only want them included if you can attach concrete substance. Don't recommend them by reflex:
- "Switch to a cheaper model (Sonnet/Haiku)" — fine to recommend if you can name *which slices of this specific workload* tolerate the downgrade and why; skip it as a generic cost lever.
- "Be more efficient" / "use less context" — only useful if you point to a specific lever (a CLAUDE.md line to add, a tool to drop, a context source to prune, a setting to flip).
- "Run fewer commands" — only useful if you name which commands and what replaces them (caching, batching, an alternate tool, an MCP).

Treat these three as needing to *earn* their spot in your answer by carrying the specifics above. Otherwise prefer recommendations the user hasn't already considered.

What I want:
1. Specific tools, CLIs, MCP servers, or scripts (cite repo names, doc URLs, or package names where possible).
2. Workflow patterns from well-reviewed Claude Code or AI-coding practitioners.
3. Concrete config changes (CLAUDE.md patterns, ~/.claude/agents/<x>.md frontmatter, hooks, slash commands, settings.json keys).
4. If a tool category exists for this problem, name 2-3 well-reviewed options and the trade-offs.

Be opinionated and prescriptive. If you don't know of a proven solution, say so plainly rather than inventing one.

Here is the specific cost driver and its data:
`

// templates by HotspotKind. Each is parsed with text/template.
var hotspotTemplates = map[aggregate.HotspotKind]*template.Template{}

func init() {
	parse := func(name, body string) {
		hotspotTemplates[aggregate.HotspotKind(name)] = template.Must(
			template.New(name).Parse(preamble + body))
	}
	parse(string(aggregate.HotspotBashPattern), `
**Cost driver:** Bash invocations matching the pattern `+"`{{.pattern}}`"+`
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend over the report window)
**Frequency:** {{.calls}} call(s) across {{.turns}} assistant turn(s)
**Surrounding turn output tokens:** {{.output_tokens}}
**Most-used model overall:** {{.dominant_model}}

Specifically address:
1. Are there tools or MCP servers that could replace this command (e.g., a state-API-like alternative, a daemon-process that serves results without invoking the CLI each time, or an MCP that wraps the same underlying data)?
2. Does this command's output bloat assistant context (and therefore inflate cache writes/reads on subsequent turns)? If yes, what flags or post-processing reduce its output volume?
3. Are there workflow patterns (e.g., reading state once and caching it in a CLAUDE.md or skill, batching multiple lookups into one) that would cut the call count?
4. Are there well-reviewed Claude Code skills or hooks that handle this category of repetitive read-only command?

For context, common Bash patterns in my data include things like `+"`grep`, `bd show`, `git log`, `go test`, `ls`"+`. The pattern in question is `+"`{{.pattern}}`"+`.
`)

	parse(string(aggregate.HotspotCacheMiss), `
**Cost driver:** Low cache hit rate in project `+"`{{.key}}`"+`
**Estimated wasteable cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend over the report window) — i.e. the row's total cost weighted by (1 − hit_ratio), an estimate of dollars that went to fresh upload + cache writes instead of cheap cache reads.
**Hit ratio:** {{printf "%.1f" .hit_ratio_pct}}%
**Miss tokens (input + cache_create):** {{.miss_tokens}}
**Cache read tokens:** {{.cache_read}}
**Breakdown of miss tokens:** input={{.input_tokens}}, cache_create_5m={{.cache_create_5m}}, cache_create_1h={{.cache_create_1h}}
**Turns in this project:** {{.turns}}

Anthropic prompt-cache primer (so we're aligned on terms): Claude reads context from a content-addressed prefix cache. A "cache hit" is `+"`cache_read`"+` — context already stored, billed at ~10% of input price. A "cache miss" causes either an `+"`input`"+` charge (no cache attempted, full price) or a `+"`cache_create`"+` charge (one-time write at ~125% of input price for 5m TTL, ~200% for 1h TTL). Subsequent turns within the TTL hit on that prefix and pay the cheap read price. A low hit ratio means either (a) the prefix the agent is sending changes turn-to-turn so the cache key keeps missing, (b) the TTL is expiring before the next turn (long gaps between turns), or (c) the agent is in mostly-fresh-input mode (lots of new files / lots of small sessions).

Specifically address:
1. What changes the prefix between turns in a Claude Code session (system prompt, tools list, conversation history, current task message)? Which of these are most prone to drift in a long-running coding session, and how do experienced Claude Code users keep them stable?
2. Are there workflow patterns that *cause* prefix churn — e.g. a CLAUDE.md that changes on every turn, hooks that mutate session state, frequent `+"`/clear`"+`, frequent compaction — and what's the fix for each?
3. For projects where many short sessions run instead of one long session, the 5m/1h cache TTLs expire between sessions. Are there proven techniques (long-running headless sessions, persistent context warmup, context-priming skills) that keep the cache warm across what would otherwise be cold-start sessions?
4. Are there well-reviewed Claude Code skills, hooks, or settings.json keys that explicitly target cache hit rate (cache-aware system prompts, conversation pinning, deterministic tool ordering, pre-warmed context)?
5. Is there an Anthropic-recommended pattern for very long sessions where context window pressure forces compaction, since compaction inherently invalidates the cached prefix? Specifically: compact-and-resume vs new-session-with-handoff-doc.

The project in question is `+"`{{.key}}`"+`. Don't tell me to "just shorten my system prompt" — I want to understand which specific elements of a Claude Code session most commonly cause prefix drift, and the proven workflow patterns to keep them stable.
`)

	parse(string(aggregate.HotspotFileExt), `
**Cost driver:** {{.tool}} of files with extension `+"`{{.pattern}}`"+`
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend)
**Frequency:** {{.calls}} call(s) across {{.turns}} assistant turn(s)
**Most-used model overall:** {{.dominant_model}}

Each {{.tool}} of a file pulls its contents into Claude's context. That content gets cached, and every subsequent turn re-reads it. So this number captures both the direct {{.tool}} cost AND the downstream cache traffic the read created.

Specifically address:
1. Tools, LSP integrations, or MCP servers that let an agent get the *structure* of code (symbols, signatures, references, callers) without reading whole files. Name 2-3 well-reviewed options if they exist (e.g., LSP-MCP servers, ctags-based MCPs, repo-summary tools).
2. Workflow patterns: when should the agent prefer Grep/offset-Reads over full Reads? Are there CLAUDE.md prompts that bias toward narrow reads?
3. For `+"`{{.pattern}}`"+` specifically: are there language-aware tools (formatters, AST queries, type-aware grep) that return just the slice the agent needs?
4. Are there proven hooks or skills that intercept oversized Reads and offer a summary instead?

The tool here is {{.tool}} and the file extension is `+"`{{.pattern}}`"+`.
`)

	parse(string(aggregate.HotspotGrepGlob), `
**Cost driver:** {{.tool}} with the pattern `+"`{{.pattern}}`"+`
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend)
**Frequency:** {{.calls}} call(s) across {{.turns}} assistant turn(s)

Specifically address:
1. Are there pre-built code-intelligence tools or MCPs (LSP-based, repo-graph based, or symbol-index based) that would answer the same question {{.tool}} is being used for, but more cheaply (less context inflation)?
2. Workflow: should the search be performed once and cached as a session artifact, rather than repeated across many turns?
3. Are there CLAUDE.md or skill patterns that scope the agent's searches to a narrower default path/glob, so the result returned to context is smaller?
4. For `+"`{{.pattern}}`"+` specifically — does its breadth suggest the agent is groping rather than acting on a known target? What patterns help an agent commit to a target sooner?
`)

	parse(string(aggregate.HotspotWebHost), `
**Cost driver:** WebFetch calls to `+"`{{.pattern}}`"+`
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of total)
**Frequency:** {{.calls}} call(s)

Specifically address:
1. Does the host have an MCP server or API client wrapper that returns smaller, structured payloads instead of full HTML?
2. Patterns for caching the fetched content as a local file once per session.
3. If the host is documentation, are there offline doc tools or ingest-once-query-many MCPs that fit?
`)

	parse(string(aggregate.HotspotProject), `
**Cost driver:** A specific project — `+"`{{.project}}`"+`
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend over the window)
**Sessions:** {{.sessions}} · **Assistant turns:** {{.turns}}
**Dominant model:** {{.dominant_model}}
**Cache reads in this project:** {{.cache_read}} tokens

Specifically address:
1. CLAUDE.md patterns that have been shown to reduce per-turn cost (e.g., explicit guidance on "read narrowly", domain glossaries, file-map directives).
2. Project-level Claude Code configurations: per-project ~/.claude/projects/.../settings.json overrides, default model selection, hook configurations, project-specific skill files.
3. MCP servers that fit this project's domain (if you can guess from the path) — name 2-3 well-reviewed candidates and what each adds.
4. Workflow patterns from teams running large codebases through Claude Code: how do they bound context, structure subagent fan-out, gate expensive operations?
5. Has anyone published cost-per-turn benchmarks for projects of similar size, and what configuration knobs moved the needle most?

I'd rather hear about 2-3 well-reviewed strategies in depth than a long list of half-remembered ones.
`)

	parse(string(aggregate.HotspotSubagentType), `
**Cost driver:** Subagent type `+"`{{.subagent_type}}`"+`
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend)
**Turns:** {{.turns}}
**Cache read tokens:** {{.cache_read}}

This is my "{{.subagent_type}}" subagent (defined either in Claude Code's built-in agent registry or in my ~/.claude/agents/{{.subagent_type}}.md). It's used for delegated multi-step work in a separate context window.

Specifically address:
1. What MCP servers, tools, or pre-built skills are designed specifically to make the {{.subagent_type}} class of work cheaper? (For example: LSP MCPs for "find references" subagents, indexed-knowledge-base MCPs for explainer subagents, etc.)
2. Are there well-reviewed agent definitions for this kind of work that I should compare against — e.g., on the Anthropic agent gallery, in popular Claude Code dotfiles, or in published repos?
3. What context-shaping patterns reduce the cache write burst that happens at the start of every subagent invocation? (System-prompt diet, narrower tool grants, pre-warmed context files.)
4. For repeated similar subagent invocations: should they be batched into a single longer-running session instead of N short ones?
5. If this is a high-fan-out pattern (many parallel subagents), are there orchestration tools (ralph, magma, agent-zero, etc.) with model-selection-per-task-type features?

Cite specific repos, doc URLs, or known practitioners where you can.
`)

	parse(string(aggregate.HotspotInvocation), `
**Cost driver:** A single subagent run.
**Subagent type:** {{.subagent_type}}
**Launch description:** "{{.description}}"
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend)
**Turns inside this run:** {{.turns}}
**Project:** {{.project}}
**Started:** {{.started}}

This was one subagent invocation that, on its own, cost ${{printf "%.2f" .cost}}. That's high enough to be worth understanding in detail.

Specifically address:
1. From the launch description ("{{.description}}"), what does this run probably look like under the hood — and which kinds of work in such a run inflate cost the most (Reads of many files? Long Bash output? Repeated greps?)?
2. What planning patterns (Plan mode first, then a smaller Implement pass) reduce the cost of a single big run?
3. Should this work have been split into N smaller subagent invocations? When does splitting save money vs. cost more (because of duplicated cache writes per child)?
4. Are there orchestration patterns (ralph, magma, agentic harnesses) that gate large invocations behind a budget — abort if cost exceeds threshold X?
5. If the description suggests "implement feature Y" — what proven CLAUDE.md or agent-prompt patterns keep an agent from going wide on file reads when it should stay focused?

Be specific. The launch description is "{{.description}}" — use it.
`)

	parse(string(aggregate.HotspotPromptPattern), `
**Cost driver:** A habitual user prompt (a normalized prefix of one I keep sending the agent).
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend over the report window)
**Invocations:** {{.invocations}} time(s) across {{.sessions}} session(s) ({{.turns}} downstream assistant turn(s) attributed)

The prompt itself (full text):
"""
{{.sample}}
"""

This is the dimension closest to my actual intent — assistant turns get attributed back through `+"`parentUuid`"+` to the originating user message, so this number captures everything the agent did because of *this specific kind of ask*. If this prompt is recurring and expensive, restructuring how I ask is likely the highest-leverage change available.

Specifically address:
1. Reframe the ask itself: how would an experienced Claude Code user phrase this prompt to get the same outcome with materially less context churn? Show 2-3 reframings, each with the trade-off (more setup time vs. cheaper turns, narrower scope vs. broader, etc.).
2. Should this be a Skill or a slash command instead of a fresh prompt each time? When does codifying repeated prompts as a skill (with pre-canned context, narrowed tool list, deterministic behavior) pay off vs. when does it ossify the workflow?
3. Pre-canned context patterns: would a CLAUDE.md addition, a checked-in handoff doc, or a pinned reference file remove the need to re-establish context every time this prompt is issued?
4. Plan-then-implement: if the agent does a lot of exploratory work in response to this prompt, would running Plan mode first (cheaper) and only then committing to implementation reduce total cost?
5. Are there well-reviewed prompt-engineering patterns specifically for agentic coding (Anthropic's docs, popular Claude Code dotfiles, public agent-prompt libraries) that target prompts of this shape?

Be specific to the actual prompt text above — don't give generic prompt-engineering advice.
`)

	parse(string(aggregate.HotspotSkill), `
**Cost driver:** Skill or slash command `+"`{{.skill_key}}`"+`
**Cost:** ${{printf "%.2f" .cost}} ({{printf "%.1f" .pct}}% of my total Claude Code spend)
**Calls:** {{.calls}} across {{.turns}} turn(s)

A "skill:" prefix is a Skill (loaded from ~/.claude/skills/ or a plugin); a "command:" prefix is a slash command. Each invocation loads the skill's instructions into context, then runs.

Specifically address:
1. Skill design patterns that reduce per-invocation cost: trimmed instruction blocks, lazy-loaded reference material via `+"`pinned-text://`"+` or similar, splitting one fat skill into many narrow ones.
2. Are there well-reviewed reference skills (in the Anthropic skills repo, in popular dotfiles) that handle similar work more cheaply?
3. Should this be a Skill at all — vs a hook, vs a subagent definition, vs an external script? When is each the right choice?
4. If this skill orchestrates other tools, is there a way to delegate the inner work to a cheaper model while keeping the orchestrator on Opus?

The key here is `+"`{{.skill_key}}`"+`.
`)
}

// HotspotPrompt renders the LLM prompt for one hotspot, with all numeric
// context substituted in. Returns the prompt text plus an error if no
// template exists for the kind.
func HotspotPrompt(h aggregate.Hotspot) (string, error) {
	tpl, ok := hotspotTemplates[h.Kind]
	if !ok {
		return "", fmt.Errorf("no prompt template for kind %q", h.Kind)
	}
	// Build a flat context with cost + pct merged in for the template.
	ctx := map[string]any{}
	for k, v := range h.Context {
		ctx[k] = v
	}
	ctx["cost"] = h.CostUSD
	ctx["pct"] = h.PctOfTotal
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()) + "\n", nil
}
