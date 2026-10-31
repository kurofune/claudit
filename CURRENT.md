# CURRENT

**Mode:** v2 initiative. Piece 1 (serve-first) shipped as v2.0.0; Piece 2 (honest numbers) is next.

**Goal:** Make claudit a local daemon (`claudit serve`) plus `watch` whose numbers can be trusted: Piece 2 makes each cost figure say what it actually measures, starting from per-tool-result context-cost attribution.

**Last change:** v2.0.0 (2026-10-31) — report, diff, `--redact` and the static SPA mode removed; bare `claudit` runs serve; Spend replaces Cost/Tokens/Cache; projects grouped by git repo root.

**Assumptions:**
- Projects key on the session cwd's git repo root (`internal/project/`): linked worktrees and subdirectories roll up into their repo; undecodable scratch dirs and deleted cwds land in `scratch` / `missing` buckets.
- Old `#cost` / `#tokens` / `#cache` bookmarks redirect to `#spend` (`web/router.js` `REDIRECTS`).
- Sonnet 5.5 is the first bundled model with rate history (`rates:` until 2026-10-06); Haiku 5.5 is the first with a prompt-length tier (`long_prompt:`).

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** Piece 2's first slice lands — context-cost attribution (in `internal/aggregate/` since claudit-ucr) surfaced in hotspots and the Tools view.

**Stop conditions:**
- Claude Code changes the JSONL schema → parser catch-up becomes the priority.
- Anthropic publishes new model rates → refresh `internal/pricing/default.yaml`
  and cut a release.
