# Orchestration Boundary 9 Plus Repair Plan

> **Status (2026-07-09 current HEAD): Historical / completed shape. Do not execute this plan as-is.** The core controller split has already landed: `service` is now a facade over `agentRegistry`, `lifecycleController`, `dagController`, `turnController`, and `reportController`; `node_router.go` no longer holds `*service`; `stopSpawnedAgentSink` no longer exists. Pure-AI maintenance must treat the unchecked tasks below as historical implementation notes, not active work. Current follow-up work should be tracked in a new short plan that starts from the present code and current failing gates.

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `cmd/mcp-orch/orchestration` 从“外部边界已硬化、内部仍是大容器”推进到 9/10 以上的内部模块边界。

**Architecture:** 不移动 `cmd/mcp-orch` 的 sidecar 边界，不扩张 `internal/contract`，只在 `orchestration` 包内建立明确 owner。先冻结 `service` 大容器继续长胖，再抽 `agentRegistry` 状态 owner，随后端口化 lifecycle/launcher，最后按 DAG、turn、report 顺序拆控制器。

**Tech Stack:** Go, fx, jrpc2 contract DTOs, taskdag stores, platform state machine, LSP MCP toolchain, `internal/archtest`, guarded Go tests.

**Verification Surface:** LSP `grep/structure/inspect/xref/file/diagnostics`, `./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/archtest -count=1`, `make guard`, `git diff --check`, touched-file diagnostics.

---

## Scope

This plan repairs the internal shape of `cmd/mcp-orch/orchestration`.

It is not the same as `docs/plans/2026-07-09-backend-boundary-closure-agent-plan.md`, which is about remaining external `contract.OrchestrationService` consumers and dashboard/module store ports.

Non-goals:

- Do not move `cmd/mcp-orch/orchestration` into `internal/module`.
- Do not widen `internal/contract.OrchestrationService`.
- Do not create a second stopper interface equivalent to `StopAgentService`.
- Do not change frontend behavior.
- Do not introduce fallback or compatibility branches that hide missing dependencies.

## Current Evidence

| Evidence | Meaning |
| --- | --- |
| `cmd/mcp-orch/orchestration/service.go:63` | `service` is still the internal aggregate. |
| `cmd/mcp-orch/orchestration/service.go:64-86` | `service` directly owns logger, event bus, launcher, DAG stores, persisted thread/binding stores, state-machine config, exit monitor, lock, agent map, suppression map, turn seq, and async lifecycle. |
| `cmd/mcp-orch/orchestration/node_router.go:653-655` | `serviceAgentLauncher` holds `*service`. |
| `cmd/mcp-orch/orchestration/node_router.go:673` | DAG agent launch calls `a.svc.launchAgentSnapshot`. |
| `cmd/mcp-orch/orchestration/node_router.go:695-696` | DAG launch validation reads `a.svc.launcher`. |
| `cmd/mcp-orch/orchestration/node_router.go:709` | DAG launched-thread stop reads `a.svc.agentThreads` and passes full `a.svc`. |
| `cmd/mcp-orch/orchestration/stop_helper.go:28-37` | `AgentThreadLookup` and `StopAgentService` already provide the correct narrow stop ports. |
| `cmd/mcp-orch/orchestration/process_lifecycle.go:81-128` | process exit touches registry state, session cleanup, state transition, events, fallback report, stop reason, and recovery. |
| `cmd/mcp-orch/orchestration/factory.go:335-345` | report fallback persists report files and drains requesters while holding runtime state. |
| `cmd/mcp-orch/orchestration/turn_lifecycle.go:292` | provider turn completion fallback can write report and force idle, so turn/report/process exit are coupled. |
| `internal/archtest/interface_isolation_guard_test.go:55` | Existing guard protects DAG store consumers, but not yet the internal `service` container/controller boundary. |

LSP notes:

