# Backend Boundary Closure Agent Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将后端模块边界硬度从当前约 92/100 推进到 95+，继续清理 `contract.OrchestrationService` compat 预算和 dashboard/module store 直连。

**Architecture:** 每个 worker 只处理一个行为面，在使用方包内定义 owner-local narrow port，先迁移 handler/service 参数，再收紧 archtest allowlist。主 agent 只做集成、冲突裁决、生成物刷新和最终验证；review agents 独立复核边界真实性与回归风险。

**Tech Stack:** Go, fx, SQLite/sqlc stores, `cmd/mcp-orch` tools, `internal/module/dashboard`, `internal/archtest`, LSP MCP toolchain, project-map/codemap/capability-contract generated artifacts.

**Verification Surface:** `./scripts/test_with_guard.sh ./internal/archtest -count=1`, affected Go packages, `make guard`, `scripts/refresh_generated_artifacts.sh all --check`, `make capcontract-check`, `make project-map-check`, `make codemap-check`, `git diff --check`.

---

## Current Baseline

Current remote main is expected to be:

```bash
git rev-parse HEAD origin/main
```

Expected:

```text
90a94b8a9905e0dd3da19ec420368932ba87048f
90a94b8a9905e0dd3da19ec420368932ba87048f
```

The known unrelated untracked file is:

```text
docs/plans/2026-07-08-frontend-code-size-guard-thaw-plan.md
```

Do not stage, modify, delete, or rely on that file.

## Remaining Boundary Debt

Current `contract.OrchestrationService` compat budget is concentrated in:

```text
cmd/mcp-orch/tools/orchestration_report_tool.go
cmd/mcp-orch/tools/orchestration_send_message.go
cmd/mcp-orch/tools/orchestration_tools.go
cmd/mcp-orch/tools/task_tools.go
cmd/mcp-orch/tools/task_apply_ops.go
cmd/mcp-orch/tools/task_lifecycle_helpers.go
cmd/mcp-orch/tools/task_diagnostics.go
cmd/mcp-orch/tools/workflow_workbench.go
internal/module/dashboard/module.go
internal/module/dashboard/service.go
internal/module/uistate/module.go
internal/module/uistate/service.go
internal/module/memory/module.go
internal/platform/mcpcontrol/module.go
internal/platform/mcpcontrol/handlers.go
internal/platform/mcpcontrol/report_handlers.go
internal/app/runtime_reporter_adapter.go
```

Current dashboard store injection still directly consumes these store interfaces:

```text
internal/module/dashboard/module.go: AgentStatuses agentstatusstore.Store
internal/module/dashboard/module.go: SystemLogs systemlogstore.Store
internal/module/dashboard/module.go: AuditLogs auditlogstore.Store
internal/module/dashboard/module.go: BusLogs buslogstore.Store
internal/module/dashboard/module.go: AILogs ailogstore.Store
```

Already closed and should not regress:

```text
cmd/mcp-orch/tools/orchestration_recover_agent.go -> agentRecoverPort
cmd/mcp-orch/tools/orchestration_interrupt_agent.go -> agentInterruptPort
internal/module/dashboard/module.go -> DBQueryExecutor, CommandCardReader, PromptTemplateReader, SharedFileReader
docs/doc/codemap/capability-contract/capability_manifest.json refreshed
```

## Agent Count and Rounds

Use **6 implementation agents + 2 review agents + 1 controller/main agent**.

Run in two implementation rounds:

```text
Round 1, lower to medium risk:
- Agent A: report tools
- Agent B: launch/list agents
- Agent F: dashboard/module read-model and remaining store ports

Round 2, medium to high risk:
- Agent C: send-message turn/report follow-up
- Agent D: DAG task tools
- Agent E: workflow diagnostics/workbench
```

After each round:

```text
1. Merge only clean worker branches into the integration worktree.
2. Run targeted validation.
3. Dispatch two review agents.
4. Fix P0/P1 before starting the next round.
5. Push only after local main and generated checks are green.
```

## Worktree Setup

Controller creates one integration worktree and one worker worktree per implementation agent:

```bash
git fetch origin main
git status --short --branch
git worktree list --porcelain
git worktree add -b codex/backend-boundary-closure .worktrees/backend-boundary-closure origin/main
git worktree add -b codex/backend-boundary-report .worktrees/backend-boundary-report codex/backend-boundary-closure
git worktree add -b codex/backend-boundary-launch-list .worktrees/backend-boundary-launch-list codex/backend-boundary-closure
git worktree add -b codex/backend-boundary-dashboard-module .worktrees/backend-boundary-dashboard-module codex/backend-boundary-closure
```

