# claudit — Design Context

This file captures design context for the claudit project so contributors (human or AI) can produce work grounded in the project's purpose and aesthetic, not generic defaults.

## Design Context

### Users

Developers and engineers who use Claude Code (Anthropic's CLI) on a daily basis. They open a claudit report when they want to know **where their token spend went** — which models, which projects, which prompts, which tools, which subagents. They are technical, comfortable in monospace, and care about scannability over storytelling. Reports are typically opened once per audit session in a browser; users skim totals first, then drill into hotspots and tables.

### Brand Personality

- **Precise** — numbers are correct, dimensions are clearly named, jargon ("miss tokens", "sidechain", "subagent") is used because it is exact.
- **No-nonsense** — no marketing language, no filler, no decorative chrome.
- **Trustworthy** — feels like an audit instrument, not a dashboard demo.

The interface should feel like a tool an engineer reaches for, not a product page.

### Aesthetic Direction

**Refined dev tool.** Stay in the dense-data-dashboard lane (think GitHub, Linear, Datadog, Stripe billing). Improve over the current state by:

- Replacing borrowed-identity tokens (the palette currently mirrors GitHub's near-verbatim) with tints that belong to claudit.
- Escaping the all-monospace body — use a refined sans for body, keep monospace for numerics, paths, code, and identifiers.
- Establishing real spacing rhythm rather than the current ad-hoc paddings.
- Supporting **light + dark via `prefers-color-scheme`** as a first-class theme, not a retrofit.

**Anti-references** — explicitly NOT this:
- Dark mode with neon/glow accents (impeccable's "AI slop" tell).
- Cyan-on-dark or purple-to-blue gradient hero metrics.
- Glassmorphism, drop-shadow rounded cards.
- Bouncy or elastic motion.

A custom display font for headings is acceptable, provided it ships embedded so the report degrades gracefully when offline.

### Design Principles

1. **Scannability first.** Information density is a feature, not a bug. Users skim 991-line reports — never sacrifice density for whitespace.
2. **Numbers are the protagonist.** Tabular figures, aligned columns, restrained color so quantities read clearly. Ornament defers to data.
3. **Borrowed identity is a smell.** GitHub-clone, Linear-clone, Tailwind-default palettes all flag the design as templated. Tint neutrals toward a hue that is claudit's.
4. **Single-file constraint is real.** All CSS, JS, and data inline. Typography uses Inter via Google Fonts as the one external request, with system sans-serif as the offline fallback; anything else visual must survive being rendered from a self-contained file opened from disk.
5. **Light and dark are equal citizens.** Every color decision is two decisions. Hard-coded `#fff` / `#f6f8fa` / `#fef9e7` are bugs, not shortcuts.
