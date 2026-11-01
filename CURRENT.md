# CURRENT

**Mode:** v3 landed (one Sessions surface; Tools folded into Overview). Polish from dogfooding is next.

**Goal:** Make claudit a local daemon (`claudit serve`) plus `watch` that a stranger can use to see what Claude Code and Codex agents did and what it cost, with numbers that say what they measure.

**Last change:** Codex `exec` calls named by their tools.* function; write_stdin polls fold into Bash (`exec` row 1,240 → 6 calls); Worth a look calibrated, sessions with a note 1060 → 456 (claudit-hts, claudit-5mp).

**Assumptions:**
- Projects key on the session cwd's git repo root (`internal/project/`); a deleted cwd outside any surviving repo is its own `missing` bucket (DECISIONS).
- Sessions group by a deterministic prompt-family key (wrapper blocks stripped, first line, ids/paths/numbers blanked); no LLM runs inside claudit.
- Old `#cost` / `#tokens` / `#cache` redirect to `#spend`; `#tools` to `#overview/all`; old `#agents/<lens>` links to the list or session (`web/router.js`).

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** next dogfooding pass by the operator on the Sessions surface.

**Stop conditions:**
- Claude Code or Codex changes the log schema → parser catch-up becomes the priority.
- Anthropic or OpenAI publishes new model rates → refresh `internal/pricing/default.yaml` and cut a release.