Round 2 worktrees are created only after Round 1 is reviewed:

```bash
git worktree add -b codex/backend-boundary-send-message .worktrees/backend-boundary-send-message codex/backend-boundary-closure
git worktree add -b codex/backend-boundary-dag-tools .worktrees/backend-boundary-dag-tools codex/backend-boundary-closure
git worktree add -b codex/backend-boundary-workflow .worktrees/backend-boundary-workflow codex/backend-boundary-closure
```

## LSP Evidence Requirement

Every implementation and review agent must read:

```text
docs/internal-notes/LSP系统提示词.md
.agents/skills/后端/SKILL.md
.agents/skills/架构设计/SKILL.md
.agents/skills/完成前验证/SKILL.md
```

Every source-touching task must keep evidence for:

```text
grep(text_search or ast_search)
structure(document_symbol or workspace_symbol)
inspect(definition or hover)
xref(references or call_hierarchy)
file(read_file)
file(diagnostics)
```

Shell `rg`, `sed`, and `go test` do not replace LSP evidence.

## Round 1

### Task A: Report Tool Port

**Owner:** Agent A

**Worktree:** `.worktrees/backend-boundary-report`

**Files:**

- Modify: `cmd/mcp-orch/tools/orchestration_report_tool.go`
- Modify tests if needed: `cmd/mcp-orch/tools/orchestration_report_*_test.go`
- Modify: `internal/archtest/orchestration_service_boundary_test.go`
- Generated if needed: `docs/doc/codemap/capability-contract/capability_manifest.json`, `docs/doc/codemap/project-map/index/*.tsv`

- [ ] **Step 1: Locate current full-service consumers**

Run LSP:

```text
grep(text_search, query="contract.OrchestrationService", path="cmd/mcp-orch/tools/orchestration_report_tool.go")
structure(document_symbol, file_path="cmd/mcp-orch/tools/orchestration_report_tool.go")
xref(call_hierarchy, pos="cmd/mcp-orch/tools/orchestration_report_tool.go:70:6", direction="incoming")
file(read_file, pos="cmd/mcp-orch/tools/orchestration_report_tool.go:70", limit=320)
```

Expected: identify the exact service methods used by get report, list reports, wait for report, and report snapshot helpers.

- [ ] **Step 2: Define owner-local report port**

Add a local interface near the top of `cmd/mcp-orch/tools/orchestration_report_tool.go`:

```go
type agentReportPort interface {
	GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error)
	RememberReportRequest(ctx context.Context, req contract.RememberReportRequest) (contract.RememberReportRequestResult, error)
}
```

Current `contract.OrchestrationService` does not expose a bulk report listing method; batch report waiting currently polls `GetReport` per agent. Do not invent list methods. Adjust method names and signatures only if LSP/read-file proves current contract definitions changed. Do not add methods that the report tool does not call.

- [ ] **Step 3: Migrate report handlers and helpers**

Change these signatures from `contract.OrchestrationService` to the new port:

```text
HandleGetAgentReport
HandleGetAgentReports
readAgentReportsSnapshot
waitForAgentReports
waitForAgentReport
pollPendingAgentReport
pollAgentReport
```

Expected: `rg "contract\\.OrchestrationService" cmd/mcp-orch/tools/orchestration_report_tool.go` returns no production matches.

- [ ] **Step 4: Update tests and archtest budget**

If tests use full service stubs only to satisfy unrelated methods, replace them with the narrow port. Then remove or reduce:

```go
"cmd/mcp-orch/tools/orchestration_report_tool.go": compat(7, ...)
```

Expected: deleted from allowlist if the file has zero selectors, or reduced to the exact remaining count with a new reason if a compatibility adapter remains.

