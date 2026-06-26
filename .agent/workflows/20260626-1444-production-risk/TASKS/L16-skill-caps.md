---
task_id: L16
owner: worker-l16
status: planned
depends_on: []
---

# L16 skill hash/mirror resource caps

## 1. Goal
Apply streaming hash/copy and single-file/total/file-count caps for skill import/hash/mirror.

## 2. Input
- Plan lane L16.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-16-skill-caps`.

## 3. Output
- Skill cap tests and implementation.

## 4. File Permissions
- RW: `internal/module/skill/skillhash/hash.go`, `mirror_manifest.go`, `mirror_publisher.go`, `skills_import.go`, `internal/module/skill/**/*_test.go`.
- NO-TOUCH: provider mirror files outside skill module unless approved.

## 5. Steps
1. RED: large file import/hash cap and mirror copy cap tests.
2. Introduce `SkillContentLimits`, streaming `io.CopyN`, bounded mirror readers, and fail-fast mirror stop.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/module/skill/... -count=1
```

## 7. DoD
- [ ] Oversize files fail before full buffering.
- [ ] Mirror stops on cap violation.

## 8. Boundary
Runtime provider home changes require `NEEDS_APPROVAL`.

## 9. Rollback
Discard lane branch/worktree.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
