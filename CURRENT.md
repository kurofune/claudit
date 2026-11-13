# CURRENT

**Mode:** Sessions rework and Codex grouping landed and released (v2.1.0). Next: the operator dogfoods Sessions and settles a new design.

**Goal:** Make claudit a local daemon (`claudit serve`) plus `watch` that a stranger can use to see what Claude Code and Codex agents did and what it cost, with numbers that say what they measure.

**Last change:** released v2.1.0 (Fable 5.2 / Mythos 5.2 priced, claudit-d68: a week of Fable 5.2 went $0 → $80.54); v2.0.0 tagged late at ae1d540, where it was cut.

**Assumptions:**
- Projects key on the session cwd's git repo root (`internal/project/`); a deleted cwd outside any surviving repo is its own `missing` bucket (DECISIONS).
- Sessions group by a deterministic prompt-family key (wrapper blocks stripped, first line, ids/paths/numbers blanked); no LLM runs inside claudit.
- Old `#cost` / `#tokens` / `#cache` redirect to `#spend`; `#tools` to `#overview/all`; old `#agents/<lens>` links to the list or session (`web/router.js`).

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** the operator dogfoods Sessions on live data and files what breaks.

**Stop conditions:**
- Claude Code or Codex changes the log schema → parser catch-up becomes the priority.
- Anthropic or OpenAI publishes new model rates → refresh `internal/pricing/default.yaml` and cut a release.
