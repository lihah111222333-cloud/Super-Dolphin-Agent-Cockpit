# P2-03：Manager / pool / client 生命周期容错

## 裁决

**MIGRATE IDEAS；DO-NOT-MIGRATE v2 manager 结构。**

v3 P1 的 scope/shard/pool/cache 隔离强于 v2，不应迁回 v2 的 rootURI/agentID 粒度；但 v2 的 client health、restart、idle recycle 思路值得迁移为 v3 原生实现。

本子计划是 generic language-service 计划：dead client rebuild、ReleaseScope、eviction 必须保留 `workspaceConfig.languageID`、`RootOptions.LanguageSpecific`、`WorkspaceKey`，不得默认回退到 Go。Go-only 行为只能存在于 P2-05。

## 当前差距

### P1：dead client 复用后缺少自动 close/restart

v3 `Client` 接口没有 health 方法，见 `internal/sidecar/lsp/multilsp/client.go:27-36`。`Request` 只检查 `ensureOpen()`，见 `internal/sidecar/lsp/multilsp/client.go:158-167`；`ensureOpen()` 只看 shutdown flag，见 `internal/sidecar/lsp/multilsp/client.go:229-236`。`lookupExistingClient()` 会直接返回 workspace client，见 `internal/sidecar/lsp/multilsp/manager_lifecycle.go:304-314`。

v2 有 `Running()` / `CanServeRequests()` / `ensureRunning()`，见 `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/lsp/client.go:179-195`；manager reuse 前会检查 `client.Running()` 并在 start 失败时删除旧 client，见 `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/lsp/manager.go:473-535`。

### P1：agent/thread stop 的 scoped cleanup 缺接口边界

v3 clone 按 `ManagerKey` 存入 shard map，见 `internal/sidecar/lsp/multilsp/pool.go:284-320`。当前测试只能证明 resolver/clone 按 scope 隔离；尚不能证明真实 agent/thread stop/archive lifecycle 会释放 manager clone、diagnostics、bootstrap state、cache index、leases。

`ManagerPool` 在 `cmd/mcp-lsp` 进程内；mcpcontrol/toolbridge 在控制面。P2 必须定义事件如何跨进程送达 mcp-lsp，而不是只写一个进程内方法名。

### P1：scoped peer fallback 应 fail-closed

若 scoped request 找不到原 agent peer，不能 fallback 到其他 agent-bound peer。只允许 fallback 到显式 shared peer，或者返回 no peer。

### P2：clone idle/cap eviction 不足

v3 有 shard 数，但 clone map 可按 `ManagerKey` 增长。P2 应增加 idle TTL / per-shard cap / LRU eviction，并且尊重 active lease。

## v3 已优于 v2的点

- v3 cache key 已包含 scope/workspace/language/URI/hash：`internal/sidecar/lsp/multilsp/cache.go:25-36`。
- v3 active lease recycler race 已有保护：`internal/sidecar/lsp/multilsp/recycler.go` 与 P1 测试 `TestRecyclerDoesNotRecycleActiveLease`。
- v3 manager clone 的 key 来自 trusted `ResolvedLSPToolScope`，不是 v2 的 workspace/language 旧 key。

## 设计

### 1. Client health

给 v3 client 增加最小健康接口：

```go
type HealthCheckedClient interface {
    Client
    Healthy() bool
}
```

实现要求：

- transport read/write error、process exit、Close、Shutdown 后 `Healthy()==false`。
- `lookupExistingClient` 返回前检查 health；dead client 需要 detach 并关闭。
- request/notify 返回 dead-client 类错误时，manager 应 advance generation、detach、重建、restore bootstrap。
- 重建必须保留原 `languageID`、root options、language-specific hash；禁止空 languageID fallback 到 Go。

### 2. Dead-client retry 边界

重建是 manager 状态修复，不等于盲目重放原操作：

- 可自动 retry：idempotent read、bootstrap、diagnostics refresh。
- 不自动 retry：edit、rename、replace_range、可能已部分发送的 DidChange/Notify。
- 不自动 retry 时返回 structured retryable error，由上层重新发起。

### 3. Scoped cleanup 跨进程边界

拆成两层：

