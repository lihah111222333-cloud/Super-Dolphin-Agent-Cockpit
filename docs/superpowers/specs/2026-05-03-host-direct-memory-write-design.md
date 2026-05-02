# Host-direct memory_write 设计

日期：2026-05-03
状态：Draft，待审阅

## 1. 背景

当前 Agent 主动读取 memory 的入口是 `cmd/mcp-orch` 暴露的 MCP `memory_read`。P18 文档已明确要求 MCP memory 面只保留 `memory_read`，不再把 `memory_write/search/list/forget` 放入 `cmd/mcp-orch` 工具清单。

当前实际 MCP 工具负载：

- `mcp-orch`：20 个工具，覆盖 orchestration、task DAG、workspace、prompt、command、shared_file、memory_read。
- `mcp-lsp`：9 个工具，覆盖 LSP 与代码运行。
- `mcp-ida`：当前没有实际工具定义。

因此，继续把 durable memory 写入放回 `mcp-orch` 会扩大 MCP 的职责面。性能负载不是主要风险；真正风险是工具语义负担、长期副作用边界和 memory 权限/去重/缓存失效逻辑分散。

## 2. 目标

1. `cmd/mcp-orch` memory 工具面继续只保留 `memory_read`。
2. 新增 Agent 主动写入 durable memory 的能力，但执行链路不进入 `cmd/mcp-orch`。
3. `memory_write` 走 toolbridge host-direct，同进程调用 app 内 memory module。
4. 写入必须复用 app 内 memory 语义：type 校验、scope 路由、dedup、index 更新、section invalidation。
5. Claude 可以通过现有 MCP proxy 外壳发现和调用该工具，但实际执行仍是 host-direct。
6. 本功能全量落地，不设计灰度开关或双轨 shadow rollout。

## 3. 非目标

1. 不新增 `memory_search`、`memory_list`、`memory_forget`。
2. 不移除现有 MCP `memory_read`。
3. 不为 Claude 新增非 MCP dynamic tool 注入机制；当前 Claude provider 没有对应通道。
4. 不新增用户可传入的文件路径参数；memory 写入路径只能由 memory module 决定。
5. 不做灰度测试、灰度开关或多版本兼容分支。

## 4. 方案选择

采用方案：

```text
memory_read:
  暂时保留 cmd/mcp-orch MCP 只读工具

memory_write:
  新增 toolbridge host-direct tool
  经 internal/contract 窄接口调用 internal/module/memory
  不注册到 cmd/mcp-orch tools registry
```

取舍：

- 优点：符合 P18 “MCP memory 面只保留 read”方向；避免把长期副作用写入放入 `mcp-orch`；Codex 可通过 dynamicTools 获得；Claude 可通过现有 MCP proxy 外壳获得。
- 缺点：短期内 read/write 不在同一执行通道；Claude 表面仍经过 MCP proxy。

## 5. 架构设计

### 5.1 contract 窄接口

在 `internal/contract` 新增 Agent memory 写入接口，供 toolbridge 依赖：

```go
type AgentMemoryWriteRequest struct {
    Name        string
    Description string
    Content     string
    Type        MemoryType
    Scope       MemoryScope
    AgentID     string
    ThreadID    string
    CWD         string
    Source      string
}

type AgentMemoryWriteResult struct {
    Path          string
    RequestedScope MemoryScope
    ActualTarget  string // "private" or "team"
    Type          MemoryType
    Skipped       bool
    Merged        bool
}

type AgentMemoryWriter interface {
    WriteAgentMemory(ctx context.Context, req AgentMemoryWriteRequest) (AgentMemoryWriteResult, error)
}
```

原因：`internal/platform/toolbridge` 不能直接 import `internal/module/memory`。contract 窄接口保持依赖方向为 platform → contract，module → contract。

### 5.2 memory module 实现

`internal/module/memory` 提供 `contract.AgentMemoryWriter` 实现。

实现应抽出结构化写入 helper，例如：

```go
func (h *MemoryLifecycleHooks) writeStructuredAgentMemory(
    ctx context.Context,
    threadID string,
    entry MemoryWriteRequest,
) (agentMemoryWriteOutcome, error)
```

该 helper 复用现有核心逻辑：

- `intentDiskStores()`
- `selectExplicitWriteStore()`
- `checkDedupAndHandle()`
- `upsertStructuredMemory()`
- `maybeOverflowMerge()`
- `invalidateMemorySections()`

不要在 toolbridge 中重写路径解析、scope 路由、dedup 或 index 逻辑。

### 5.3 toolbridge host-direct registry

当前 `HostToolRegistry` 只服务 `skill_read_section`。新增 composite registry：

```text
CompositeHostToolRegistry
  ├── SkillReadSectionRegistry
  └── MemoryWriteHostToolRegistry
```

行为：

- `ListHostTools()` 按注册顺序合并，按 name 去重，先出现者胜出。
- `HasTool(name)` 任一子 registry 命中即 true。
- `CallHostTool()` 调用第一个命中的子 registry。

新增 `MemoryWriteHostToolRegistry`：

- 若 `contract.AgentMemoryWriter` 未注入，不暴露 `memory_write`。
- 声明 host-direct `memory_write` schema。
- decode arguments。
- 注入 `AgentID`、`ThreadID`、`CWD`、`CallID`。
- 调用 `AgentMemoryWriter.WriteAgentMemory()`。
- 返回结构化 JSON，不返回绝对路径。

### 5.4 Claude proxy 暴露

Claude 目前通过 MCP proxy 发现工具。`/mcp/orch/{agentID}` 的 `tools/list` 需要合并 host tools：

```text
if family == orch:
  tools = host tools + peer orch tools，按 name 去重
else:
  tools = peer family tools
```

