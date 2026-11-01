// A minimal, dependency-free DOM for mounting SPA view modules under
// `node --test`. It covers what the views touch: innerHTML parsing of
// their template strings, compound/descendant/child selectors,
// closest/matches, classList/dataset/attributes, bubbling events,
// focus, and window.location/history. Not a general HTML parser: it
// trusts well-formed templates and does no implicit tag closing.

const VOID = new Set(['area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link', 'meta', 'source', 'track', 'wbr']);
const SVG_NS = 'http://www.w3.org/2000/svg';
const HTML_NS = 'http://www.w3.org/1999/xhtml';

const ENTITIES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' };
function decode(s) {
  return s.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (m, e) => {
    if (e[0] === '#') {
      const n = e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
      return String.fromCodePoint(n);
    }
    return e in ENTITIES ? ENTITIES[e] : m;
  });
}
const escText = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
const escAttr = s => escText(s).replace(/"/g, '&quot;');

export class Event {
  constructor(type, init = {}) {
    this.type = type;
    this.bubbles = !!init.bubbles;
    this.defaultPrevented = false;
    this.target = null;
    this.currentTarget = null;
    this._stop = false;
    Object.assign(this, init);
  }
  preventDefault() { this.defaultPrevented = true; }
  stopPropagation() { this._stop = true; }
}

class EventTarget {
  constructor() { this._listeners = new Map(); }
  addEventListener(type, fn) {
    if (!this._listeners.has(type)) this._listeners.set(type, []);
    const list = this._listeners.get(type);
    if (!list.includes(fn)) list.push(fn);
  }
  removeEventListener(type, fn) {
    const list = this._listeners.get(type);
    if (list) this._listeners.set(type, list.filter(f => f !== fn));
  }
  _fire(ev) {
    ev.currentTarget = this;
    for (const fn of [...(this._listeners.get(ev.type) || [])]) fn.call(this, ev);
  }
  dispatchEvent(ev) {
    ev.target = this;
    let node = this;
    while (node) {
      node._fire(ev);
      if (ev._stop || !ev.bubbles) break;
      node = node.parentNode || (node.nodeType === 9 ? node.defaultView : null);
    }
    return !ev.defaultPrevented;
  }
}

class Node extends EventTarget {
  constructor(ownerDocument) {
    super();
    this.ownerDocument = ownerDocument;
    this.parentNode = null;
    this.childNodes = [];
  }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get nextSibling() {
    if (!this.parentNode) return null;
    const sib = this.parentNode.childNodes;
    return sib[sib.indexOf(this) + 1] || null;
  }
  get nextElementSibling() {
    if (!this.parentNode) return null;
    const sib = this.parentNode.children;
    return sib[sib.indexOf(this) + 1] || null;
  }
  get children() { return this.childNodes.filter(n => n.nodeType === 1); }
  get parentElement() { return this.parentNode && this.parentNode.nodeType === 1 ? this.parentNode : null; }
  appendChild(child) { return this.insertBefore(child, null); }
  insertBefore(child, ref) {
    if (child.nodeType === 11) {
      for (const c of [...child.childNodes]) this.insertBefore(c, ref);
      return child;
    }
    if (child.parentNode) child.parentNode.removeChild(child);
    const i = ref ? this.childNodes.indexOf(ref) : -1;
    if (i < 0) this.childNodes.push(child);
    else this.childNodes.splice(i, 0, child);
    child.parentNode = this;
    return child;
  }
  removeChild(child) {
    const i = this.childNodes.indexOf(child);
    if (i >= 0) this.childNodes.splice(i, 1);
    child.parentNode = null;
    return child;
  }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  contains(other) {
    for (let n = other; n; n = n.parentNode) if (n === this) return true;
    return false;
  }
  get textContent() { return this.childNodes.map(c => c.textContent).join(''); }
  set textContent(v) {
    this.childNodes.forEach(c => { c.parentNode = null; });
    this.childNodes = [];
    if (v !== '' && v != null) this.appendChild(new Text(this.ownerDocument, String(v)));
  }
  *_descendants() {
    for (const c of this.childNodes) {
      if (c.nodeType !== 1) continue;
      yield c;
      yield* c._descendants();
    }
  }
  querySelectorAll(sel) {
    const groups = parseSelectorList(sel);
    const out = [];
    for (const el of this._descendants()) {
      if (groups.some(g => matchComplex(el, g, this))) out.push(el);
    }
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

class Text extends Node {
  constructor(doc, data) { super(doc); this.nodeType = 3; this.data = data; }
  get textContent() { return this.data; }
  set textContent(v) { this.data = String(v); }
}

class Fragment extends Node {
  constructor(doc) { super(doc); this.nodeType = 11; }
}

const camelToKebab = s => s.replace(/[A-Z]/g, c => '-' + c.toLowerCase());
const kebabToCamel = s => s.replace(/-([a-z])/g, (_, c) => c.toUpperCase());

class Element extends Node {
  constructor(doc, tag, ns) {
    super(doc);
    this.nodeType = 1;
    this.namespaceURI = ns || HTML_NS;
    this.localName = this.namespaceURI === HTML_NS ? tag.toLowerCase() : tag;
    this.tagName = this.namespaceURI === HTML_NS ? tag.toUpperCase() : tag;
    this.attributes = new Map();
    this.style = {
      setProperty(k, v) { this[k] = String(v); },
      getPropertyValue(k) { return this[k] || ''; },
      removeProperty(k) { delete this[k]; },
    };
    this._value = null;
    const self = this;
    this.dataset = new Proxy({}, {
      get(_, k) { return typeof k === 'string' ? (self.getAttribute('data-' + camelToKebab(k)) ?? undefined) : undefined; },
      set(_, k, v) { self.setAttribute('data-' + camelToKebab(k), String(v)); return true; },
      deleteProperty(_, k) { self.removeAttribute('data-' + camelToKebab(k)); return true; },
      has(_, k) { return self.hasAttribute('data-' + camelToKebab(k)); },
      ownKeys() {
        return [...self.attributes.keys()].filter(a => a.startsWith('data-')).map(a => kebabToCamel(a.slice(5)));
      },
      getOwnPropertyDescriptor(_, k) {
        const v = self.getAttribute('data-' + camelToKebab(k));
        return v == null ? undefined : { value: v, enumerable: true, configurable: true, writable: true };
      },
    });
    this.classList = {
      _get: () => (self.getAttribute('class') || '').split(/\s+/).filter(Boolean),
      _set: list => self.setAttribute('class', list.join(' ')),
      contains(c) { return this._get().includes(c); },
      add(...cs) { const l = this._get(); cs.forEach(c => { if (!l.includes(c)) l.push(c); }); this._set(l); },
      remove(...cs) { this._set(this._get().filter(c => !cs.includes(c))); },
      toggle(c, force) {
        const on = force === undefined ? !this.contains(c) : !!force;
        if (on) this.add(c); else this.remove(c);
        return on;
      },
    };
  }
  getAttribute(n) { return this.attributes.has(n) ? this.attributes.get(n) : null; }
  setAttribute(n, v) { this.attributes.set(n, String(v)); }
  hasAttribute(n) { return this.attributes.has(n); }
  removeAttribute(n) { this.attributes.delete(n); }
  get id() { return this.getAttribute('id') || ''; }
  set id(v) { this.setAttribute('id', v); }
  get className() { return this.getAttribute('class') || ''; }
  set className(v) { this.setAttribute('class', v); }
  get hidden() { return this.hasAttribute('hidden'); }
  set hidden(v) { if (v) this.setAttribute('hidden', ''); else this.removeAttribute('hidden'); }
  get disabled() { return this.hasAttribute('disabled'); }
  set disabled(v) { if (v) this.setAttribute('disabled', ''); else this.removeAttribute('disabled'); }
  get type() { return this.getAttribute('type') || (this.localName === 'button' ? 'submit' : ''); }
  set type(v) { this.setAttribute('type', v); }
  get href() { return this.getAttribute('href') || ''; }
  get title() { return this.getAttribute('title') || ''; }
  set title(v) { this.setAttribute('title', v); }
  get value() { return this._value != null ? this._value : (this.getAttribute('value') || ''); }
  set value(v) { this._value = String(v); }
  // Natively focusable elements default to 0, others to -1 — the rule
  // keyboard reachability depends on.
  get tabIndex() {
    const a = this.getAttribute('tabindex');
    if (a != null) return parseInt(a, 10);
    const native = ['button', 'input', 'select', 'textarea'].includes(this.localName)
      || (this.localName === 'a' && this.hasAttribute('href'));
    return native && !this.disabled ? 0 : -1;
  }
  set tabIndex(v) { this.setAttribute('tabindex', String(v)); }
  focus() { this.ownerDocument.activeElement = this; }
  blur() { if (this.ownerDocument.activeElement === this) this.ownerDocument.activeElement = this.ownerDocument.body; }
  getBoundingClientRect() { return { top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0, x: 0, y: 0 }; }
  matches(sel) { return parseSelectorList(sel).some(g => matchComplex(this, g, null)); }
  closest(sel) {
    for (let n = this; n && n.nodeType === 1; n = n.parentNode) if (n.matches(sel)) return n;
    return null;
  }
  get innerHTML() { return this.childNodes.map(serialize).join(''); }
  set innerHTML(html) {
    this.childNodes.forEach(c => { c.parentNode = null; });
    this.childNodes = [];
    parseInto(this, String(html));
  }
  get outerHTML() { return serialize(this); }
}

function serialize(n) {
  if (n.nodeType === 3) return escText(n.data);
  const attrs = [...n.attributes].map(([k, v]) => ` ${k}="${escAttr(v)}"`).join('');
  if (VOID.has(n.localName)) return `<${n.localName}${attrs}>`;
  return `<${n.localName}${attrs}>${n.childNodes.map(serialize).join('')}</${n.localName}>`;
}

const ATTR_RE = /([^\s=/>]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/y;

function parseInto(root, html) {
  const doc = root.ownerDocument;
  const stack = [root];
  const top = () => stack[stack.length - 1];
  let i = 0;
  while (i < html.length) {
    if (html.startsWith('<!--', i)) {
      const end = html.indexOf('-->', i + 4);
      i = end < 0 ? html.length : end + 3;
      continue;
    }
    if (html[i] === '<' && html[i + 1] === '/') {
      const end = html.indexOf('>', i);
      const name = html.slice(i + 2, end).trim().toLowerCase();
      for (let k = stack.length - 1; k > 0; k--) {
        if (stack[k].localName.toLowerCase() === name) { stack.length = k; break; }
      }
      i = end + 1;
      continue;
    }
    if (html[i] === '<' && /[a-zA-Z]/.test(html[i + 1] || '')) {
      let j = i + 1;
      while (j < html.length && /[^\s/>]/.test(html[j])) j++;
      const tag = html.slice(i + 1, j);
      const parent = top();
      const ns = tag.toLowerCase() === 'svg' || parent.namespaceURI === SVG_NS ? SVG_NS : HTML_NS;
      const el = new Element(doc, tag, ns);
      let selfClose = false;
      for (;;) {
        while (/\s/.test(html[j] || '')) j++;
        if (html[j] === '>') { j++; break; }
        if (html[j] === '/' && html[j + 1] === '>') { selfClose = true; j += 2; break; }
        if (j >= html.length) break;
        ATTR_RE.lastIndex = j;
        const m = ATTR_RE.exec(html);
        if (!m) { j++; continue; }
        el.setAttribute(ns === SVG_NS ? m[1] : m[1].toLowerCase(), decode(m[2] ?? m[3] ?? m[4] ?? ''));
        j = ATTR_RE.lastIndex;
      }
      parent.appendChild(el);
      if (!selfClose && !(ns === HTML_NS && VOID.has(el.localName))) stack.push(el);
      i = j;
      continue;
    }
    let j = html.indexOf('<', i + 1);
    if (j < 0) j = html.length;
    top().appendChild(new Text(doc, decode(html.slice(i, j))));
    i = j;
  }
}

// ── Selectors ─────────────────────────────────────────────────────────
// Supports: tag, #id, .class, [attr], [attr="v"], :scope, and the
// descendant (space) and child (>) combinators, comma-separated.

function splitTopLevel(s, ch) {
  const out = [];
  let depth = 0, quote = null, start = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) { if (c === quote) quote = null; continue; }
    if (c === '"' || c === "'") quote = c;
    else if (c === '[' || c === '(') depth++;
    else if (c === ']' || c === ')') depth--;
    else if (c === ch && depth === 0) { out.push(s.slice(start, i)); start = i + 1; }
  }
  out.push(s.slice(start));
  return out;
}

function parseCompound(s) {
  const c = { tag: null, ids: [], classes: [], attrs: [], scope: false };
  const re = /^(\*|[a-zA-Z][\w-]*)|#([\w-]+)|\.([\w-]+)|\[\s*([\w-]+)\s*(?:=\s*(?:"([^"]*)"|'([^']*)'|([^\]\s]+))\s*)?\]|:scope/gy;
  let m, pos = 0;
  re.lastIndex = 0;
  while (pos < s.length && (m = re.exec(s))) {
    if (m[1]) c.tag = m[1] === '*' ? null : m[1].toLowerCase();
    else if (m[2]) c.ids.push(m[2]);
    else if (m[3]) c.classes.push(m[3]);
    else if (m[4]) c.attrs.push([m[4], m[5] ?? m[6] ?? m[7] ?? null]);
    else c.scope = true;
    pos = re.lastIndex;
  }
  if (pos !== s.length) throw new Error(`mini-dom: unsupported selector "${s}"`);
  return c;
}

const selectorCache = new Map();
function parseSelectorList(sel) {
  if (selectorCache.has(sel)) return selectorCache.get(sel);
  const groups = splitTopLevel(sel, ',').map(part => {
    const tokens = [];
    let cur = '', depth = 0, quote = null;
    const flush = () => { if (cur) tokens.push(cur); cur = ''; };
    for (const ch of part.trim()) {
      if (quote) { cur += ch; if (ch === quote) quote = null; continue; }
      if (ch === '"' || ch === "'") quote = ch;
      else if (ch === '[') depth++;
      else if (ch === ']') depth--;
      if (depth === 0 && /\s/.test(ch)) { flush(); continue; }
      if (depth === 0 && ch === '>') { flush(); tokens.push('>'); continue; }
      cur += ch;
    }
    flush();
    const steps = [];
    let comb = ' ';
    for (const t of tokens) {
      if (t === '>') { comb = '>'; continue; }
      steps.push({ comb, compound: parseCompound(t) });
      comb = ' ';
    }
    return steps;
  });
  selectorCache.set(sel, groups);
  return groups;
}

function matchCompound(el, c, scope) {
  if (c.scope && el !== scope) return false;
  if (c.tag && el.localName.toLowerCase() !== c.tag) return false;
  if (c.ids.some(id => el.id !== id)) return false;
  if (c.classes.some(k => !el.classList.contains(k))) return false;
  return c.attrs.every(([n, v]) => (v == null ? el.hasAttribute(n) : el.getAttribute(n) === v));
}

function matchComplex(el, steps, scope) {
  const at = (node, k) => {
    if (!matchCompound(node, steps[k].compound, scope)) return false;
    if (k === 0) return true;
    if (steps[k].comb === '>') {
      const p = node.parentNode;
      return !!p && p.nodeType === 1 && at(p, k - 1);
    }
    for (let p = node.parentNode; p && p.nodeType === 1; p = p.parentNode) {
      if (at(p, k - 1)) return true;
    }
    return false;
  };
  return at(el, steps.length - 1);
}

class Document extends Node {
  constructor() {
    super(null);
    this.ownerDocument = this;
    this.nodeType = 9;
    this.documentElement = new Element(this, 'html');
    this.appendChild(this.documentElement);
    this.body = new Element(this, 'body');
    this.documentElement.appendChild(this.body);
    this.activeElement = this.body;
    this.hidden = false;
  }
  createElement(tag) { return new Element(this, tag); }
  createElementNS(ns, tag) { return new Element(this, tag, ns); }
  createTextNode(s) { return new Text(this, s); }
  createDocumentFragment() { return new Fragment(this); }
  getElementById(id) { return this.querySelector(`#${id}`); }
}

// installDOM puts a fresh document/window/history on globalThis and
// returns them. location.hash follows history.replaceState and direct
// assignment; replaceCalls records every replaceState URL.
export function installDOM({ hash = '', search = '' } = {}) {
  const document = new Document();
  const location = { hash, search, pathname: '/' };
  const replaceCalls = [];
  const applyURL = url => {
    const s = String(url);
    const h = s.indexOf('#');
    location.hash = h >= 0 ? s.slice(h) : '';
  };
  const history = {
    state: null,
    replaceState(state, _title, url) { this.state = state; replaceCalls.push(String(url)); applyURL(url); },
    pushState(state, _title, url) { this.state = state; applyURL(url); },
  };
  const window = new EventTarget();
  Object.assign(window, {
    document, location, history,
    innerWidth: 1280, innerHeight: 800,
    getComputedStyle: () => ({ paddingLeft: '0', paddingRight: '0' }),
    requestAnimationFrame: fn => setTimeout(fn, 0),
  });
  document.defaultView = window;
  Object.assign(globalThis, { document, window, history, Event });
  return { document, window, location, history, replaceCalls };
}

export const click = el => el.dispatchEvent(new Event('click', { bubbles: true, button: 0 }));
export const keydown = (el, key) => el.dispatchEvent(new Event('keydown', { bubbles: true, key }));
