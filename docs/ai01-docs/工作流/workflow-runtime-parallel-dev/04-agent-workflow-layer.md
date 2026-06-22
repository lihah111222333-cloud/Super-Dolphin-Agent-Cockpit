# Agent Workflow Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为多 Agent 协作补齐一等对象：WorkflowPlan、AgentTask、ReviewGate、CrossValidation、HandoffPackage、ArtifactLifecycle 和 AcceptanceRecord。

**Architecture:** 该层建立在 Runtime Kernel 之上，不改变 DAG 调度核心。先实现领域类型、存储、MCP/RPC 入口和最小工作台数据接口，再由产品 UI 消费。

**Tech Stack:** Go, sqlc, SQLite, MCP tools, jrpc2 contracts, React later consumption.

---

## Ownership

**Primary owner:** Agent workflow agent.

**Write scope:**
- `internal/contract/`
- `cmd/mcp-orch/orchestration/agentworkflow/`
- `cmd/mcp-orch/store/agentworkflow/`
- `cmd/mcp-orch/tools/agent_workflow_tools.go`
- migrations for agent workflow tables
- focused tests for the new packages

**Do not modify:**
- wakeup dispatcher reliability
- command runner security
- template marketplace UI
- MR provider integration

## Functional Requirements

- `WorkflowPlan` records goal, non-goals, risks, acceptance criteria, eval list, allowed write scope and expected artifacts.
- `AgentTask` records role, input context, output contract, verification command, budget, dependency list and status.
- `ReviewGate` records reviewer, target artifact, blocking findings, non-blocking findings, re-review state and pass condition.
- `CrossValidation` records independent reviewers, disagreements, evidence and arbitration result.
- `HandoffPackage` records current goal, completed work, attempted paths, failure evidence, residual risks and next actions.
- `ArtifactLifecycle` tracks draft, candidate, reviewed, accepted, merged, discarded.
- `AcceptanceRecord` records user acceptance, automated verification and residual risk.

## Tasks

### Task 1: Define Contract Types

**Files:**
- Create: `internal/contract/agent_workflow.go`
- Test: `internal/contract/agent_workflow_test.go`

- [ ] Define DTOs for plan, task, review gate, cross validation, handoff, artifact and acceptance.
- [ ] Use explicit enum types for statuses.
- [ ] Add JSON round-trip tests.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\internal\contract\agent_workflow.go
```

Expected: exit 0.

### Task 2: Add Store Schema And sqlc Queries

**Files:**
- Create migration for agent workflow tables.
- Create: `cmd/mcp-orch/sql/queries/agent_workflow.sql`
- Create: `cmd/mcp-orch/store/agentworkflow/`

- [ ] Add tables for plans, tasks, artifacts, review gates and acceptance records.
- [ ] Add FK links to workflow run id where available.
- [ ] Add indexes for status queue queries.
- [ ] Generate sqlc through existing repository workflow.

Run:

```powershell
make sqlc-verify
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./cmd/mcp-orch/store/agentworkflow -count=1
```

Expected: exit 0.

### Task 3: Add Domain Service

**Files:**
- Create: `cmd/mcp-orch/orchestration/agentworkflow/service.go`
- Test: `cmd/mcp-orch/orchestration/agentworkflow/service_test.go`

- [ ] Implement create plan.
- [ ] Implement create task.
- [ ] Implement transition task status.
- [ ] Implement open and resolve review gate.
- [ ] Implement record acceptance.
- [ ] Reject invalid status transitions.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./cmd/mcp-orch/orchestration/agentworkflow -count=1
```

Expected: exit 0.

### Task 4: Add MCP Tools

**Files:**
- Create: `cmd/mcp-orch/tools/agent_workflow_tools.go`
- Modify: `cmd/mcp-orch/tools/factory.go`
- Test: `cmd/mcp-orch/tools/agent_workflow_tools_test.go`

- [ ] Add tools for plan create/get, task create/update, review gate open/resolve, artifact attach and acceptance record.
- [ ] Keep schemas strongly typed.
- [ ] Add validation tests for required fields.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\tools\agent_workflow_tools.go
```

Expected: exit 0.

## Acceptance

- A requirement can be represented as a `WorkflowPlan` with multiple `AgentTask` records.
- Each task can carry verification command and output artifact.
- Review gates can block acceptance and be resolved.
- Handoff package preserves enough context for another agent to resume.
- No runtime DAG scheduling behavior changes are required for this layer.
