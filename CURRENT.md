# CURRENT

**Mode:** Sessions rework done (epic claudit-255 closed). Next: a Sessions tab that stays light while live logs write, a session page whose tiles agree with its failure block, and Codex runs grouped by what they did.

**Goal:** Make claudit a local daemon (`claudit serve`) plus `watch` that a stranger can use to see what Claude Code and Codex agents did and what it cost, with numbers that say what they measure.

**Last change:** failing-check flags 81 → 5 sessions (claudit-b0r): a check counts only when the call's exit is surely its own (final command, not gated by a failable `&&`, input not truncated); all 5 left hand-checked true.

**Assumptions:**
- Projects key on the session cwd's git repo root (`internal/project/`); a deleted cwd outside any surviving repo is its own `missing` bucket (DECISIONS).
- Sessions group by a deterministic prompt-family key (wrapper blocks stripped, first line, ids/paths/numbers blanked); no LLM runs inside claudit.
- Old `#cost` / `#tokens` / `#cache` redirect to `#spend`; `#tools` to `#overview/all`; old `#agents/<lens>` links to the list or session (`web/router.js`).

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** the drain of the memory, session-page and Codex-grouping beads lands; then the operator dogfoods Sessions.

**Stop conditions:**
- Claude Code or Codex changes the log schema → parser catch-up becomes the priority.
- Anthropic or OpenAI publishes new model rates → refresh `internal/pricing/default.yaml` and cut a release.
