# Host-direct memory_read 全量落地实施计划

> 目标：让 Codex dynamicTools 与 Claude `/mcp/orch/{agentID}` 中的 `memory_read` **全量走 host-direct**，同进程调用 app 内 memory module；`cmd/mcp-orch` 不再注册 `memory_read` / `memory_write`。不做灰度、不做运行时回滚开关、不保留 peer/mcp-orch 兜底。

## 背景与约束

- 迁移前 `memory_write` 已是 host-direct：`internal/platform/toolbridge/memory_write_tool.go` 暴露 schema，`routePrePeerToolCall()` 命中 host tool 后不走 peer。
- 迁移前 `memory_read` 由 `internal/sidecar/orch/tools/memory_tools.go` 注册，并调用 `cmd/mcp-orch/memory/service.go`；当前实现已移除这条工具注册链路。
- 目标是全量 host-direct：agent-terminal 内 `memory_read` 只能来自 app host registry，不得继续通过 mcp-orch peer 暴露或调用。
- 必须保持：
  - `cmd/mcp-orch` 不再注册 `memory_read` / `memory_write`。
  - Codex / Claude agent-terminal 场景中 `memory_read` 只能来自 host-direct。
  - `memory_read` 和记忆中心必须同源：同一 durable memory root、同一 name/path 解析、同一 path containment 校验、同一 type/scope 过滤语义。
  - 不修改 archtest 默认限额、不修改 freeze 限额来绕过 guard。
  - 不做灰度、不做 feature flag 双轨、不保留 runtime fallback；若出问题，修复代码或回滚提交。

## 产品语义

`memory_read` 是 **host-direct only**。

- Codex dynamicTools 暴露的 `memory_read` 只能来自 app host registry。
- Claude `/mcp/orch/{agentID}` 暴露的 `memory_read` 只能来自 app host registry。
- `cmd/mcp-orch` 不注册 `memory_read` / `memory_write`。
- 当 host reader 不存在、memory disabled、tools disabled、输入非法、scope 不支持或读取失败时，不得查询 peer/mcp-orch；必须直接返回稳定 tool error envelope。
- 测试 fixture 即使模拟 peer 侧存在同名 `memory_read`，也只用于证明 peer 未被查询/调用，不代表生产保留 兜底。
- 这是 breaking change：旧调用方如果直接依赖 mcp-orch standalone `memory_read`，会失去该工具。

## Scope 语义（首版）

首版必须限制 scope，避免 Codex、Claude、记忆中心三边语义漂移。

| Agent `memory_read` scope | 状态 | Storage/UI 语义 | 行为 |
|---|---|---|---|
| empty | 支持 | 等同 `user` | 读取 private durable memory root |
| `user` | 支持 | 记忆中心 private/个人记忆 | 支持 name/path 读取与 type 过滤 |
| `team` | 支持（若 team memory enabled） | 记忆中心 team durable memory | 只读；按当前 project root 隔离 |
| `project` | 不支持 | 语义不稳定，避免混同 team/project | 返回 `unsupported_scope` |
| `local` | 不支持 | 不保证与记忆中心同源 | 返回 `unsupported_scope` |
| `private` | 不作为公开 schema 值 | 内部/UI/storage 概念 | toolbridge raw 输入返回 `invalid_input`；module contract 若收到该值返回 `unsupported_scope`；不要作为 alias |

## 无 兜底 失败语义

所有错误均应是 tool 业务错误，而不是误变成 JSON-RPC transport/protocol 错误。Claude proxy 下应返回 JSON-RPC `result`，其中 `isError=true`，content 中包含稳定错误 envelope。即使 tools/list 隐藏 `memory_read`，stale/direct `tools/call memory_read` 也必须由 host route 识别并返回稳定 envelope，不得继续 peer fallback。

错误 envelope 形状固定为：

```json
{
  "kind": "host_tool_error",
  "tool": "memory_read",
  "code": "reader_unavailable",
  "error": "..."
}
```

