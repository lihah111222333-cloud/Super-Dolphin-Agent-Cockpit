---
task_id: P4-contract-app
owner: agent-4
status: done
depends_on: [P0-plan]
---

# P4-contract-app

## 1. Goal

Make orchestration boundary failures explicit for final-output metadata and missing orchestration service.

## 2. Inputs

- Review findings in `internal/contract/orchestration.go` and `internal/app/thread_orchestration_adapter.go`.

## 3. Outputs

- Regression tests first.
- Minimal contract/app changes that do not silently collapse parse errors or missing orchestration service into success-like behavior.

## 4. File Permissions

- RW: `internal/contract/orchestration.go`, `internal/contract/dag_final_output_test.go`, `internal/app/thread_orchestration_adapter.go`, matching app tests.
- RO: `internal/module/thread/` consumers.
- NO-TOUCH: module implementations, provider, store, frontend.

## 5. Steps

1. Add failing tests for malformed final-output metadata and missing orchestration service facade.
2. Run focused tests to capture RED.
3. Implement smallest compatible API change; if existing consumers require old function, add an error-returning helper and keep old wrapper only for intentionally lossy callers.
4. Run file and package guards.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh internal/contract/orchestration.go
./scripts/test_with_guard.sh internal/app/thread_orchestration_adapter.go
./scripts/test_with_guard.sh ./internal/contract ./internal/app -count=1
```

## 7. DoD

- [ ] RED failures observed.
- [ ] Parse errors and missing service are explicitly test-covered.
- [ ] Consumer compatibility is preserved or every call site is updated.
- [ ] Focused guards pass or blocker is reported.

## 8. Edge Cases

- Existing ok=false behavior may be relied on; retain a wrapper if needed while introducing a strict path for delete/GC/dashboard consumers.
- Noop runtime reporter is out of scope unless a test proves shared risk.

## 9. Rollback

Revert owned files only.

## 10. Evidence

Append command summaries to `CHECKS/EVIDENCE.md` via orchestrator.