```go
// mcp-lsp process-local API
type ReleaseScopeKind string

const (
    ReleaseScopeAgentThread    ReleaseScopeKind = "agent_thread"
    ReleaseScopeAgentAllThreads ReleaseScopeKind = "agent_all_threads"
    ReleaseScopeManagerKey     ReleaseScopeKind = "manager_key"
)

func (p *ManagerPool) ReleaseScope(req ReleaseScopeRequest) (ReleaseScopeResult, error)
func (p *ManagerPool) ReleaseManagerKey(managerKey string) error

type ReleaseScopeRequest struct {
    ScopeKind  ReleaseScopeKind // required; empty is invalid
    AgentID    string
    ThreadID   string // required for agent_thread; empty only valid for agent_all_threads
    ManagerKey string // required for manager_key
    Drain      bool   // true: wait/mark closing; false: fail busy when active lease exists
    Reason     string
}

type ReleaseScopeResult struct {
    MatchedManagers int
    ClosedManagers  int
    BusyLeases      int
    Drained         bool
    ScopeKeys       []string
}
```

控制面事件必须通过唯一明确通道送达 mcp-lsp：新增 mcp-lsp admin tool/method，由 mcpcontrol 在 `AgentStopped` / `ThreadStopped` / archive 后调用。`OnConfigChanged` 只能作为后续补充信号，不能作为 P2 主通道。admin request/response 必须携带 trusted scope 派生结果与 `ScopeKind`，并测试失败/重试语义。

ReleaseScope 必须：

- active lease 存在时不得强关 client；返回 `busy` 或 mark-closing/drain。
- 先从 shard map 移除匹配 clone，再在锁外 close manager/client。
- 清理该 scope diagnostics snapshot、bootstrap state、cache index/tombstone。
- 不影响其他 agent/thread/workspace/languageID。

### 4. Fail-closed peer fallback

路由顺序：

```text
exact agent+thread peer
  -> same agent peer
  -> explicit shared peer
  -> fail no peer
```

shared peer 最小判定：

```text
PeerKind == "shared-service" && ClientKind == requested family && Shared == true
```

空 `AgentID` / `ThreadID` 只表示 metadata 缺失，不得被解释为 shared peer。`PeerKindSharedService` 应新增到 peer registration DTO / registry payload，`Shared bool` 或等价 capability 必须由受信注册链路写入 `ToolInstance`，不得从单次 tool request 的 arguments 推断。禁止 agent A scoped request fallback 到 agent B bound peer；缺 exact/same-agent/explicit-shared 时 fail no peer。

### 5. Idle/cap eviction

- per shard clone cap：默认安全值，允许 env 调整。
- idle TTL：只淘汰无 active lease 且非最近使用 clone。
- eviction 必须从 `shard.clones` 删除 entry。
- base manager 不参与 eviction。
- close manager/client 在锁外执行。
- eviction 默认不清 scoped cache；只有 ReleaseScope、删除文件、tombstone 路径清 cache。

## 实现步骤

1. client/transport 增加 health state。
2. `lookupExistingClient` / `withPooledClient` / request/notify 错误路径接入 dead-client detach。
3. `ManagerPool` 增加 ReleaseScope / ReleaseManagerKey。
4. mcpcontrol/agent lifecycle 事件接入 mcp-lsp admin ReleaseScope 通道，并定义 request/response schema。
5. mcpcontrol fallback 改为 explicit-shared-only fallback，空 identity 一律 fail-closed。
6. clone idle/cap eviction 与 recycler 整合。
7. 增加非 Go fake-client lifecycle 矩阵，证明 TS/Java/Python 等不受 Go fallback 污染。

## 必要测试

- `TestTransportClosedDetachesWorkspaceClientAndRebuilds`
- `TestRequestFailureAdvancesGenerationAndRebootstrap`
- `TestEditFailureAfterDeadClientReturnsRetryableWithoutAutoReplay`
- `TestInitializeFailureDoesNotLeaveStaleWorkspaceClient`
- `TestReleaseScopeClosesOnlyMatchingAgentThreadClone`
- `TestReleaseScopeRespectsActiveLeaseBusyOrDrain`
- `TestReleaseScopeClearsDiagnosticsBootstrapAndCache`
- `TestPeerFallbackRejectsUnrelatedAgentPeer`
- `TestPeerFallbackAllowsExplicitSharedPeerOnly`
- `TestSharedPeerRequiresRegistrySharedFlagAndPeerKind`
- `TestReleaseScopeRejectsEmptyIdentityWithoutExplicitScopeKind`
- `TestAgentStopDispatchesLSPReleaseScopeAdminCall`
- `TestLSPReleaseScopeAdminCallCarriesTrustedScope`
- `TestManagerPoolEvictsOldIdleCloneAtCap`
- `TestManagerPoolDoesNotEvictActiveLeaseClone`
- `TestDeadClientRebuildPreservesTypeScriptWorkspace`
- `TestRecyclerRebuildDoesNotDefaultNonGoLanguageToGo`
- `TestEvictionKeepsJavaWorkspaceKeyAndLanguageSpecificHash`

