---
task_id: L15
owner: worker-l15
status: planned
depends_on: []
---

# L15 memory context/prefetch/team sync

## 1. Goal
Fail memory root resolution, surface prefetch errors, and stop team metadata CWD fallback.

## 2. Input
- Plan lane L15.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-15-memory`.

## 3. Output
- Memory tests and fail-fast implementation.

## 4. File Permissions
- RW: `internal/module/memory/rules_provider.go`, `service.go`, `retrieval/prefetch.go`, `team/thread_metadata.go`, `module.go`, matching memory/team/retrieval tests.
- NO-TOUCH: other files.

## 5. Steps
1. RED: invalid root, prefetch error surfaced, team metadata parse/store error tests.
2. Return errors from provider/builders and expose prefetch error.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/module/memory/... -count=1
```

## 7. DoD
- [ ] Invalid root blocks Fx construction.
- [ ] Prefetch/team metadata errors are returned.

## 8. Boundary
Memory store contract changes outside RW set require approval.

## 9. Rollback
Discard lane.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
