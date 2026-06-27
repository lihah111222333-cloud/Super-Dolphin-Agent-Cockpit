---
task_id: L07
owner: worker-l07
status: planned
depends_on: []
---

# L07 Cron and appupdate

## 1. Goal
Finalize submitted/running cron runs on terminal events and cap appupdate downloads by manifest size and timeout.

## 2. Input
- Plan lane L07.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-07-cron-appupdate`.

## 3. Output
- Cron terminal handling and appupdate size fail-fast tests/implementation.

## 4. File Permissions
- RW: `internal/module/cron/scheduler_recovery.go`, `scheduler.go`, `turn_adapter.go`, cron tests, `internal/module/appupdate/service.go`, appupdate tests.
- NO-TOUCH: other files.

## 5. Steps
1. RED: `TestCronTerminalEventFinalizesSubmittedRun`, `TestAppUpdateDownloadRejectsBodyLargerThanManifestSize`.
2. Add unresolved-run CAS finalization and appupdate limited reader/counting writer cleanup.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/module/cron ./internal/module/appupdate -count=1
```

## 7. DoD
- [ ] Submitted and running runs finalize.
- [ ] Oversized body deletes tmp and returns error.

## 8. Boundary
Store/schema changes require approval.

## 9. Rollback
Discard lane or revert merge.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
