---
task_id: L05
owner: worker-l05
status: planned
depends_on: []
---

# L05 Claude CLI provider fail-fast

## 1. Goal
Make auth/manifest/timestamp parse failures explicit and normalize process-gone ForceComplete as idempotent success.

## 2. Input
- Plan lane L05.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-05-claudecli`.

## 3. Output
- Tests and minimal provider changes.

## 4. File Permissions
- RW: `internal/provider/claudecli/auth_preflight.go`, `transport_config.go`, `event_map.go`, `session_log_watcher_integration.go`, `session_config.go`, matching tests.
- NO-TOUCH: other files.

## 5. Steps
1. RED tests: inconclusive auth, rejected manifest server, missing event timestamp, process-gone ForceComplete.
2. Implement fail-fast errors and normalized ForceComplete behavior.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/provider/claudecli -count=1
```

## 7. DoD
- [ ] Parse/manifest/timestamp errors are not swallowed.
- [ ] Process-gone ForceComplete closes idempotently.

## 8. Boundary
Transport APIs outside listed files require approval.

## 9. Rollback
Discard lane branch/worktree.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
