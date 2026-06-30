---
task_id: A2-toolbridge-stage-gate
owner: agent-a2
status: not_applicable_with_evidence
depends_on: [A1-toolpolicy-core]
---

# A2-toolbridge-stage-gate

## 1. Goal

Wire the planning-stage gate into toolbridge only after A0 proves an authoritative stage source.

## 0. Orchestrator Decision

A0 evidence recorded `stage_source_found=false`; this task is closed as `not_applicable_with_evidence`. Do not wire runtime planning-stage blocking from `AgentTypePlan`, `update_plan`, Claude plan-mode tools, or external `readOnlyHint`.

## 2. Inputs

- A0 stage-source decision.
- A1 `toolpolicy` package.
- `internal/platform/toolbridge/types.go`
- `internal/platform/toolbridge/handler*.go`
- `internal/platform/toolbridge/host_tools.go`
- `internal/platform/toolbridge/ports.go`
- Provider sandbox/security tests.

## 3. Outputs

- Toolbridge tests for planning allow/deny.
- Runtime gate implementation if and only if A0 found a valid stage source.
- No bypass of MCP lifecycle policy.

## 4. File Permissions

- RW: `internal/platform/toolbridge/types.go`
- RW: `internal/platform/toolbridge/handler*.go`
- RW: `internal/platform/toolbridge/*_test.go`
- RO: `internal/provider/codexapp/native_tool_policy_validation_test.go`
- RO: `internal/provider/claudecli/transport_config_security_test.go`
- NO-TOUCH: `cmd/mcp-orch/tools/` production code.

## 5. Steps

1. If A0 says `stage_source_found=false`, stop and mark this task `not_applicable_with_evidence`; do not wire runtime blocking.
2. Add failing tests for host tool allow/deny in planning stage.
3. Add failing tests proving disabled/suspended/removed lifecycle policy is not bypassed.
4. Add failing tests proving schema validation still happens before handler invocation.
5. Add minimal stage propagation from the A0-proven source into `ToolCallRequest`; default must be `execution`.
6. Call `toolpolicy` before host-direct or V3-owned MCP execution where the source plan allows.
7. Re-run provider sandbox/security regression tests.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Lifecycle|Sandbox|Permission' -count=1
```

## 7. DoD

- [x] N/A: A0 recorded `stage_source_found=false`; no runtime planning-stage blocking was wired.
- [x] N/A: planning deny behavior remains owned by A1 `toolpolicy`; A2 added no runtime gate.
- [x] N/A: lifecycle deny was not changed by this non-applicable task.
- [x] N/A: schema-validation order was not changed by this non-applicable task.
- [x] N/A: provider sandbox tests remained integration-gate coverage, not A2 implementation output.

## 8. Rollback

Revert only task-owned toolbridge changes.
