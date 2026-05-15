# P1-06：验证、灰度与回滚

## Feature flags

建议分阶段引入：

```text
AGENT_LSP_MULTI_AGENT=1         # 开启 scoped LSP manager
AGENT_LSP_GO_WORK=1             # 开启 go.work resolver
AGENT_LSP_CACHE_PERSISTENT=0    # 默认不持久化 scoped LSP cache
```

不设置 `AGENT_LSP_SCOPE_STRICT`：LSP 隔离统一从 trusted scope 自动派生，不提供 `agent/pool/shared` 运行时模式。

不设置 `AGENT_LSP_HTTP_MCP`：HTTP MCP 兼容路径在 P1 中保留，不作为本轮删除/回滚开关。

## 验证顺序

### Wave 0：协议边界与 HTTP 兼容基线

命令建议：

```bash
go test ./cmd/mcp-lsp ./cmd/mcp-orch ./internal/mcpserver/common ./internal/provider/claudecli ./internal/provider/unified ./internal/contract
go test ./internal/platform/toolbridge ./internal/platform/mcpcontrol ./internal/module/turn ./internal/archtest
```

验收：

- 现有 HTTP MCP runner/proxy/discovery/manifest 测试继续按当前兼容语义通过。
- P1 文档和测试不要求删除 HTTP MCP，也不要求 manifest stdio-only。
- 非 MCP HTTP 测试/代码保留。

### Wave 1：scope routing

命令建议：

```bash
go test ./internal/platform/toolbridge ./internal/platform/mcpcontrol ./internal/mcpserver/common ./cmd/mcp-lsp ./cmd/mcp-orch
```

验收：

- 多 active peer 下有 `agentID/threadID` scope 能选中，无 scope 报 ambiguous。
- `_agentId/_threadId/_callId/_cwd` 能进入 mcp-lsp context，并被解析为 `agentID/threadID/callID/cwd` trusted scope。
- peer routing 不依赖 `PoolKey/ShardKey`，也不依赖 session 注册维度。

### Wave 2：ManagerPool

命令建议：

```bash
go test ./cmd/mcp-lsp/multilsp ./cmd/mcp-lsp/manager ./cmd/mcp-lsp/tools
```

验收：

- `AGENT_LSP_POOL_SIZE` 创建多个 shard。
- 不存在 `AGENT_LSP_SCOPE_STRICT`；共享/隔离完全由 trusted scope + workspace key 自动派生。
- 同 agent/workspace 复用同 manager。
- 不同 workspace 使用 clone。
- recycler 扫描所有 shard/clone。

### Wave 3：diagnostics/cache/bootstrap

命令建议：

```bash
go test ./cmd/mcp-lsp/multilsp ./cmd/mcp-lsp/tools -run 'Diagnostics|Bootstrap|Cache|Recycler'
```

验收：

- old generation publish ignored。
- stale file refresh。
- deleted file diagnostics/bootstrap/cache cleared。
- diagnostics(all) 不跨 scope。

### Wave 4：Go root resolver

命令建议：

```bash
rg 'func Test.*(GoRoot|GoWork|WorkspaceFolder|GoMod)' cmd/mcp-lsp/multilsp
go test ./cmd/mcp-lsp/multilsp -run 'GoRoot|GoWork|WorkspaceFolder|GoMod'
```

验收：

- `rg` 必须命中真实测试函数，禁止 `[no tests to run]` 空跑绿灯。
- `go.work`、单子模块、多子模块、nested module、linked worktree 均有测试。

### Wave 5：端到端

必须构造两个临时 agent / 两个 worktree，并把 fixture 路径显式传给 integration test 或脚本：

```bash
#!/usr/bin/env bash
set -euo pipefail

tmp="$(mktemp -d)"
cleanup() {
  git worktree remove "$tmp/wt-a" --force >/dev/null 2>&1 || true
  git worktree remove "$tmp/wt-b" --force >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

git worktree add --detach "$tmp/wt-a" HEAD
git worktree add --detach "$tmp/wt-b" HEAD

rg 'func (TestTwoAgentsSameRepoNoDiagnosticLeak|TestTwoWorktreesNoWorkspaceKeyCollision|TestAgentStopCleansScopeWithoutKillingOtherAgent)' cmd/mcp-lsp/tools cmd/mcp-lsp/manager

AGENT_LSP_E2E_WORKTREE_A="$tmp/wt-a" \
AGENT_LSP_E2E_WORKTREE_B="$tmp/wt-b" \
AGENT_LSP_E2E_AGENT_A="agent-p1-a" \
AGENT_LSP_E2E_AGENT_B="agent-p1-b" \
go test ./cmd/mcp-lsp/tools ./cmd/mcp-lsp/manager -run 'TestTwoAgentsSameRepoNoDiagnosticLeak|TestTwoWorktreesNoWorkspaceKeyCollision|TestAgentStopCleansScopeWithoutKillingOtherAgent'
```

断言：

- Agent A 与 Agent B 同时调用 `lsp_file diagnostics`。
- A 修改文件后 B 的 diagnostics 不返回 A 的旧状态。
- B 在另一个 worktree 中 `definition/references/diagnostics` 正常。
- `diagnostics(all)` 在 A/B 两个 scope 中返回的 URI 集合互不包含对方 manager-key clone。

## 回滚策略

- scoped manager 可通过 `AGENT_LSP_MULTI_AGENT=0` 回到 legacy singleton。
- go.work resolver 可通过 `AGENT_LSP_GO_WORK=0` 回到 `go.mod` only。
- scoped cache 默认不持久化，降低回滚污染。
- HTTP MCP 不在本 P1 中删除，因此无需 HTTP 删除回滚策略。

## 必要测试清单

### Unit

以下是必须新增或保留的测试；执行验收前必须用 `rg 'func TestName'` 证明测试函数存在，不能只依赖 `go test -run` 的空跑成功。

- `TestToolBridgeSelectsPeerByScope`
- `TestToolBridgeAmbiguousWithoutScope`
- `TestLSPOnToolsCallInjectsScopeContext`
- `TestManagerPoolForScopeStableShard`
- `TestManagerPoolWorkspaceCloneIsolation`
- `TestDiagnosticsDropsOldGeneration`
- `TestDiagnosticsRefreshesStaleFileBeforeReturn`
- `TestDiagnosticsClearsDeletedFile`
- `TestDiagnosticsAllUsesCallerScopeOnly`
- `TestRegistryGroupURIsUsesCallerContext`
- `TestDeletedFileClearsBootstrapAndCache`
- `TestGoRootResolverGoWork`
- `TestGoRootResolverSingleSubmodule`
- `TestGoRootResolverMultiModule`
- `TestGoRootResolverGOWORKOff`

### Integration

以下 integration 测试当前是 P1 计划要求；如果实现前尚不存在，报告必须标为 BLOCKED，不得把 E2E 命令作为已通过证据。

- `TestTwoAgentsSameRepoNoDiagnosticLeak`
- `TestTwoWorktreesNoWorkspaceKeyCollision`
- `TestGoWorkMultiModuleDiagnostics`
- `TestRecyclerDoesNotRecycleActiveLease`
- `TestAgentStopCleansScopeWithoutKillingOtherAgent`

## 完成定义

- 所有新增/调整测试存在且通过；验收命令不得出现 `[no tests to run]` 后仍判 PASS。
- 多 agent LSP 走 trusted scope 自动派生，不依赖 HTTP 删除。
- 现有 HTTP MCP 兼容路径继续通过原有测试。
- 文档、代码、测试对 scope key、workspace root、diagnostics stale 行为描述一致。
