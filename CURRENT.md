# CURRENT

**Mode:** v2 initiative. Piece 1 (serve-first) shipped as v2.0.0; Piece 2 (honest numbers) is next.

**Goal:** Make claudit a local daemon (`claudit serve`) plus `watch` whose numbers can be trusted: Piece 2 makes each cost figure say what it actually measures, starting from per-tool-result context-cost attribution.

**Last change:** re-measured by-project rows with no name-based reassignment: 637 (1540 before repo grouping), 621 of them missing buckets for deleted worktrees (claudit-80r).

**Assumptions:**
- Projects key on the session cwd's git repo root (`internal/project/`): linked worktrees and subdirectories roll up into their repo; undecodable scratch dirs land in `scratch`; a deleted cwd outside any surviving repo is its own `missing` bucket named by basename (621 of 637 rows; DECISIONS).
- Old `#cost` / `#tokens` / `#cache` bookmarks redirect to `#spend` (`web/router.js` `REDIRECTS`).
- Sonnet 5.5 is the first bundled model with rate history (`rates:` until 2026-10-06); Haiku 5.5 is the first with a prompt-length tier (`long_prompt:`).

**Open questions:** see DECISIONS.md → Open questions.

**Next checkpoint:** Piece 2's first slice lands — context-cost attribution (in `internal/aggregate/` since claudit-ucr) surfaced in hotspots and the Tools view.

**Stop conditions:**
- Claude Code changes the JSONL schema → parser catch-up becomes the priority.
- Anthropic publishes new model rates → refresh `internal/pricing/default.yaml`
  and cut a release.