| 场景 | 稳定 code |
|---|---|
| reader 未注入 | `reader_unavailable` |
| memory product disabled | `feature_disabled` |
| memory tools disabled | `tools_disabled` |
| scope 不支持 | `unsupported_scope` |
| name/path 都空 | `invalid_input` |
| malformed input / enum 非法 | `invalid_input` |
| path 越界或非法 | `invalid_path` |
| entry 不存在 | `not_found` |
| 持久化/读取失败 | `read_failed` |

## 设计选择

采用“全量 host-direct”方案：

1. 在 `internal/contract/memory.go` 新增唯一 reader 合约 `AgentMemoryReader`。不要扩展或复用旧 `MemoryService` 作为 host-direct contract。
2. 在 `internal/module/memory` 提供 app 内 reader 实现，读取与 `ui/memory/get` 同一 durable memory root。
3. 在 `internal/platform/toolbridge` 新增 `memory_read` host tool registry，加入 `CompositeHostToolRegistry`。
4. Codex 和 Claude 不新增 provider 分支：
   - Codex 由 `ListToolsForCodex()` 暴露 host-direct `memory_read`。
   - Claude 由 `/mcp/orch/{agentID}` `tools/list` 暴露 host-direct `memory_read`。
   - 调用由 `routePrePeerToolCall()` 命中 host tool 后直接返回；对于 `memory_read`，host 不可用时不得继续 peer fallback。
5. `cmd/mcp-orch` registry / runtime / assembly 层彻底移除 memory tool 依赖。

## 任务拆分

### Task 1: mcp-orch 不再暴露 memory tools

**Files:**
- Modify: `internal/sidecar/orch/tools/memory_tools.go`
- Modify: `internal/sidecar/orch/tools/memory_tools_test.go`
- Modify: `internal/sidecar/orch/tools/registry.go`
- Modify: `cmd/mcp-orch/runtime.go`
- Modify/Delete: `cmd/mcp-orch/runtime_memory_test.go`
- Potentially delete: `cmd/mcp-orch/memory/*` if no remaining imports depend on it

- [ ] **Step 1: 写失败测试 — tools registry 不暴露 memory tools**

把 `TestMemoryToolDefinitionsExposeOnlyRead` 改成：

```go
func TestMemoryToolDefinitionsExposeNoMemoryTools(t *testing.T) {
    registry := NewRegistry(Dependencies{})
    if _, ok := registry.Lookup("memory_read"); ok {
        t.Fatal("memory_read must not be exposed by mcp-orch; host-direct owns memory tools")
    }
    if _, ok := registry.Lookup("memory_write"); ok {
        t.Fatal("memory_write must not be exposed by mcp-orch")
    }
}
```

Run:

```bash
go test ./internal/sidecar/orch/tools -run TestMemoryToolDefinitionsExposeNoMemoryTools -count=1 -v
```

Expected: FAIL because `memory_read` is still registered.

- [ ] **Step 2: 写失败测试 — mcp-orch runtime tools/list 不暴露 memory tools**

新增或改造 `cmd/mcp-orch/runtime_memory_test.go`：

```go
func TestRuntimeToolsListDoesNotExposeMemoryReadOrWrite(t *testing.T) { ... }
func TestRuntimeToolCallMemoryReadIsUnknownAfterRegistryRemoval(t *testing.T) { ... }
```

断言：
- `tools/list` 不包含 `memory_read`。
- `tools/list` 不包含 `memory_write`。
- standalone mcp-orch `tools/call memory_read` 返回 unknown tool / stable method-level tool error。
- 不保留旧 callback。

Run:

```bash
go test ./cmd/mcp-orch -run 'TestRuntimeToolsListDoesNotExposeMemoryReadOrWrite|TestRuntimeToolCallMemoryReadIsUnknownAfterRegistryRemoval' -count=1 -v
```

Expected: FAIL。

- [ ] **Step 3: 最小实现 — 移除 mcp-orch memory tool assembly**

