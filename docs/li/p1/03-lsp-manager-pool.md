# P1-03：通用 LSP ManagerPool / shard / scoped manager clone

## 目标

把 v3 当前名义上的 `ManagerPool` 改为面向多语言适配的通用 LSP 多 shard pool，使不同 agent/thread/workspace 的 language-server client、diagnostics、bootstrap cache 自动隔离，同时保留 runner-owned recycler 的生命周期设计。

设计边界：这是通用 LSP 基础设施，不是 Go-only `gopls` 改造。`cmd/mcp-lsp/multilsp` 是当前通用 LSP manager 包；除非明确指 Go 语言服务器 binary，否则本文统一用 “LSP manager / language-server client”。真实 `gopls` 只服务 Go/gomod/gosum/gowork，其他语言必须走各自 language server。

## 当前证据

- `cmd/mcp-lsp/multilsp/manager.go:82-99`：manager 持有 `workspaces` 与 diagnostics map。
- `cmd/mcp-lsp/multilsp/manager_lifecycle.go:231-297`：按 workspace key 创建/复用 client。
- `cmd/mcp-lsp/multilsp/pool.go:19-27`：已有 `ManagerPool` 字段，但只包含 primary、leases、recycler。
- `cmd/mcp-lsp/multilsp/pool.go:34-45`：`NewManagerPool(primary, size)` 只绑定 primary。
- `cmd/mcp-lsp/multilsp/pool.go:106-111`：`snapshotManagers` 只返回 primary。
- `cmd/mcp-lsp/multilsp/recycler.go:60-78`：recycler 是 `platformrunner.Runner`，由 root runner group 驱动。
- `cmd/mcp-lsp/runtime.go:60-71`：当前注册 Go/JS/TS/Python/CSS/Rust/Java 多语言 manager。
- `cmd/mcp-lsp/runtime.go:118-144`：`createGenericManager` 对所有语言都调用 `multilsp.NewManager(...)`；本次设计应把新接口、key、pool、recycler 都定义为通用 LSP 语义。

## 借鉴 go-agent-v2

可借鉴：

- `ManagerPool` 内维护 `[]*Manager`。
- agentID 通过 hash 固定到 shard。
- rootURI/workspaceKey 在 shard 内 clone manager。
- recycler 检查 base managers 与 clones。

不可照搬：

- constructor 中启动 goroutine；v3 必须继续由 runner group 管理。
- hidden `__tool_call_meta`；v3 使用 trusted context scope。
- agentID-only key；v3 key 必须从 trusted scope 自动派生，并至少包含 agent/thread/workspace/language。
- 显式 `shared-root mode` 配置；同 root 共享应是 scope 缺失或单 agent 单 root 时的自然退化，再由 recycler 自动收缩资源。

## 数据结构

```go
type ManagerPool struct {
    factory ManagerFactory
    size    int

    shards   []*managerShard
    leases   map[Client]int
    leasesMu sync.Mutex
    recycler *poolRecycler
}

type managerShard struct {
    index int
    base  *manager

    mu     sync.RWMutex
    clones map[string]*pooledManager // managerKey -> pooled manager
}

type pooledManager struct {
    key        string // managerKey = optional scopeKey + workspaceKey
    manager    *manager
    lastUsedAt time.Time
}

type LSPToolScope struct {
    // trusted identity; comes from server-side context only, never from
    // user-controlled request JSON.
    AgentID   string
    ThreadID  string
    Family    string // lsp/orch/ida; ManagerPool only accepts lsp, but key keeps namespace explicit

    // resolved language/workspace target. The registry/tool layer must fill
    // these before calling ForScope; the pool must not guess workspaceKey from
    // identity alone.
    LanguageID            string
    TargetPath            string
    TargetURI             string
    RootKind              string
    WorkspaceRoot         string
    LanguageWorkspaceRoot string
    ProjectRoot           string
    LanguageSpecific      map[string]string
}
```