- [ ] **Step 5: Validate and commit**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/archtest -run 'Test.*Report.*|Test.*Orchestration.*|Test.*Boundary.*' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/archtest -count=1
make capcontract-check
scripts/refresh_generated_artifacts.sh all --check
git diff --check
git status --short
```

If `make capcontract-check` fails because the new port changed the manifest, run:

```bash
go run ./scripts/capcontract
node scripts/generate_ai_project_map.mjs --filesystem-scan
```

Commit owned files only. Do not stage wildcard matches blindly; first inspect changed files:

```bash
git diff --name-status
git add cmd/mcp-orch/tools/orchestration_report_tool.go
git add internal/archtest/orchestration_service_boundary_test.go
git add docs/doc/codemap/capability-contract/capability_manifest.json
git add cmd/mcp-orch/tools/orchestration_report_wait_closure_test.go
git add docs/doc/codemap/project-map/index/orchestration.tsv
git add docs/doc/codemap/project-map/index/docs-agent.tsv
git commit -m "refactor: 收束编排报告工具窄端口"
```

Skip any `git add` path above that is absent from `git diff --name-status`; do not replace it with a wildcard.

### Task B: Launch/List Agent Ports

**Owner:** Agent B

**Worktree:** `.worktrees/backend-boundary-launch-list`

**Files:**

- Modify: `cmd/mcp-orch/tools/orchestration_tools.go`
- Modify tests if needed: `cmd/mcp-orch/tools/orchestration_*_test.go`, `cmd/mcp-orch/tools/handler_regression_test.go`
- Modify: `internal/archtest/orchestration_service_boundary_test.go`

- [ ] **Step 1: Separate launch/list responsibilities**

Use LSP to identify methods used by:

```text
HandleLaunchAgent
launchRequestForHandler
rejectChildAgentDelegation
matchingAgentID
reserveLaunchAgentID
existingLaunchAgentIDs
HandleListAgents
hydrateListAgentReports
```

Expected: launch, list, snapshot, and report hydration are distinct clusters.

- [ ] **Step 2: Define local ports**

Add narrow interfaces in `cmd/mcp-orch/tools/orchestration_tools.go`:

```go
type agentLaunchPort interface {
	LaunchAgent(ctx context.Context, req contract.LaunchRequest) error
	ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error)
	Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error)
}

type agentListPort interface {
	ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error)
	GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error)
}
```

Adjust method set to exact usage. Avoid one large `agentToolsPort` that recreates the fat interface.

- [ ] **Step 3: Migrate low-coupling functions first**

Move `HandleListAgents` and `hydrateListAgentReports` to `agentListPort`.

Then move `HandleLaunchAgent` and launch helpers to `agentLaunchPort`.

Expected: `compat(10)` for `orchestration_tools.go` drops by at least 4.

- [ ] **Step 4: Validate nil guard and registry behavior**

Run focused tests around launch/list/handler nil service:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -run 'Test.*Launch.*|Test.*ListAgents.*|Test.*Nil.*|Test.*Handler.*' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Test.*Orchestration.*|Test.*Boundary.*' -count=1
```

- [ ] **Step 5: Update generated artifacts and commit**

Run:

```bash
make capcontract-check || go run ./scripts/capcontract
scripts/refresh_generated_artifacts.sh all --check || node scripts/generate_ai_project_map.mjs --filesystem-scan
git diff --check
```

Commit owned files only:

```bash
git diff --name-status
git add cmd/mcp-orch/tools/orchestration_tools.go
git add internal/archtest/orchestration_service_boundary_test.go
git add docs/doc/codemap/capability-contract/capability_manifest.json
git add cmd/mcp-orch/tools/orchestration_tools_test.go
git add cmd/mcp-orch/tools/handler_regression_test.go
git add docs/doc/codemap/project-map/index/orchestration.tsv
git add docs/doc/codemap/project-map/index/docs-agent.tsv
git commit -m "refactor: 收束编排启动列表工具端口"
```

Skip any `git add` path above that is absent from `git diff --name-status`; do not replace it with a wildcard.

### Task F: Dashboard and Module Read-Model Ports

**Owner:** Agent F

**Worktree:** `.worktrees/backend-boundary-dashboard-module`

**Files:**

- Modify: `internal/module/dashboard/module.go`
- Modify: `internal/module/dashboard/wire_dto.go` only if a missing owner-local interface must be declared there.
- Modify: `internal/archtest/interface_isolation_guard_test.go`
- Optional later in same branch only if small: `internal/module/uistate/module.go`, `internal/module/uistate/service.go`, `internal/module/memory/module.go`

- [ ] **Step 1: Convert remaining dashboard store params**

Change `serviceParams` fields in `internal/module/dashboard/module.go`:

```go
AgentStatuses AgentStatusReader
SystemLogs    SystemLogReader
AuditLogs     AuditLogReader
BusLogs       BusLogReader
AILogs        AILogReader
```

Keep adapters as the only store-facing boundary:

```go
fx.Provide(
	adaptAgentStatusReader,
	adaptSystemLogReader,
	adaptAuditLogReader,
	adaptBusLogReader,
	adaptAILogReader,
	adaptDBQueryExecutor,
	adaptCommandCardReader,
	adaptPromptTemplateReader,
	adaptSharedFileReader,
)
```

- [ ] **Step 2: Expand dashboard archtest matrix**

In `internal/archtest/interface_isolation_guard_test.go`, add these expected field checks:

```go
{field: "AgentStatuses", want: "AgentStatusReader"},
{field: "SystemLogs", want: "SystemLogReader"},
{field: "AuditLogs", want: "AuditLogReader"},
{field: "BusLogs", want: "BusLogReader"},
{field: "AILogs", want: "AILogReader"},
```

Add adapter checks:

```go
{funcName: "adaptAgentStatusReader", paramName: "store", want: "agentstatusstore.Store"},
{funcName: "adaptSystemLogReader", paramName: "store", want: "systemlogstore.Store"},
{funcName: "adaptAuditLogReader", paramName: "store", want: "auditlogstore.Store"},
{funcName: "adaptBusLogReader", paramName: "store", want: "buslogstore.Store"},
{funcName: "adaptAILogReader", paramName: "store", want: "ailogstore.Store"},
```

- [ ] **Step 3: Validate dashboard Fx wiring**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/dashboard -count=1
./scripts/test_with_guard.sh ./internal/app -run TestAppModuleGraphIsClosed -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Test.*Dashboard.*|Test.*Store.*|Test.*Interface.*' -count=1
```

- [ ] **Step 4: Optional module orchestration read-model split**

Only if Step 3 is clean and diff remains small, inspect:

```text
internal/module/uistate/module.go
internal/module/uistate/service.go
internal/module/memory/module.go
```

Add owner-local ports only where the service uses one or two orchestration methods. If a file needs more than three methods from `OrchestrationService`, leave it for a dedicated task.

- [ ] **Step 5: Commit**

Run:

```bash
scripts/refresh_generated_artifacts.sh all --check
git diff --check
git status --short
```

Commit owned files only:

```bash
git diff --name-status
git add internal/module/dashboard/module.go
git add internal/archtest/interface_isolation_guard_test.go
git add internal/module/dashboard/wire_dto.go
git add internal/module/uistate/module.go
git add internal/module/uistate/service.go
git add internal/module/memory/module.go
git add docs/doc/codemap/capability-contract/capability_manifest.json
git add docs/doc/codemap/project-map/index/modules.tsv
git add docs/doc/codemap/project-map/index/docs-agent.tsv
git commit -m "refactor: 收束 dashboard 模块读模型端口"
```

Skip any `git add` path above that is absent from `git diff --name-status`; do not replace it with a wildcard.

## Round 1 Integration Gate

Controller merges clean Round 1 branches into `.worktrees/backend-boundary-closure`:

```bash
git status --short --branch
git merge --no-ff codex/backend-boundary-report -m "集成: 收口报告工具端口"
git merge --no-ff codex/backend-boundary-launch-list -m "集成: 收口启动列表工具端口"
git merge --no-ff codex/backend-boundary-dashboard-module -m "集成: 收口 dashboard 模块端口"
```

Then run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/dashboard ./internal/app -count=1
scripts/refresh_generated_artifacts.sh all --check
make guard
git diff --check
```

Dispatch two review agents:

```text
Review 1: architecture boundary truth. Check allowlist reductions, port ownership, interface method size, LSP references, no fake narrow ports.
Review 2: regression and generated artifacts. Check tests, Fx graph, capcontract/project-map/codemap, pushed-state readiness.
```

Round 2 starts only if there are no P0/P1 findings.

## Round 2

### Task C: Send Message Ports

**Owner:** Agent C

**Worktree:** `.worktrees/backend-boundary-send-message`

**Files:**

- Modify: `cmd/mcp-orch/tools/orchestration_send_message.go`
- Modify: `cmd/mcp-orch/tools/orchestration_tools.go` if `HandleSendMessage` still lives there.
- Modify tests: `cmd/mcp-orch/tools/*send*_test.go`, `cmd/mcp-orch/tools/orchestration_report_wait_closure_test.go` if needed.
- Modify: `internal/archtest/orchestration_service_boundary_test.go`

