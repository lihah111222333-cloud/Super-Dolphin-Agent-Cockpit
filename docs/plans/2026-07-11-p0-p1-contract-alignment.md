# P0/P1 Contract Alignment Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Do not commit: the user reserved all commits until final review approval.

**Goal:** Repair the four confirmed P0/P1 contract defects with minimal compatibility-preserving changes and regression tests.

**Architecture:** Keep the existing Go wire protocol. Fix native Wails metadata at the Go ingress, make cross-language guards read production sources, and normalize snapshot status data at the frontend store boundary.

**Tech Stack:** Go, jrpc2, Wails v3, React/Zustand, Node ESM, Vitest.

**Verification Surface:** `internal/ui/wails`, `internal/module/uistate`, `frontend-app/scripts/rpc-contract-audit*`, frontend bridge/store validators, full frontend lint/test/build.

---

### Task 1: Strip `_aoRequestId` before strict dispatch

**Files:**
- Modify: `internal/ui/wails/binding.go`
- Test: `internal/ui/wails/binding_id_test.go`

- [ ] Add a strict-handler test whose input includes `_aoRequestId` and whose handler DTO does not declare it.
- [ ] Run `go test ./internal/ui/wails -run 'TestCallAPI.*RequestID' -count=1`; expect `invalid parameters` before the fix.
- [ ] Add `_aoRequestId` to the explicit frontend metadata classifier used before ordinary dispatch and the `ui/log` trace-preserving path.
- [ ] Re-run the focused test; expect PASS.
- [ ] Run `./scripts/test_with_guard.sh ./internal/ui/wails -count=1`.

### Task 2: Bind Method ID tests to frontend production constants

**Files:**
- Modify: `internal/ui/wails/binding_id_test.go`
- Read-only production source: `frontend-app/src/shared/api/wails/wailsBridgeConstants.js`

- [ ] Add fail-first parser/guard tests covering missing, unknown, duplicate, and changed `METHOD_IDS` entries.
- [ ] Run the new focused test and confirm RED because the current test uses a copied numeric map.
- [ ] Replace the copied expected map with strict parsing of the production `METHOD_IDS` object and compare every entry to the backend FNV-1a method FQN.
- [ ] Run `go test ./internal/ui/wails -run TestFrontendMethodIDsMatchBackendFQN -count=1`.
- [ ] Re-run the full guarded Wails package test.

### Task 3: Audit runtime RPC facts

**Files:**
- Modify: `frontend-app/scripts/rpc-contract-audit.mjs`
- Modify: `frontend-app/scripts/rpc-contract-audit.test.mjs`
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Read/possibly modify only if required by the runtime-source extraction: `frontend-app/src/shared/api/backend/backendRpcMethods.js`, `frontend-app/src/shared/api/backend/backendApiFactoryThread.js`

- [ ] Add a fixture test where the runtime method map or actual payload builder drifts while the facade shadow stays unchanged; expect the current audit to miss it.
- [ ] Run `npx vitest run scripts/rpc-contract-audit.test.mjs` and verify RED.
- [ ] Point method extraction at `backend/backendRpcMethods.js` and payload extraction at the real runtime builder/factory source.
- [ ] Remove the duplicate method table and dummy payload builders from `backendApi.js`; re-export the canonical runtime table only when consumers require it.
- [ ] Run `npm run audit:rpc-contracts` and the focused audit tests.

### Task 4: Normalize snapshot status entries

**Files:**
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreSnapshotModel.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Test: `frontend-app/src/entities/client/model/useClientStore.test.js`
- Test: `frontend-app/src/shared/api/backendResponseValidators.test.js`

- [ ] Add a real-wire snapshot fixture with string `statuses` plus the parallel header/details/interruptible/activity/runtime maps; assert a rich status object in store state.
- [ ] Add malformed snapshot status-map validation cases; run focused tests and verify RED.
- [ ] Normalize all snapshot thread IDs into the same object shape produced by `bridgePatchState.js`, preserving absent optional fields without defaulting malformed values.
- [ ] Extend snapshot validation to reject non-string status-map values and malformed parallel maps.
- [ ] Run focused store and validator tests, then `npx vitest run src/entities/client/model/bridgePatchState.test.js`.

### Final Verification and Review Handoff

- [ ] Run LSP diagnostics on every changed Go/JS file; all severities must be empty or explicitly resolved.
- [ ] Run `./scripts/test_with_guard.sh ./internal/ui/wails ./internal/module/uistate -count=1`.
- [ ] Run `cd frontend-app && npm run lint && npm test && npm run build`.
- [ ] Run `git diff --check`, inspect the full owned diff, and confirm no generated/unrelated files are included.
- [ ] Perform final specification and code-quality review.
- [ ] Stop with uncommitted changes for user review. Commit/merge/push only after explicit approval.
