---
task_id: L11
owner: worker-l11
status: planned
depends_on: []
---

# L11 platform config/cache/pidregistry fail-fast

## 1. Goal
Fail on invalid env, propagate cache shutdown errors, and abort spawn when pidregistry persist fails.

## 2. Input
- Plan lane L11.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-11-config-cache-pidregistry`.

## 3. Output
- Fail-fast tests and implementation.

## 4. File Permissions
- RW: `internal/platform/config/`, `internal/platform/cachekeepalive/`, `internal/platform/pidregistry/`, `internal/provider/codexapp/pool_spawner.go`, `internal/provider/codexapp/*pidregistry*_test.go`.
- NO-TOUCH: non-pidregistry codexapp tests owned by L04.

## 5. Steps
1. RED: invalid env fails startup, cache shutdown error propagates, pidregistry persist error aborts spawn.
2. Implement unset-vs-invalid parsing, OnStop error return, `Register` error, child kill on registration failure.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/platform/config ./internal/platform/cachekeepalive ./internal/platform/pidregistry ./internal/provider/codexapp -count=1
```

## 7. DoD
- [ ] Invalid present env never silently defaults.
- [ ] Persist failure aborts spawn and cleans process.

## 8. Boundary
Codexapp files beyond `pool_spawner.go` or pidregistry tests require approval.

## 9. Rollback
Discard lane branch/worktree.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
