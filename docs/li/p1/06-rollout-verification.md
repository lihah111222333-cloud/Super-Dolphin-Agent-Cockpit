# P1-06：验证、灰度与回滚

## 当前开关事实

当前 `main@b19d2f94` 没有 `AGENT_LSP_MULTI_AGENT` 或
`AGENT_LSP_GO_WORK` 运行时 gate：scoped manager 与 Go root resolver 已是当前
代码路径的一部分，验收文档不得要求设置或回滚这两个不存在的开关。

当前仍存在并可用于验收/调试的 LSP 相关环境变量：

```text
AGENT_LSP_POOL_SIZE             # 控制 ManagerPool shard 数
AGENT_LSP_CACHE_PERSISTENT=0    # 默认不持久化 scoped LSP cache
AGENT_LSP_CACHE_DIR             # persistent cache 目录，仅在开启 persistent 时使用
AGENT_LSP_RSS_LIMIT_MB          # recycler RSS 阈值
```

不设置 `AGENT_LSP_SCOPE_STRICT`：LSP 隔离统一从 trusted scope 自动派生，不提供
`agent/pool/shared` 运行时模式。

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

### Wave 5：端到端 / cross-scope regression

Wave 5 只承认以下脚本级验收：先用 `rg` 证明每个 E2E / cross-scope test 函数存在，
再运行对应 `go test`。其中 `cmd/mcp-lsp/manager/go_work_e2e_test.go` 带
`lsp_integration` build tag，因此执行命令必须包含 `-tags lsp_integration`。任一
`rg` 失败即 `BLOCKED`，不得继续把 `go test` 结果记为 PASS；任何
`[no tests to run]` 输出都必须按失败处理。

```bash
set -euo pipefail

wave5_tests=(
  'cmd/mcp-lsp/tools:TestTwoAgentsSameRepoNoDiagnosticLeak'
  'cmd/mcp-lsp/tools:TestAgentStopCleansScopeWithoutKillingOtherAgent'
  'cmd/mcp-lsp/manager:TestTwoWorktreesNoWorkspaceKeyCollision'
  'cmd/mcp-lsp/manager:TestGoWorkMultiModuleDiagnostics'
  'cmd/mcp-lsp/multilsp:TestRecyclerDoesNotRecycleActiveLease'
)

for item in "${wave5_tests[@]}"; do
  pkg="${item%%:*}"
  test="${item##*:}"
  rg -n "func ${test}\\(" "$pkg" --glob '*_test.go' >/dev/null
done

output="$(
  go test -tags lsp_integration ./cmd/mcp-lsp/tools ./cmd/mcp-lsp/manager ./cmd/mcp-lsp/multilsp \
    -run 'TestTwoAgentsSameRepoNoDiagnosticLeak|TestAgentStopCleansScopeWithoutKillingOtherAgent|TestTwoWorktreesNoWorkspaceKeyCollision|TestGoWorkMultiModuleDiagnostics|TestRecyclerDoesNotRecycleActiveLease' \
    -count=1 2>&1
)"
printf '%s\n' "$output"
if printf '%s\n' "$output" | rg '\[no tests to run\]'; then
  echo 'P1 Wave 5 failed: go test reported [no tests to run]' >&2
  exit 1
fi
```

断言：

- Agent A 与 Agent B 同 repo / 同 URI 不串 diagnostics。
- 两个 worktree 使用不同 workspace key，不共享 gopls client/cache/bootstrap。
- `go.work` multi-module diagnostics 返回当前 caller scope 的结果。
- agent stop 清理当前 agent scope，不杀其他 agent scope。
- recycler 遇到 active lease 时不回收正在使用的 client。

## 回滚策略

- scoped manager 与 Go root resolver 当前没有 env gate；回滚必须通过代码回滚/后续补丁
  实现，不得写成 `AGENT_LSP_MULTI_AGENT=0` 或 `AGENT_LSP_GO_WORK=0`。
- scoped cache 默认不持久化，降低回滚污染。
- HTTP MCP 不在本 P1 中删除，因此无需 HTTP 删除回滚策略。

## 必要测试清单

### 当前 required exact tests

以下是当前 main 的必要测试清单。执行验收前必须用 `rg 'func TestName'` 证明每个测试函数
存在，不能只依赖 `go test -run` 的空跑成功。

