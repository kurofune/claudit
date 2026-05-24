// Theme picker — applies a named palette by setting <html data-theme="...">
// and persists the choice in localStorage. The actual CSS lives in
// theme-*.css files linked from index.html; this module just wires up
// the catalog + the gear-popover UI in the sidebar footer.
//
// Boot order: applyStored() runs synchronously from an inline <script>
// in <head> *before* first paint, so a user's chosen theme is in place
// when the page renders (no flash of default colors). The popover wiring
// is initialized later from app.js once the DOM is interactive.

const STORAGE_KEY = "claudit.theme";

// Order matters — the popover renders themes in this sequence. Light
// group then dark group, each ordered roughly by popularity / familiarity.
// "auto" is the sentinel for "use system preference" → remove data-theme
// entirely so the :root + @media (prefers-color-scheme: dark) defaults
// in tokens.css take effect.
export const THEMES = [
  { slug: "auto",               label: "Auto (system)",      scheme: "auto"  },
  // Light — alphabetical by label
  { slug: "ayu-light",          label: "Ayu Light",          scheme: "light" },
  { slug: "catppuccin-latte",   label: "Catppuccin Latte",   scheme: "light" },
  { slug: "gruvbox-light",      label: "Gruvbox Light",      scheme: "light" },
  { slug: "one-light",          label: "One Light",          scheme: "light" },
  { slug: "papercolor-light",   label: "PaperColor Light",   scheme: "light" },
  { slug: "solarized-light",    label: "Solarized Light",    scheme: "light" },
  // Dark — alphabetical by label
  { slug: "catppuccin-mocha",   label: "Catppuccin Mocha",   scheme: "dark"  },
  { slug: "dracula",            label: "Dracula",            scheme: "dark"  },
  { slug: "github-dark",        label: "GitHub Dark",        scheme: "dark"  },
  { slug: "gruvbox-dark",       label: "Gruvbox Dark",       scheme: "dark"  },
  { slug: "monokai-pro",        label: "Monokai Pro",        scheme: "dark"  },
  { slug: "night-owl",          label: "Night Owl",          scheme: "dark"  },
  { slug: "nord",               label: "Nord",               scheme: "dark"  },
  { slug: "one-dark",           label: "One Dark",           scheme: "dark"  },
  { slug: "solarized-dark",     label: "Solarized Dark",     scheme: "dark"  },
  { slug: "tokyo-night",        label: "Tokyo Night",        scheme: "dark"  },
];

const VALID_SLUGS = new Set(THEMES.map(t => t.slug));

export function getStored() {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return VALID_SLUGS.has(v) ? v : "auto";
  } catch {
    return "auto";
  }
}

export function setStored(slug) {
  try { localStorage.setItem(STORAGE_KEY, slug); } catch { /* private mode etc. */ }
}

export function apply(slug) {
  const root = document.documentElement;
  if (slug === "auto" || !VALID_SLUGS.has(slug)) {
    root.removeAttribute("data-theme");
  } else {
    root.setAttribute("data-theme", slug);
  }
}

// applyStored is called once from an inline <script> in <head> before
// first paint. Safe to call again later (idempotent).
export function applyStored() {
  apply(getStored());
}

// init wires up the gear button + popover. Idempotent — multiple calls
// are harmless because we always replace listeners by re-querying.
export function init() {
  const btn  = document.getElementById("theme-toggle");
  const menu = document.getElementById("theme-menu");
  if (!btn || !menu) return;

  // Build the menu once. Each row is a button (radio-style) — the
  // active theme gets aria-checked + .is-active.
  menu.innerHTML = THEMES.map((t, i) => {
    // Insert a divider before the first light theme and before the
    // first dark theme so the list groups visually.
    let divider = "";
    if (i > 0 && THEMES[i - 1].scheme !== t.scheme) {
      divider = `<div class="theme-menu-sep" role="separator"></div>`;
    }
    return `${divider}<button type="button" role="menuitemradio"
       class="theme-menu-item" data-theme-slug="${t.slug}"
       aria-checked="false">
       <span class="theme-menu-swatch" data-scheme="${t.scheme}"></span>
       <span class="theme-menu-label">${t.label}</span>
       <span class="theme-menu-check" aria-hidden="true">✓</span>
     </button>`;
  }).join("");

  const refresh = () => {
    const current = getStored();
    menu.querySelectorAll(".theme-menu-item").forEach(el => {
      const isActive = el.dataset.themeSlug === current;
      el.setAttribute("aria-checked", isActive ? "true" : "false");
      el.classList.toggle("is-active", isActive);
    });
  };
  refresh();

  const close = () => {
    menu.hidden = true;
    btn.setAttribute("aria-expanded", "false");
  };
  const open = () => {
    menu.hidden = false;
    btn.setAttribute("aria-expanded", "true");
  };

  btn.addEventListener("click", (e) => {
    e.stopPropagation();
    if (menu.hidden) open(); else close();
  });

  menu.addEventListener("click", (e) => {
    const item = e.target.closest(".theme-menu-item");
    if (!item) return;
    const slug = item.dataset.themeSlug;
    setStored(slug);
    apply(slug);
    refresh();
    close();
    btn.focus();
  });

  // Outside-click + Esc to dismiss.
  document.addEventListener("click", (e) => {
    if (menu.hidden) return;
    if (menu.contains(e.target) || btn.contains(e.target)) return;
    close();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !menu.hidden) {
      close();
      btn.focus();
    }
  });
}
