# Frontend RPC 1to1 Interface Test Design

**Date:** 2026-06-08
**Scope:** `frontend-app` current React/Vite UI
**Status:** Design only

## Goal

Build a project-specific frontend interface testing scheme for Super-Dolphin. The scheme adapts the 1to1 API testing idea to this repository's actual Wails/JRPC boundary:

```text
Go handler.Map RPC method
        ->
frontend-app RPC_METHODS constant
        ->
backendApi facade method
        ->
Vitest contract block
```

The goal is not to add more tests mechanically. The goal is to make frontend-to-backend contracts visible, consistently tested, and hard to drift accidentally.

## Current Context

The current React UI does not primarily call HTTP URLs. It calls the desktop host through the Wails runtime bridge:

```text
React page/store
  -> frontend-app/src/shared/api/backendApi.js
  -> frontend-app/src/shared/api/wailsBridge.js callAPI(method, params)
  -> internal/ui/wails.App.CallAPI
  -> internal/platform/rpc.Server.Dispatch
  -> Go handler.Map
```

Relevant source-of-truth files:

- `frontend-app/src/shared/api/backendApi.js` centralizes `RPC_METHODS`, facade methods, and frontend-side payload validation.
- `frontend-app/src/shared/api/wailsBridge.js` owns Wails runtime calls, frontend RPC telemetry, trace ingestion, and bridge errors.
- `frontend-app/src/shared/api/backendApi.test.js` already tests many facade method-to-payload contracts with `createBackendApi({ callAPI: vi.fn() })`.
- `docs/契约/jrpc2-convention.md` defines the core RPC direction: slash-style method names, object params, typed handlers, and JSON-RPC error semantics.
- `docs/ai01-docs/审查文档/frontend-app-vs-agent-terminal-backend-interface-diff-2026-06-03.md` records the current frontend RPC surface and confirms that `backendApi.js` is the current React UI's auditable RPC list.

The design is intentionally scoped to the current React UI. Legacy Vue/package-embed paths are out of scope unless a later migration task explicitly targets them.

## 1to1 Contract Definition

For this project, "1to1" means one backend-facing RPC capability has one auditable frontend contract path:

| Contract layer | Super-Dolphin mapping | What must be testable |
|---|---|---|
| Backend RPC method | Go `handler.Map` key such as `thread/start` | Method exists and accepts object params |
| Frontend registry | `RPC_METHODS.THREAD_START` | Constant string matches backend public method |
| Frontend facade | `backendApi.startThread(params)` | UI-friendly input becomes canonical backend payload |
| Contract test | `backendApi.test.js` describe/it block | Success payload, fail-fast validation, response passthrough or normalization |
| Consumer test | Page/store test using mocked facade | UI calls facade method, not `callAPI` directly |

Direct page/store calls to `callAPI()` should remain exceptional bridge-level code. Normal product code should consume `backendApi` facade methods.

## Risk Model

The reference HTTP plan names four interface risks. In this repo they translate as follows:

| General risk | Super-Dolphin RPC risk |
|---|---|
| Request construction error | Wrong RPC method, missing `cwd`/`threadId`, wrong camelCase/snake_case mapping, UI-only fields leaking to backend |
| Response understanding error | Store/page assumes a response field that backend does not return, or ignores a structured response needed for routing/status |
| Inconsistent error handling | Facade silently accepts invalid input, page-level timeouts hide still-running bridge RPCs, bridge errors are not surfaced consistently |
| Contract drift | Backend handler params change but `RPC_METHODS`, facade payload mapping, or tests do not change with it |

The preferred behavior is fail-fast. If required RPC params are empty, malformed, or ambiguous, the facade should throw before calling the backend.

## Test Layers

### L0 Static Surface Checks

Purpose: catch obvious drift before runtime tests.

Recommended future checks:

- Scan `backendApi.js` exports and `RPC_METHODS` to produce an RPC surface matrix.
- Scan product code for direct `callAPI(` usage outside `shared/api` and approved bridge tests.
- Compare `RPC_METHODS` strings with known Go handler registrations for high-value methods.
- Require every new public facade method to appear in the matrix.

This can start as a repo script or Vitest test. It does not require a new runtime dependency.

### L1 Facade Contract Tests

Purpose: one facade method maps one UI input shape to one canonical backend RPC call.

Use the existing pattern:

```js
const callAPI = vi.fn().mockResolvedValue({ ok: true });
const api = createBackendApi({ callAPI });

await api.startDag({ dagKey: 'dag-1', triggerSource: 'manual' });

expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_START, {
  dagKey: 'dag-1',
  triggerSource: 'manual',
});
```

Each facade contract block should cover:

- Success call: correct `RPC_METHODS` constant and canonical payload.
- Required params: missing `cwd`, `threadId`, key, path, boolean, or id fails before `callAPI`.
- Field translation: aliases such as `thread_id`/`threadId`, `draftKey`/`draft_key`, and provider/model fields are normalized once.
- Field stripping: UI-only fields such as optimistic message state do not cross the RPC boundary unless declared by backend contract.
- Response behavior: raw response is passed through, normalized, or rejected exactly as the facade intends.

### L2 Consumer Integration Tests

Purpose: pages and stores consume facade methods correctly.

These tests should mock `backendApi.js` exports rather than mocking Wails directly. They should verify user-facing flows such as:

- Chat send path calls `startThread` and `startTurn` with the active project cwd and visible input payload.
- Settings flows call preference/config facade methods instead of `callAPI` directly.
- Prompt, DAG, skill, memory, and file pages call the expected facade method for write actions.
- Failure states display the page/store-level error expected for a rejected facade call.

