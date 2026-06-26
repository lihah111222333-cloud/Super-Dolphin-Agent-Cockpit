---
task_id: L06
owner: worker-l06
status: planned
depends_on: []
---

# L06 Unified provider dream/session resolver

## 1. Goal
Reject unknown `DREAM_PROVIDER_ORDER` entries and fail auto-resume on store/decode errors except NotFound.

## 2. Input
- Plan lane L06.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-06-unified-provider`.

## 3. Output
- Resolver tests and implementation.

## 4. File Permissions
- RW: `internal/provider/unified/dream_executor.go`, `session_resolver.go`, `session_resolver_auto_resume.go`, matching tests.
- NO-TOUCH: other files.

## 5. Steps
1. RED: `TestDreamProviderOrderRejectsUnknownProvider`, `TestAutoResumeRuntimeConfigFailsOnThreadStoreError`.
2. Change provider-order resolver and runtime config helper to return errors.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/provider/unified -count=1
```

## 7. DoD
- [ ] Unknown provider blocks startup.
- [ ] DB/decode errors are surfaced.

## 8. Boundary
Shared provider contracts outside RW set require `NEEDS_APPROVAL`.

## 9. Rollback
Discard lane branch/worktree.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