- `grep(text_search)` located `type service struct` at `service.go:63`.
- `structure(document_symbol)` showed `service` owns `launcher`, all DAG stores, `agentThreads`, `agentBindings`, `mu`, `agents`, `suppressedStoppedThreads`, `nextTurnSeq`, and async fields.
- `inspect(definition)` on `service` resolves to `service.go:63`; `inspect(hover)` exceeded tool output budget for the struct and must not be reported as a clean hover result.
- `xref(references)` for `service` returned 302 references and was truncated at 50 shown results, confirming the refactor must be staged.
- `file(diagnostics)` on the touched orchestration files currently reports `cmd/mcp-orch/orchestration/stop_helper.go:40` info diagnostic: unused type `stopSpawnedAgentSink`.

## Target Shape

| Owner | Owns | Must Not Own |
| --- | --- | --- |
| `service` facade | fx/RPC method surface, logger/event bus wiring, controller delegation | `mu`, `agents`, DAG stores, launcher implementation details, persisted thread/binding stores, process exit monitor, report requesters |
| `agentRegistry` | `contextlock.RWMutex`, `map[string]*agentRuntime`, identity lookup, rekey, snapshots, `nextTurnSeq`, stopped-hook suppression | launcher calls, store calls, report persistence, DAG mutation |
| `agentLifecycleController` | launch/stop/archive/recover/process exit/runner actor coordination, launcher mode validation, session cleaner, turn starter, process exit monitor | DAG store mutation, report file persistence policy, external `contract.OrchestrationService` expansion |
| `dagController` | DAG/run/dispatch/scheduled stores, node dispatch, terminate DAG, apply ops, DAG agent launch/stop ports | `*service`, direct `agentRuntime` map access, direct `contextlock.RWMutex` |
| `turnController` | submit, complete, interrupt, approval/user input, remote/local turn transitions | report storage internals, DAG store mutation |
| `reportController` | `report_seq`, report requesters, report file persistence/GC, terminal fallback report, runtime-missing report fallback | process wait/kill, DAG store mutation |

Success criteria:

- `service` no longer has fields named `launcher`, `dagStore`, `runStore`, `scheduledStartStore`, `dispatchStore`, `recoveryStore`, `agentThreads`, `agentBindings`, `machineCfg`, `processExitWaitTimeout`, `exitMonitor`, `mu`, `agents`, `suppressedStoppedThreads`, or `nextTurnSeq`.
- `node_router.go` no longer contains `*service`, `svc *service`, `.svc.agentThreads`, `.svc.launcher`, or `.svc.launchAgentSnapshot`.
- Only `agentRegistry` can own `map[string]*agentRuntime` and `contextlock.RWMutex` for runtime agents.
- DAG code depends on `nodeexec.AgentLauncher`, `AgentThreadLookup`, and `StopAgentService`, not on `*service`.
- `StopAgentService` remains the single stop port for `StopAgent(ctx, agentID) error`.
- LSP diagnostics for touched files are zero, or every remaining diagnostic has an explicit blocker with file, line, severity, source, and reason.

## Execution Strategy

Use a dedicated implementation worktree. The first two tasks should be sequential because they affect lock/state ownership. After Task 4 passes, DAG work can run independently from later turn/report exploration, but only one worker should edit `agentRegistry` at a time.

Recommended lane order:

1. Controller lane: baseline, diagnostic cleanup, guard ratchet.
2. Single worker lane: `agentRegistry`.
3. Single worker lane: lifecycle/launcher port extraction.
4. DAG worker lane: `dagController`.
5. Turn worker lane: `turnController`.
6. Report worker lane: `reportController`.
7. Review lane: two read-only agents, one architecture-focused and one behavior/test-focused.

Each lane must keep exact LSP evidence:

```text
grep(text_search or ast_search)
structure(document_symbol or workspace_symbol)
inspect(definition or hover)
xref(references or call_hierarchy)
file(read_file)
file(diagnostics)
```

Shell `rg`, `sed`, and `go test` may supplement but do not replace LSP evidence.

## Task 0: Baseline and Stop Conditions

**Files:**

- Read: `README.md`
- Read: `docs/doc/codemap/README.md`
- Read: `docs/doc/codemap/02-mcp-orch.md`
- Read: `docs/契约/modularity-convention.md`
- Read: `docs/internal-notes/LSP系统提示词.md`
- Read: `cmd/mcp-orch/orchestration/service.go`
- Read: `cmd/mcp-orch/orchestration/node_router.go`
- Read: `cmd/mcp-orch/orchestration/stop_helper.go`
- Read: `internal/archtest/interface_isolation_guard_test.go`