L2 is not a one-test-per-RPC layer. It should cover critical product chains and prevent pages from bypassing the API layer.

### L3 Frontend-to-Backend Contract Samples

Purpose: ensure representative frontend payloads still satisfy real Go handlers.

Recommended samples:

- For P0/P1 RPC groups, add Go-side or mixed contract tests that dispatch representative JSON payloads through `internal/platform/rpc.Server.Dispatch`.
- Prioritize typed handler boundaries and strict object params from `docs/契约/jrpc2-convention.md`.
- Keep samples small and focused; they are contract probes, not full backend business tests.

### L4 Desktop Smoke

Purpose: verify the live desktop bridge for the smallest set of critical chains.

Recommended smoke paths:

- Bootstrap current React UI through `run-new-ui-desktop.sh`.
- Confirm `thread/start -> turn/start` can be issued in the dev Wails path.
- Confirm key read paths such as `ui/sidebar/get`, `ui/dashboard/get`, and observability status do not fail immediately.
- Confirm frontend trace/bridge telemetry can record failures when an RPC rejects.

L4 should stay small. It should not become the primary interface test mechanism.

## Risk Levels

Use risk levels to decide how much testing each RPC family needs.

| Level | RPC family | Required layers |
|---|---|---|
| P0 | `thread/start`, `turn/start`, `turn/interrupt`, prompt writes, DAG start/dispatch/apply/delete, skill create/write/delete/import, memory write/delete/merge | L0 + L1 + selected L2 + selected L3 + minimal L4 |
| P1 | thread reads/config, dashboard details, prompt reads, memory reads, observability queries, projects/preferences that change active workspace context | L0 + L1 + targeted L2 or L3 |
| P2 | app update wrappers, auxiliary config reads, low-risk list/read helpers, native helpers that do not mutate project state | L0 + L1 |

When in doubt, prefer a lower-risk implementation first but document why the method is not P0.

## Registry and Matrix

The first implementation step should be a lightweight registry/matrix, not a new framework. A small data shape is enough:

```text
key: Thread.start
method: thread/start
facade: startThread
level: P0
source: backendApi.js
backend_owner: internal/module/thread
tests:
  facade_contract: backendApi.test.js
  consumer: useClientStore.test.js
  backend_contract: internal/module/thread
notes: strips optimistic UI-only fields before backend call
```

The matrix can initially live in documentation or a small JSON/JS test fixture. It should be generated only after the static rules are stable enough to avoid noisy churn.

## Standard L1 Test Template

Every new facade method should follow the same test shape:

```text
describe('<Domain> RPC facade', () => {
  it('maps <facade> to <rpc/method> payload', async () => ...)
  it('rejects invalid <required field> before callAPI', () => ...)
  it('normalizes aliases or strips UI-only fields when applicable', async () => ...)
})
```

Assertions:

- `expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.X, expectedPayload)`
- `expect(callAPI).not.toHaveBeenCalled()` for fail-fast validation
- `await expect(api.method(...)).resolves.toEqual(response)` when response passthrough is part of the contract
- `await expect(api.method(...)).rejects.toThrow(...)` when facade-level normalization rejects invalid inputs

Avoid testing implementation-private helpers directly unless a helper is exported as a public test utility. Test through `createBackendApi`.

## Anti-Patterns

These are explicitly out of bounds for the first implementation pass:

- Introducing MSW for Wails/JRPC facade tests. MSW is useful for HTTP, but the current interface risk is method/payload drift, not URL/header drift.
- Adding OpenAPI or Zod as a default requirement. The backend contract source here is Go typed handlers and JSON-RPC object params.
- Testing every RPC through desktop E2E. That would be slow and brittle.
- Letting pages or stores call `callAPI` directly when a facade method exists.
- Adding broad CI changes before a local matrix and focused tests prove useful.
- Adding silent fallback behavior to make tests pass. Contract mismatches should fail loudly.

## Phased Rollout

### Phase 1: Document and Inventory

- Publish this design.
- Build an initial RPC matrix from `backendApi.js`, current tests, and known Go handler locations.
- Mark P0/P1/P2 levels for current React facade methods.

### Phase 2: Facade Coverage

- Fill L1 gaps in `backendApi.test.js` for P0 methods first.
- Keep tests grouped by domain: thread/turn, prompt, DAG, skill, memory, config/projects.
- Add fail-fast assertions where a facade already has validation.

### Phase 3: Consumer Guardrails

- Add focused page/store tests for critical flows.
- Add a static check that product code does not import or call bridge-level `callAPI` directly outside `shared/api` and bridge tests.

### Phase 4: Backend Contract Samples

- Add small dispatch-level samples for the most brittle P0 payloads.
- Use real Go handler registration where practical, or a representative server fixture where full service wiring is too costly.

### Phase 5: CI Gate

- Run the static surface check and targeted facade contract tests in the normal frontend verification path.
- Keep broad frontend build/test commands as existing repo verification, not as the only interface contract proof.

## Definition of Done for Future RPC Changes

For a new or changed React frontend RPC facade, the change is complete only when:

1. `RPC_METHODS` contains the backend public method string.
2. A facade method maps UI input to the canonical backend object payload.
3. Required params fail before calling `callAPI`.
4. UI-only fields are stripped or intentionally sent with backend evidence.
5. The L1 contract test covers success payload and at least one relevant invalid input.
6. P0/P1 changes update consumer or backend contract samples when the behavior crosses that layer.
7. Product code calls the facade method rather than bridge-level `callAPI`.

## Verification for This Design-Only Change

This document changes no runtime code. Verification for this branch is:

```bash
git diff --check
git status --short --branch
```

Frontend tests, Go tests, and builds are intentionally skipped for this branch because the change is documentation-only.
