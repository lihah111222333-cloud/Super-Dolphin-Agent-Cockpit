# mcp-orch Boundary Refactor Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 mcp-orch 的宽 lifecycle contract 和集中 Fx 组合收敛为 workflow port 与可独立验证的 option groups，保持外部行为不变。

**Architecture:** orchestration service 继续是唯一状态拥有者，但只以消费者实际需要的 contract interface 暴露能力。`cmd/mcp-orch` 根 package 保留 Fx 装配所有权，按 lifecycle、DAG、execution、transport 分组；测试直接使用生产 group，避免镜像 bindings。

**Tech Stack:** Go 1.25、go.uber.org/fx、`internal/archtest` 的 Go AST/type/SSA guards、仓库 `test_with_guard.sh`。

**Verification Surface:** `internal/contract`、`cmd/mcp-orch`、`internal/app/dashboard_adapter.go`、`internal/archtest`、LSP diagnostics、`make guard`。

---

## File map

- Modify: `internal/contract/orchestration.go` — 用 workflow ports 替换 `AgentLifecyclePort`。
- Create: `internal/archtest/agent_lifecycle_port_boundary_test.go` — 禁止旧宽 port 回归。
- Modify: `cmd/mcp-orch/tools/registry.go`, `orchestration_tool_definitions.go`, `orchestration_tools.go`, `orchestration_stop_agent_wait.go`, `orchestration_interrupt_agent.go`, `orchestration_recover_agent.go` — 使用 contract workflow ports。
- Modify: `cmd/mcp-orch/runtime.go`, `cmd/mcp-orch/orchestration/rpc.go`, `internal/app/dashboard_adapter.go` — 注入实际消费的端口。
- Create: `cmd/mcp-orch/fx_orchestration_lifecycle.go`, `fx_orchestration_dag.go`, `fx_orchestration_execution.go`, `fx_transport.go` — option groups。
- Modify: `cmd/mcp-orch/fx.go`, `cmd/mcp-orch/fx_test.go` — 只组合 groups；测试复用生产 lifecycle group。
- Modify: `internal/app/orchestration_adapter_ports_test.go` 与受影响 `cmd/mcp-orch/**/*_test.go` — 使 fakes 和断言匹配窄 ports。

### Task 1: Guard and workflow contract ports

**Files:**
- Create: `internal/archtest/agent_lifecycle_port_boundary_test.go`
- Modify: `internal/contract/orchestration.go:131-140`

- [x] **Step 1: Write the failing legacy-port guard.**

```go
func TestContractDoesNotDeclareLegacyAgentLifecyclePort(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(repoRoot(t), "internal/contract/orchestration.go"), nil, 0)
	if err != nil { t.Fatalf("parse orchestration contract: %v", err) }
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE { continue }
		for _, spec := range gen.Specs {
			if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name.Name == "AgentLifecyclePort" {
				t.Fatalf("legacy AgentLifecyclePort must be replaced by workflow ports")
			}
		}
	}
}
```

- [x] **Step 2: Run the guard and verify RED.**

Run: `./scripts/test_with_guard.sh ./internal/archtest -run TestContractDoesNotDeclareLegacyAgentLifecyclePort -count=1`

Expected: FAIL containing `legacy AgentLifecyclePort must be replaced`.

- [x] **Step 3: Add workflow ports beside the legacy contract.**

```go
type AgentLaunchPort interface {
	ListAgents(context.Context) ([]AgentSnapshot, error)
	LaunchAgent(context.Context, LaunchRequest) error
	Snapshot(context.Context, string) (AgentSnapshot, error)
}
type AgentStateReader interface {
	ListAgents(context.Context) ([]AgentSnapshot, error)
	Snapshot(context.Context, string) (AgentSnapshot, error)
	GetState(context.Context, string) (AgentStateResult, error)
}
type AgentStopPort interface { StopAgent(context.Context, string) error }
type AgentStopWaitPort interface { AgentStopPort; ListAgents(context.Context) ([]AgentSnapshot, error); Snapshot(context.Context, string) (AgentSnapshot, error) }
type AgentInterruptPort interface { InterruptAgent(context.Context, string, string) (AgentStateResult, error) }
type AgentRecoveryPort interface { Snapshot(context.Context, string) (AgentSnapshot, error); Recover(context.Context, string) error }
```

