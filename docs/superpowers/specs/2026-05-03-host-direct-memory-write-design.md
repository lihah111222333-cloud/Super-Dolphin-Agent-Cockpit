# Host-direct memory_write 设计

日期：2026-05-03
状态：Draft，已按 3 个只读子 agent 审查意见修订

## 1. 背景

当前 Agent 主动读取 memory 的目标入口已切为 toolbridge host-direct `memory_read`；`cmd/mcp-orch` 不再注册任何 memory tools。旧 P18 “MCP memory 面只保留 read”前提已被 host-direct memory_read 计划取代。

当前实际 MCP 工具负载：

- `mcp-orch`：不再包含 memory tools；memory_read / memory_write 均由 app host registry 提供。
- `mcp-lsp`：9 个工具，覆盖 LSP 与代码运行。
- `mcp-ida`：当前没有实际工具定义。

因此，继续把 durable memory 写入放回 `mcp-orch` 会扩大 MCP 的职责面。性能负载不是主要风险；真正风险是工具语义负担、长期副作用边界和 memory 权限/去重/缓存失效逻辑分散。

## 2. 目标

1. `cmd/mcp-orch` 不注册 `memory_read` / `memory_write` / `memory_search` / `memory_list` / `memory_forget` 等任何 memory tools。
2. 新增 Agent 主动写入 durable memory 的能力，但执行链路不进入 `cmd/mcp-orch`。
3. `memory_write` 走 toolbridge host-direct，同进程调用 app 内 memory module。
4. 写入必须复用 app 内 memory 语义：type 校验、scope 路由、dedup、index 更新、section invalidation。
5. Claude 实现后可以通过现有 MCP proxy 外壳发现和调用该工具，但实际执行仍是 host-direct。
6. 本功能全量落地，不设计新增灰度开关或双轨 shadow rollout；已有 `ENABLE_MEMORY_TOOLS` 作为统一可见性/止损开关。

## 3. 非目标

1. 不新增 `memory_search`、`memory_list`、`memory_forget`。
2. 不保留 `cmd/mcp-orch` MCP `memory_read`；这是旧 standalone memory_read 调用方的 breaking change。
3. 不为 Claude 新增非 MCP dynamic tool 注入机制；当前 Claude provider 没有对应通道。
4. 不新增用户可传入的文件路径参数；memory 写入路径只能由 memory module 决定。
5. 不允许 Agent 通过参数指定 `private/team` 实际落点。
6. 不做灰度测试、灰度开关或多版本兼容分支。

## 4. 方案选择

采用方案：

```text
memory_read:
  toolbridge host-direct tool
  经 internal/contract.AgentMemoryReader 调用 internal/module/memory
  不注册到 cmd/mcp-orch tools registry

memory_write:
  toolbridge host-direct tool
  经 internal/contract.AgentMemoryWriter 调用 internal/module/memory
  不注册到 cmd/mcp-orch tools registry
```

取舍：

- 优点：memory_read / memory_write 均由 host-direct 统一持有，避免把 durable memory 语义继续分散到 `mcp-orch`；Codex 可通过 dynamicTools 获得；Claude 可通过 MCP proxy 外壳获得。
- 缺点：Claude 表面仍经过 MCP proxy；`cmd/mcp-orch` standalone 直接调用旧 `memory_read` 的调用方需要迁移到 host-direct agent-terminal 路径。

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
    CallID      string
    Source      string
    // Source should be "agent_tool" for host-direct memory_write.
}

type AgentMemoryWriteResult struct {
    Path           string
    RequestedScope MemoryScope
    ActualTarget   string // "private" or "team"
    Type           MemoryType
    Skipped        bool
    Merged         bool
}

