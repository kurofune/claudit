// module-graph reads web/'s ES module graph from source text: each
// module's exports, its import statements (runtime imports and JSDoc
// @import type imports) and the modules web/index.html loads.
import { readFileSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

export const WEB = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'web');

// stripComments drops // and /* */ comments but keeps JSDoc @import
// lines, which are type imports. String contents are left alone, which
// is enough for the import and export forms web/ uses.
function stripComments(src) {
  const imports = [...src.matchAll(/@import\s+\{[^}]*\}\s+from\s+['"][^'"]+['"]/g)].map(m => m[0]).join('\n');
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:'"`\\])\/\/[^\n]*/g, '$1') + '\n' + imports;
}

// parseModule lists a module's exported names, the names it imports from
// each sibling module, and the siblings it only type-imports.
export function parseModule(src) {
  const code = stripComments(src);
  const exports = new Set();
  for (const m of code.matchAll(/^\s*export\s+(?:async\s+)?(?:function\*?|const|let|var|class)\s+([A-Za-z_$][\w$]*)/gm)) {
    exports.add(m[1]);
  }
  for (const m of code.matchAll(/^\s*export\s*\{([^}]*)\}\s*;?\s*$/gm)) {
    for (const part of m[1].split(',')) {
      const name = part.trim().split(/\s+as\s+/).pop();
      if (name) exports.add(name);
    }
  }
  /** @type {Map<string, Set<string>>} module file → names imported from it */
  const imports = new Map();
  const add = (file, names) => {
    if (!imports.has(file)) imports.set(file, new Set());
    for (const n of names) imports.get(file).add(n);
  };
  for (const m of code.matchAll(/^\s*(?:import|export)\s*\{([^}]*)\}\s*from\s*['"]\.\/([\w.-]+\.js)['"]/gm)) {
    add(m[2], m[1].split(',').map(p => p.trim().split(/\s+as\s+/)[0]).filter(Boolean));
  }
  for (const m of code.matchAll(/^\s*(?:import|export)\s+\*\s+(?:as\s+[\w$]+\s+)?from\s*['"]\.\/([\w.-]+\.js)['"]/gm)) {
    add(m[1], ['*']);
  }
  for (const m of code.matchAll(/^\s*import\s+['"]\.\/([\w.-]+\.js)['"]/gm)) add(m[1], []);
  const calledInside = new Set();
  for (const name of exports) {
    const uses = code.match(new RegExp(`(?<![\\w$.])${name.replace(/\$/g, '\\$')}(?![\\w$])`, 'g')) || [];
    if (uses.length > 1) calledInside.add(name);
  }
  const typeImports = new Set();
  for (const m of code.matchAll(/@import\s+\{[^}]*\}\s+from\s+['"]\.\/([\w.-]+\.js)['"]/g)) typeImports.add(m[1]);
  return { exports, imports, typeImports, calledInside };
}

// htmlModules lists the web/ modules an HTML page loads by script src,
// plain or through the {{asset "x.js"}} template helper.
export function htmlModules(html) {
  const out = new Set();
  for (const m of html.matchAll(/<script\b[^>]*\bsrc=["'](?:\{\{asset\s+"([\w.-]+\.js)"\}\}|\.?\/?([\w.-]+\.js))["']/g)) {
    out.add(m[1] || m[2]);
  }
  return out;
}

// readWeb parses every web/*.js module and web/index.html.
export function readWeb(dir = WEB) {
  const modules = new Map();
  for (const f of readdirSync(dir).filter(f => f.endsWith('.js')).sort()) {
    modules.set(f, parseModule(readFileSync(join(dir, f), 'utf8')));
  }
  return { modules, roots: htmlModules(readFileSync(join(dir, 'index.html'), 'utf8')) };
}

// reachable is every module loaded from roots by runtime or type imports.
export function reachable(modules, roots) {
  const seen = new Set();
  const stack = [...roots];
  while (stack.length) {
    const f = stack.pop();
    if (seen.has(f) || !modules.has(f)) continue;
    seen.add(f);
    const m = modules.get(f);
    stack.push(...m.imports.keys(), ...m.typeImports);
  }
  return seen;
}

// unimportedExports lists `<module>: <export>` for every export of the
// named modules that no other module imports by name (or through *).
// With insideCounts, an export the module's own code also uses is live
// and not listed.
export function unimportedExports(modules, only = [...modules.keys()], insideCounts = false) {
  const used = new Map();
  for (const m of modules.values()) {
    for (const [file, names] of m.imports) {
      if (!used.has(file)) used.set(file, new Set());
      for (const n of names) used.get(file).add(n);
    }
  }
  const out = [];
  for (const f of only) {
    const names = used.get(f) || new Set();
    const m = modules.get(f);
    for (const e of m.exports) {
      if (names.has(e) || names.has('*') || (insideCounts && m.calledInside.has(e))) continue;
      out.push(`${f}: ${e}`);
    }
  }
  return out.sort();
}