- Keep `AgentLifecyclePort` until Tasks 2 and 3 have migrated every consumer and Fx binding. The guard remains RED by design during that transition; no package test may be called green until the legacy interface is removed.

- [x] **Step 4: Check contract diagnostics without claiming GREEN.**

Run LSP `file(diagnostics)` for `internal/contract/orchestration.go`.

Expected: no diagnostics. The legacy-port guard still fails until Task 4.

### Task 2: Migrate consumers to their workflow ports

**Files:**
- Modify: `cmd/mcp-orch/tools/{registry.go,orchestration_tool_definitions.go,orchestration_tools.go,orchestration_stop_agent_wait.go,orchestration_interrupt_agent.go,orchestration_recover_agent.go}`
- Modify: `cmd/mcp-orch/runtime.go`, `cmd/mcp-orch/orchestration/rpc.go`, `internal/app/dashboard_adapter.go`
- Test: affected `cmd/mcp-orch/**/*_test.go`, `internal/app/orchestration_adapter_ports_test.go`

- [x] **Step 1: Change tool-port fields and handlers to exact contracts while the legacy guard remains RED.**

```go
type ToolPorts struct {
	AgentLaunch            contract.AgentLaunchPort
	AgentMessenger         SendMessagePorts
	AgentStopWait          contract.AgentStopWaitPort
	AgentRecovery          contract.AgentRecoveryPort
	AgentInterrupt         contract.AgentInterruptPort
	AgentList              AgentListPorts
	AgentReports           agentReportPort
	DAGCreate              contract.DAGCreateRuntime
	DAGRuntime             contract.DAGRuntime
	DAGDelete              contract.DAGDeleteRuntime
	NodeStatus             taskNodeStatusUpdater
	NodeDispatch           taskNodeDispatcher
	WorkflowDiagnostics    workflowDiagnosticsPort
	WorkflowRecovery       workflowRecoveryPort
	DAGIdentityDiagnostics dagPromptIdentityDiagnosticsPort
}
```

Change only the parameter types of `HandleStopAgent`, `stopAgentResult`, `waitForStopAgentSettlement`, `stopAgentSettlementState`, `stopAgentToolDefinition`, `HandleInterruptAgent` and `HandleRecoverAgent`. Preserve every handler body statement, including the `agentArchiver` assertion, wait timeout validation, tool names, schemas and returned envelopes.

- [x] **Step 2: Replace runtime, RPC and dashboard injections.**

```go
type newRegistryParams struct {
	fx.In
	AgentLaunch contract.AgentLaunchPort
	AgentState contract.AgentStateReader
	AgentStopWait contract.AgentStopWaitPort
	AgentRecovery contract.AgentRecoveryPort
	AgentInterrupt contract.AgentInterruptPort
	AgentReports contract.AgentReportPort
	TurnSubmission contract.TurnSubmissionPort
	DAGCreate contract.DAGCreateRuntime
	DAGRuntime contract.DAGRuntime
	DAGDelete contract.DAGDeleteRuntime
	DAGNodeStatus contract.DAGNodeStatusRuntime
	DAGNodeDispatch contract.DAGNodeDispatchRuntime
	WS workspace.Service
	Prompt promptstore.Store
	BuiltinPrompts contract.BuiltinPromptRegistry
	Command commandcardstore.Store
	SharedFile sharedfilestore.Store
	ModelRegistry modelregistry.Registry
}
type RPCFacadeParams struct {
	fx.In
	Launch contract.AgentLaunchPort
	State contract.AgentStateReader
	Stop contract.AgentStopPort
	Turns contract.TurnSubmissionPort
	Runtime contract.AgentRuntimePort
	Reports contract.AgentReportPort
	DAGCreate contract.DAGCreateRuntime
	DAGRuntime contract.DAGRuntime
	DAGDelete contract.DAGDeleteRuntime
	NodeStatus contract.DAGNodeStatusRuntime
}
```