- [ ] **Step 1: Identify exact send-message method set**

Use LSP to read:

```text
submitMessageAndWaitForReport
waitForFollowUpReport
previousFollowUpReportSeq
submitSendMessageTurn
submissionThreadID
```

Expected clusters:

```text
turn submission
agent snapshot/thread lookup
report read/wait
```

- [ ] **Step 2: Define split local ports**

Add local interfaces:

```go
type agentTurnSubmissionPort interface {
	SubmitTurn(ctx context.Context, submission contract.TurnSubmission) error
	Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error)
}

type agentFollowUpReportPort interface {
	GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error)
	RememberReportRequest(ctx context.Context, req contract.RememberReportRequest) (contract.RememberReportRequestResult, error)
}
```

Current follow-up waiting polls `GetReport`; there is no bulk report listing method in `contract.OrchestrationService`. Adjust signatures to exact current service methods only after LSP confirms a contract change.

- [ ] **Step 3: Migrate helper signatures**

Expected: `rg "contract\\.OrchestrationService" cmd/mcp-orch/tools/orchestration_send_message.go` returns no matches or only one transitional adapter with a reduced allowlist reason.

- [ ] **Step 4: Validate wait behavior**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -run 'Test.*Send.*|Test.*FollowUp.*|Test.*ReportWait.*|Test.*Message.*' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Test.*Orchestration.*|Test.*Boundary.*' -count=1
```

- [ ] **Step 5: Commit**

Run generated checks and commit:

```bash
scripts/refresh_generated_artifacts.sh all --check
make capcontract-check
```

If either generated check fails because new local ports changed generated artifacts, refresh explicitly and rerun checks:

```bash
go run ./scripts/capcontract
node scripts/generate_ai_project_map.mjs --filesystem-scan
scripts/refresh_generated_artifacts.sh all --check
git diff --check
```

Commit owned files only:

```bash
git diff --name-status
git add cmd/mcp-orch/tools/orchestration_send_message.go
git add cmd/mcp-orch/tools/orchestration_tools.go
git add internal/archtest/orchestration_service_boundary_test.go
git add docs/doc/codemap/capability-contract/capability_manifest.json
git add cmd/mcp-orch/tools/orchestration_report_wait_closure_test.go
git add cmd/mcp-orch/tools/handler_regression_test.go
git add docs/doc/codemap/project-map/index/orchestration.tsv
git add docs/doc/codemap/project-map/index/docs-agent.tsv
git commit -m "refactor: 收束发送消息工具端口"
```

Skip any `git add` path above that is absent from `git diff --name-status`; do not replace it with a wildcard.

### Task D: DAG Task Tool Ports

**Owner:** Agent D

**Worktree:** `.worktrees/backend-boundary-dag-tools`

**Files:**

- Modify: `cmd/mcp-orch/tools/task_tools.go`
- Modify: `cmd/mcp-orch/tools/task_apply_ops.go`
- Modify: `cmd/mcp-orch/tools/task_lifecycle_helpers.go`
- Modify: `cmd/mcp-orch/tools/task_tool_definitions.go` only if fanout can be narrowed safely.
- Modify: `internal/archtest/orchestration_service_boundary_test.go`

- [ ] **Step 1: Prefer existing DAG runtime**

Inspect:

```text
internal/contract/orchestration.go
contract.DAGRuntime
cmd/mcp-orch/tools/task_tools.go
```

If `contract.DAGRuntime` already has the required DAG methods, use it instead of creating duplicate ports.

- [ ] **Step 2: Define minimal DAG ports only for gaps**

Prefer existing contract ports first:

```go
type dagReadPort interface {
	GetDAG(ctx context.Context, dagKey string) (contract.DAGDetail, error)
	ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error)
	ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error)
	GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error)
}

