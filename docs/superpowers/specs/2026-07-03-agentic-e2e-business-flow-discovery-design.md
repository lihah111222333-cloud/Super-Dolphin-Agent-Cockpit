# Agentic E2E Business Flow Discovery Design

## Context

The current experimental harness proves that a Playwright-driven agent can read DOM and accessibility structure, choose safe actions, execute a frontend path, and produce evidence. The first verified path opens the app, identifies the chat composer, fills it, navigates to observability, queries recent logs, and finishes with a step-by-step evidence package.

The next goal is not to hand-write every business flow. The user wants the agent to discover most visible business flows automatically, because manually reading the frontend to enumerate every route and action is the expensive part.

This design keeps the product frontend as the system under test. It changes the harness boundary only.

## Goal

Build a discovery-oriented Agentic E2E harness that can:

1. Open the frontend app.
2. Read visible DOM, accessibility, and `data-testid` structure.
3. Identify candidate business entries and safe business actions.
4. Explore those candidates with a strict safety policy.
5. Produce a structured and human-readable business flow inventory.

The first implementation should prove the local test flow. CI integration is intentionally deferred until discovery output is stable enough to promote selected paths into deterministic goals.

## Non-Goals

- Do not mutate product frontend architecture.
- Do not require manually enumerating all frontend pages before discovery.
- Do not execute real provider turns.
- Do not send chat messages.
- Do not save, delete, reset, apply, upload, or otherwise mutate durable product state.
- Do not make every discovered path a CI test in the first phase.

## Proposed Approach

Use a hybrid of automatic discovery and conservative execution.

The harness should first identify visible business surfaces from page structure, then explore only actions that are likely to be safe. The output is a discovery report that humans can review. Stable and valuable paths can later be promoted into fixed `goal` definitions.

This makes the first phase useful even when some discovered paths are incomplete or not yet deterministic. The product of the phase is business flow knowledge, not only pass/fail automation.

## Harness Boundary

The harness should split into these internal units:

- Runner: owns browser lifecycle, step loop, config, and evidence directory.
- Facts collector: extracts route, title, headings, test ids, roles, controls, forms, tables, dialogs, and visible text summaries.
- Business flow discoverer: converts facts into candidate entries and candidate actions.
- Safety policy: classifies actions as allowed, blocked, or requiring future manual approval.
- Action executor: performs only allowed actions through Playwright locators.
- Reporter: writes per-step evidence plus aggregate discovery JSON and Markdown.

This boundary is internal to `frontend-app/scripts`. It does not imply changes to React pages or app state modules.

## Business Flow Model

A discovered flow should be represented as:

```json
{
  "id": "sidebar-observability-recent-logs",
  "entry": {
    "route": "/",
    "label": "链路追踪",
    "source": "sidebar-secondary-nav"
  },
  "page": {
    "route": "/observability",
    "heading": "链路追踪",
    "testIds": ["observability-page", "observability-recent-logs"]
  },
  "actions": [
    {
      "type": "click",
      "label": "查询最新日志",
      "safety": "allowed",
      "reason": "query/read action"
    }
  ],
  "result": {
    "status": "discovered",
    "summary": "Recent log table became visible"
  }
}
```

The exact field names may change during implementation, but the report must preserve these concepts: entry, page identity, safe actions, executed steps, result, and evidence links.

## Discovery Rules

Candidate business entries include:

- Sidebar and navigation buttons.
- Page headings and route changes after navigation.
- Buttons that reveal read-only panels, tabs, filters, details, or query results.
- Search/filter text inputs paired with query buttons.
- Expand/collapse buttons for existing results or details.

The discoverer should prefer semantic structure:

- Role and accessible name.
- `data-testid`.
- Heading text.
- Route.
- Form labels.

CSS selectors can be included in evidence, but should not be the primary discovery signal unless no semantic signal exists.

## Safety Policy

Allowed first-phase actions:

- Navigate through sidebar or tab-like buttons.
- Click query/search/filter buttons.
- Fill text filters with harmless probe values when the input is clearly a filter.
- Expand or collapse existing detail sections.
- Open read-only detail panels.

Blocked first-phase actions:

- Send, submit, or interrupt chat/provider turns.
- Save, apply, create, delete, reset, remove, upload, import, export, or install.
- File picker and file mutation actions.
- Settings mutation actions.
- Buttons without a stable accessible name or test id.
- Actions that cause console errors.

Blocked actions should still appear in the report as discovered but not executed, with the blocking reason.

## Readiness And Stabilization

The first proof run showed that route changes and main content rendering can be temporarily out of sync. The next harness version should model readiness explicitly.

After an action, the runner should wait for one of these expected conditions:

- Route changes to the expected path.
- A page-level `data-testid` becomes visible.
- A heading with the expected accessible name becomes visible.
- A known result region becomes visible after a query.
- The DOM/accessibility summary is stable across two short samples.

The runner should avoid fixed sleep as the primary synchronization mechanism. Short delays may remain as a backstop, but success should be tied to observable page state.

## Evidence Package

Each run should write:

- `result.json`: overall pass/fail and aggregate metadata.
- `business-flow-discovery.json`: structured inventory of discovered entries, explored actions, blocked actions, and outcomes.
- `business-flow-discovery.md`: human-readable summary grouped by page/entry.
- Per-step JSON with compact facts and chosen action.
- Per-step ARIA snapshot.
- Per-step DOM summary.
- Final screenshot or failure screenshot.

Reports should make it easy to answer:

- What business surfaces did the agent find?
- Which actions did it execute?
- Which actions did it block?
- Which flows look stable enough to promote into fixed goals?
- What evidence supports each conclusion?

## Testing Strategy

Unit tests should cover:

- Facts normalization.
- Candidate business entry extraction.
- Safety classification.
- Action selection.
- Readiness condition selection.
- Report shape.

The local executable flow should cover:

- Start from the app root.
- Discover sidebar business entries.
- Explore at least one read/query path, initially observability recent logs.
- Produce both JSON and Markdown discovery reports.

Full frontend validation remains:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

The discovery run should be validated locally before any CI proposal:

```bash
cd frontend-app
npm run agentic:e2e
```

## Promotion Path

Discovery output is not the same as CI coverage.

The promotion flow should be:

1. Run discovery locally.
2. Review `business-flow-discovery.md`.
3. Select stable, valuable, non-mutating flows.
4. Encode selected flows as deterministic goals.
5. Run deterministic goals locally.
6. Add CI only after the deterministic goals are stable.

This keeps discovery exploratory and CI deterministic.

## Open Decisions

The first implementation should choose conservative defaults:

- Start with one discovery mode and one existing fixed probe.
- Keep blocked action keywords hard-coded in the harness until evidence shows a need for configuration.
- Keep all generated evidence under `.tmp/agentic-e2e/<run-id>`.
- Use command-line `--goal` or `--mode` only after the discovery report path is working.

These decisions can be revised after the first discovery report is produced.