`submissionFromParams` and `submissionThreadID` receive `AgentStateReader`; dashboard's adapter is backed by `AgentStateReader`; the pre-existing fallback semantics of `submissionThreadID` are not changed in this refactor.

- [x] **Step 3: Run focused behavioral tests, but keep the archtest guard RED until Task 4.**

Run: `./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1`

Expected: PASS with tool schemas and stop/recover/interrupt behavior unchanged.

Run: `./scripts/test_with_guard.sh ./internal/app -run OrchestrationAdapterPorts -count=1`

Expected: PASS.

### Task 3: Split production Fx wiring into option groups

**Files:**
- Create: `cmd/mcp-orch/fx_orchestration_lifecycle.go`, `cmd/mcp-orch/fx_orchestration_dag.go`, `cmd/mcp-orch/fx_orchestration_execution.go`, `cmd/mcp-orch/fx_transport.go`
- Modify: `cmd/mcp-orch/fx.go`, `cmd/mcp-orch/fx_test.go`

- [x] **Step 1: Turn the existing parent-startup test into the RED test.**

In `TestParentFxStartup`, replace the handwritten assembly declaration with:

```go
orchAssembly := orchestrationLifecycleOptions()
```

Run: `./scripts/test_with_guard.sh ./cmd/mcp-orch -run TestParentFxStartup -count=1`

Expected: FAIL to compile because `orchestrationLifecycleOptions` does not yet exist. Keep all existing fake logger, event dispatcher, local launcher, run-store, schedule-store and runtime-locker providers unchanged.

- [x] **Step 2: Move service bindings into `orchestrationLifecycleOptions`.**

```go
func orchestrationLifecycleOptions() fx.Option {
	return fx.Module("orchestration-lifecycle",
		fx.Provide(fx.Annotate(orchestration.ProvideService,
			fx.As(fx.Self()),
			fx.As(new(contract.AgentLaunchPort)),
			fx.As(new(contract.AgentStateReader)),
			fx.As(new(contract.AgentStopPort)),
			fx.As(new(contract.AgentStopWaitPort)),
			fx.As(new(contract.AgentInterruptPort)),
			fx.As(new(contract.AgentRecoveryPort)),
			fx.As(new(contract.AgentReportPort)),
			fx.As(new(contract.TurnSubmissionPort)),
			fx.As(new(contract.DAGCreateRuntime)),
			fx.As(new(contract.DAGRuntime)),
			fx.As(new(contract.DAGDeleteRuntime)),
			fx.As(new(contract.DAGNodeStatusRuntime)),
			fx.As(new(contract.DAGNodeDispatchRuntime)),
			fx.As(new(orchestration.ScheduledDAGStartService)),
			fx.As(new(orchestration.WakeupLauncher)),
			fx.As(new(orchestration.HookConsumerRuntime)),
			fx.As(new(orchestration.HookReportPort)),
			fx.As(new(orchestration.AgentLaunchSnapshotter)),
			fx.As(new(orchestration.StopAgentService)),
			fx.As(new(orchestration.RunnerLifecyclePort)),
			fx.As(new(orchestration.RunnerRuntimePort)),
			fx.As(new(orchestration.TurnLifecyclePort)),
			fx.As(new(orchestration.ApprovalLifecyclePort)),
		)),
		fx.Provide(orchestration.ProvideHookAfterHandler, orchestration.ProvideRPCFacade),
		fx.Invoke(orchestration.RegisterTurnLifecycle, orchestration.RegisterApprovalLifecycle),
	)
}
```

Move, without rewriting, the DAG subscriber/store/runner providers to `orchestrationDAGOptions`, launcher/node-executor providers to `orchestrationExecutionOptions(remoteAddr)`, and bootstrap/stdio/http providers to `orchestrationTransportOptions`. Keep `run()` as the only process lifecycle owner.

- [x] **Step 3: Reduce `buildOrchestrationOptions` to composition only.**

```go
func buildOrchestrationOptions(remoteAddr string) []fx.Option {
	return []fx.Option{
		orchestrationLifecycleOptions(),
		orchestrationDAGOptions(),
		orchestrationExecutionOptions(remoteAddr),
	}
}
```