修改点：
- `internal/sidecar/orch/tools/registry.go` 不再 append `memoryToolDefinitions(deps.Memory)`。
- `internal/sidecar/orch/tools.Dependencies` 删除 `Memory contract.MemoryService` 字段；旧 `cmd/mcp-orch/memory` 包若暂留，只能作为未装配 legacy seam，不能参与 registry/runtime 装配。
- `cmd/mcp-orch/runtime.go` 删除 memory service 参数与 `memory.NewService(...)` assembly 接线。
- `newRegistry(...)` 删除 memory 参数位，并同步所有调用点。
- `internal/sidecar/orch/tools/memory_tools.go` 删除或让 `memoryToolDefinitions` 不再被引用；若文件无引用则删除。
- `cmd/mcp-orch/memory/*` 若没有剩余 import，本任务删除；若因测试/其它非工具路径暂留，必须确认它不被 runtime/registry 装配。

不要把 `memory_write` 注册回 mcp-orch。

- [ ] **Step 4: 验证**

Run:

```bash
go test ./internal/sidecar/orch/tools ./cmd/mcp-orch -run 'Memory|memory|ToolsList|ToolCall' -count=1 -v
```

Expected: PASS。

### Task 2: 定义 host-direct reader contract

**Files:**
- Modify: `internal/contract/memory.go`

- [ ] **Step 1: 新增唯一接口**

只采用这个方案，不要扩展 `MemoryService`：

```go
type AgentMemoryReader interface {
    ReadAgentMemory(ctx context.Context, req MemoryReadRequest) (MemoryReadResult, error)
    MemoryReadEnabled() bool
    MemoryReadToolsEnabled() bool
}
```

`MemoryService` 是旧 mcp-orch read service seam；host-direct app reader 不应复用它。

- [ ] **Step 2: 如果需要上下文字段，一次性定义**

若 reader 需要 agent/thread/cwd 上下文，应扩展 `MemoryReadRequest`：

```go
type MemoryReadRequest struct {
    Name string
    Path string
    Scope MemoryScope
    Type MemoryType
    AgentID string
    ThreadID string
    CWD string
    CallID string
}
```

如果实现不需要这些字段，不要加；不要让 toolbridge 私下把上下文塞进 module-local 类型。

- [ ] **Step 3: 编译验证**

Run:

```bash
go test ./internal/contract -count=1
```

Expected: PASS。

### Task 3: app memory module reader — 先测试后实现

**Files:**
- Modify: `internal/module/memory/domain_bridges.go`
- Modify/Test: `internal/module/memory/hooks_test.go`
- Create/Modify: a non-RPC helper file if needed, e.g. `internal/module/memory/agent_read.go`
- Avoid implementing agent reader in: `internal/module/memory/ui_rpc.go`, `internal/module/memory/ui_rpc_mutations.go`

- [ ] **Step 1: 写失败测试 — agent read 能读到 agent 写入且 UI 可见的 entry**

```go
func TestReadAgentMemoryReadsEntryVisibleInMemoryCenter(t *testing.T) {
    root := filepath.Join(t.TempDir(), "memory-root")
    projectRoot := filepath.Join(t.TempDir(), "project")
    cfg := &Config{Enabled: true, EnableTools: true, RootDir: root, ProjectRoot: projectRoot}
    hooks := newMemoryLifecycleHooks(cfg, nil, nil, nil, nil, nil, nil, nil)

    _, err := hooks.WriteAgentMemory(context.Background(), validAgentMemoryRequest(nil))
    if err != nil { t.Fatalf("WriteAgentMemory() error = %v", err) }

    got, err := hooks.ReadAgentMemory(context.Background(), contract.MemoryReadRequest{
        Name: "daily-report-style",
        Scope: contract.MemoryScopeUser,
        Type: contract.MemoryTypeFeedback,
    })
    if err != nil { t.Fatalf("ReadAgentMemory() error = %v", err) }
    if got.Entry == nil || got.Entry.Name != "daily-report-style" { t.Fatalf("result = %+v", got) }

    snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
    if err != nil { t.Fatalf("buildUIMemorySnapshot() error = %v", err) }
    assertUIMemoryEntryVisible(t, snapshot.Private.Entries, "daily-report-style", "agent_tool")
}
```

Run:

```bash
go test ./internal/module/memory -run TestReadAgentMemoryReadsEntryVisibleInMemoryCenter -count=1 -v
```

