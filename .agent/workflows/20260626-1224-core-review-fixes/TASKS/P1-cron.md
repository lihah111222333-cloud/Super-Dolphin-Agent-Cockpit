---
task_id: P1-cron
owner: agent-1
status: done
depends_on: [P0-plan]
---

# P1-cron

## 1. Goal

Make cron schedule and runtime config handling fail fast instead of hiding invalid inputs.

## 2. Inputs

- Review findings: invalid schedule/timezone accepted; scheduler recovery reuses old `next_run_at`; runtime config JSON parse failure returns nil.
- Input files: `internal/module/cron/schedule.go`, `service.go`, `scheduler_recovery.go`, `turn_adapter.go`, and same-package tests.

## 3. Outputs

- Regression tests that fail before production edits.
- Production code that rejects invalid schedule/timezone/config errors instead of silently falling back.

## 4. File Permissions

- RW: `internal/module/cron/`
- RO: `internal/contract/`, `internal/store/cronstore`
- NO-TOUCH: frontend, provider, store schema, workflow files except evidence through orchestrator.

## 5. Steps

1. Add focused failing tests for invalid timezone, invalid schedule on create/update, scheduler recovery parse failure, and bad runtime config.
2. Run the focused tests and record the RED failure.
3. Implement minimal fail-fast changes.
4. Run `./scripts/test_with_guard.sh` for edited Go files and package tests.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh internal/module/cron/schedule.go
./scripts/test_with_guard.sh internal/module/cron/service.go
./scripts/test_with_guard.sh internal/module/cron/scheduler_recovery.go
./scripts/test_with_guard.sh internal/module/cron/turn_adapter.go
./scripts/test_with_guard.sh ./internal/module/cron -count=1
```

## 7. DoD

- [ ] RED failures observed for new tests.
- [ ] All new tests pass after implementation.
- [ ] No edits outside `internal/module/cron/`.
- [ ] Guard commands pass or exact blocker is reported.

## 8. Edge Cases

- Empty timezone remains allowed as UTC only if tests document it.
- Corrupt historical DB rows should not be silently treated as valid runtime config.

## 9. Rollback

Revert only files under `internal/module/cron/`.

## 10. Evidence

Append command summaries to `CHECKS/EVIDENCE.md` via orchestrator.
