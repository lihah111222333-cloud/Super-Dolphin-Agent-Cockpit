---
task_id: L18
owner: worker-l18
status: planned
depends_on: []
---

# L18 updater helper timeout and rollback

## 1. Goal
Run helper external commands with timeout/process-group kill and install via staged copy with rollback.

## 2. Input
- Plan lane L18.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-18-updater`.

## 3. Output
- Updater timeout/rollback tests and implementation.

## 4. File Permissions
- RW: `cmd/super-dolphin-updater/install.go`, `cmd/super-dolphin-updater/main.go`, updater tests.
- NO-TOUCH: other files.

## 5. Steps
1. RED: `TestRunCommandTimesOutAndKillsProcessGroup`, `TestInstallRollsBackWhenDittoTimesOutAfterBackup`.
2. Implement `runCommand(ctx, timeout, name, args...)` with process group kill and staged install rollback.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./cmd/super-dolphin-updater -count=1
```

## 7. DoD
- [ ] Timeout kills command group.
- [ ] Failed post-backup install rolls back.

## 8. Boundary
macOS packaging scripts outside updater command require approval.

## 9. Rollback
Discard lane.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
