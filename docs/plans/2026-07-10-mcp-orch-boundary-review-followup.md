# mcp-orch Boundary Review Follow-up Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复两项交叉审查 P2：将 wakeup 存储端口精确收窄，并为完整生产 Fx 图添加本地/远端构建 smoke。

**Architecture:** `WakeupDispatchStore` 直接声明 dispatcher 实际调用的四个 wakeup 操作和 `GetDAG`；可选失败原子写、运行节点读取和 smart-retry 能力继续经显式类型断言保持原错误语义。`WakeupReclaimStore` 只声明过期 wakeup 回收和半写节点恢复。把 `run()` 的 `fx.New` 提取为可测试的 `newMCPOrchApp(remoteAddr)`，使测试解析生产 modules、四个 option groups 和 `bindRuntime` invoke。

**Tech Stack:** Go 1.26、go.uber.org/fx、taskdag ports、`internal/archtest`、`test_with_guard.sh`。

**Verification Surface:** `cmd/mcp-orch/store/taskdag`、`cmd/mcp-orch/orchestration`、`cmd/mcp-orch/fx.go`、`cmd/mcp-orch/fx_test.go`、`internal/archtest`、LSP diagnostics、`go build ./cmd/mcp-orch`、`make guard`。

---

### Task 1: Lock exact wakeup port method sets

**Files:**
- Create: `internal/archtest/taskdag_wakeup_port_boundary_test.go`
- Modify: `cmd/mcp-orch/store/taskdag/contract.go:223-258`
- Modify: `cmd/mcp-orch/store/taskdag/module.go:60-73`
- Modify: `cmd/mcp-orch/store/taskdag/store_compile_assertions_test.go:44-47`

- [x] **Step 1: Write the failing port-boundary guard.**

Parse `cmd/mcp-orch/store/taskdag/contract.go`, find `WakeupDispatchStore` and `WakeupReclaimStore`, and fail unless both interfaces have zero embedded interfaces and exactly these named methods:

```go
map[string][]string{
	"WakeupDispatchStore": {"ClaimDueWakeups", "MarkWakeupSent", "FailWakeup", "RetryWakeup", "GetDAG"},
	"WakeupReclaimStore":  {"ReclaimStaleDispatchingWakeups", "MarkDispatchIncompleteNodesWithoutActiveWakeup"},
}
```

- [x] **Step 2: Verify RED.**

Run `./scripts/test_with_guard.sh ./internal/archtest -run TestTaskDAGWakeupPortsExposeOnlyRequiredMethods -count=1`.

Expected: FAIL because both current ports embed `WakeupStore`.

- [x] **Step 3: Declare the direct methods only.**

```go
type WakeupDispatchStore interface {
	ClaimDueWakeups(context.Context, ClaimDueWakeupsInput) ([]Wakeup, error)
	MarkWakeupSent(context.Context, MarkWakeupSentInput) (int64, error)
	FailWakeup(context.Context, FailWakeupInput) (int64, error)
	RetryWakeup(context.Context, RetryWakeupInput) (int64, error)
	GetDAG(context.Context, string) (*DAG, error)
}

type WakeupReclaimStore interface {
	ReclaimStaleDispatchingWakeups(context.Context) (int64, error)
	MarkDispatchIncompleteNodesWithoutActiveWakeup(context.Context) ([]Node, error)
}
```

Keep `listDispatcherNodesForRun` and `WakeupNodeFailureStore` checks as explicit optional assertions; do not add a broad port, default or fallback.

- [x] **Step 4: Preserve Fx bindings and static implementation proofs.**

Keep `ProvideWakeupDispatchStore(store Store) WakeupDispatchStore { return store }`; keep `ProvideWakeupReclaimStore` as the concrete narrow-interface assertion. Retain both `*store` compile-time assertions.

- [x] **Step 5: Verify GREEN.**

Run `./scripts/test_with_guard.sh ./internal/archtest -run TestTaskDAGWakeupPortsExposeOnlyRequiredMethods -count=1` and `./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration -count=1`.

Expected: PASS with no priority-SSA baseline additions.

### Task 2: Make the production Fx graph testable and smoke both launcher branches