type AgentMemoryWriter interface {
    WriteAgentMemory(ctx context.Context, req AgentMemoryWriteRequest) (AgentMemoryWriteResult, error)
    MemoryWriteEnabled() bool
    MemoryWriteToolsEnabled() bool
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
    req contract.AgentMemoryWriteRequest,
) (agentMemoryWriteOutcome, error)
```

该 helper 复用现有写盘核心逻辑：

- `intentDiskStores()`
- `selectExplicitWriteStore()`
- `scopeNamesForIntentStores()`
- `checkDedupAndHandle()`
- `upsertStructuredMemory()`
- `maybeOverflowMerge()`

helper 必须返回实际写入目标：当 `store == secondary` 时使用 secondary scope name，否则使用 primary scope name，并映射为 `actualTarget=private|team`。不要在 toolbridge 中重写路径解析、scope 路由、dedup 或 index 逻辑。

writer 在 helper 成功返回后显式调用 `invalidateMemorySections()`，与当前显式保存链路中“写盘完成后由上层触发 invalidation”的位置保持一致；不要把 `invalidateMemorySections()` 误认为 `writeIntent()` 内部天然包含的写盘步骤。


首版 host-direct writer 在进入 helper 前执行额外策略 guard：

- `description` 必填，与 UI upsert 和 store frontmatter 校验保持一致。
- `scope` 不映射为写盘根目录；它只表达工具调用方的写入意图。首版只接受默认映射：`feedback -> user`、`project -> project`。传入 `local` 或与 type 默认值不一致的 scope 必须拒绝。
- `feedback` 默认只允许写 private；不得因为 team store 存在就创建 team feedback。若现有同名 team feedback 会导致写入 team，应拒绝并返回 `team_scope_not_allowed`，而不是静默更新 team。
- `project` 可写 team，但只能由 memory module 根据 thread metadata、team memory gate、team guard 和既有同名条目选择；Agent 不能通过参数强制 team。

### 5.3 toolbridge host-direct registry

host-direct tools 通过 composite registry 汇总；`skill_read_section`、host-direct `memory_read` 与 host-direct `memory_write` 共享同一条 list/call 去重规则：

```text
CompositeHostToolRegistry
  ├── SkillReadSectionRegistry
  ├── MemoryReadHostToolRegistry
  └── MemoryWriteHostToolRegistry
```

行为：

- `ListHostTools()` 按注册顺序合并，按 name 去重，先出现者胜出。
- `HasTool(name)` 任一子 registry 命中即 true。
- `CallHostTool()` 调用第一个命中的子 registry。
- 同名 host tool shadow peer tool 时记录 warn 和 metric；不改变调用方可见的 MCP schema。

新增 `MemoryWriteHostToolRegistry`：

- 若 `contract.AgentMemoryWriter` 未注入，或 memory product/tools gate 关闭，不暴露 `memory_write`。
- 声明 host-direct `memory_write` schema。
- decode arguments。
- 注入 `AgentID`、`ThreadID`、`CWD`、`CallID`。
- 调用 `AgentMemoryWriter.WriteAgentMemory()`。
- 返回结构化 JSON，不返回绝对路径。

### 5.4 Claude proxy 暴露

Claude 目前通过 MCP proxy 发现工具。当前代码里的 `handleProxyToolsList()` 只调用 `listPeerTools()`，peer 失败时直接返回 JSON-RPC error；它还没有合并 host tools，也没有 host-only 降级。

本设计要求把 `/mcp/orch/{agentID}` 的 `tools/list` 改为：

```text
if family == orch:
  tools = host tools + peer orch tools，按 name 去重，host 先出现者胜出
  peer orch 失败时仍返回 host tools，并记录 degraded warn/metric
else:
  tools = peer family tools
```

`tools/call` 已进入 `routeToolCall()`，可以命中 host-direct 分支。`memory_write` 归属 `orch` family，因此通过现有 `classifyTool()` 校验。若 host tool 名未来不属于 `classifyTool()` 默认 orch 规则，必须同步扩展 family 判定，不能只改 list。

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
  "description": "string, required",
  "content": "string, required",
  "type": "feedback|project, required",
  "scope": "user|project, optional"
}
```

默认 scope：

