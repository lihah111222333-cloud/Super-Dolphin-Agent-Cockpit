---
task_id: L03
owner: worker-l03
status: planned
depends_on: []
---

# L03 mcp-orch workspace/sharedfile boundaries

## 1. Goal
Reject out-of-scope workspace roots and fail fast for missing disk-only shared files.

## 2. Input
- Plan lane L03.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-03-workspace-sharedfile`.

## 3. Output
- Workspace root containment and shared file content-location behavior.

## 4. File Permissions
- RW: `cmd/mcp-orch/tools/workspace_tools.go`, `cmd/mcp-orch/workspace/service.go`, `service_merge.go`, `cmd/mcp-orch/store/sharedfile/store.go`, `internal/platform/sharedfilefs/disk.go`, matching workspace/sharedfile tests.
- NO-TOUCH: other files without approval.

## 5. Steps
1. RED: `TestWorkspaceCreateRunRejectsSourceRootOutsideScope`, `TestSharedFileDiskOnlyMissingFileFails`.
2. Add allowed-root request plumbing, containment checks on create/merge/write/delete, and `content_location=disk|inline` fail-fast reads.
3. Run per-file guard after each Go edit.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/workspace ./cmd/mcp-orch/store/sharedfile ./cmd/mcp-orch/tools -count=1
```

## 7. DoD
- [ ] Out-of-root source rejected in tool and service paths.
- [ ] Missing disk body returns error, not empty content.

## 8. Boundary
Schema/migration files outside RW set require `NEEDS_APPROVAL`.

## 9. Rollback
Discard lane branch/worktree before integration or revert merge commit.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, risk.