**Files:**
- Modify: `cmd/mcp-orch/fx.go:24-66`
- Modify: `cmd/mcp-orch/fx_test.go`

- [x] **Step 1: Write the failing local/remote graph test.**

```go
func TestNewMCPOrchAppBuildsCompleteGraph(t *testing.T) {
	for _, remoteAddr := range []string{"", "127.0.0.1:65535"} {
		t.Run(strconv.Quote(remoteAddr), func(t *testing.T) {
			if err := newMCPOrchApp(remoteAddr).Err(); err != nil {
				t.Fatalf("production Fx graph (%q): %v", remoteAddr, err)
			}
		})
	}
}
```

The test must not call `Start`, run stdio, connect a remote launcher, or wait on the app; it only resolves production modules, groups, providers and the `bindRuntime` invoke.

- [x] **Step 2: Verify RED.**

Run `./scripts/test_with_guard.sh ./cmd/mcp-orch -run TestNewMCPOrchAppBuildsCompleteGraph -count=1`.

Expected: FAIL to compile because `newMCPOrchApp` does not exist.

- [x] **Step 3: Extract the existing Fx construction verbatim.**

```go
func newMCPOrchApp(remoteAddr string) *fx.App {
	return fx.New(/* current run modules, groups, providers and bindRuntime invoke */)
}
```

Keep `run()` as the only owner of env lookup, `app.Err`, `Start`, `Wait` and `Stop`; it calls `newMCPOrchApp(strings.TrimSpace(os.Getenv("GO_AGENT_CTL_RPC_ADDR")))`. Do not reorder option groups, alter runner tags, or change transport/MCP behavior.

- [x] **Step 4: Verify the smoke and all mcp-orch tests.**

Run `./scripts/test_with_guard.sh ./cmd/mcp-orch -run 'Test(NewMCPOrchAppBuildsCompleteGraph|RunIncludesDBMigrationLifecycleModule)' -count=1`, then `./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1`.

Expected: PASS for both launcher branches and the full package.

### Task 3: Close the review findings

**Files:**
- Modify: `docs/superpowers/specs/2026-07-10-mcp-orch-boundary-refactor-design.md`
- Modify: `docs/plans/2026-07-10-mcp-orch-boundary-refactor.md`
- Modify: this plan checklist after evidence exists.

- [x] **Step 1: Verify changed Go files with LSP diagnostics.**

Expected: no Error, Warning, Information or Hint diagnostics.

- [x] **Step 2: Run final guards.**

Run `./scripts/test_with_guard.sh ./internal/archtest -count=1`, `./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1`, `make guard`, and `git diff --check`.

Expected: all commands exit 0, no new priority-SSA baseline entries, and no unrelated user files change.

- [x] **Step 3: Record the P2 findings as resolved.**

Update the original plan and design with exact evidence. Do not stage, commit, push or delete the worktree without explicit user direction.

## Execution evidence (2026-07-10)

- RED: `TestTaskDAGWakeupPortsExposeOnlyRequiredMethods` failed because both ports embedded `WakeupStore`; `TestNewMCPOrchAppBuildsCompleteGraph` then failed to compile before `newMCPOrchApp` existed.
- GREEN: `WakeupDispatchStore` now has exactly `ClaimDueWakeups`、`MarkWakeupSent`、`FailWakeup`、`RetryWakeup`、`GetDAG`; `WakeupReclaimStore` now has exactly its two direct recovery methods. The reclaimer test fake also implements only that narrow port.
- Full graph smoke builds both `remoteAddr == ""` and `remoteAddr == "127.0.0.1:65535` without starting the app. It uses test bootstrap, a temporary SQLite path and a temporary `mcpStdout` binding, preserving production configuration and stdio fail-fast behavior.
- The smoke exposed a real duplicate `taskdag.NodeFlowStore` Fx binding. The obsolete DAG-option registration and its unused identity provider were removed; `taskdag.Module` is now the single binding owner.
- Passed: LSP diagnostics for all changed Go files; `./scripts/test_with_guard.sh ./internal/archtest -count=1`; `./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration -count=1`; `./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1`; `go build ./cmd/mcp-orch`; `make guard`; `git diff --check`.
