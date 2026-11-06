# CURRENT

**Mode:** Sessions rework landed (epic claudit-255): fast opens, a Worth opening block with counted reasons, session page leads with how it ended. Dogfooding it next.

**Goal:** Make claudit a local daemon (`claudit serve`) plus `watch` that a stranger can use to see what Claude Code and Codex agents did and what it cost, with numbers that say what they measure.

**Last change:** session open 9–10 s → under 0.4 s with live logs writing (claudit-jvr); list says what to open (claudit-3om); session page leads with last message and unfixed failures (claudit-9u9).

**Assumptions:**
- Projects key on the session cwd's git repo root (`internal/project/`); a deleted cwd outside any surviving repo is its own `missing` bucket (DECISIONS).
- Sessions group by a deterministic prompt-family key (wrapper blocks stripped, first line, ids/paths/numbers blanked); no LLM runs inside claudit.
- Old `#cost` / `#tokens` / `#cache` redirect to `#spend`; `#tools` to `#overview/all`; old `#agents/<lens>` links to the list or session (`web/router.js`).

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** operator dogfoods the reworked list; known gap: "unfixed failed commands" flags 638 of 1,421 sessions, mostly harmless grep/ls exits.

**Stop conditions:**
- Claude Code or Codex changes the log schema → parser catch-up becomes the priority.
- Anthropic or OpenAI publishes new model rates → refresh `internal/pricing/default.yaml` and cut a release.
