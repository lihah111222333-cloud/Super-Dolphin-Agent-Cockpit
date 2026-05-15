# P1-04：diagnostics stale / cache / bootstrap 生命周期

## 目标

让 diagnostics、document bootstrap、LSP cache 在多 agent、多 workspace、多 shard 场景下可隔离、可失效、可恢复，避免旧 diagnostics/cache 从旧 agent、旧 workspace、旧 language-server generation 串到新请求。

## 当前证据

- `cmd/mcp-lsp/multilsp/manager.go:108-111`：diagnostic snapshot 只有 `params/generation/updatedAt`。
- `cmd/mcp-lsp/multilsp/manager_diagnostics.go:72-78`：`AdvanceDiagnosticGeneration` 会增 generation 并清 diagnostics。
- `cmd/mcp-lsp/multilsp/manager_diagnostics.go:84-95`：旧 generation diagnostics 会被丢弃。
- `cmd/mcp-lsp/multilsp/state.go:9-17`：bootstrap 有 `pending/bootstrapping/ready/stale/error`。
- `cmd/mcp-lsp/multilsp/state.go:97-105`：fingerprint 变化时 bootstrap state 标 stale。
- `cmd/mcp-lsp/multilsp/cache.go:25-29`：cache key 当前为 `Workspace/Language/URI`，缺少 agent/shard/go.work/module 维度。
- `cmd/mcp-lsp/tools/tool_diagnostics.go:29-55`：已有 diagnostics 时会直接返回，不强制校验磁盘 stale。

## 状态机

```text
Absent
  -> Bootstrapping
  -> ReadyFresh
  -> StaleOnDisk
  -> DirtyInMemory
  -> Deleted
  -> Closing
  -> Closed
  -> Error
```

状态含义：

- `ReadyFresh`：fingerprint/mtime/size 与磁盘一致。
- `StaleOnDisk`：磁盘文件变化，LSP document 尚未同步。
- `DirtyInMemory`：工具内存编辑已发送 DidChange，但磁盘/formatter 尚未完成稳定验证。
- `Deleted`：磁盘文件删除，diagnostics、bootstrap document state、document cache record 必须同步清空；如有持久 cache，只保留短 TTL tombstone 防止旧 cache 复活。
- `Closing/Closed`：manager/recycle/agent stop 清理阶段。

## Snapshot 模型

```go
type DiagnosticSnapshot struct {
    ScopeKey    string
    WorkspaceKey string
    Language    string
    URI         string
    Generation  uint64
    DocVersion  int
    Fingerprint string
    MtimeNS     int64
    Size        int64
    UpdatedAt   time.Time
    Source      string // publish, reactive_bootstrap, restored
    State       DiagnosticState
    Params      protocol.PublishDiagnosticsParams
}
```

## Cache key

新增 URI -> last resolved key 索引，用于 marker 文件删除后的旧 key 清理：

```go
type lspDocumentIndexKey struct {
    URI string
}

type lspDocumentIndexValue struct {
    LastResolvedScope ResolvedLSPToolScope
    LastFingerprint   string
    LastSeenAt        time.Time
}
```

新 cache key：

```go
type lspCacheKey struct {
    ScopeKey      string
    WorkspaceKey  string
    LanguageID    string
    URI           string
    LanguageSpecificHash string
}
```

规则：

- `ScopeKey` 与 `WorkspaceKey` 必须复用 `03-lsp-manager-pool.md` 中 `ForScope` 返回的 canonical `ResolvedLSPToolScope`；不存在 agent/pool strict cache 模式。
- Go 的 `goWorkPath/moduleRoot` 只能进入 `LanguageSpecificHash` 或 `WorkspaceKey` 的 language-specific 部分；通用 cache key 不出现 Go-only 字段。
- `turnID/callID` 不进入 cache key，只用于日志/追踪。
- persistent cache 默认关闭；如果开启，必须带 TTL + fingerprint 校验。
- 每次成功 bootstrap/diagnostics/edit 后更新 `URI -> LastResolvedScope` 索引；索引与 diagnostics/cache 同生命周期。

## 失效规则

### publishDiagnostics

写入前必须校验：

- generation 等于当前 scope generation。
- URI 属于当前 workspace/root。
- manager 未关闭。

空 diagnostics：