```text
type=feedback -> scope=user
type=project  -> scope=project
```

`scope` 是调用意图校验字段，不是写盘根目录，也不能指定 `private/team`。首版只接受默认映射；传 `scope=local`、`feedback+project`、`project+user` 都必须拒绝。

#### read/write scope 对照

| scope 输入 | host-direct `memory_read` | host-direct `memory_write` |
|---|---|---|
| empty | 等同 `user`，读 private durable memory root | 按 `type` 默认：`feedback -> user`、`project -> project` |
| `user` | 读 private / 个人记忆 | 只允许 `type=feedback` 的默认意图；不表示写入 `private` 路径参数 |
| `team` | 支持只读 team durable memory（需 team memory enabled） | 不作为 schema 值；Agent 不能指定 team。`feedback` 若因同名 team entry 会落到 team，当前实现返回 `team_scope_not_allowed`；`project` 是否实际落 team 由 memory module 路由决定 |
| `project` | 不支持，返回 `unsupported_scope` | 只允许 `type=project` 的默认意图；实际 `private/team` 落点由 memory module 的 gate/guard/同名条目决定 |
| `local` | 不支持，返回 `unsupported_scope` | 不支持，返回 `unsupported_scope` |
| `private` | 不作为公开 schema 值；raw 输入为 `invalid_input`/contract 内为 `unsupported_scope` | 不作为公开 schema 值，不能作为 `user` alias |

暂不开放：

- `user` type：个人资料误写风险更高。
- `reference` type：外部链接/指针容易被不可信内容诱导写入。
- `local` scope：当前 app memory 写链没有 local 写入落点；host-direct memory_read 也不开放 local 作为同源 durable root。
- path 参数：禁止 Agent 自选落盘路径。
- confirmation 参数：首版不允许模型自证“用户已确认”。需要用户确认的场景直接拒绝，未来如需支持应接入现有用户确认/审批机制。

## 8. 安全与错误处理

### 8.1 输入校验

拒绝：

- 空 `name`
- 空 `description`
- 空 `content`
- unknown type
- unknown scope
- type/scope 组合不等于默认映射
- `local` scope
- 任意 `path`、`target`、`actualTarget`、`team`、`private` 等试图指定落点的参数
- 超长 `name`
- 超长 `description`
- 超大 `content`
- probable secret

限制值是 host-direct `memory_write` 的新增 contract 限额，不是当前代码里已有的统一全局常量；实现时必须新增明确常量并用测试锁定：

```text
name <= 96 chars
schema description <= 240 chars
content <= 8 KiB
```

`name <= 96` 参考当前 extracted memory 的名称上限；`description <= 240` 与 `content <= 8 KiB` 是本工具新增边界，不能声称已由现有 memory 写链统一保证。

### 8.2 secret 拒写

host-direct writer 必须在写盘前对 `name`、`description`、`content` 做 probable secret 检测，命中后返回 `secret_detected`，并且日志/错误不得回显原文。

不能直接把现有 team memory guard 当作完整实现。当前 team guard 只覆盖一部分凭据形态；本功能需要新增或抽出共享 secret detector，把最低覆盖集合下沉为可测试规则常量，并逐条单测锁定。

首版至少覆盖：

- private key / certificate block，例如 `BEGIN PRIVATE KEY`。
- 常见 token/key 前缀或字段名，例如 `api_key`、`access_token`、`refresh_token`、`password`、`secret`、`client_secret`。
- 常见凭据形态，例如 GitHub `ghp_`/`github_pat_`、OpenAI `sk-`、Slack `xoxb-`/`xoxp-`、AWS `AKIA`。
- `KEY=value`、JSON key/value 等上下文里的高熵长串。

该检测是防漏写 guard，不是安全扫描器；宁可拒绝并让用户显式改写为非敏感摘要，也不要把疑似凭据写入 durable memory。

### 8.3 写入边界