- [ ] **Step 0.1: Verify clean ownership boundary**

Run:

```bash
git status --short
```

Expected: only pre-existing unrelated dirty files may appear. Do not stage or edit unrelated frontend/model registry files.

- [ ] **Step 0.2: Capture source evidence through LSP**

Run LSP:

```text
grep(text_search, query="type service struct", path="cmd/mcp-orch/orchestration", glob="*.go")
structure(document_symbol, file_path="cmd/mcp-orch/orchestration/service.go")
inspect(definition, pos="cmd/mcp-orch/orchestration/service.go:63:11")
xref(references, pos="cmd/mcp-orch/orchestration/service.go:63:11", max_results=50)
file(read_file, pos="cmd/mcp-orch/orchestration/node_router.go:653", limit=90)
file(diagnostics, file_paths=[
  "cmd/mcp-orch/orchestration/service.go",
  "cmd/mcp-orch/orchestration/node_router.go",
  "cmd/mcp-orch/orchestration/stop_helper.go",
  "internal/archtest/interface_isolation_guard_test.go"
])
```

Expected:

```text
service.go:63 defines service
node_router.go:653 serviceAgentLauncher holds *service
stop_helper.go:40 unused stopSpawnedAgentSink diagnostic exists until Task 1
```

- [ ] **Step 0.3: Run current package baseline**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/archtest -count=1
git diff --check
```

Expected: tests pass before refactor. If they fail before any owned edit, stop and record the failing command, package, and first failure.

## Task 1: Remove Stale Stop Helper Diagnostic

**Unique fix:** delete the unused `stopSpawnedAgentSink` type. Do not replace it with another metric abstraction.

**Files:**

- Modify: `cmd/mcp-orch/orchestration/stop_helper.go:39-42`

- [ ] **Step 1.1: Confirm the type is unused**

Run LSP:

```text
xref(references, pos="cmd/mcp-orch/orchestration/stop_helper.go:40:6", include_declaration=true)
file(read_file, pos="cmd/mcp-orch/orchestration/stop_helper.go:28", scope=lines, limit=45)
```

Expected: only the declaration appears for `stopSpawnedAgentSink`; `StopSpawnedAgentMetrics` and `stopSpawnedAgentCounter` remain used.

- [ ] **Step 1.2: Delete only the unused type**

Remove:

```go
// stopSpawnedAgentSink 是 StopSpawnedAgent 指标计数器接口。
type stopSpawnedAgentSink interface {
	Inc(result StopResult)
}
```

- [ ] **Step 1.3: Verify diagnostics and behavior**

Run:

```text
file(diagnostics, file_path="cmd/mcp-orch/orchestration/stop_helper.go")
```

Then:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run 'Test.*Stop.*|Test.*DAG.*' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/archtest -count=1
git diff --check
```

Expected: no `unusedfunc` diagnostic in `stop_helper.go`; guarded tests pass.

## Task 2: Add Internal Boundary Ratchet Guards

**Unique fix:** create a ratchet that prevents new container growth before moving code. The guard starts with explicit current-debt allowances and must be tightened in later tasks.

**Files:**

- Create: `internal/archtest/orchestration_internal_boundary_test.go`
- Modify if shared helper is needed: `internal/archtest/interface_isolation_guard_test.go`

- [ ] **Step 2.1: Add a service-state owner guard**

Create `TestOrchestrationServiceStateOwnershipRatchet` that parses `cmd/mcp-orch/orchestration/service.go` and records these current container fields as debt:

```text
launcher
sessionCleaner
turnStarter
dagStore
runStore
scheduledStartStore
dispatchStore
recoveryStore
agentThreads
agentBindings
machineCfg
processExitWaitTimeout
exitMonitor
mu
agents
suppressedStoppedThreads
nextTurnSeq
asyncCtx
asyncCancel
asyncWg
```

The test must fail if a new field is added to this debt list without explicitly updating the test reason. It must also fail after a field is removed from `service` unless the allowance is deleted in the same commit.

- [ ] **Step 2.2: Add a node-router full-service guard**

