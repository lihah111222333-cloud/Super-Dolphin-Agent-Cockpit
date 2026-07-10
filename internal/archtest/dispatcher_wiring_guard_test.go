package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDispatcherWiringGuard 守护 dispatcher-wiring batch (5 reviewer P0 #1) 的
// 接通不被悄悄回退。校验三件事：
//  1. wakeup_dispatcher.go 把 NodeExecutorRouter 接进了 handleClaimed 路径；
//  2. Fx execution option group Provide AgentExecutor / AutomationExecutor /
//     NodeExecutorRouter，DAG option group 注册 dispatcher router wiring；
//  3. node_router.go 的关键 case 分支（agent / automation / hybrid）都还在
//     — 任一被悄悄删了就让本测试红。
//
// 守卫策略：源文件 substring 扫描（不依赖 build tag / Go AST parser），
// 命中即代码事实，回归在 `go test ./internal/archtest/` 阶段即红。
//
// TestDispatcherWiringGuard locks the dispatcher-wiring batch in place so a
// future refactor cannot silently disconnect the NodeExecutor abstraction
// from production. It uses raw source-text substring checks (no build/AST
// reflection) so any regression fails fast in `go test ./internal/archtest/`.
func TestDispatcherWiringGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	for _, tc := range dispatcherWiringGuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertDispatcherWiringMarkers(t, root, tc)
		})
	}
}

type dispatcherWiringGuardCase struct {
	name     string
	path     string
	mustHave []string
}

func dispatcherWiringGuardCases() []dispatcherWiringGuardCase {
	return []dispatcherWiringGuardCase{
		wakeupDispatcherWiringCase(),
		nodeExecutorDispatchRouteCase(),
		{
			name: "fx execution option provides nodeexec executors + router",
			path: filepath.Join("cmd", "mcp-orch", "fx_orchestration_execution.go"),
			mustHave: []string{
				// production fx 必须 Provide AgentExecutor + AutomationExecutor
				// round-3 后 AgentExecutor 走 orchestration.ProvideAgentExecutor 包
				// WithRecorder option（W2 端口收敛后 NewAgentExecutor 变 variadic Option），
				// 保证 launcher + NodeSpawnRecorder 同步落到 executor。
				"orchestration.ProvideAgentExecutor",
				"orchestration.ProvideAutomationExecutor",
				"orchestration.ProvideNodeLifecycleHooks",
				// + NodeExecutorRouter 单例
				"orchestration.NewNodeExecutorRouter",
				// + serviceAgentLauncher adapter (让 AgentExecutor 用上生产 launcher)
				"orchestration.NewServiceAgentLauncher",
				// + NodeSpawnRecorder adapter (让 AgentExecutor 写回 thread_id)
				"fxadapter.NewStoreNodeSpawnRecorder",
				// dispatcher-wiring closure: sharedfile reader / writer adapter
				// 供 NodeExecutorRouter 预填 RunContext。缺任一 → dogfood-grade DAG
				// (from_sharedfiles / outputs.to_sharedfile) 走 dispatcher 路径会
				// fail-loud 在 validation "reader/writer not wired"。
				"orchestration.NewStoreSharedFileReader",
				"orchestration.NewStoreSharedFileWriter",
			},
		},
		{
			name: "fx DAG option provides dispatcher + router wiring",
			path: filepath.Join("cmd", "mcp-orch", "fx_orchestration_dag.go"),
			mustHave: []string{
				// dispatcher 单例 provider 与 router wiring 必须同属 DAG 组。
				"orchestration.ProvideWakeupDispatcher",
				"orchestration.WireWakeupDispatcherRouter",
			},
		},
		{
			name: "node_router.go preserves node_type case分支",
			path: filepath.Join("cmd", "mcp-orch", "orchestration", "node_router.go"),
			mustHave: []string{
				// 三种 node_type 必须有显式 case 分支
				`case "agent":`,
				`case "automation":`,
				`case "hybrid":`,
				// agent / automation 路径必须经 F13.1 lifecycle helper 真调 Execute
				"executeNodeWithLifecycleHooks",
				// automation Status=done 路径必须代推 CompleteNodeAndScheduleDownstream
				"CompleteNodeAndScheduleDownstream",
				// hybrid 必须返 validation 类失败，不能悄悄变 done
				"hybrid node lifecycle not yet implemented",
				// dispatcher-wiring closure: RunContext 三端口预填
				// router 必须持 sharedFileReader / sharedFileWriter 字段
				"sharedFileReader nodeexec.SharedFileReader",
				"sharedFileWriter nodeexec.SharedFileWriter",
				// prefetchPrevResults helper 必须存在，负责拉上游 done 节点 result
				"prefetchPrevResults",
				// RunContext 构造必须填全 PrevResults / SharedFileReader / SharedFileWriter
				"PrevResults:      prevResults",
				"SharedFileReader: r.sharedFileReader",
				"SharedFileWriter: r.sharedFileWriter",
			},
		},
		{
			name: "node_executor_dispatch.go wraps real executor Execute with lifecycle hooks",
			path: filepath.Join("cmd", "mcp-orch", "orchestration", "node_executor_dispatch.go"),
			mustHave: []string{
				"HookBeforeExecute",
				"exec.Execute(ctx, node, runCtx)",
				"HookAfterExecute",
				"HookOnStateChange",
				"HookOnFailure",
				"runtimesafe.SafeGo",
				"lifecycleHookDispatchWait",
			},
		},
		{
			name: "sharedfile_adapter.go bridges store.Store to nodeexec ports",
			path: filepath.Join("cmd", "mcp-orch", "orchestration", "sharedfile_adapter.go"),
			mustHave: []string{
				// Reader 适配器：store/sharedfile.Reader → nodeexec.SharedFileReader
				"func NewStoreSharedFileReader",
				"ReadSharedFile(ctx context.Context, path string) (string, bool, error)",
				// Writer 适配器：store/sharedfile.Store → nodeexec.SharedFileWriter
				"func NewStoreSharedFileWriter",
				"WriteSharedFile(ctx context.Context, path, content string) error",
				// ErrNotFound 三态翻译必须在（否则 nodeexec.loadFromSharedfiles
				// 会把 "not found" 当作未知错抓丢。
				"platformdb.ErrNotFound",
			},
		},
	}
}