Expected: FAIL because `ReadAgentMemory` is not implemented.

- [ ] **Step 2: 写失败测试 — 记忆中心已有 durable entry，agent 可读**

新增：

```go
func TestReadAgentMemoryReadsExistingDurablePrivateEntry(t *testing.T) { ... }
func TestReadAgentMemoryByPathMatchesMemoryCenterEntry(t *testing.T) { ... }
```

要求：
- 不只用 `WriteAgentMemory` 造数据。
- 直接构造 durable memory root / topic file / `MEMORY.md` pointer（若当前 scan 不依赖 pointer，也要确保 UI snapshot 能读到）。
- `ReadAgentMemory` 和 `buildUIMemorySnapshot` 看到同一个 entry。
- name 读取与 path 读取一致。

Run:

```bash
go test ./internal/module/memory -run 'TestReadAgentMemoryReadsExistingDurablePrivateEntry|TestReadAgentMemoryByPathMatchesMemoryCenterEntry' -count=1 -v
```

Expected: FAIL。

- [ ] **Step 3: 写失败测试 — scope 与错误语义**

新增：

```go
func TestReadAgentMemoryValidationAndDisabled(t *testing.T) { ... }
func TestReadAgentMemoryRejectsPathOutsideMemoryRoot(t *testing.T) { ... }
func TestReadAgentMemoryMissingEntryReturnsNotFound(t *testing.T) { ... }
func TestReadAgentMemoryUnsupportedScopes(t *testing.T) { ... }
```

覆盖：
- `Enabled=false` -> `feature_disabled`
- `EnableTools=false` -> `tools_disabled`
- empty name/path -> `invalid_input`
- `scope=project/local` -> `unsupported_scope`
- toolbridge raw `scope=private` -> `invalid_input`；module contract `MemoryScope("private")` -> `unsupported_scope`
- invalid / escaped path -> `invalid_path`
- missing entry -> `not_found`

Run:

```bash
go test ./internal/module/memory -run 'TestReadAgentMemoryValidationAndDisabled|TestReadAgentMemoryRejectsPathOutsideMemoryRoot|TestReadAgentMemoryMissingEntryReturnsNotFound|TestReadAgentMemoryUnsupportedScopes' -count=1 -v
```

Expected: FAIL。

- [ ] **Step 4: 必做 team 测试**

首版支持 `scope=team`，必须新增：

```go
func TestReadAgentMemoryReadsExistingDurableTeamEntry(t *testing.T) { ... }
```

该测试必须验证 team durable root 与记忆中心 `Team.Entries` 同源；不得把 team 测试降为可选。

- [ ] **Step 5: 最小实现**

在 `domain_bridges.go` 或新的非 RPC helper 文件实现：

```go
var _ contract.AgentMemoryReader = (*MemoryLifecycleHooks)(nil)

func (h *MemoryLifecycleHooks) MemoryReadEnabled() bool { ... }
func (h *MemoryLifecycleHooks) MemoryReadToolsEnabled() bool { ... }
func (h *MemoryLifecycleHooks) ReadAgentMemory(ctx context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) { ... }
```

实现原则：
- `h == nil` -> `reader_unavailable`
- `cfg.Enabled=false` -> `feature_disabled`
- `cfg.EnableTools=false` -> `tools_disabled`
- name/path 都空 -> `invalid_input`
- `scope=""` -> `user`
- `scope=user` -> private durable memory root
- `scope=team` -> team durable memory root only if team memory enabled
- `scope=project/local` -> `unsupported_scope`
- toolbridge raw `scope=private` -> `invalid_input`；module contract `MemoryScope("private")` -> `unsupported_scope`
- path 必须做 root containment 校验
- not found 返回稳定 `not_found`
- 不依赖 UI RPC DTO / handler；最多提取纯 store/root/entry helper

优先复用或提取：
- `resolvedStoreRoot(...)`
- `configuredTeamMemRoot(...)`
- `scanMemoryEntries(...)`
- `ValidateMemoryReadPath(...)` / equivalent containment helper
- `findMemoryEntry(...)`