type dagLifecyclePort interface {
	StartDAG(ctx context.Context, req contract.StartDAGRequest) (contract.StartDAGResponse, error)
	TerminateDAG(ctx context.Context, req contract.TerminateDAGRequest) error
	DeleteDAG(ctx context.Context, req contract.DeleteDAGRequest) error
	ApplyOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error)
}
```

Only define additional local ports for gaps not covered by `contract.DAGRuntime`, `contract.DAGDeleteRuntime`, or `contract.DAGCreateRuntime`:

```go
type dagMutationPort interface {
	CreateDAG(ctx context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error)
	UpdateNodeStatus(ctx context.Context, req contract.UpdateNodeStatusRequest) (contract.DAGNode, error)
	DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
}
```

Do not invent names if the current contract uses different DTOs; follow exact definitions.

- [ ] **Step 3: Migrate handlers in small groups**

Group 1:

```text
HandleGetDAG
HandleListDAGs
HandleListRuns
```

Group 2:

```text
HandleCreateDAG
HandleUpdateNode
HandleDispatchNode
```

Group 3:

```text
HandleStartDAG
HandleTerminateDAG
HandleDeleteDAG
HandleApplyOps
HandleGetRun
```

Stop after Group 1 or 2 if tests become unclear; reduce allowlist instead of forcing a risky full migration.

- [ ] **Step 4: Validate DAG behavior**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -run 'Test.*DAG.*|Test.*Run.*|Test.*Node.*|Test.*ApplyOps.*' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Test.*Orchestration.*|Test.*Boundary.*' -count=1
```

- [ ] **Step 5: Commit**

Commit owned files only:

```bash
git diff --name-status
git add cmd/mcp-orch/tools/task_tools.go
git add cmd/mcp-orch/tools/task_apply_ops.go
git add cmd/mcp-orch/tools/task_lifecycle_helpers.go
git add internal/archtest/orchestration_service_boundary_test.go
git add docs/doc/codemap/capability-contract/capability_manifest.json
git add cmd/mcp-orch/tools/task_tool_definitions.go
git add cmd/mcp-orch/tools/task_tools_test.go
git add cmd/mcp-orch/tools/task_apply_ops_test.go
git add docs/doc/codemap/project-map/index/orchestration.tsv
git add docs/doc/codemap/project-map/index/docs-agent.tsv
git commit -m "refactor: 收束 DAG 工具端口"
```

Skip any `git add` path above that is absent from `git diff --name-status`; do not replace it with a wildcard.

### Task E: Workflow Diagnostics Ports

**Owner:** Agent E

**Worktree:** `.worktrees/backend-boundary-workflow`

**Files:**

- Modify: `cmd/mcp-orch/tools/task_diagnostics.go`
- Modify: `cmd/mcp-orch/tools/workflow_workbench.go`
- Modify: `cmd/mcp-orch/tools/task_tool_definitions.go` if needed.
- Modify: `internal/archtest/orchestration_service_boundary_test.go`

- [ ] **Step 1: Identify diagnostic method clusters**

Use LSP to read:

```text
HandleDiagnoseDAGPromptIdentityGaps
workflowDiagnostics
workflowDiagnosticsByRunKey
workflow recovery action helpers
```

Expected clusters:

```text
DAG/read run lookup
prompt identity diagnostics
workflow recovery/update actions
```

- [ ] **Step 2: Define local diagnostics ports**

Acceptable shapes:

```go
type workflowDiagnosticsPort interface {
	GetDAG(ctx context.Context, dagKey string) (contract.DAGDetail, error)
	GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error)
	ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error)
}

type workflowRecoveryPort interface {
	UpdateNodeStatus(ctx context.Context, req contract.UpdateNodeStatusRequest) (contract.DAGNode, error)
	DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
}
```

Adjust to exact current DTOs and method names.

- [ ] **Step 3: Migrate diagnostics and workbench helpers**

Expected: reduce or delete:

```text
"cmd/mcp-orch/tools/task_diagnostics.go": compat(6, ...)
"cmd/mcp-orch/tools/workflow_workbench.go": compat(7, ...)
```