`workspaceKey` 建议：

```text
language \x00 rootKind \x00 workspaceRoot \x00 languageWorkspaceRoot \x00 projectRoot \x00 languageSpecific
```

`languageSpecific` 按语言填充，不把 Go-only 概念塞进通用 key。例如 Go 可包含 `goWorkPath/moduleRoot`，JS/TS 可包含 `package.json` root 或 `tsconfig` root，Java 可包含项目 root。

`scopeKey` 从 trusted context 自动派生；本 P1 不引入 session 维度，`turnID/callID` 不进入 manager key：

```text
family/clientKind \x00 agentID \x00 threadID
```

最终 manager key：

```text
scopeKey != "" ? scopeKey \x00 workspaceKey : workspaceKey
```

因此不需要单独配置 `shared-root mode`：没有 trusted scope 或只有单 agent 单 root 时自然共享同 root manager；出现多 agent/thread 并发时 scope 自动进入 key，避免活跃索引和 diagnostics 污染。

## API

```go
type ScopedManager struct {
    Manager      Manager
    ResolvedScope ResolvedLSPToolScope // contains ScopeKey/WorkspaceKey/ShardKey/ManagerKey
}

type ResolvedLSPToolScope struct {
    LSPToolScope
    ScopeKey     string
    WorkspaceKey string
    ShardKey     string
    ManagerKey   string
}

func (p *ManagerPool) ForScope(scope LSPToolScope) (ScopedManager, error)
func (p *ManagerPool) SnapshotManagers() []poolManagerSnapshot
```

说明：

- `ForScope` 接收的是已解析 scope；它负责 canonical key 派生、scope/workspace 到 manager 的路由与 activity touch。
- `ForScope` 必须把同一份 canonical `ResolvedLSPToolScope` 返回给 caller；diagnostics/cache/bootstrap 必须复用它，禁止各自重新拼 key。
- registry/tool 层必须先根据 ctx、target file/URI、languageID 解析 `LSPToolScope`，再调用 `ForScope`。
- 现有 per-client lease 继续由 `withPooledClient` 的 acquire/release 保护 active client，避免把 scope lease 与 client lease 混成两套生命周期。

`ForScope` 流程：

1. 计算 workspace key。
2. 从已解析 `LSPToolScope` 计算 scope key；scope 为空则只使用 workspace key。
3. 拼出 manager key。
4. 计算 shard key 与 shard index。
5. clone 不存在则调用 internal `ManagerFactory` 创建 manager；创建失败返回 error。
6. touch manager key 的 `lastUsedAt`。
7. 返回 `ScopedManager{Manager, ResolvedScope}`。

## Shard 策略

默认 shard key：

```text
scopeKey != "" ? scopeKey : workspaceKey
```

shard index：

```text
hash(shardKey) % poolSize
```

自动退化规则：

- trusted scope 为空：同 language/workspace root 共享 manager。
- 单 agent 单 root：只有一个活跃 key，资源效果等价 shared-root。
- 多 agent/thread 并发：scope 自动参与 manager key，隔离活跃 client/index/diagnostics。
- 不要求不同 agent 一定落到不同 shard；hash collision 允许存在，但同 shard 内的 manager key 必须不同，不能共享 clone。
- idle clone 由 recycler 回收；不通过用户配置在 shared/strict 之间切换。

## Manager 创建

当前 `runtime.go:createGenericManager` 直接创建 `multilsp.NewManager`。P1 需要在 `multilsp` 包内抽出面向多语言的 internal manager factory，用于 shard/clone 创建：

```go
type ManagerFactory interface {
    NewManager(language string, workspaceRoot string, options RootOptions) (*manager, error)
}
```

注意：

