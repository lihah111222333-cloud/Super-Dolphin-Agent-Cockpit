---
task_id: L13
owner: worker-l13
status: planned
depends_on: []
---

# L13 frontend runtime event and interrupt semantics

## 1. Goal
Make runtime subscription retryable and report interrupt `ok:false` as warning/failure.

## 2. Input
- Plan lane L13.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-13-frontend-runtime`.

## 3. Output
- Vitest RED/GREEN and frontend implementation.

## 4. File Permissions
- RW: `frontend-app/src/entities/client/model/runtimeSlice.js`, `frontend-app/src/shared/api/wailsBridge.js`, `frontend-app/src/entities/client/model/threadLifecycleRuntime.js`, matching runtime/wailsBridge tests.
- NO-TOUCH: Go Wails files.

## 5. Steps
1. RED: first runtime unavailable then second available re-registers; `turn/interrupt` `ok:false` does not show success.
2. Implement `{ready, unsubscribe}` contract and result handling.
3. Run frontend verification.

## 6. Verification
```bash
cd frontend-app && npm test -- src/entities/client/model src/shared/api
cd frontend-app && npm run lint && npm run build
```

## 7. DoD
- [ ] Failed subscription can retry.
- [ ] Interrupt failure warns and returns false.

## 8. Boundary
Changing Go bridge contract requires `NEEDS_APPROVAL`.

## 9. Rollback
Discard lane branch/worktree.

## 10. Evidence
Report RED/GREEN, verification, files, approvals, residual risk.