- [ ] **Step 4: Validate workflow diagnostics**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -run 'Test.*Workflow.*|Test.*Diagnostics.*|Test.*PromptIdentity.*|Test.*Recovery.*' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Test.*Orchestration.*|Test.*Boundary.*' -count=1
```

- [ ] **Step 5: Commit**

Commit owned files only:

```bash
git diff --name-status
git add cmd/mcp-orch/tools/task_diagnostics.go
git add cmd/mcp-orch/tools/workflow_workbench.go
git add internal/archtest/orchestration_service_boundary_test.go
git add docs/doc/codemap/capability-contract/capability_manifest.json
git add cmd/mcp-orch/tools/task_tool_definitions.go
git add cmd/mcp-orch/tools/workflow_workbench_test.go
git add docs/doc/codemap/project-map/index/orchestration.tsv
git add docs/doc/codemap/project-map/index/docs-agent.tsv
git commit -m "refactor: 收束 workflow 诊断端口"
```

Skip any `git add` path above that is absent from `git diff --name-status`; do not replace it with a wildcard.

## Round 2 Integration Gate

Controller merges Round 2:

```bash
git merge --no-ff codex/backend-boundary-send-message -m "集成: 收口发送消息工具端口"
git merge --no-ff codex/backend-boundary-dag-tools -m "集成: 收口 DAG 工具端口"
git merge --no-ff codex/backend-boundary-workflow -m "集成: 收口 workflow 诊断端口"
```

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch ./cmd/mcp-orch/tools ./internal/contract ./internal/module/dashboard ./internal/app ./internal/module/uistate ./internal/module/memory ./internal/platform/mcpcontrol -count=1
scripts/refresh_generated_artifacts.sh all --check
make guard
git diff --check
```

Dispatch the same two review agents again.

## Review Agent 1: Architecture Boundary Review

Prompt:

```text
Review the concrete commit range supplied by the controller, for example `git diff --stat BASE_SHA..HEAD_SHA`. Do not modify code.

Focus:
- Are deleted/reduced allowlist entries backed by real owner-local ports?
- Did any worker create a fake fat interface under a new name?
- Are interfaces defined on the consumer side?
- Did any port cross package/layer boundaries incorrectly?
- Are existing compat entries still explicit, reasoned, and counted?

Use .agents/skills/代码审查维度/SKILL.md and LSP evidence:
grep, structure, inspect, xref, file(read_file), file(diagnostics).

Output findings first with P0/P1/P2. If no findings, state remaining risk.
```

## Review Agent 2: Regression and Generated Artifact Review

Prompt:

```text
Review the concrete commit range supplied by the controller, for example `git diff --stat BASE_SHA..HEAD_SHA`. Do not modify code.

Focus:
- Handler behavior, nil guard behavior, wait/timeout behavior.
- Fx graph closure and provider ambiguity.
- Tests cover the migrated behavior surfaces.
- capcontract, codemap, project-map are all current.
- Remote/main readiness if this is after push.

Run narrow verification before conclusions:
affected go packages, generated artifact checks, git diff --check.

Output findings first with P0/P1/P2. If no findings, state residual test gaps.
```

## Final Main Merge and Push

Only after both review agents report no P0/P1:

```bash
git -C /Users/mima0000/Desktop/wj/super-agent-v3 status --short --branch
git -C /Users/mima0000/Desktop/wj/super-agent-v3 fetch origin main
git -C /Users/mima0000/Desktop/wj/super-agent-v3 merge --ff-only origin/main
git -C /Users/mima0000/Desktop/wj/super-agent-v3 merge --no-ff codex/backend-boundary-closure -m "集成: 合并后端边界收口"
./scripts/test_with_guard.sh ./internal/archtest -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch ./cmd/mcp-orch/tools ./internal/contract ./internal/module/dashboard ./internal/app -count=1
scripts/refresh_generated_artifacts.sh all --check
make guard
git diff --check
git push origin main
```

After push:

```bash
git rev-parse HEAD origin/main
git ls-remote origin refs/heads/main
```

Expected: both local and remote hashes match.

## Score Targets

Expected scoring progression:

```text
Current: 92/100
After Round 1: 93.5-94/100
After Round 2: 95-96/100
```

Do not claim 97+ unless:

```text
1. cmd/mcp-orch/tools has no direct handler-level OrchestrationService consumers.
2. dashboard/uistate/memory/mcpcontrol legacy compat entries are removed or reduced to explicit adapters only.
3. capability manifest, project-map, and codemap are all current.
4. Two independent review agents report no P0/P1/P2 findings.
```

## Stop Conditions

Stop and ask for controller decision if any of these happen:

```text
1. A port needs more than 5 methods.
2. A worker must touch more than two behavior surfaces.
3. Tests require broad fixture rewrites.
4. LSP diagnostics cannot be obtained after narrowing.
5. Generated artifact checks disagree with package tests.
6. A review agent reports P0 or P1.
```

## Execution Options

1. **子代理驱动（推荐）**: 6 implementation agents in two rounds, 2 review agents after each round.
2. **当前会话内执行**: one controller session executes tasks serially with review checkpoints after each task.

Recommended: option 1.
