# Runtime Reliability Lease Idempotency And Final Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 wakeup lease、run idempotency、retry 和 final output 事务边界，避免重复执行、旧 owner 回写和 run status / output 不一致。

**Architecture:** 保持 `cmd/mcp-orch` 本地 runtime 模型，不引入外部 queue。以 taskdag store/sqlc 为事务边界，明确短 dispatch lease 和长 Agent execution ownership 的差异。

**Tech Stack:** Go, sqlc, SQLite, `cmd/mcp-orch/store/taskdag`, wakeup dispatcher, TurnCompleted subscriber.

---

## Ownership

**Primary owner:** Runtime reliability agent.

**Write scope:**
- `cmd/mcp-orch/sql/queries/task_dag_run.sql`
- `cmd/mcp-orch/sql/queries/task_dag_wakeup.sql`
- `cmd/mcp-orch/store/taskdag/`
- `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- focused tests under `cmd/mcp-orch/store/taskdag` and `cmd/mcp-orch/orchestration`

**Do not modify:**
- UI recovery buttons
- policy/tool registry
- Agent workflow layer objects
- template productization

## Functional Requirements

- Define lease owner, lease TTL, fencing token, release, expiry handling.
- Reject writes from expired or stale lease owners.
- Make duplicate wakeup claim deterministic.
- Make duplicate TurnCompleted events idempotent.
- Define idempotency behavior for run states: `running`, `succeeded`, `failed`, `cancelled`.
- Move final output materialization and run success transition into a single production transaction, or lower the API/UI claim until transaction support lands.

## Tasks

### Task 1: Document Store Reliability Contract In Code

**Files:**
- Modify: `cmd/mcp-orch/store/taskdag/contract.go`

- [ ] Add function-level Chinese comments for lease and idempotency methods that explain:
  - short dispatch lease scope
  - stale owner rejection
  - retry ownership
  - duplicate completion behavior

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\store\taskdag\contract.go
```

Expected: exit 0.

### Task 2: Add Lease Fencing Tests

**Files:**
- Test: `cmd/mcp-orch/store/taskdag/wakeup_lease_fencing_test.go`

- [ ] Add a test where two workers claim the same due wakeup and only one succeeds.
- [ ] Add a test where an expired lease owner attempts to finish dispatch and receives a stale lease result.
- [ ] Add a test where reclaimer makes an expired wakeup claimable again.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\store\taskdag\wakeup_lease_fencing_test.go
```

Expected: exit 0.

### Task 3: Add Run Idempotency State Table Tests

**Files:**
- Test: `cmd/mcp-orch/store/taskdag/run_idempotency_test.go`
- Modify if required: `cmd/mcp-orch/store/taskdag/store_start_run.go`

- [ ] Test same idempotency key with existing `running` run returns existing run.
- [ ] Test same idempotency key with `succeeded` run returns existing run.
- [ ] Test same idempotency key with `failed` run returns exhausted failure.
- [ ] Test same idempotency key with `cancelled` run returns exhausted cancellation.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\store\taskdag\run_idempotency_test.go
```

Expected: exit 0.

### Task 4: Make TurnCompleted Idempotent

**Files:**
- Modify: `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- Test: `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber_idempotency_test.go`

- [ ] Add a regression test where the same `spawning_thread_id` completion is received twice.
- [ ] Verify downstream nodes are promoted once.
- [ ] Verify node result/shared artifact is not duplicated.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\orchestration\dag_turn_completed_subscriber.go
```

Expected: exit 0.

### Task 5: Transactional Final Output

**Files:**
- Modify: `cmd/mcp-orch/sql/queries/task_dag_run.sql`
- Modify generated sqlc files only through existing sqlc workflow.
- Modify: `cmd/mcp-orch/store/taskdag/`
- Test: `cmd/mcp-orch/store/taskdag/final_output_transaction_test.go`

- [ ] Add a production store path that validates final node completion.
- [ ] In the same transaction, write `metadata.final_output` and set run status `succeeded`.
- [ ] Add a failure test where final output write fails and run status remains non-success.

Run:

```powershell
make sqlc-verify
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\store\taskdag\final_output_transaction_test.go
```

Expected: sqlc verification exit 0 and targeted guard exit 0.

## Acceptance

- Concurrent wakeup claim cannot double execute a node.
- Stale lease owner cannot update wakeup state.
- Duplicate TurnCompleted cannot promote downstream twice.
- Run idempotency behavior is explicit for all terminal states.
- `succeeded` and final output metadata cannot diverge in the production store path.