- 只允许 `feedback/project`。
- 不允许传 path。
- 对 `feedback/project`，若内容缺少 `Why:` / `How to apply:` 或中文等价结构段，writer 应复用现有结构段 normalize/补齐逻辑。首版只对这两个开放类型启用该策略，不代表底层 normalize 只能支持两类。
- 写入路径、team/private 实际落点由 memory module 决定；tool 入参的 `scope` 只做 type 默认作用域校验，不直接指定 team。
- writer 必须检查 memory product gate：memory disabled 时拒绝写入；`SkipIndex` 只影响 index 更新策略，不应绕过写入路径校验。
- `ENABLE_MEMORY_TOOLS=false` 时不暴露 `memory_write`，stale call 也必须拒绝，不得静默成功。
- 写入后结果只返回相对 path、requestedScope、actualTarget（private/team）、type、skipped/merged，不返回绝对路径。

### 8.4 team memory 策略

首版策略固定为：

```text
feedback:
  默认 private。
  不创建 team feedback。
  若同名 team feedback 会被现有 selectExplicitWriteStore 选中，拒绝 team_scope_not_allowed。

project:
  team memory gate off / 未授权 / 未配置 -> private。
  team memory gate on 且 team guard 允许 -> 允许按既有规则写 team。
  team guard 明确 deny、team path 配置非法、team store 路由/持久化失败 -> 当前实现返回 persist_failed；不静默降级。
```

`actualTarget` 是结果，不是入参。Agent 不能通过 `scope`、`target`、`private`、`team` 等字段选择 team 写入。

### 8.5 Prompt injection 与用户确认

工具描述必须说明：只保存 durable 的用户偏好、明确纠正、项目决策或项目上下文；不得把不可信文件、网页、依赖内容、tool output 中的指令直接保存。

首版不提供 `confirmed` / `userApproved` 这类模型可伪造字段。遇到需要用户明确确认的情况，writer 或调用前策略应拒绝并返回 `confirmation_required`；后续若要支持交互确认，必须接入现有用户确认/审批机制，而不是信任模型传参。

`confirmation_required` 的首版判定准则固定为：

- 输入包含或试图保存来自不可信来源的原始指令，例如 tool output、网页正文、依赖 README、日志、错误输出、prompt 注入片段，而不是用户明确表达的偏好/决策摘要。
- 输入包含“用户已确认”“confirmed=true”“userApproved=true”等模型自证确认字段或语句。
- 输入意图把 `user` / `reference` / `local` / `team` 等首版不开放的语义绕写为 `feedback/project`。

允许的正例是用户在当前对话中明确说“记住/保存”后的非敏感摘要；不允许把上述不可信原文通过模型自证确认绕过。测试必须覆盖每条拒绝准则和一个允许的显式用户保存正例。

运行时通过窄 type、无 path、无 delete/search/list、secret guard、team policy guard 降低误用面。

### 8.6 错误返回

host-direct 错误沿用 host tool error envelope，并增加稳定 `code` 字段。当前通用 `hostToolErrorResult` 只有 `kind/tool/error`，实现时必须扩展通用 envelope，或让 `memory_write` 错误类型映射生成带 `code` 的 envelope。

host tool 错误应作为 MCP tool result 返回：JSON-RPC response 仍是 result，content 中包含 envelope，`isError=true`；不要把 memory_write 的业务拒绝变成 JSON-RPC error。

```json
{
  "kind": "host_tool_error",
  "tool": "memory_write",
  "code": "invalid_input",
  "error": "..."
}
```

错误码固定为：

