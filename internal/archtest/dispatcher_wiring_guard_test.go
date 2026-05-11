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
//  2. fx.go 同时 Provide AgentExecutor / AutomationExecutor / NodeExecutorRouter
//     + WireWakeupDispatcherRouter invoke；
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

	cases := []struct {
		name     string
		path     string
		mustHave []string
	}{
		{
			name: "wakeup_dispatcher routes through NodeExecutorRouter",
			path: filepath.Join("cmd", "mcp-orch", "orchestration", "wakeup_dispatcher.go"),
			mustHave: []string{
				// dispatcher struct 必须持有 NodeExecutorRouter 字段
				"nodeRouter *NodeExecutorRouter",
				// 必须有 setter 让 fx invoke 接线
				"WithNodeRouter(router *NodeExecutorRouter)",
				// handleClaimed 必须按 shouldRouteThroughNodeExecutor 路由
				"shouldRouteThroughNodeExecutor",
				"handleClaimedViaRouter",
				"handleClaimedViaLegacyLauncher",
				// 通过 router 路径必须真正调用 RouteByWakeup
				"d.nodeRouter.RouteByWakeup",
			},
		},
		{
			name: "fx.go provides nodeexec executors + router wiring",
			path: filepath.Join("cmd", "mcp-orch", "fx.go"),
			mustHave: []string{
				// production fx 必须 Provide AgentExecutor + AutomationExecutor
				"nodeexec.NewAgentExecutor",
				"nodeexec.NewAutomationExecutor",
				// + NodeExecutorRouter 单例
				"orchestration.NewNodeExecutorRouter",
				// + serviceAgentLauncher adapter (让 AgentExecutor 用上生产 launcher)
				"orchestration.NewServiceAgentLauncher",
				// + NodeSpawnRecorder adapter (让 AgentExecutor 写回 thread_id)
				"orchestration.NewStoreNodeSpawnRecorder",
				// dispatcher 单例 provider 必须存在
				"orchestration.ProvideWakeupDispatcher",
				// 装接 router 的 invoke 必须 wire 在 root assembly
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
				// agent 路径必须真调 AgentExecutor.Execute
				"r.agentExec.Execute(",
				// automation 路径必须真调 AutomationExecutor.Execute
				"r.autoExec.Execute(",
				// automation Status=done 路径必须代推 CompleteNodeAndScheduleDownstream
				"CompleteNodeAndScheduleDownstream",
				// hybrid 必须返 validation 类失败，不能悄悄变 done
				"hybrid node lifecycle not yet implemented",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
		})
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
	// 当前 schema：agent / automation / hybrid（蓝图 v2 §1.1）。
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