- [ ] **Step 6: 验证**

Run:

```bash
go test ./internal/module/memory -run 'TestReadAgentMemory|TestWriteAgentMemory|TestBuildUIMemorySnapshot' -count=1 -v
```

Expected: PASS。

### Task 4: toolbridge host registry — 先测试后实现

**Files:**
- Create: `internal/platform/toolbridge/memory_read_tool.go`
- Modify: `internal/platform/toolbridge/module.go`
- Modify/Test: `internal/platform/toolbridge/host_tools_test.go`

- [ ] **Step 1: 写失败测试 — registry schema + call**

在 `host_tools_test.go` 增加 reader stub：

```go
type stubAgentMemoryReader struct {
    calls int
    last contract.MemoryReadRequest
    err error
}
```

新增：

```go
func TestMemoryReadHostToolRegistry_ListSchemaAndCall(t *testing.T) { ... }
```

断言：
- tool name = `memory_read`
- schema 有 `name/path/scope/type`
- call 会传 `Name/Path/Scope/Type`
- result 是 `contract.MemoryReadResult`

Run:

```bash
go test ./internal/platform/toolbridge -run TestMemoryReadHostToolRegistry_ListSchemaAndCall -count=1 -v
```

Expected: FAIL because `NewMemoryReadHostToolRegistry` is missing.

- [ ] **Step 2: 写失败测试 — gating / stale call / reader unavailable / invalid input**

新增：

```go
func TestMemoryReadHostToolRegistry_ListHiddenWhenDisabled(t *testing.T) { ... }
func TestMemoryReadHostToolRegistry_StaleCallWhenFeatureDisabled(t *testing.T) { ... }
func TestMemoryReadHostToolRegistry_StaleCallWhenToolsDisabled(t *testing.T) { ... }
func TestMemoryReadHostToolRegistry_ReaderUnavailable(t *testing.T) { ... }
func TestMemoryReadHostToolRegistry_InvalidInput(t *testing.T) { ... }
func TestMemoryReadHostToolRegistry_ReaderError(t *testing.T) { ... }
```

Expected: FAIL。

- [ ] **Step 3: 实现 `memory_read_tool.go`**

结构参考 `memory_write_tool.go`：

```go
const ToolNameMemoryRead = "memory_read"

type MemoryReadHostToolOptions struct {
    Enabled bool
    ToolsEnabled bool
}

type MemoryReadHostToolRegistry struct {
    reader contract.AgentMemoryReader
    opts MemoryReadHostToolOptions
}
```

Call 行为：
- decode input
- parse scope/type
- call `reader.ReadAgentMemory(ctx, contract.MemoryReadRequest{...})`
- reader nil -> `reader_unavailable`
- disabled stale call -> `feature_disabled` / `tools_disabled`
- malformed input / invalid enum -> `invalid_input`
- path 只透传给 reader；最终 containment 在 module 层完成

- [ ] **Step 4: 注入 composite**

修改 `hostToolRegistryIn`：

```go
Reader contract.AgentMemoryReader `optional:"true"`
Writer contract.AgentMemoryWriter `optional:"true"`
```

修改 `provideHostToolRegistry`：

```go
return NewCompositeHostToolRegistry(
    NewSkillReadSectionRegistry(in.Tool),
    NewMemoryReadHostToolRegistry(in.Reader, memoryReadHostToolOptions(in.Reader)),
    NewMemoryWriteHostToolRegistry(in.Writer, memoryWriteHostToolOptions(in.Writer)),
)
```

- [ ] **Step 5: 验证**

Run:

```bash
go test ./internal/platform/toolbridge -run 'TestMemoryReadHostToolRegistry|TestCompositeHostToolRegistry' -count=1 -v
```

Expected: PASS。

### Task 5: Codex dynamicTools / call 路径 — 提前写测试

**Files:**
- Modify/Test: `internal/platform/toolbridge/host_tools_test.go`
- Potentially modify/test: `internal/provider/codexapp/driver_toolbridge_test.go`

- [ ] **Step 1: 写失败测试 — Codex list 包含 memory_read / memory_write**

在实现 composite 注入前写：

