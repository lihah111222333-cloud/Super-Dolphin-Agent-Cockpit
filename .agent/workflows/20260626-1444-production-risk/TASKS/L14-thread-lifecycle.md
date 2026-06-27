---
task_id: L14
owner: worker-l14
status: planned
depends_on: []
---

# L14 thread lifecycle prompt/archive

## 1. Goal
Support pending_launch unarchive without binding and persist inherited prompt snapshot during fork failure boundary.

## 2. Input
- Plan lane L14.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-14-thread-lifecycle`.

## 3. Output
- Thread lifecycle tests and implementation.

## 4. File Permissions
- RW: `internal/module/thread/archive.go`, `lifecycle_fork.go`, `lifecycle_helpers.go`, `prompt_snapshot.go`, matching thread tests.
- NO-TOUCH: other files.

## 5. Steps
1. RED: `TestUnarchivePendingLaunchDoesNotRequireBinding`, `TestForkPersistsInheritedPromptSnapshotBeforeReturning`.
2. Add pending_launch transaction/event path and fork snapshot persistence before return.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/module/thread -count=1
```

## 7. DoD
- [ ] pending_launch unarchive works and publishes projection event.
- [ ] snapshot failure stops new session and rolls back.

## 8. Boundary
Store schema changes require approval.

## 9. Rollback
Discard lane.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