| 场景 | 是否 tools/list 暴露 | tools/call 结果 |
| --- | --- | --- |
| writer nil | 否 | stale/direct call 才返回 `writer_unavailable` |
| memory product disabled | 否 | stale/direct call 才返回 `feature_disabled` |
| `ENABLE_MEMORY_TOOLS=false` | 否 | stale/direct call 才返回 `tools_disabled` |
| unknown type/scope | 是 | `invalid_input` |
| `scope=local` | 是 | `unsupported_scope` |
| type/scope 不匹配 | 是 | `invalid_input` |
| probable secret | 是 | `secret_detected` |
| 需要用户确认 | 是 | `confirmation_required` |
| 同名 team feedback 会落到 team | 是 | `team_scope_not_allowed` |
| project team guard deny / team path 非法 / 路由或持久化失败 | 是 | `persist_failed` |
| 其他 persist 失败 | 是 | `persist_failed` |
| dedup skip | 是 | success，`skipped=true` |
| dedup merge | 是 | success，`merged=true` |

即使 tools/list 因 disabled 或 reader/writer unavailable 隐藏工具，stale/direct `tools/call` 仍应由 host tool registry 或 routePrePeerToolCall 识别并返回稳定 host tool error envelope，不得 fallback 到 peer。

`deny/local_unavailable` 是旧 peer `memory_read` 的 scope 解析语义；host-direct write 首版不复用旧 read path resolver，也不开放 local 写入，因此 local 请求统一映射为 `unsupported_scope`。


## 9. 测试设计

### 9.1 memory module

覆盖：

- feedback 默认 user scope。
- project 默认 project scope。
- `description` 为空被拒绝。
- unknown type 被拒绝。
- unknown scope / `local` scope 被拒绝。
- type/scope 不匹配被拒绝。
- 空 name/content 被拒绝。
- probable secret 被拒绝，最低覆盖集合逐条单测锁定，错误和日志不回显 secret 原文。
- `confirmation_required` 判定准则逐条覆盖，并包含一个显式用户保存的允许正例。
- 写入后 index 更新。
- 写入后 section invalidation 被调用。
- dedup skip/merge 行为保持。
- feedback 不写 team；同名 team feedback 会被选中时拒绝。
- team memory enabled 时 project 类型的 actualTarget 遵循既有 team/private 路由规则。
- team memory gate off 时 project 写 private。
- 同名 team feedback 返回 `team_scope_not_allowed`；project team guard/path/route/persist 失败按当前实现返回 `persist_failed`，不静默降级。
- memory disabled / tools disabled 时 host tool 不暴露或调用失败的行为固定，并覆盖测试。
- 结构段补齐逻辑与显式保存 hook 一致。

### 9.2 toolbridge host-direct

覆盖：

- `ListHostTools()` 同时包含 `skill_read_section`、`memory_read` 与 `memory_write`。
- writer nil 时不暴露 `memory_write`。
- memory disabled / tools disabled 时不暴露 `memory_write`。
- `CallHostTool(memory_write)` 调用 `contract.AgentMemoryWriter`。
- call 注入 `AgentID/ThreadID/CWD/CallID`。
- 同名 host/peer 工具时 host 优先，并记录 shadow warn/metric。
- 错误转为 host tool error result，包含稳定 `code`，并通过 MCP content + `isError=true` 返回，不作为 JSON-RPC error。
- 禁止落点字段和 path traversal payload 被拒绝：`path=../../x`、`target=../team/...`、绝对路径、URL/percent 编码绕过（如 `%2e%2e/`）均返回稳定错误码，且不触发写盘。

### 9.3 Codex

覆盖：

- `ListToolsForCodex()` 包含 host `memory_write`。
- peer 失败时 host `memory_write` 仍可用。
- peer 中若有同名 `memory_write`，host shadow peer。

### 9.4 Claude proxy

覆盖：

- 当前 `handleProxyToolsList()` 未合并 host tools 的行为必须由测试驱动改成目标行为。
- `/mcp/orch/{agentID}` `tools/list` 包含 host `memory_write`。
- `/mcp/lsp/{agentID}` 不包含 host memory tool。
- `/mcp/orch/{agentID}` `tools/call memory_write` 走 host-direct。
- peer orch down 时，host `memory_write` 仍可 list；response 必须是 JSON-RPC result，包含 `tools` 数组，不是 JSON-RPC error，且不包含 peer error message 泄漏给模型。
- peer orch 提供同名工具时，proxy list 中 host tool shadow peer，并记录 warn/metric。