```go
func TestListToolsForCodex_IncludesHostMemoryReadAndWrite(t *testing.T) { ... }
```

断言：
- `ListToolsForCodex()` 返回 `memory_read`
- `ListToolsForCodex()` 返回 `memory_write`
- host tools 不依赖 orch peer 成功

Run:

```bash
go test ./internal/platform/toolbridge -run TestListToolsForCodex_IncludesHostMemoryReadAndWrite -count=1 -v
```

Expected: FAIL before Task 4 wiring.

- [ ] **Step 2: 写失败测试 — peer 同名 memory_read 不被调用**

```go
func TestListToolsForCodex_HostMemoryReadPreventsPeerMemoryReadUse(t *testing.T) { ... }
func TestCodexMemoryReadCallUsesHostDirect(t *testing.T) { ... }
```

断言：
- fixture 可模拟 peer 也有 `memory_read`，但最终调用 reader calls = 1。
- peer call counter = 0。
- 该测试只证明 no peer fallback，不代表生产保留 peer 同名工具。

- [ ] **Step 3: provider support 必做测试**

必须覆盖真实 provider path，而不是只测 helper：

```go
func TestToolBridge_StartSession_InjectsMemoryReadDynamicTool(t *testing.T) { ... }
func TestCodexStartSession_InjectsHostMemoryReadAndFiltersPeerMemoryRead_E2E(t *testing.T) { ... }
```

锁定最终 `thread/start.dynamicTools` 包含 host-direct `memory_read`，并且 peer 同名 `memory_read` 被过滤/不覆盖 host schema。

- [ ] **Step 4: 验证**

Run:

```bash
go test ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/e2e -run 'TestListToolsForCodex|TestCodex.*MemoryRead|TestToolBridge_StartSession_UsesDynamicTools' -count=1 -v
```

Expected: PASS。

### Task 6: Claude proxy list/call/envelope — 提前写测试

**Files:**
- Modify/Test: `internal/platform/toolbridge/handler_test.go`

- [ ] **Step 1: 写失败测试 — tools/list 包含 host-direct memory_read**

```go
func TestProxyToolsList_OrchIncludesHostMemoryRead(t *testing.T) { ... }
```

断言：
- `/mcp/orch/{agentID}` `tools/list` 包含 `memory_read`
- `/mcp/lsp/{agentID}` 不包含 `memory_read`
- 不以 peer down 作为产品语义；只验证 host-direct list 可用

Expected: FAIL before Task 4 wiring.

- [ ] **Step 2: 写失败测试 — tools/call memory_read 走 host-direct**

```go
func TestProxyToolCall_MemoryReadUsesHostDirect(t *testing.T) { ... }
```

断言：
- JSON-RPC response 是 result，不是顶层 JSON-RPC error
- `isError=false`
- reader calls = 1
- registry/peer gotKinds 或 callback counter = 0

Expected: FAIL before Task 4 wiring.

- [ ] **Step 3: 写失败测试 — disabled/stale/error envelope**

新增：

```go
func TestProxyToolsList_HidesMemoryReadWhenToolsDisabled(t *testing.T) { ... }
func TestProxyToolCall_MemoryReadToolsDisabledReturnsToolErrorEnvelope(t *testing.T) { ... }
func TestProxyToolCall_MemoryReadFeatureDisabledReturnsToolErrorEnvelope(t *testing.T) { ... }
func TestProxyToolCall_StaleMemoryReadCallReturnsStableToolError(t *testing.T) { ... }
func TestProxyToolCall_MemoryReadReaderErrorUsesToolErrorNotJSONRPCError(t *testing.T) { ... }
func TestProxyToolCall_MemoryReadUnsupportedScopeReturnsToolErrorEnvelope(t *testing.T) { ... }
```

断言：
- list disabled 时不暴露 `memory_read`
- stale call 返回 JSON-RPC `result`
- `isError=true`
- content 中固定 `kind/tool/code/error` envelope
- 不查 peer

- [ ] **Step 4: 验证**

Run:

```bash
go test ./internal/platform/toolbridge -run 'TestProxyToolsList.*MemoryRead|TestProxyToolCall_MemoryRead' -count=1 -v
```

Expected: PASS。

### Task 7: memory module FX wiring

**Files:**
- Modify: `internal/module/memory/module.go`

- [ ] **Step 1: provider 注入**

新增：

```go
func provideAgentMemoryReader(hooks *MemoryLifecycleHooks) contract.AgentMemoryReader { return hooks }
```

加入 `fx.Provide(...)`。

- [ ] **Step 2: 检查 fx 图 / 编译**

Run:

```bash
go test ./internal/module/memory ./internal/platform/toolbridge -count=1
```

Expected: PASS。

### Task 8: 文档更新

**Files:**
- Modify: `docs/superpowers/specs/2026-05-03-host-direct-memory-write-design.md`
- Check for paired zh/en docs before editing

- [ ] **Step 1: grep 中英文或对应文档**

Run:

```bash
rg 'host-direct memory|memory_write|memory_read' docs/superpowers/specs docs/plans
```

如果存在中英文两份文档，必须同步更新。

- [ ] **Step 2: 更新设计说明**

更新要点：
- `memory_write` host-direct 已落地。
- `memory_read` 全量 host-direct，不走 mcp-orch。
- `cmd/mcp-orch` 不注册 `memory_read` / `memory_write`。
- 不做灰度、不做运行时回滚开关、不保留 peer fallback。
- scope 首版支持与错误码表。

### Task 9: 全量验证

Run:

```bash
go test ./internal/contract -count=1

go test ./internal/sidecar/orch/tools ./cmd/mcp-orch \
  -run 'Memory|memory|ToolsList|ToolCall' \
  -count=1 -v

go test ./internal/module/memory \
  -run 'TestReadAgentMemory|TestWriteAgentMemory|TestBuildUIMemorySnapshot' \
  -count=1 -v

go test ./internal/platform/toolbridge \
  -run 'TestMemoryReadHostToolRegistry|TestCompositeHostToolRegistry|TestListToolsForCodex|TestProxyToolsList|TestProxyToolCall' \
  -count=1 -v

go test ./internal/platform/toolbridge ./internal/module/memory ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider/unified ./internal/sidecar/orch/tools ./cmd/mcp-orch \
  -count=1

go vet ./internal/platform/toolbridge ./internal/module/memory ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider/unified ./internal/sidecar/orch/tools ./cmd/mcp-orch

./scripts/test_with_guard.sh --guard-only
```

Expected:
- 所有 Go 测试 PASS。
- `go vet` PASS。
- guard PASS。
- 不出现 archtest 包文件数/文件行数/复杂度违规。

### Task 10: 架构 grep 与收尾检查

Run:

```bash
rg 'internal/module/memory' internal/platform/toolbridge || true
rg 'cmd/mcp-orch/memory' internal/module/memory internal/platform/toolbridge || true
rg 'memory_read|memory_write' internal/sidecar/orch/tools cmd/mcp-orch/runtime.go || true
git diff --stat
git status --short --branch
```

确认：
- `internal/platform/toolbridge` 没有 import `internal/module/memory`。
- `internal/module/memory` / `internal/platform/toolbridge` 没有 import `cmd/mcp-orch/memory`。
- `cmd/mcp-orch` registry/runtime 不再注册或装配 memory tools。
- 没有修改 archtest 默认限额或 freeze 限额。
- 没有 commit / push，除非用户明确要求。

## 风险与注意事项

- 不要把 `cmd/mcp-orch/memory` 直接 import 到 `internal/module/memory` 或 `internal/platform/toolbridge`。
- 不要让 `internal/platform/toolbridge` import `internal/module/memory`，会违反 platform 不能依赖 module 的架构约束。
- 不要为了 reader 复用而把 UI RPC DTO 当 contract DTO 返回；UI detail 和 agent tool result 是两个边界。
- 对 path 读取必须做 root containment 校验；最终授权在 reader 内完成，toolbridge 只做 schema/decode。
- 以 app memory module 与记忆中心语义为准；不要为了兼容旧 mcp-orch read 服务保留双轨。
