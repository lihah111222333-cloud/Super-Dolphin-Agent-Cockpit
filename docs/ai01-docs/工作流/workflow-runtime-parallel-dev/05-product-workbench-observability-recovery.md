# Product Workbench Observability And Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让用户在工作台中理解 workflow 运行状态、定位卡点，并执行安全的恢复动作。

**Architecture:** 前端工作台只展示和触发动作，不直接写持久状态。后端提供 derived state、diagnostics、artifact summary 和受控 recovery actions。

**Tech Stack:** React/Vite, Go RPC contracts, MCP toolbridge, `cmd/mcp-orch` diagnostics, existing workflow page.

---

## Ownership

**Primary owner:** Product workbench agent.

**Write scope:**
- `frontend-app/src/pages/workflows/`
- focused frontend components under `frontend-app/src/`
- `internal/contract/orchestration.go`
- `internal/app/orchestration_dag_runtime_adapter.go`
- `cmd/mcp-orch/tools/task_tools.go`
- focused tests for new UI/backend actions

**Do not modify:**
- core state enum implementation without coordination with Group A
- lease/final output store internals without coordination with Group B
- policy internals without coordination with Group C
- template marketplace
- MR provider integration

## Functional Requirements

- Run list shows derived status: active, waiting_for_assignee, waiting_timer, failed, recoverable_failed.
- Node detail shows executor, spawning thread id, failure class, last wakeup, artifact links and next action.
- Diagnostics support lookup by trace id, run id, node key and child thread id.
- Recovery actions include retry failed node, rerun from node, resume run, cancel with cleanup and edit-and-retry.
- Recovery actions must call backend tools/RPC and pass policy checks.
- UI must not show Hybrid/HITL as executable until supported.

## Tasks

### Task 1: Add Derived Run Summary Contract

**Files:**
- Modify: `internal/contract/orchestration.go`
- Modify: `internal/app/orchestration_dag_runtime_adapter.go`
- Test: focused contract/adapter tests

- [ ] Add derived state fields without changing persisted run status.
- [ ] Include blocked reason, next action and artifact count.
- [ ] Map tool response fields in the adapter.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\internal\contract\orchestration.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\internal\app\orchestration_dag_runtime_adapter.go
```

Expected: exit 0.

### Task 2: Add Diagnostics Tool/RPC Data

**Files:**
- Modify: `cmd/mcp-orch/tools/task_tools.go`
- Modify: `cmd/mcp-orch/tools/task_tool_definitions.go`
- Test: `cmd/mcp-orch/tools/task_diagnostics_test.go`

- [ ] Extend existing diagnostics to return run id, node key, child thread id, failure class and artifact links.
- [ ] Add validation for missing identifiers.
- [ ] Keep response small for list views.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\tools\task_tools.go
```

Expected: exit 0.

### Task 3: Build Workbench Summary UI

**Files:**
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- Create focused components under `frontend-app/src/pages/workflows/components/`
- Test: frontend tests if existing workflow page tests are present

- [ ] Add run summary list with derived state badges.
- [ ] Add node detail panel with failure and artifact summary.
- [ ] Add diagnostics drawer for trace/run/thread lookup.
- [ ] Hide unsupported Hybrid/HITL create actions.

Run:

```powershell
cd frontend-app
npm run lint
npm test -- --runInBand
```

Expected: lint exit 0 and tests exit 0. If the test runner does not support `--runInBand`, run the repository's existing frontend test command and record the output.

### Task 4: Add Recovery Action Entry Points

**Files:**
- Modify: `cmd/mcp-orch/tools/task_tool_definitions.go`
- Modify: `cmd/mcp-orch/tools/task_tools.go`
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- Test: backend and frontend focused tests

- [ ] Add backend actions for retry failed node and cancel with cleanup first.
- [ ] Add UI buttons only when derived state allows the action.
- [ ] Block actions when policy denies them.
- [ ] Record recovery action in run events.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./cmd/mcp-orch/tools -count=1
cd frontend-app
npm run lint
```

Expected: Go guard exit 0 and frontend lint exit 0.

## Acceptance

- User can open a workflow run and understand why it is active, waiting or failed.
- User can trace a child agent thread back to the workflow node.
- Unsupported node types are not presented as executable options.
- At least one recovery action is end-to-end functional with policy and event logging.