Add `TestNodeRouterDoesNotGrowServiceAgentLauncherDebt` with current explicit allowance:

```text
type serviceAgentLauncher struct { svc *service }
a.svc.launchAgentSnapshot
a.svc.launcher
a.svc.agentThreads
StopSpawnedAgent(ctx, a.svc.agentThreads, a.svc, threadID)
```

The test must fail if any additional `.svc.` selector appears in `node_router.go`.

- [ ] **Step 2.3: Add duplicate stopper guard**

Add a guard that scans `cmd/mcp-orch/orchestration/**/*.go` for interfaces with exactly:

```go
StopAgent(ctx context.Context, agentID string) error
```

Allowed names:

```text
StopAgentService
```

If another same-shape interface appears, the test fails and points to `stop_helper.go:35` as the reuse target.

- [ ] **Step 2.4: Validate guard baseline**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run 'TestOrchestration.*Boundary|TestNodeRouter.*Service|Test.*Stopper' -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
git diff --check
```

Expected: tests pass with explicit current-debt allowances. The allowances are not success; they are the ratchet to shrink in later tasks.

## Task 3: Extract `agentRegistry`

**Unique fix:** move runtime-agent state ownership out of `service`. This is the first structural cut because every later controller needs a single state owner.

**Files:**

- Create: `cmd/mcp-orch/orchestration/agent_registry.go`
- Modify: `cmd/mcp-orch/orchestration/service.go`
- Modify: `cmd/mcp-orch/orchestration/helpers.go`
- Modify: `cmd/mcp-orch/orchestration/launch_helpers.go`
- Modify: `cmd/mcp-orch/orchestration/service_launcher_bridge.go`
- Modify: `cmd/mcp-orch/orchestration/process_lifecycle.go`
- Modify: `cmd/mcp-orch/orchestration/persistent_runtime_rehydrate.go`
- Modify: `cmd/mcp-orch/orchestration/recover.go`
- Modify tests that directly construct `&service{agents: ...}`
- Modify: `internal/archtest/orchestration_internal_boundary_test.go`

- [ ] **Step 3.1: Add the registry type**

Create:

```go
type agentRegistry struct {
	mu                       contextlock.RWMutex
	agents                   map[string]*agentRuntime
	suppressedStoppedThreads sync.Map
	nextTurnSeq              int64
}

func newAgentRegistry() *agentRegistry {
	return &agentRegistry{
		agents: make(map[string]*agentRuntime),
	}
}
```

Keep this file package-private. Do not export `agentRuntime` or move it to `internal/contract`.

- [ ] **Step 3.2: Move lock/map helpers first**

Move these service-owned responsibilities onto `agentRegistry` before changing behavior methods:

```text
lookup by agent id
lookup by identity
lookup by launch seq
new agent runtime
list runtime snapshots
withAgentLocked
withAgentReadLocked
withAgentReadLockedByAgentID
turnIDFor
suppressStoppedHookThreadLocked
suppressStoppedHookThreadUntilLocked
stoppedHookThreadSuppressed
```

Use wrappers only as a temporary compilation bridge inside the same task. At task end, remove service wrappers that only expose `s.registry` without adding domain meaning.

- [ ] **Step 3.3: Update service construction**

Change `service` from owning direct state fields to:

```go
registry *agentRegistry
```

The constructor path must initialize `registry: newAgentRegistry()`. Test helpers must do the same. Do not leave nil-registry fallback logic; construction failure should fail fast in tests and runtime.

- [ ] **Step 3.4: Shrink the ratchet**

Remove these allowances from `TestOrchestrationServiceStateOwnershipRatchet`:

```text
mu
agents
suppressedStoppedThreads
nextTurnSeq
```

Add a new guard: only `agent_registry.go` may define `map[string]*agentRuntime` or `contextlock.RWMutex` fields for runtime agents.

- [ ] **Step 3.5: Validate registry extraction**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run 'Test.*Launch.*|Test.*Submit.*|Test.*Runtime.*|Test.*Recover.*|Test.*Hook.*|Test.*Report.*' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/archtest -count=1
git diff --check
```

Expected: no behavior change; only state ownership changes.