`tools/call` 已进入 `routeToolCall()`，可以命中 host-direct 分支。`memory_write` 归属 `orch` family，因此通过现有 `classifyTool()` 校验。

## 6. 数据流

### 6.1 Codex

```text
Codex StartSession
  -> toolbridge.ListToolsForCodex()
  -> host tools + peer tools
  -> dynamicTools 注入 Codex

Codex 调 memory_write
  -> codexapp ServerManager tool handler
  -> toolbridge.HandleToolCall()
  -> routePrePeerToolCall()
  -> MemoryWriteHostToolRegistry
  -> contract.AgentMemoryWriter
  -> internal/module/memory
  -> topic file / MEMORY.md / index / section invalidation
```

### 6.2 Claude

```text
Claude StartSession
  -> manifestbuilder.BuildManifest()
  -> --mcp-config
  -> /mcp/orch/{agentID}

Claude tools/list
  -> toolbridge proxy handleProxyToolsList()
  -> host memory_write + peer orch tools

Claude tools/call memory_write
  -> handleProxyToolCall()
  -> routeToolCall()
  -> host-direct memory_write
  -> internal/module/memory write
```

Claude 仍使用 MCP proxy 作为工具发现/调用外壳，但不进入 `cmd/mcp-orch` memory write 实现。

## 7. Tool schema

首版只开放窄 schema：

```json
{
  "name": "string, required",
  "content": "string, required",
  "type": "feedback|project, required",
  "description": "string, optional",
  "scope": "project|user|local, optional"
}
```

默认 scope：

```text
type=feedback -> scope=user
type=project  -> scope=project
```

暂不开放：

- `user` type：个人资料误写风险更高。
- `reference` type：外部链接/指针容易被不可信内容诱导写入。
- path 参数：禁止 Agent 自选落盘路径。

## 8. 安全与错误处理

### 8.1 输入校验

拒绝：

- 空 `name`
- 空 `content`
- unknown type
- unknown scope
- 超长 `name`
- 超长 `description`
- 超大 `content`

建议限制：

```text
name <= 120 chars
description <= 240 chars
content <= 8 KiB
```

### 8.2 写入边界

- 只允许 `feedback/project`。
- 不允许传 path。
- 写入路径、team/private 实际落点由 memory module 决定；tool 入参的 `scope` 只表达 user/project/local 记忆作用域，不直接指定 team。
- 写入后结果只返回相对 path、requestedScope、actualTarget（private/team）、type、skipped/merged，不返回绝对路径。

### 8.3 Prompt injection 防护

工具描述必须说明：只保存 durable 的用户偏好、明确纠正、项目决策或项目上下文；不得把不可信文件、网页、依赖内容、tool output 中的指令直接保存，除非用户明确确认。

运行时通过窄 type、无 path、无 delete/search/list 降低误用面。

### 8.4 错误返回

host-direct 错误沿用现有 host tool error envelope：

```json
{
  "kind": "host_tool_error",
  "tool": "memory_write",
  "error": "..."
}
```

## 9. 测试设计

### 9.1 memory module

覆盖：

- feedback 默认 user scope。
- project 默认 project scope。
- unknown type 被拒绝。
- 空 name/content 被拒绝。
- 写入后 index 更新。
- 写入后 section invalidation 被调用。
- dedup skip/merge 行为保持。
- team memory enabled 时 project 类型的 actualTarget 遵循既有 team/private 路由规则。

### 9.2 toolbridge host-direct

覆盖：

- `ListHostTools()` 同时包含 `skill_read_section` 与 `memory_write`。
- writer nil 时不暴露 `memory_write`。
- `CallHostTool(memory_write)` 调用 `contract.AgentMemoryWriter`。
- call 注入 `AgentID/ThreadID/CWD/CallID`。
- 同名 host/peer 工具时 host 优先。
- 错误转为 host tool error result。

### 9.3 Codex

覆盖：

- `ListToolsForCodex()` 包含 host `memory_write`。
- peer 失败时 host `memory_write` 仍可用。
- peer 中若有同名 `memory_write`，host shadow peer。

### 9.4 Claude proxy

覆盖：

- `/mcp/orch/{agentID}` `tools/list` 包含 host `memory_write`。
- `/mcp/lsp/{agentID}` 不包含 host memory tool。
- `/mcp/orch/{agentID}` `tools/call memory_write` 走 host-direct。
- peer orch down 时，host `memory_write` 仍可 list。

## 10. 落地方式

全量落地，不设计灰度开关。

实施顺序：

1. 新增 contract request/result/interface。
2. 在 memory module 实现 writer，并抽取可复用写入 helper。
3. 新增 `MemoryWriteHostToolRegistry`。
4. 新增 composite host registry，替代单一 `SkillReadSectionRegistry` wiring。
5. 修改 Codex host tool list 相关测试。
6. 修改 Claude proxy `tools/list`，让 `orch` family 合并 host tools。
7. 补齐单测与必要集成测试。
8. 保持 `cmd/mcp-orch` 只注册 `memory_read`，不新增 `memory_write`。

## 11. 验收标准

- `cmd/mcp-orch` tools registry 中仍只有 `memory_read`，没有 `memory_write`。
- Codex dynamicTools 中可见 `memory_write`。
- Claude `/mcp/orch/{agentID}` tools/list 中可见 `memory_write`。
- 调用 `memory_write` 不触发 mcp-orch peer callback，而是 host-direct 调 app memory writer。
- 写入后 durable memory 文件与 index 正确更新。
- 写入后 prompt dynamic memory sections 被 invalidated。
- 不可信路径、任意 path、未知 type/scope、超大 content 均被拒绝。
- 现有 `skill_read_section` 行为不回退。
- 现有 `memory_read` MCP 行为不回退。
