# Runtime Warning Redaction Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent failed bridge/runtime warning payloads from storing or rendering raw paths, prompts, tokens, stack-like content, or credential values in the frontend runtime activity panel and frontend trace metadata.

**Architecture:** Redact warning fields at the `addWarning` ingestion boundary, then keep a display-layer sanitizer in `runtimeLogAdapter` as upper defense for warning entries that bypass the store in tests or future call sites. Preserve safe correlation fields such as method, thread id, request id, trace id, status, and code when they match a constrained non-path format.

**Tech Stack:** React/Vite frontend, Zustand-style client store, Vitest, existing `safeDiagnosticPreview` helper.

**Verification Surface:** `frontend-app/src/entities/client/model/warningRuntime.js`, `frontend-app/src/pages/chat/adapters/runtimeLogAdapter.js`, focused warning/runtime component tests, full `frontend-app` lint/test/build.

---

## Review Scope

This round used 20 effective read-only review agents across:

- API facade and contract matrix.
- Wails bridge and native IPC wrappers.
- Client store state machines.
- Chat runtime panel, warning popovers, timeline adapters.
- App shell and bridge event wiring.
- Workflows/DAG/material upload.
- Skills/MCP controls.
- Memory/shared files.
- Settings/model providers/runtime preferences.
- Files/dashboard.
- Prompts.
- Observability.
- Shared UI/focus/layout.
- Frontend guard/audit scripts.
- Embed/build integration.

Five cross-decision agents then compared the repeated candidates. Four recommended runtime warning privacy hardening; one recommended video API key response validators. The chosen fix is runtime warning redaction because it has the broadest direct user-visible privacy impact, was independently confirmed by multiple review surfaces, and can be repaired atomically without a broad response-policy rewrite.

## Evidence Summary

- `warningRuntime.addWarning` stored raw `fields`, used raw fields for the warning signature, and emitted raw error text to frontend traces.
- `runtimeLogAdapter.warningDetailText` sanitized only `runtimeKind === 'result'`; ordinary warnings rendered `entry.detail` or `JSON.stringify(entry.fields)` directly.
- `RuntimeActivityLog` rendered the warning detail string in the runtime activity popover.
- Failed bridge events such as `rpc.failed`, `*.failed`, and `*/failed` can carry path, prompt, token, raw preview, or error text payloads.
- A red test confirmed current code stored `/home/l4place/private-project/secret.txt`, `private prompt body`, and `sk-live-secret` in warning fields.
- A red component test confirmed warning popovers displayed raw JSON detail before the fix.

## Final Decision

Implement runtime warning privacy hardening:

1. Redact warning fields before storing, deduplicating, signing, or emitting frontend trace metadata.
2. Preserve only safe correlation fields when they are simple non-path identifiers.
3. Sanitize ordinary warning popover details through the same warning-specific safe preview path.
4. Keep runtime result redaction behavior unchanged.

Rejected for this round:

- Video API key response validators: valid credential-facing follow-up, but narrower.
- `approval/respond` response validator: valid P0 response-policy follow-up, but backend success currently returns `nil` and needs separate policy handling.
- DAG/MCP/prompt/memory/shared-file/model-provider validators: valid fail-fast follow-ups, but broader contract work.
- Cross-thread unscoped warning scoping: related risk, but not mixed into this privacy redaction commit to keep the behavioral surface focused.

## Implementation Tasks

### Task 1: Add Failing Tests

**Files:**
- Modify: `frontend-app/src/entities/client/model/warningRuntime.test.js`
- Modify: `frontend-app/src/pages/chat/components/RuntimePanelComponents.test.jsx`

- [x] Add a store-boundary test proving warning fields/signature/frontend trace do not contain raw path, prompt body, filename, or API key values while preserving `req_id` and safe method.
- [x] Add a component test proving warning popovers redact sensitive detail/fields while preserving safe method and `req_id`.
- [x] Update the existing warning popover test so raw detail strings such as `missing permission` are not rendered verbatim.
- [x] Confirm red failure before implementation:

```bash
cd frontend-app
npm test -- --run src/entities/client/model/warningRuntime.test.js src/pages/chat/components/RuntimePanelComponents.test.jsx -t "redacts sensitive warning fields|redacts warning popover|opens stat detail"
```

Observed: FAIL. Raw `/home/l4place`, `private prompt body`, and `sk-live-secret` were stored or displayed.

### Task 2: Redact Warning Ingestion

**Files:**
- Modify: `frontend-app/src/entities/client/model/warningRuntime.js`

- [x] Import `safeDiagnosticPreviewValue`.
- [x] Add `safeWarningFields(fields)` helper.
- [x] Preserve constrained correlation fields (`method`, `threadId`, `req_id`, trace/span ids, status, code, etc.) only when they are non-empty, short, and not path-like.
- [x] Use sanitized fields for warning entry storage, merge/coalescing, signature generation, and frontend trace emission.

### Task 3: Add Display-Layer Defense

**Files:**
- Modify: `frontend-app/src/pages/chat/adapters/runtimeLogAdapter.js`

- [x] Reuse `safeWarningFields` for ordinary warning fields and JSON detail objects.
- [x] Avoid double-sanitizing already-safe correlation values before compact display.
- [x] Keep plain string detail values redacted.
- [x] Keep existing runtime result redaction path unchanged.

### Task 4: Verify

- [x] Focused red/green test:

```bash
cd frontend-app
npm test -- --run src/entities/client/model/warningRuntime.test.js src/pages/chat/components/RuntimePanelComponents.test.jsx -t "redacts sensitive warning fields|redacts warning popover|opens stat detail"
```

- [x] App regression focused test:

```bash
cd frontend-app
npm test -- --run src/App.test.jsx -t "renders warning log entries from bridge events"
```

- [x] Related full file tests:

```bash
cd frontend-app
npm test -- --run src/entities/client/model/warningRuntime.test.js src/pages/chat/components/RuntimePanelComponents.test.jsx src/App.test.jsx
```

- [x] LSP symbols/references were used for `warningRuntime.js` and `runtimeLogAdapter.js`. LSP diagnostics for changed files timed out repeatedly with `lsp_timeout`; use lint/typecheck/Vitest/build as substitute evidence.

- [x] Full frontend validation:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Observed: PASS. Full test result: 79 files, 1011 tests passed. Build completed successfully.

- [x] Diff checks:

```bash
git diff --check
git diff --cached --check
```

- [ ] Commit and push:

```bash
git commit -m "fix: 脱敏运行告警详情"
git push origin HEAD:main
```

## Stop Condition

This plan stops after runtime warning redaction is fixed, verified, committed, pushed to remote `main`, and the local r32 worktree/branch are cleaned. Other review findings remain candidates for later loop rounds and must not be mixed into this commit.
