# Policy Tool Registry And Security Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 workflow 写操作、MCP tool、command runner、workspace cwd 和 shared file 的统一治理边界。

**Architecture:** Policy Gate 管权限和风险，Review Gate 管质量审查。MCP tools 保持 adapter 方向，先为工具注册和危险执行补元数据、校验和审计，不做完整人工审批系统。

**Tech Stack:** Go, MCP tools, toolbridge, command card runner, shared file store, PowerShell guard.

---

## Ownership

**Primary owner:** Policy and security agent.

**Write scope:**
- `cmd/mcp-orch/tools/types.go`
- `cmd/mcp-orch/tools/factory.go`
- `cmd/mcp-orch/tools/task_tool_definitions.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- `cmd/mcp-orch/orchestration/nodeexec/executor_agent.go`
- `internal/platform/toolbridge/`
- focused tests in same packages

**Do not modify:**
- node state transition matrix
- final output store transaction
- Product Workbench UI
- template marketplace

## Functional Requirements

- Tool metadata includes name, version, input schema, output schema, capabilities, risk class, permission, workspace scope, timeout, idempotency requirement, approval flag, audit event type, redaction policy.
- Policy decision set is limited to `allow`, `deny`, `fail-fast` until approval MVP exists.
- Command cwd must resolve inside an allowed workspace root.
- Shell execution requires high-risk policy.
- Shared file write must validate path ownership, content type, size, owner node, producer actor and prompt reference rules.
- toolbridge records source actor, target peer, tool name and trace id.
- Error messages must not expose secrets.

## Tasks

### Task 1: Extend Tool Definition Metadata

**Files:**
- Modify: `cmd/mcp-orch/tools/types.go`
- Modify: `cmd/mcp-orch/tools/task_tool_definitions.go`
- Test: `cmd/mcp-orch/tools/tool_metadata_test.go`

- [ ] Add metadata fields without changing existing handler signatures unless necessary.
- [ ] Mark each workflow write tool with risk and permission metadata.
- [ ] Add a test that every registered task tool has non-empty metadata.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\tools\types.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\tools\task_tool_definitions.go
```

Expected: exit 0.

### Task 2: Add Policy Decision Boundary

**Files:**
- Create: `cmd/mcp-orch/orchestration/policy/policy.go`
- Test: `cmd/mcp-orch/orchestration/policy/policy_test.go`

- [ ] Define `DecisionAllow`, `DecisionDeny`, and `DecisionFailFast`.
- [ ] Explicitly reject `require_approval` until Product Phase 2 implements approval MVP.
- [ ] Add table tests for workflow write, command execution, shared file write and provider identity override.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\orchestration\policy\policy.go
```

Expected: exit 0.

### Task 3: Harden Command Runner Inputs

**Files:**
- Modify: `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- Test: `cmd/mcp-orch/orchestration/nodeexec/executor_automation_security_test.go`

- [ ] Resolve cwd and reject paths outside workspace root.
- [ ] Reject symlink/path escape.
- [ ] Use env allowlist.
- [ ] Require high-risk policy before shell execution.
- [ ] Redact sensitive stdout/stderr fields before audit logging.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\cmd\mcp-orch\orchestration\nodeexec\executor_automation.go
```

Expected: exit 0.

### Task 4: Harden Shared File Writes

**Files:**
- Modify focused shared file adapter used by `NodeExecutorRouter`
- Test: same package shared file security test

- [ ] Validate path ownership under workflow/shared-file root.
- [ ] Validate content type and size.
- [ ] Strip control fields from automation output before prompt reuse.
- [ ] Record owner node and producer actor.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./cmd/mcp-orch/... -count=1
```

Expected: exit 0.

### Task 5: Add Toolbridge Audit Fields

**Files:**
- Modify focused files under `internal/platform/toolbridge/`
- Test: same package tests

- [ ] Record source actor, target peer, tool name, trace id and redaction policy in tool dispatch traces.
- [ ] Add tests that sensitive params are redacted.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/platform/toolbridge -count=1
```

Expected: exit 0.

## Acceptance

- Every workflow write tool has governance metadata.
- Policy does not return approval states until approval MVP exists.
- Command execution cannot escape workspace root.
- Shared file writes have owner, type, size and prompt reference rules.
- Audit records do not expose secrets.