| 包/目录 | 必要测试 |
| --- | --- |
| `internal/platform/toolbridge` | `TestToolBridgeSelectsPeerByScope` |
| `internal/platform/toolbridge` | `TestToolBridgeAmbiguousWithoutScope` |
| `cmd/mcp-lsp` | `TestLSPOnToolsCallInjectsScopeContext` |
| `cmd/mcp-lsp/multilsp` | `TestManagerPoolForScopeStableShard` |
| `cmd/mcp-lsp/multilsp` | `TestManagerPoolWorkspaceCloneIsolation` |
| `cmd/mcp-lsp/multilsp` | `TestDiagnosticsDropsOldGeneration` |
| `cmd/mcp-lsp/multilsp` | `TestDiagnosticsRefreshesStaleFileBeforeReturn` |
| `cmd/mcp-lsp/multilsp` | `TestDiagnosticsClearsDeletedFile` |
| `cmd/mcp-lsp/manager` | `TestDiagnosticsAllUsesCallerScopeOnly` |
| `cmd/mcp-lsp/manager` | `TestRegistryGroupURIsUsesCallerContext` |
| `cmd/mcp-lsp/multilsp` | `TestDeletedFileClearsBootstrapAndCache` |
| `cmd/mcp-lsp/multilsp` | `TestGoRootResolverGoWork` |
| `cmd/mcp-lsp/multilsp` | `TestGoRootResolverSingleSubmodule` |
| `cmd/mcp-lsp/multilsp` | `TestGoRootResolverMultiModule` |
| `cmd/mcp-lsp/multilsp` | `TestGoRootResolverGOWORKOff` |
| `cmd/mcp-lsp/tools` | `TestTwoAgentsSameRepoNoDiagnosticLeak` |
| `cmd/mcp-lsp/manager` | `TestTwoWorktreesNoWorkspaceKeyCollision` |
| `cmd/mcp-lsp/manager` | `TestGoWorkMultiModuleDiagnostics` |
| `cmd/mcp-lsp/multilsp` | `TestRecyclerDoesNotRecycleActiveLease` |
| `cmd/mcp-lsp/tools` | `TestAgentStopCleansScopeWithoutKillingOtherAgent` |

可复制的存在性检查：

```bash
set -euo pipefail

required_tests=(
  'internal/platform/toolbridge:TestToolBridgeSelectsPeerByScope'
  'internal/platform/toolbridge:TestToolBridgeAmbiguousWithoutScope'
  'cmd/mcp-lsp:TestLSPOnToolsCallInjectsScopeContext'
  'cmd/mcp-lsp/multilsp:TestManagerPoolForScopeStableShard'
  'cmd/mcp-lsp/multilsp:TestManagerPoolWorkspaceCloneIsolation'
  'cmd/mcp-lsp/multilsp:TestDiagnosticsDropsOldGeneration'
  'cmd/mcp-lsp/multilsp:TestDiagnosticsRefreshesStaleFileBeforeReturn'
  'cmd/mcp-lsp/multilsp:TestDiagnosticsClearsDeletedFile'
  'cmd/mcp-lsp/manager:TestDiagnosticsAllUsesCallerScopeOnly'
  'cmd/mcp-lsp/manager:TestRegistryGroupURIsUsesCallerContext'
  'cmd/mcp-lsp/multilsp:TestDeletedFileClearsBootstrapAndCache'
  'cmd/mcp-lsp/multilsp:TestGoRootResolverGoWork'
  'cmd/mcp-lsp/multilsp:TestGoRootResolverSingleSubmodule'
  'cmd/mcp-lsp/multilsp:TestGoRootResolverMultiModule'
  'cmd/mcp-lsp/multilsp:TestGoRootResolverGOWORKOff'
  'cmd/mcp-lsp/tools:TestTwoAgentsSameRepoNoDiagnosticLeak'
  'cmd/mcp-lsp/manager:TestTwoWorktreesNoWorkspaceKeyCollision'
  'cmd/mcp-lsp/manager:TestGoWorkMultiModuleDiagnostics'
  'cmd/mcp-lsp/multilsp:TestRecyclerDoesNotRecycleActiveLease'
  'cmd/mcp-lsp/tools:TestAgentStopCleansScopeWithoutKillingOtherAgent'
)

for item in "${required_tests[@]}"; do
  pkg="${item%%:*}"
  test="${item##*:}"
  rg -n "func ${test}\\(" "$pkg" --glob '*_test.go' >/dev/null
done
```

## 完成定义

- 所有新增/调整测试存在且通过；验收命令不得出现 `[no tests to run]` 后仍判 PASS。
- 多 agent LSP 走 trusted scope 自动派生，不依赖 HTTP 删除。
- 现有 HTTP MCP 兼容路径继续通过原有测试。
- 文档、代码、测试对 scope key、workspace root、diagnostics stale 行为描述一致。