- `ManagerFactory` 必须定义在 `multilsp` 包内部，允许返回未导出的 `*manager`，以便 recycler/snapshot 访问内部状态；跨包只暴露导出的 `multilsp.Manager` 接口。
- `ClientFactory.NewClient(rootDir, handler)` 仍必须使用 per-call resolved root。
- 不要捕获 mcp-lsp 启动 cwd 作为 language-server subprocess Dir。
- Go route 才使用真实 `gopls` binary；非 Go route 继续使用对应 language server binary。

## Recycler

保留 v3 当前 runner-owned 设计：

- `NewManagerPool` 不启动 goroutine。
- `RecyclerRunner()` 返回 runner。
- `fx` 继续用 `provideLSPBackgroundRunners` 注入 root `group:"runners"`。

扩展 `snapshotManagers`：

- 返回每个 shard base manager。
- 返回每个 shard 的 manager-key clone / scoped workspace clone。
- snapshot 时避免长时间持锁。

回收规则：

- active lease > 0 不回收。
- RSS 超限回收对应 workspace manager。
- idle clone 按 `lastUsedAt + idleTTL` 回收；base shard 可保留最小容量。
- scope 消失后的 clone 可被 idle recycler 回收，自动回落到同 root 最小资源占用；不需要 `shared-root mode` 配置。
- 回收必须 `AdvanceDiagnosticGeneration` 并 close bootstrap coordinator。

## 实现步骤

1. 把 `ManagerPool.primary` 改为 shard 集合。
2. 把 `PoolSizeFromEnv` 保留，但真实创建 N 个 shard。
3. 新增 `ForScope`，从 context scope 自动路由并 touch `lastUsedAt`；不要暴露 shared/strict 模式配置。
4. registry/tool 层新增 scope resolver：从 caller ctx 读取 trusted identity，从 target file/URI 与 languageID 解析 workspace/root/languageSpecific，构造 `LSPToolScope`。
5. registry 调 `ForScope` 获取 `ScopedManager`，而不是直接返回 language singleton；后续 diagnostics/cache/bootstrap 使用 `ScopedManager.ResolvedScope`。
6. registry 的 `Diagnostics` / `WaitDiagnosticsStable` / `BootstrapDocument` / URI grouping 必须传递 caller ctx，并通过同一个 `LSPToolScope` 路由；禁止用 `context.Background()` 重新取 manager。
7. `Diagnostics(ctx, nil)` 只能枚举当前 caller scope 下的 manager-key clones；不能扫全局所有 language singleton / shard。
8. recycler snapshot 扫描所有 shard/clone。
9. close 时关闭全部 shard/clone manager。
10. 新 touched 的 error/log/metric 文案使用通用 LSP 术语；旧兼容观测名如需保留，必须注明兼容原因。

## 测试

- pool size=4，两个不同 agent 多次调用，稳定命中各自 manager key；允许 shard collision，但不能命中同 clone。
- 同 agent 同 workspace 命中同 manager。
- 同 agent 不同 workspace 命中不同 clone。
- trusted scope 为空时，同 language/root 命中同 manager。
- 单 agent 单 root 无需配置 shared mode，idle 后 recycler 收缩到最小 manager 集合。
- Go 文件使用真实 `gopls` binary；JS/TS/Python/CSS/Rust/Java 使用各自 language server binary，但走同一 pool/shard 抽象。
- recycle shard A 不影响 shard B diagnostics。
- active lease 中 client 不被 recycler 回收。
- shard collision 下，agent A 回收/diagnostics 变化不影响同 shard 的 agent B clone。
- pool close 后所有 clone manager close。

## 完成定义

- `AGENT_LSP_POOL_SIZE` 对实际 manager 数量生效。
- `ManagerPool.snapshotManagers()` 不再只返回 primary。
- `diagnostics(all)` 只能扫当前 scope manager，不扫全局所有 shard。
- 不存在用户可选的 `shared-root mode` / `strict-agent mode` 配置；共享和隔离完全由 trusted scope + workspace key + recycler 自动决定。
