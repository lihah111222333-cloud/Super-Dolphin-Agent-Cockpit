# Reasonix Production Hardening - Current Main Recheck

TASK_ID: `TASK_0_CURRENT_MAIN_RECHECK`

DATE: `2026-07-16`

STATUS: `MAIN_REVIEW_COMPLETE`

SOURCE_HEAD: `1ea371f4e39279703dd2023a94add2dccafbcfa8`

VERDICT: `RELEASE_EXECUTABLE_MCP_DECISION_PENDING`

`release_design_complete=true`

`mcp_design_complete=false`

`implementation_design_complete=derived(release_design_complete && mcp_design_complete)=false`

`p0_release_executable=true`

`p0_mcp_executable=false`

`p0_executable=derived(p0_release_executable && p0_mcp_executable)=false`

## Purpose

本文件记录 `main@1ea371f4...` 上的源码 owner 重检和本次计划合理性瘦身。主 Agent 已完成 Release lane 初审并开放 Task 1-3；MCP lane 仅保留 compiler cancellation/hard-bound 或有界进程隔离 decision blocker。逐任务采用实现 Agent -> 主 Agent 初审 -> integration 合并；三 Agent 审查统一放到全部任务完成后的 integration exact commit。

## Review Object And Worktree

| Field | Value |
| --- | --- |
| Checkout | `/Users/mima0000/Desktop/wj/super-agent-v3` |
| Branch | `main` |
| Local HEAD | `1ea371f4e39279703dd2023a94add2dccafbcfa8` |
| User-supplied expected HEAD | `1ea371f4e39279703dd2023a94add2dccafbcfa8` |
| Historical Task 0 evidence base | `b40867229af8e17916c00393639ccb0fcb4bf6fc` |
| Revised plan SHA-256 | `79fc9e3fd36fb318122abf546f7dc989632e5739bfba16ba3a665020612c37aa` |
| Revised Design Freeze SHA-256 | `a8e05561f2ba449e07bea79b89cb3f1dd219288da6873c2f04b7500fda95694d` |
| Immutable Git object | not available; revised documents remain uncommitted |

This agent's only write set:

- `docs/plans/2026-07-15-reasonix-production-hardening-next-absorption.md`
- `docs/plans/evidence/reasonix-production-hardening-next/01-task0-design-freeze.md`
- `docs/plans/evidence/reasonix-production-hardening-next/02-current-main-recheck.md`

Preserved external dirty path:

- `docs/plans/evidence/reasonix-production-hardening-next/00-task0-baseline.md`
- `internal/module/uistate/overlay_test.go` (appeared during this run; removes the two historical `unusedwrite` assignments)

这些外部修改未被回退、覆盖或纳入本 agent 的写集。

## Five LSP Evidence Categories

本次通过当前 `mcp__lsp.*` surface 取得五类证据，`work_dir=/Users/mima0000/Desktop/wj/super-agent-v3`。

| Category | Action / target | Result |
| --- | --- | --- |
| Locate | `grep(text_search)` for `turnStartHandler`, `mcpServerConfigBinary`, `func Bind(`；`structure(document_symbol)` on eventsurface/turn files | 定位 `bindAgentLifecycle`/payload owner、`turnStartHandler`、`(*service).StartTurn`、`mcpServerConfigBinary` |
| Understand | `inspect(hover|definition|implementation)` | `bind.go` payload 调用定义跳到 `bind_payloads.go`; `Service.StartTurn` 实现跳到 `service.go:216`; `dto.MCPBinary` 定义含 `TrustedServerID` |
| Impact | `xref(references)` | `bindAgentLifecycle` 由 `Bind` 装配；`turnStartHandler` 由 `rpc.go` 注册；`StartTurn` 有 RPC、orchestration 和 tests 调用；`TrustedServerID` 引用不包含 turn manifest 构造 |
| Read | `file(read_file)` | 精读 `bindAgentLifecycle`、`agentRuntimeReportedPayload`、`turnStartHandler`、`(*service).StartTurn`、`mcpServerConfigBinary` 与 `MCPBinary` |
| Diagnostics | `file(diagnostics)` on exact P0 owner files and current-worktree `internal/module/uistate/overlay_test.go` | `No diagnostics found`; overlay result depends on an external uncommitted edit |

### Owner findings

1. Event projection owner is `internal/platform/eventsurface/bind_payloads.go` plus `bind.go`, not `internal/ui/eventsurface/agent_runtime.go`.
2. Backend `turn/start` entry is `internal/module/turn/rpc_helpers.go:turnStartHandler`; it calls `Service.PrepareTurn` then `Service.StartTurn`, whose production implementation is `internal/module/turn/service.go:(*service).StartTurn`. `internal/app/turn_rpc.go` is not the owner.
3. `internal/dto/provider/manifest.go:MCPBinary` contains `TrustedServerID`, but `internal/module/turn/manifest.go:mcpServerConfigBinary` currently initializes only name/transport/URL/headers or command/env. The turn manifest therefore drops the trusted server identity.

