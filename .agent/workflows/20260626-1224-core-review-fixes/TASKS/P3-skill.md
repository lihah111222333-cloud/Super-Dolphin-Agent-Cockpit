---
task_id: P3-skill
owner: agent-3
status: done
depends_on: [P0-plan]
---

# P3-skill

## 1. Goal

Fix skill subsystem silent truncation, command-output short write, personal-home fallback, and import boundary skip behavior.

## 2. Inputs

- Review findings in `skills_fs.go`, `exec.go`, `scope_model.go`, and `skills_import.go`.

## 3. Outputs

- Regression tests first.
- Production fixes that fail explicitly on oversize remote skill, preserve io.Writer semantics, reject missing home, and return boundary-check errors.

## 4. File Permissions

- RW: `internal/module/skill/`
- RO: `internal/provider/shared/provider_home.go`, provider driver files for context only.
- NO-TOUCH: provider implementation, app, contract, cron, memory, prompt, turn.

## 5. Steps

1. Add failing tests for each A4 finding.
2. Run focused tests to capture RED.
3. Implement minimal fixes.
4. Run file guards and `./internal/module/skill` tests.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh internal/module/skill/skills_fs.go
./scripts/test_with_guard.sh internal/module/skill/exec.go
./scripts/test_with_guard.sh internal/module/skill/scope_model.go
./scripts/test_with_guard.sh internal/module/skill/skills_import.go
./scripts/test_with_guard.sh ./internal/module/skill -count=1
```

## 7. DoD

- [ ] RED failures observed.
- [ ] All four A4 findings have regression coverage.
- [ ] No provider edits.
- [ ] Focused guards pass or blocker is reported.

## 8. Edge Cases

- `io.Writer` must report `len(p), nil` after accepting data even if the stored buffer is capped.
- Empty explicit env override should remain ignored only if current behavior already documents that.

## 9. Rollback

Revert `internal/module/skill/` only.

## 10. Evidence

Append command summaries to `CHECKS/EVIDENCE.md` via orchestrator.
