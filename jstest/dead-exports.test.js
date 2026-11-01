// Dead code in web/: every module must load from the page, and every
// export of the detector and model modules must have a live caller.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readWeb, reachable, parseModule, unimportedExports, htmlModules } from './helpers/module-graph.js';

const { modules, roots } = readWeb();

test('every web/*.js module is loaded from web/app.js or web/index.html', () => {
  assert.ok(roots.has('app.js'), 'index.html loads app.js');
  const live = reachable(modules, roots);
  assert.deepEqual([...modules.keys()].filter(f => !live.has(f)), []);
});

// The detector and model modules once served five lenses; what is left
// serves the session header. An export no web/ module imports is live
// only when the module's own code calls it (detectRetries, percentiles
// and retryTarget feed the detectors and keep their correctness tests).
test('every export of agents-model.js and agents-insights-logic.js is imported or called', () => {
  assert.deepEqual(unimportedExports(modules, ['agents-model.js', 'agents-insights-logic.js'], true), []);
});

test('every export of agents-model.js and agents-insights-logic.js but the detector helpers has a web/ importer', () => {
  assert.deepEqual(unimportedExports(modules, ['agents-model.js', 'agents-insights-logic.js']), [
    'agents-insights-logic.js: detectRetries',
    'agents-insights-logic.js: percentiles',
    'agents-insights-logic.js: retryTarget',
  ]);
});

test('the scan flags an export nothing imports or calls', () => {
  const mods = new Map([
    ['app.js', parseModule("import { used } from './lib.js';\nused();\n")],
    ['lib.js', parseModule("export function used() { return helper(); }\nexport function helper() {}\nexport const dead = 1;\n// export function commented() {}\n")],
  ]);
  assert.deepEqual(unimportedExports(mods, ['lib.js'], true), ['lib.js: dead']);
  assert.deepEqual(unimportedExports(mods, ['lib.js']), ['lib.js: dead', 'lib.js: helper']);
});

test('the scan flags a module nothing loads, and follows multi-line, re-export and type imports', () => {
  const mods = new Map([
    ['app.js', parseModule("import {\n  a,\n  b as c,\n} from './one.js';\nexport { d } from './two.js';\n")],
    ['one.js', parseModule("/** @import { T } from './types.js' */\nexport const a = 1, b = 2;\n")],
    ['two.js', parseModule('export const d = 1;\n')],
    ['types.js', parseModule('export {};\n')],
    ['orphan.js', parseModule("import { a } from './one.js';\n")],
  ]);
  const live = reachable(mods, htmlModules('<script type="module" src="{{asset "app.js"}}"></script>'));
  assert.deepEqual([...mods.keys()].filter(f => !live.has(f)), ['orphan.js']);
  assert.deepEqual(unimportedExports(mods, ['one.js']), []);
});