- 删除该 URI diagnostics snapshot。
- 保留最近 clean timestamp 可选，用于稳定等待。

### diagnostics(file)

流程：

1. 读取 `URI -> LastResolvedScope` 旧索引；随后 resolve 当前 scope + workspace root，并调用 `ForScope` 获得 canonical `ResolvedLSPToolScope`。
2. 若 target file 已删除：同时按旧 `LastResolvedScope` 与当前 `ResolvedLSPToolScope` 清理该 URI 的 diagnostics、bootstrap state、cache record，写短 TTL tombstone，返回当前 scope 下该 URI 的空 diagnostics。
3. bootstrap target document。
4. refresh tracked siblings。
5. wait stable。
6. 返回当前 scope + URI diagnostics。

### diagnostics(all)

流程：

1. resolve current scope。
2. refresh current scope tracked docs。
3. 对 deleted docs 先读取旧 `URI -> LastResolvedScope`，再按旧 key + 当前 key 清理 diagnostics、bootstrap state、cache record，并写短 TTL tombstone。
4. wait stable。
5. 只返回当前 scope diagnostics。

### DidChange / edit

- `DidChange` 成功后，目标 URI 标记 `DirtyInMemory`。
- edit 落盘后，重新读取 snapshot 并 bootstrap。
- 如果 DidChange 失败，尝试 DidClose + DidOpen；再失败则 recycle workspace manager。

### recycle / close

- manager close/recycle：advance scope generation。
- detach workspace manager：清 workspace diagnostics。
- close bootstrap coordinator。
- old publishDiagnostics 因旧 generation 被丢弃。

### agent stop/archive

- 清理 agent/thread scope 下所有 manager-key clones、diagnostics snapshots、bootstrap states、cache records 或释放 active leases。
- 不强杀同 shard 中其他 manager key；shard 是资源分桶，不是共享 diagnostics/cache 的 scope。

## 实现步骤

1. 增加 scoped diagnostics store。
2. `publishDiagnosticsForGeneration` 增加 scope/workspace 校验。
3. `fetchDiagnosticsWithRetry` 改为先 refresh/validate stale，再 collect。
4. bootstrap coordinator key 从 manager pointer 扩展为 canonical resolved scope/workspace manager。
5. cache key 增加 scope/workspace/root 维度，并复用 `ScopedManager.ResolvedScope`。
6. deleted file 同步清理 diagnostics + bootstrap state + cache record，并记录 tombstone。
7. 增加 `URI -> LastResolvedScope` 索引；删除 `go.work/go.mod` 或子模块 marker 时必须用旧 key 清理旧 workspace cache/bootstrap，再用当前 key 写 tombstone。
8. agent lifecycle 接入 scope cleanup。
9. `cmd/mcp-lsp/manager/registry.go:141-149` 的 `Diagnostics(ctx, nil)` 不再遍历所有 language manager；必须先解析 caller scope，只枚举当前 scope 的 manager-key clones。
10. `cmd/mcp-lsp/manager/registry.go:190-196` 的 URI grouping 必须改为 `groupURIsByManager(ctx, uris)`，禁止用 `context.Background()` 重新取 manager。

## 测试

- 旧 generation publish 被忽略。
- 文件修改后 diagnostics(file) 先 refresh，不返回旧诊断。
- sibling 文件修改后 diagnostics(file) 刷新 sibling。
- 文件删除后 diagnostics(file) 返回空 diagnostics，并按旧 `LastResolvedScope` + 当前 resolved scope 清理该 URI 的 bootstrap/cache record。
- 删除 `go.work/go.mod` 或子模块 marker 后，旧 workspace key 下的 bootstrap/cache record 不残留。
- 文件删除后 diagnostics(all) 不再返回该 URI。
- 文件删除后旧 bootstrap/cache record 不会从 persistent cache 或 `WorkspaceDocuments` 复活。
- agent A 与 agent B 同 URI 不串 diagnostics。
- recycle shard A 不清 shard B。
- persistent cache 开启时，不同 workspace/root 不共享 document version。

## 完成定义

- diagnostics snapshot 包含 scope/workspace freshness 信息。
- all diagnostics 不跨 scope。
- stale/deleted 文件有明确清理路径。
- bootstrap/cache 生命周期与 manager/recycler/agent stop 对齐。