The exact P0 owner set reports no diagnostics. The previous `overlay_test.go` diagnostics are also absent in the current worktree, but the clearing edit is external, uncommitted and not part of this write set, so it is not claimed as immutable HEAD evidence. Provider-wide readiness has moved to follow-up, so that test file is no longer a Release or MCP lane owner blocker. This does not open either implementation lane because plan review and MCP compiler-decision blockers remain.

## Plan Rationality Revision

This revision made the following decisions:

- replaced global all-or-nothing `p0_executable` with independent `p0_release_executable` and `p0_mcp_executable`; total status is derived by AND;
- made Task 1-3 depend only on Release blockers and Task 4 only on MCP blockers;
- reduced P0 to release transaction/recovery and Codex toolbridge schema compile/quarantine;
- moved ProductIP/SBOM component graph, provider-wide executable/compatibility/ingress, Claude provider-wide readiness and generic RPC principal/admission into explicit follow-up entries, not cancellation;
- retained minimal update trust, MCP authority/current-CAS and actual changed-producer field guards;
- removed the proposed global future-field registry/scanner claim; guards are created dynamically for each actual changed producer;
- changed compiler isolation from a preselected helper binary to a decision gate based on cancellation/hard-bound evidence;
- corrected the event, turn/send and `TrustedServerID` source landing facts.

## Implementation And Final Review Rule

Each implementation task uses an isolated task branch/worktree. The implementation Agent completes the scoped change, the main Agent performs the only per-task review with LSP/diff/tests, and an accepted task is immediately merged into `codex/integration-reasonix-p0`. No additional per-task reviewer Agent is created.

After Task 1-4 and Task 5 are complete, three fresh Agents with no inherited context review the frozen integration commit for Release, MCP/security and repo-wide integration. Final P0 completion requires all three to report `0 P0 / 0 P1`.

## Blockers

### Release lane

- none; main Agent review completed.

### MCP lane

- `MCP_COMPILER_CANCELLATION_OR_ISOLATION_DECISION_REQUIRED`

## Validation Record

| Check | Result |
| --- | --- |
| LSP owner files diagnostics | PASS: no diagnostics |
| Historical `overlay_test.go` diagnostics | current worktree clean; external uncommitted fix preserved, not claimed as HEAD evidence or P0 owner gate |
| `git diff --check` | PASS on the full current worktree |
| Relevant docs gates | PASS: AI maintenance plan selected only `diff:whitespace`; `ai_maintenance_gates.sh` exited 0 |
| Write-set audit | PASS: this agent changed only the three declared files; external dirty `00-task0-baseline.md` and `overlay_test.go` were preserved |

## Current Verdict

`RELEASE_EXECUTABLE_MCP_DECISION_PENDING`

Task 1-3 are open. Task 4 remains closed only until the compiler decision evidence is implemented and accepted by the main Agent.

## AI Maintenance Evidence

This block reports completion of the current-main investigation, plan revision and
diagnostic cleanup verification. It does not override the lane-specific design
blockers recorded above.

```yaml
PACKAGE: TASK_0_CURRENT_MAIN_RECHECK
STATUS: DONE_WITH_EVIDENCE
AGENTID: 019f6a51-990d-7ac1-8c94-8098bb840007
BASE_HEAD: 1ea371f4e39279703dd2023a94add2dccafbcfa8
OWNED_FILES_CHANGED:
  - docs/plans/2026-07-15-reasonix-production-hardening-next-absorption.md
  - docs/plans/evidence/reasonix-production-hardening-next/00-task0-baseline.md
  - docs/plans/evidence/reasonix-production-hardening-next/01-task0-design-freeze.md
  - docs/plans/evidence/reasonix-production-hardening-next/02-current-main-recheck.md
  - internal/module/uistate/overlay_test.go
UNRELATED_DIRTY_FILES_PRESERVED: []
LSP_EVIDENCE:
  locate: PASS
  inspect: PASS
  xref: PASS
  read_file: PASS
  diagnostics: PASS
COMMANDS_RUN:
  - cmd: ./scripts/test_with_guard.sh ./internal/module/uistate -count=1
    exit: 0
  - cmd: go run ./scripts/lsp_diagnostics_gate --file internal/module/uistate/overlay_test.go
    exit: 0
  - cmd: make capcontract-check
    exit: 0
  - cmd: make codemap-check
    exit: 0
  - cmd: make project-map-check
    exit: 0
  - cmd: git diff --check
    exit: 0
BLOCKERS: []
```