`run()` calls `orchestrationTransportOptions()` alongside the existing process-level database, workspace and config providers. Do not add `fx.Module` to the business subpackage and do not alter `group:"runners"` tags.

- [x] **Step 4: Run the startup tests.**

Run: `./scripts/test_with_guard.sh ./cmd/mcp-orch -run 'Test(ParentFxStartup|BuildOrchestrationOptionsIncludesScheduledDAGCronRunner)' -count=1`

Expected: PASS.

### Task 3.5: Narrow the taskdag support ports uncovered by SSA

**Files:**
- Modify: `cmd/mcp-orch/store/taskdag/{contract.go,module.go,store_compile_assertions_test.go}`
- Modify: `cmd/mcp-orch/orchestration/{dag_subscriber_module.go,node_router.go,retry_strategy.go,service.go,wakeup_dispatcher.go,wakeupreclaim/reclaimer.go}`

- [x] **Step 1: Replace aggregate `taskdag.Store` consumer edges with exact store ports.**

Added `NodeFlowStore`, `DAGDetailStore`, `WakeupDispatchStore`, and `WakeupReclaimStore` Fx bindings. `WakeupDispatchStore` intentionally retains `DAGDetailStore` because smart retry reads DAG metadata; run-node reading remains the existing optional assertion path.

- [x] **Step 2: Re-run priority SSA without changing its rule or adding a freeze.**

The guard automatically removed 15 no-longer-matching frozen violations from `internal/archtest/freeze_baseline.json`; the remaining 25 records are pre-existing and unrelated.

### Task 4: Remove the legacy contract and close architecture verification

**Files:**
- Modify: `internal/archtest/agent_lifecycle_port_boundary_test.go` only if the RED fixture needs a precise regression assertion.
- Modify: `docs/superpowers/specs/2026-07-10-mcp-orch-boundary-refactor-design.md` status and this plan checklist only after evidence exists.

- [x] **Step 1: Delete `AgentLifecyclePort` and verify the original RED guard turns GREEN.**

Remove the complete legacy interface declaration from `internal/contract/orchestration.go` after `rg -n "AgentLifecyclePort" cmd/mcp-orch internal/app internal/contract` reports no production references.

Run: `./scripts/test_with_guard.sh ./internal/archtest -run TestContractDoesNotDeclareLegacyAgentLifecyclePort -count=1`

Expected: PASS.

- [x] **Step 2: Verify all migrated files with LSP diagnostics.**

Run LSP `file(diagnostics)` on every changed `.go` file.

Expected: no Error, Warning, Information or Hint diagnostics.

- [x] **Step 3: Run architecture and package verification.**

Run: `./scripts/test_with_guard.sh ./internal/archtest -count=1`

Expected: PASS, including sidecar dependency-direction and wide-service guards.

Run: `./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1`

Expected: PASS.

Run: `make guard`

Expected: exit 0 with no newly frozen production or test files.

- [x] **Step 4: Inspect the final diff.**

Run: `git diff --check` and `git diff --stat`.

Expected: no whitespace errors; only the files in this plan change; no frontend, provider, migration, tool-schema or `internal/module/**` file changes.

- [x] **Step 5: Review checkpoint.**

Do not stage, commit, push or delete the worktree unless the user explicitly requests it. Report the exact diff and verification evidence first.

## Follow-up review closure (2026-07-10)

- 两项交叉审查 P2 已关闭：wakeup dispatcher/reclaimer 的 taskdag 端口改为直接方法集，并由 `internal/archtest/taskdag_wakeup_port_boundary_test.go` 防回归。
- `run()` 的完整 Fx 构造已提取为 `newMCPOrchApp(remoteAddr)`；其 smoke 覆盖本地和远端 launcher 分支，并在不启动 app 的情况下解析全部 production modules、option groups、transport 和 `bindRuntime`。
- 新 smoke 发现并修复了 `taskdag.NodeFlowStore` 的重复 Fx binding：移除了 DAG option group 中已被 `taskdag.Module` 取代的转发 provider 注册及其遗留 helper。
- 验证已通过：全 archtest、taskdag/orchestration/mcp-orch 包守卫测试、LSP diagnostics、`go build ./cmd/mcp-orch`、`make guard`、`git diff --check`。
