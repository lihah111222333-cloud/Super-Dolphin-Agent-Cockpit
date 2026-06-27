---
task_id: L02
owner: worker-l02
status: planned
depends_on: []
---

# L02 mcp-orch DAG terminal and lease fencing

## 1. Goal
Add durable compensation for terminal failures, require `TurnID`, fence `task_update_node`, and parameterize retry attempts.

## 2. Input
- Plan lane L02.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-02-orch-dag`.

## 3. Output
- Tests and code for terminal event validation, lease checks, compensation, and retry policy.

## 4. File Permissions
- RW: `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`, `hook_consumer_dag_fallback.go`, `launcher.go`, `dag.go`, `cmd/mcp-orch/tools/task_tools.go`, `task_tool_definitions.go`, SQL queries `task_dag_node_runtime.sql`, `task_dag_wakeup_dispatch.sql`, matching orchestration/tools tests.
- Approved expansion after `NEEDS_APPROVAL`: `cmd/mcp-orch/store/taskdag/contract.go`, `cmd/mcp-orch/store/taskdag/store_wakeup.go`, `cmd/mcp-orch/store/sqlc/task_dag_wakeup_dispatch.sql.go`, `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`. This approval is limited to retry hard-cap parameterization. Any further generated file or store/orchestration path must request approval again.
- NO-TOUCH: other files unless approved.

## 5. Steps
1. RED tests: `TestRemoteTerminalRequiresTurnID`, `TestTaskUpdateNodeRejectsNonLeaseHolder`, subscriber/fallback compensation tests.
2. Implement metadata injection, service/store lease fencing, compensation queue/outbox, retry attempts parameter.
3. Run single-file guard after every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1
```

## 7. DoD
- [ ] Non-lease holder rejected.
- [ ] Empty `TurnID` rejected.
- [ ] Store failure compensation recorded.

## 8. Boundary
SQL regeneration or store-layer file needs beyond RW set require `NEEDS_APPROVAL`.

Approved expansion is only for the four files listed above and only for retry hard-cap parameterization.

## 9. Rollback
Controller drops branch or reverts lane merge.

## 10. Evidence
Report RED/GREEN, guards, changed files, approvals, residual risk.