## Task 4: Extract Lifecycle and Launcher Ports Before DAG

**Unique fix:** remove `*service` from `node_router.go` before extracting `dagController`. This is the cross-review correction to the original plan.

**Files:**

- Create: `cmd/mcp-orch/orchestration/agent_lifecycle_controller.go`
- Modify: `cmd/mcp-orch/orchestration/service.go`
- Modify: `cmd/mcp-orch/orchestration/service_launcher_bridge.go`
- Modify: `cmd/mcp-orch/orchestration/process_lifecycle.go`
- Modify: `cmd/mcp-orch/orchestration/node_router.go`
- Modify tests around launch/stop/process exit
- Modify: `internal/archtest/orchestration_internal_boundary_test.go`

- [ ] **Step 4.1: Define lifecycle ownership**

Create `agentLifecycleController` to own:

```text
launcher
sessionCleaner
turnStarter
agentThreads
agentBindings
machineCfg
processExitWaitTimeout
exitMonitor
asyncCtx
asyncCancel
asyncWg
```

It depends on:

```text
registry *agentRegistry
eventBus *event.Dispatcher
logger *slog.Logger
report fallback/applier narrow port
```

Do not give it DAG stores.

- [ ] **Step 4.2: Replace `serviceAgentLauncher` with a port-backed adapter**

`node_router.go` must construct an adapter that satisfies `nodeexec.AgentLauncher`, `nodeexec.AgentLauncherWithSpawnRecord`, and `nodeexec.LaunchedThreadStopper` without holding `*service`.

The adapter may hold:

```text
launch snapshot function or lifecycle launch port
launcher mode validator
AgentThreadLookup
StopAgentService
```

It must not hold:

```text
*service
agentRegistry
map[string]*agentRuntime
AgentLauncher concrete local/remote state beyond mode inspection
```

- [ ] **Step 4.3: Reuse existing stop ports**

Keep stop flow through:

```go
StopSpawnedAgent(ctx, threads, stopper, threadID)
```

where `threads` implements `AgentThreadLookup` and `stopper` implements `StopAgentService`.

Do not create a new `agentStopper`, `threadStopper`, or `lifecycleStopper` with the same `StopAgent(ctx, agentID) error` shape.

- [ ] **Step 4.4: Shrink node-router debt allowance**

Remove all `serviceAgentLauncher` debt allowance from `TestNodeRouterDoesNotGrowServiceAgentLauncherDebt`.

The expected steady-state assertion is:

```text
node_router.go contains no "*service"
node_router.go contains no ".svc."
```

- [ ] **Step 4.5: Validate lifecycle extraction**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run 'Test.*Launch.*|Test.*Stop.*|Test.*Process.*|Test.*Interrupt.*|Test.*LocalLauncher.*|Test.*Remote.*' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/archtest -count=1
git diff --check
```

Expected: `node_router.go` no longer depends on full service; launch/stop/process lifecycle tests pass.

## Task 5: Extract `dagController`

**Unique fix:** move DAG orchestration to its own owner after lifecycle ports are available.

**Files:**

- Create: `cmd/mcp-orch/orchestration/dag_controller.go`
- Modify: `cmd/mcp-orch/orchestration/dag.go`
- Modify: `cmd/mcp-orch/orchestration/dag_query.go`
- Modify: `cmd/mcp-orch/orchestration/dag_dispatch.go`
- Modify: `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`
- Modify: `cmd/mcp-orch/orchestration/service.go`
- Modify DAG tests
- Modify: `internal/archtest/orchestration_internal_boundary_test.go`

- [ ] **Step 5.1: Move DAG stores into `dagController`**

`dagController` owns:

```text
dagStore
runStore
scheduledStartStore
dispatchStore
```

It may depend on:

```text
nodeexec.AgentLauncher
AgentThreadLookup
StopAgentService
logger
eventBus
```

It must not depend on:

```text
*service
agentRegistry
contextlock.RWMutex
map[string]*agentRuntime
```

- [ ] **Step 5.2: Move public DAG methods behind service delegation**

`service` keeps the public/RPC-facing methods for compatibility, but each method delegates:

```text
CreateDAG
GetDAG
ListDAGs
UpdateNodeStatus
DeleteDAG
ApplyOps
GetRun
ListRuns
TerminateDAG
DispatchNode
StartDAG
StartScheduledDAG
```

No method should read DAG stores directly from `service` after this task.

- [ ] **Step 5.3: Shrink service field ratchet**

Remove these allowances:

```text
dagStore
runStore
scheduledStartStore
dispatchStore
```

Add a guard that no file named `dag*.go` has a method receiver `func (s *service)` except service-delegation methods retained in `service.go`. If wrappers remain in old files during the task, list each wrapper by exact function name and remove that allowance before task completion.

- [ ] **Step 5.4: Validate DAG extraction**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run 'Test.*DAG.*|Test.*Dispatch.*|Test.*Wakeup.*|Test.*ApplyOps.*|Test.*Downstream.*' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/archtest -count=1
git diff --check
```

