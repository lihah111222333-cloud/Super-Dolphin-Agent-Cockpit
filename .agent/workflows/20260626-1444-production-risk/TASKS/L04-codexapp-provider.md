---
task_id: L04
owner: worker-l04
status: planned
depends_on: []
---

# L04 Codex app provider pool/recovery/native policy

## 1. Goal
Fix concurrent pool acquire, prevent unsafe replay, reject invalid native tool policy, and report runtime session URL.

## 2. Input
- Plan lane L04.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-04-codexapp-provider`.

## 3. Output
- Tests and implementation in codexapp provider files.

## 4. File Permissions
- RW: `internal/provider/codexapp/server_pool.go`, `recovery.go`, `transport.go`, `driver_pool_routing.go`, `support.go`, codexapp tests except `*pidregistry*_test.go`.
- NO-TOUCH: `internal/provider/codexapp/*pidregistry*_test.go` belongs to L11.

## 5. Steps
1. RED: `TestServerPoolConcurrentAcquireSharesInFlightSpawn`, `TestRecoveryDoesNotReplayWhenProviderTurnStillActive`, `TestNativeToolPolicyRejectsInvalidListTypes`.
2. Add in-flight spawn/refCount handling, provider state confirmation before replay, strict config validation, runtime report URL parsing.
3. Run guard for each changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
```

## 7. DoD
- [ ] No duplicate spawn under concurrent acquire.
- [ ] Replay blocked unless loss is confirmed.
- [ ] Invalid native policy fails startup.

## 8. Boundary
Pidregistry-specific tests or files beyond RW set require `NEEDS_APPROVAL`.

## 9. Rollback
Discard or revert lane.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