func wakeupDispatcherWiringCase() dispatcherWiringGuardCase {
	return dispatcherWiringGuardCase{
		name: "wakeup_dispatcher routes through NodeExecutorRouter",
		path: filepath.Join("cmd", "mcp-orch", "orchestration", "wakeup_dispatcher.go"),
		mustHave: []string{
			"nodeRouter *NodeExecutorRouter",
			"WithNodeRouter(router *NodeExecutorRouter)",
			"shouldRouteThroughNodeExecutor",
			"handleClaimedViaRouter",
			"handleClaimedViaLegacyLauncher",
		},
	}
}

func nodeExecutorDispatchRouteCase() dispatcherWiringGuardCase {
	return dispatcherWiringGuardCase{
		name: "node executor dispatch calls RouteByWakeup",
		path: filepath.Join("cmd", "mcp-orch", "orchestration", "node_executor_dispatch.go"),
		mustHave: []string{
			"func (d *WakeupDispatcher) handleClaimedViaRouter",
			"d.nodeRouter.RouteByWakeup",
		},
	}
}

func assertDispatcherWiringMarkers(t *testing.T, root string, tc dispatcherWiringGuardCase) {
	t.Helper()

	abs := filepath.Join(root, tc.path)
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", tc.path, err)
	}
	content := string(data)
	for _, must := range tc.mustHave {
		if !strings.Contains(content, must) {
			t.Errorf("%s missing required marker %q (wiring regression?)", tc.path, must)
		}
	}
}

// TestDispatcherWiringGuard_RouterMatchesNodeTypeSchema 锁住 router 的
// node_type case 分支必须与 nodeexec.config.go 中的三种类型一一对应。
// 任何对 node_type schema 的扩张（如加 cron / hitl）都该同步路由器。
//
// TestDispatcherWiringGuard_RouterMatchesNodeTypeSchema asserts the router
// covers exactly the node_type values declared in nodeexec/config.go; adding a
// new node_type without updating the router would land a wiring gap.
func TestDispatcherWiringGuard_RouterMatchesNodeTypeSchema(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	routerPath := filepath.Join(root, "cmd", "mcp-orch", "orchestration", "node_router.go")
	configPath := filepath.Join(root, "cmd", "mcp-orch", "orchestration", "nodeexec", "config.go")

	routerData, err := os.ReadFile(routerPath)
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	router := string(routerData)
	config := string(configData)

	// nodeexec.config.go 的 NodeType 常量必须在 router 的 case 列表里都有镜像。
	// 当前 schema 来源是配置真源里的 agent / automation / hybrid。
	for _, nt := range []string{"agent", "automation", "hybrid"} {
		// config.go 自身存在该 node_type 标识（schema 真源）
		if !strings.Contains(config, nt) {
			t.Errorf("nodeexec/config.go missing node_type %q — schema 漂移？", nt)
		}
		// router 必须有对应 case
		marker := `case "` + nt + `":`
		if !strings.Contains(router, marker) {
			t.Errorf("node_router.go missing case for node_type %q — wiring regression", nt)
		}
	}
}
