# Runtime Foundation And State Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 冻结 Workflow Runtime Kernel 的术语、状态模型和现有能力清单，消除文档、契约、UI 和 runtime 之间的状态歧义。

**Architecture:** 不引入新的 workflow backend。以现有 DAG v2 / `cmd/mcp-orch` 为事实来源，增加 canonical term/state 文档和最小契约测试，明确持久状态与派生展示状态的边界。

**Tech Stack:** Go, sqlc, SQLite migrations, `cmd/mcp-orch`, `internal/contract`, React workflow UI.

---

## Ownership

**Primary owner:** Runtime foundation agent.

**Write scope:**
- `docs/adr/`
- `docs/契约/`
- `internal/contract/orchestration.go`
- `cmd/mcp-orch/orchestration/nodeexec/types.go`
- `cmd/mcp-orch/orchestration/nodeexec/status.go`
- `cmd/mcp-orch/tools/task_schemas.go`
- focused tests in the same packages

**Do not modify:**
- policy implementation
- command runner security
- product workbench UI beyond labels required for state naming
- template marketplace logic
- MR/PR integration

## Functional Requirements

- Define `Workflow Runtime Kernel` as `cmd/mcp-orch` owned runtime.
- Document current mappings: `task_dags`, `task_dags.version`, `task_dag_runs`, `task_dag_nodes.run_id`, wakeups, leases.
- Keep `waiting_for_assignee` as derived state from `ready + assigned_to == ""` or `StartDAG.execution_state`.
- Keep persisted run statuses aligned with DB CHECK: `running`, `succeeded`, `failed`, `cancelled`.
- Mark `waiting_human`, `skipped`, `awaiting_verify`, and `hybrid` as reserved or legacy until runtime closes the loop.
- Remove any user-facing implication that Hybrid/HITL is executable.

## Tasks

### Task 1: Add Canonical Runtime ADR

**Files:**
- Create: `docs/adr/00xx-workflow-runtime-kernel.md`

- [ ] Write an ADR that records:
  - `cmd/mcp-orch` owns runtime.
  - `workflowtemplate` is template/draft only.
  - MCP tools are adapters.
  - `waiting_for_assignee` is derived, not persisted.
  - Temporal is a future backend option.

- [ ] Verify no conflicting wording:

```powershell
Select-String -Path .\docs\adr\*.md -Pattern "waiting_for_assignee|Workflow Runtime Kernel|workflowtemplate"
```

### Task 2: Add Canonical State Contract

**Files:**
- Create: `docs/契约/workflow-runtime-state-contract.md`
- Modify only if required: `internal/contract/orchestration.go`

- [ ] Document persisted node states and derived display states.
- [ ] Document persisted run states and derived run display states.
- [ ] Document transition rules and forbidden transitions.
- [ ] State that `ready + assigned_to == ""` cannot move to `running`.

### Task 3: Lock Current State Names With Tests

**Files:**
- Test: `cmd/mcp-orch/orchestration/nodeexec/status_test.go`
- Test: `cmd/mcp-orch/orchestration/nodeexec/types_test.go`

- [ ] Add table tests for the persisted node status set.
- [ ] Add table tests that reject derived display states as persisted node statuses.
- [ ] Add tests that `waiting_for_assignee` is not a persisted node status.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\orchestration\nodeexec\status_test.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\orchestration\nodeexec\types_test.go
```

Expected: exit 0.

### Task 4: Hide Or Fail-Fast Incomplete Runtime Capabilities

**Files:**
- Modify: `cmd/mcp-orch/tools/task_schemas.go`
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- Test: same-package Go tests and focused frontend tests if present

- [ ] Identify every path that accepts `hybrid`, `waiting_human`, `awaiting_verify`, or `skipped` as user-created executable capability.
- [ ] Either hide it from UI/schema choices or fail-fast at create/start validation.
- [ ] Preserve legacy read/display of historical data.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\tools\task_schemas.go
cd frontend-app
npm run lint
```

Expected: Go guard exit 0 and frontend lint exit 0.

## Acceptance

- A reviewer can point from every public runtime term to a code/store mapping.
- No persisted status list includes `waiting_for_assignee`.
- UI/schema cannot create executable Hybrid/HITL nodes until runtime support is implemented.
- No Temporal or external queue dependency is introduced.
