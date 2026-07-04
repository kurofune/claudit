// @ts-check
// Pure, DOM-free logic for the Agents observability tab: lane packing
// for the swimlane, time→x scaling, agent-bar geometry, and the
// status/elapsed derivations the cards display, all computed over the
// /_claudit/api/agents JSON payload.
//
// This is the swappable-UI insurance: view-agents.js does only DOM/SVG
// and pulls every number from here, so a redesign rewrites the view
// against the same contract without touching tested math. Unit-tested
// under `node --test` in jstest/agents-logic.test.js.
//
// The implementation now lives in domain modules split along the lens
// seams; this file is a pure facade re-exporting every name so existing
// importers (view modules, tests) keep working unchanged.

export * from './agents-model.js';
export * from './agents-conversation-logic.js';
export * from './agents-tree-logic.js';
export * from './agents-timeline-logic.js';
export * from './agents-feed-logic.js';
export * from './agents-drawer-logic.js';
export * from './agents-filter-logic.js';
export * from './agents-insights-logic.js';