Expected: DAG behavior and archtest pass; `dagController` has no route back to full service.

## Task 6: Extract `turnController`

**Unique fix:** move turn submission/completion/user-input transitions after registry and lifecycle are stable, while report remains behind a small applier port.

**Files:**

- Create: `cmd/mcp-orch/orchestration/turn_controller.go`
- Modify: `cmd/mcp-orch/orchestration/service.go`
- Modify: `cmd/mcp-orch/orchestration/helpers.go`
- Modify: `cmd/mcp-orch/orchestration/turn_lifecycle.go`
- Modify: `cmd/mcp-orch/orchestration/service_launcher_bridge.go`
- Modify: `cmd/mcp-orch/orchestration/hook_consumer.go`
- Modify turn/user-input/interrupt tests

- [ ] **Step 6.1: Define narrow report applier**

Before moving turn code, define the smallest package-private report port used by turn completion fallback:

```go
type reportApplier interface {
	applyReportEventLocked(ctx context.Context, agent *agentRuntime, eventType string, data json.RawMessage, report string) (ReportEventResult, error)
}
```

If the implementation no longer requires the locked suffix after registry extraction, rename the method to reflect the new lock owner. Do not widen the port with report listing, persistence, or requester draining methods.

- [ ] **Step 6.2: Move turn behavior**

Move ownership for:

```text
SubmitTurn
CompleteTurn
BindActiveTurnID
markAwaitingUserInput
resolveAwaitingUserInput
interruptTurn
remote turn submit success/failure
local turn queue claim/start/finish
```

The controller depends on `agentRegistry`, lifecycle submit/stop ports, and `reportApplier`.

- [ ] **Step 6.3: Keep process exit out of turn controller**

Do not move `handleProcessExit` into `turnController`. It belongs to lifecycle because it owns launch sequence fencing, process guard cleanup, session cleanup, and recovery decision.

- [ ] **Step 6.4: Validate turn extraction**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run 'Test.*Turn.*|Test.*UserInput.*|Test.*Interrupt.*|Test.*Submit.*|Test.*Remote.*' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/archtest -count=1
git diff --check
```

Expected: turn behavior remains stable; report extraction is still deferred.

## Task 7: Extract `reportController`

**Unique fix:** move report ownership last because report fallback is used by process exit, turn completion, runtime-missing events, and terminal state changes.

**Files:**

- Create: `cmd/mcp-orch/orchestration/report_controller.go`
- Modify: `cmd/mcp-orch/orchestration/report.go`
- Modify: `cmd/mcp-orch/orchestration/factory.go`
- Modify: `cmd/mcp-orch/orchestration/process_lifecycle.go`
- Modify: `cmd/mcp-orch/orchestration/turn_lifecycle.go`
- Modify: `cmd/mcp-orch/orchestration/service.go`
- Modify report/process/turn tests

- [ ] **Step 7.1: Move report state and persistence**

`reportController` owns:

```text
GetReport
RememberReportRequest
HandleReportEvent
runtime-missing report fallback
process-exit fallback report
state-changed fallback report
report_seq update
report requester drain
report file persistence and GC
```

It depends on `agentRegistry`, report store/filesystem dependencies already used in the package, logger, and event bus.

- [ ] **Step 7.2: Preserve lock semantics explicitly**

For every moved method, write down whether it requires registry lock ownership before calling it:

```text
requires write lock
requires read lock
performs its own lock
must be called after unlock
```

Encode this in method names or comments only where the code would otherwise be ambiguous. Do not leave stale `Locked` suffixes on methods that no longer require the caller to hold the lock.

- [ ] **Step 7.3: Validate report extraction**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run 'Test.*Report.*|Test.*Process.*Exit.*|Test.*Turn.*Completion.*|Test.*Runtime.*' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/archtest -count=1
git diff --check
```

