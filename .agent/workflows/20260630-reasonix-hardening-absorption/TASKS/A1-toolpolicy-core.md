---
task_id: A1-toolpolicy-core
owner: agent-a1
status: done
depends_on: [A0-stage-source-inventory]
---

# A1-toolpolicy-core

## 1. Goal

Create a small `internal/platform/toolpolicy` owner for stage, trust source, capability, decisions, and shell safety.

## 2. Inputs

- A0 evidence.
- `SOURCE_PLAN_SNAPSHOT.md` Lane A tests.
- Reasonix concepts only as design input, not copied architecture.

## 3. Outputs

- `internal/platform/toolpolicy` package.
- Unit tests for plan/read-only/trust/shell invariants.

## 4. File Permissions

- RW: `internal/platform/toolpolicy/`
- RW: `internal/archtest/toolpolicy_dependency_guard_test.go` only if dependency guard is needed for this package.
- RO: `internal/platform/toolbridge/`, `internal/provider/`
- NO-TOUCH: `cmd/mcp-orch/tools/`, frontend, generated files.

## 5. Steps

1. Add failing tests for `PlanSafe => ReadOnly`.
2. Add failing tests proving `ReadOnly` does not imply `PlanSafe`.
3. Add failing tests for unknown trust and external hints failing closed.
4. Add failing tests for shell syntax, background/process-control, and dangerous arguments.
5. Implement minimal types: `Stage`, `TrustSource`, `Capability`, `Decision`.
6. Implement shell classification with a command/subcommand table and fail-closed parser behavior.
7. Run focused package tests.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy -run 'Plan|ReadOnly|Trust|Shell' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'ToolPolicy|Dependency' -count=1
```

## 7. DoD

- [x] Tests prove `PlanSafe => ReadOnly`.
- [x] Tests prove `ReadOnly != PlanSafe`.
- [x] Unknown and external hint paths deny with stable codes.
- [x] Shell policy denies process-control/background/dangerous forms.
- [x] Package has no unnecessary internal dependencies.

## 8. Rollback

Revert `internal/platform/toolpolicy/` and any task-owned archtest changes.