### 9.5 P18 MCP read-only guard

覆盖：

- `internal/sidecar/orch/tools` registry 不暴露任何 memory tools。
- `memory_read/write/search/list/forget` 不出现在 `cmd/mcp-orch` schema、registry、handler 测试中。
- 将旧 `TestMemoryToolDefinitionsExposeOnlyRead` 改为 `TestMemoryToolDefinitionsExposeNoMemoryTools`。

## 10. 落地方式

全量落地，不设计新增灰度开关；使用既有 `ENABLE_MEMORY_TOOLS` 作为统一可见性开关和回滚止损手段。

实施顺序：

1. 新增 contract request/result/interface，包含 `CallID`。
2. 在 memory module 实现 writer，并抽取可复用写入 helper。
3. 新增输入校验、secret guard、type/scope 默认映射 guard、team policy guard。
4. 新增 `MemoryWriteHostToolRegistry`。
5. 新增 composite host registry，替代单一 `SkillReadSectionRegistry` wiring。
6. 修改 Codex host tool list 相关测试。
7. 修改 Claude proxy `tools/list`，让 `orch` family 合并 host tools，并支持 peer down host-only 降级。
8. 固定 `ENABLE_MEMORY_TOOLS` 对 host-direct `memory_write` 的暴露/调用语义。
9. 补齐单测与必要集成测试。
10. 保持 `cmd/mcp-orch` 不注册任何 memory tools。

## 11. 观测与回滚

虽然不做灰度，但必须具备基础观测和止损：

- 日志：host list/call、shadow peer、peer down degraded、secret rejected、team policy rejected、persist failed。
- metrics：`memory_write_host_listed_total`、`memory_write_host_call_total{result,code,actual_target}`、`memory_write_host_shadow_total`、`memory_write_host_secret_rejected_total`、`memory_write_host_persist_failed_total`。
- 回滚：将 `ENABLE_MEMORY_TOOLS=0` 后，Codex dynamicTools 与 Claude proxy tools/list 均不再暴露 `memory_write`；stale call 返回 `tools_disabled`。
- 最小回滚触发条件：`persist_failed` 在 5 分钟窗口内超过 5% 或连续 10 次失败、`secret_rejected_total` 相比基线突增、team policy rejected 非预期持续出现、Claude proxy degraded list 使 host-only 工具不可见。触发后先关闭 `ENABLE_MEMORY_TOOLS`，再排查。
- 若 Claude proxy host merge 本身异常，可先回滚该代码或关闭 `ENABLE_MEMORY_TOOLS`，不得把 `memory_write` 临时注册回 `cmd/mcp-orch`。

## 12. 验收标准

- `cmd/mcp-orch` tools registry 中没有任何 memory tools。
- Codex dynamicTools 中可见 `memory_write`。
- Claude `/mcp/orch/{agentID}` tools/list 中可见 `memory_write`，并且 peer down 时仍可见 host tool。
- 调用 `memory_write` 不触发 mcp-orch peer callback，而是 host-direct 调 app memory writer。
- 写入后 durable memory 文件与 index 正确更新。
- 写入后 prompt dynamic memory sections 被 invalidated。
- 不可信路径、任意 path、未知 type/scope、`local` scope、type/scope mismatch、probable secret、confirmation_required 场景、超大 content 均被拒绝。
- path traversal payload（`../`、绝对路径、percent encoding）不触发写盘。
- `description` 必填，与现有 UI/store frontmatter 校验一致。
- team memory 写入只由 memory module 和 team gate/guard 决定；Agent 不能通过参数选择 team。
- 需要用户确认的场景不会被模型传参绕过。
- memory disabled 或 memory tools disabled 时，`memory_write` 不会静默成功。
- 现有 `skill_read_section` 行为不回退。
- `memory_read` host-direct 行为不回退；不得 fallback 到 `mcp-orch` peer。