Expected: report requesters, persisted fallback reports, terminal report events, and runtime-missing report fallback stay behaviorally identical.

## Task 8: Final Facade Tightening

**Unique fix:** turn `service` into a real facade and delete ratchet allowances instead of leaving documented debt.

**Files:**

- Modify: `cmd/mcp-orch/orchestration/service.go`
- Modify: `internal/archtest/orchestration_internal_boundary_test.go`
- Modify package tests if constructor signatures changed

- [ ] **Step 8.1: Shrink service to controller fields**

The final `service` struct may keep:

```text
logger
eventBus
registry
lifecycle
dags
turns
reports
```

If `logger` or `eventBus` are fully owned by controllers and only used for construction, remove them from `service` too.

- [ ] **Step 8.2: Delete all state-debt allowances**

Remove every current-debt allowance from `TestOrchestrationServiceStateOwnershipRatchet`.

The steady-state guard should assert the forbidden field names are absent, not merely allowed with comments.

- [ ] **Step 8.3: Assert no full-service propagation**

Add final guard assertions:

```text
No controller struct field type is *service.
No adapter struct field type is *service.
node_router.go contains no ".svc." selector.
DAG files contain no direct agent runtime map access.
Only agent_registry.go defines map[string]*agentRuntime fields.
Only stop_helper.go defines StopAgentService.
```

- [ ] **Step 8.4: Run complete validation**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools ./internal/archtest -count=1
make guard
git diff --check
```

Run LSP diagnostics:

```text
file(diagnostics, file_paths=[
  "cmd/mcp-orch/orchestration/service.go",
  "cmd/mcp-orch/orchestration/agent_registry.go",
  "cmd/mcp-orch/orchestration/agent_lifecycle_controller.go",
  "cmd/mcp-orch/orchestration/dag_controller.go",
  "cmd/mcp-orch/orchestration/turn_controller.go",
  "cmd/mcp-orch/orchestration/report_controller.go",
  "cmd/mcp-orch/orchestration/node_router.go",
  "internal/archtest/orchestration_internal_boundary_test.go"
])
```

Expected:

```text
Guarded tests pass.
make guard passes.
git diff --check passes.
LSP diagnostics are zero for touched files.
```

## Review Gate

After Task 8, dispatch two read-only review agents:

1. Architecture reviewer:
   - Verify no controller or adapter holds `*service`.
   - Verify controller ownership matches the target table.
   - Verify no external contract was widened.
2. Behavior reviewer:
   - Verify launch/stop/process exit/turn/report/DAG tests cover the moved behavior.
   - Verify no diagnostics were ignored.
   - Verify `StopAgentService` remains the only stop port.

Both reviewers must include file:line evidence and the exact commands they ran.

## Rollback Rules

Stop the lane immediately if any of these occur:

- A task needs to change `internal/contract.OrchestrationService` to make internal extraction compile.
- A task adds a nil fallback for a missing controller or registry.
- A task introduces another `StopAgent(ctx, agentID string) error` interface.
- A task makes `node_router.go` depend on `agentRegistry` directly.
- A task removes report fallback behavior to simplify extraction.
- LSP diagnostics are non-zero and the worker reports the lane as PASS.

## Expected Score After Completion

Current state: about 8/10 for internal `orchestration` boundaries, despite stronger external backend boundaries.

After this plan:

- External backend boundaries remain at 9/10+.
- Internal `orchestration` package boundary should reach 9/10+ because state, lifecycle, DAG, turn, and report have explicit owners.
- Remaining acceptable debt: `orchestration` is still one sidecar package with shared `agentRuntime` domain type. That is acceptable until a future package-level split has enough evidence to justify the extra API surface.