## 验收命令

```bash
set -euo pipefail
required_tests=(
  './internal/sidecar/lsp/multilsp:TestTransportClosedDetachesWorkspaceClientAndRebuilds'
  './internal/sidecar/lsp/multilsp:TestRequestFailureAdvancesGenerationAndRebootstrap'
  './internal/sidecar/lsp/tools:TestEditFailureAfterDeadClientReturnsRetryableWithoutAutoReplay'
  './internal/sidecar/lsp/multilsp:TestInitializeFailureDoesNotLeaveStaleWorkspaceClient'
  './internal/sidecar/lsp/multilsp:TestReleaseScopeClosesOnlyMatchingAgentThreadClone'
  './internal/sidecar/lsp/multilsp:TestReleaseScopeRespectsActiveLeaseBusyOrDrain'
  './internal/sidecar/lsp/multilsp:TestReleaseScopeClearsDiagnosticsBootstrapAndCache'
  './internal/platform/mcpcontrol:TestPeerFallbackRejectsUnrelatedAgentPeer'
  './internal/platform/mcpcontrol:TestPeerFallbackAllowsExplicitSharedPeerOnly'
  './internal/platform/mcpcontrol:TestSharedPeerRequiresRegistrySharedFlagAndPeerKind'
  './internal/sidecar/lsp/multilsp:TestReleaseScopeRejectsEmptyIdentityWithoutExplicitScopeKind'
  './internal/platform/mcpcontrol:TestAgentStopDispatchesLSPReleaseScopeAdminCall'
  './internal/platform/toolbridge:TestLSPReleaseScopeAdminCallCarriesTrustedScope'
  './internal/sidecar/lsp/multilsp:TestManagerPoolEvictsOldIdleCloneAtCap'
  './internal/sidecar/lsp/multilsp:TestManagerPoolDoesNotEvictActiveLeaseClone'
  './internal/sidecar/lsp/multilsp:TestDeadClientRebuildPreservesTypeScriptWorkspace'
  './internal/sidecar/lsp/multilsp:TestRecyclerRebuildDoesNotDefaultNonGoLanguageToGo'
  './internal/sidecar/lsp/multilsp:TestEvictionKeepsJavaWorkspaceKeyAndLanguageSpecificHash'
)
run_required_test() {
  local pkg="$1"
  local test_name="$2"
  rg -n "func ${test_name}\(" "$pkg" --glob '*_test.go' >/dev/null
  if ! list_output="$(./scripts/test_with_guard.sh "$pkg" -list "^${test_name}$" 2>&1)"; then
    printf '%s\n' "$list_output"
    echo "P2 gate failed: go test -list failed for ${pkg}:${test_name}" >&2
    exit 1
  fi
  printf '%s\n' "$list_output"
  if ! printf '%s\n' "$list_output" | rg "^${test_name}$" >/dev/null; then
    echo "P2 gate failed: required test not in test binary: ${pkg}:${test_name}" >&2
    exit 1
  fi
  if ! output="$(./scripts/test_with_guard.sh "$pkg" -run "^${test_name}$" -count=1 -v 2>&1)"; then
    printf '%s\n' "$output"
    echo "P2 gate failed: test command failed for ${pkg}:${test_name}" >&2
    exit 1
  fi
  printf '%s\n' "$output"
  if printf '%s\n' "$output" | rg -i '\[?no tests to run\]?' >/dev/null; then
    echo "P2 gate failed: [no tests to run] for ${pkg}:${test_name}" >&2
    exit 1
  fi
  if printf '%s\n' "$output" | rg "^--- SKIP: ${test_name}(\b|/)" >/dev/null; then
    echo "P2 gate failed: required test skipped: ${pkg}:${test_name}" >&2
    exit 1
  fi
}
for item in "${required_tests[@]}"; do
  pkg="${item%%:*}"
  test_name="${item##*:}"
  run_required_test "$pkg" "$test_name"
done
```
