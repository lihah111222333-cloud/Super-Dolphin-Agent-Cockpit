# MCP 独立服务契约

## 0. 核心层职责边界

agent-terminal 核心层只承担四类职责：
1. Agent 生命周期管理
2. MCP manifest 构建与注入
3. UI / approval / hook 编排
4. 暴露 `ctl/*` v1 基线接口和 `ctl/hook/*` v2 扩展接口（供外部启动的 MCP binary register / heartbeat / hook subscribe / hook resolve / shutdown）

核心层不负责启动、托管或计数 MCP 进程；manifest 只描述“有哪些 MCP 工具可用 + 如何启动它们”，实际拉起由 Claude CLI 等外部执行器决定。MCP 进程是共享服务，`agent_id` 必须从 tool call 参数传入，而不是在 bootstrap 阶段绑定固定 agent。

其他能力继续按 MCP 独立 binary 落地：
- `cmd/mcp-orch`：编排、DAG、workspace、prompt、command card、shared file
- `cmd/mcp-lsp`：LSP 代码工具
- `cmd/mcp-ida`：IDA 逆向工具

P8.5 之后必须明确区分两条并存且不矛盾的通道：
- 工具执行通道：`stdio`，面向 manifest 消费方（如 Claude CLI）的 MCP tool call，自包含闭环，不承载注册、心跳、审批恢复或退出控制
- 生命周期管理通道：`jrpc2` over TCP，承载 `ctl/*` 控制协议，用于注册、lease、心跳、上下文、遥测、审批、hook 回调 / resolve、配置推送和优雅退出

这条边界决定了约束：
- `stdio` 是工具执行面，不是 control plane
- `ctl/*` 是 lifecycle/control plane，不是 MCP 标准工具方法
- `ctl/hook/*` 是 `ctl/*` 的扩展族，不是第三条独立通道
- 同一 binary 可以同时持有两条连接，但职责不能混写

## 1. 目标态

V3 的目标契约是：

- `cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 都是独立二进制
- 每个 binary 都对 manifest 消费方 / 外部执行器暴露本地 `stdio` MCP 服务
- 每个 binary 同时通过共享 bootstrap client 回连核心 `jrpc2` TCP 生命周期通道
- 生命周期通道统一使用 `ctl/*` 命名，不再使用 `mcp/*`
- 核心侧 lifecycle registry / handler / fanout / lease 管理统一落在 `internal/platform/mcpcontrol/*`
- 工具侧共享 bootstrap client 落在 `internal/mcpserver/runtime/bootstrap/*`
- DTO 统一落在 `internal/dto/mcp/*`
- 接口契约统一落在 `internal/contract/*`
- 规模目标调整为 20-30 个活跃实例，而不是 30-50

目标态下的职责划分：

- `internal/platform/rpc`：只负责 `jrpc2` transport、连接 accept、通用 push helper
- `internal/platform/mcpcontrol`：负责 lease、peer 分类、`ctl/*` handler、定向 fanout、错误映射
- `internal/platform/hooks`：负责核心层 hooks 拦截点、merge、pending review、TTL、resolve 收敛
- `internal/mcpserver/runtime/bootstrap`：负责 env 解析、register/heartbeat/reconnect、shutdown hook、boot snapshot fallback
- `cmd/mcp-*`：负责各自工具执行逻辑和本地 stdio server

### 1.1 当前代码现状

当前仓库还没有达到上述目标态：

- `internal/platform/rpc/server.go` 只有 active 连接集合和 `NotifyAll`，没有 `peer_kind` 分类
- `internal/platform/rpc/push.go` 已有单连接 `NotifyClient` / `CallbackClient`
- `internal/platform/rpc/approval.go` 已有 pending approval 生命周期，但恢复目标还没有限制到 `ui` 连接
- `internal/dto/provider/manifest.go` 只注入了有限 `GO_AGENT_MCP_*` env
- `internal/mcpserver/runtime/server.go` 仍是骨架，尚未形成 `stdio serve + rpc bootstrap` 协调启动

因此本文描述的是目标契约，不表示当前实现已经落地。

### 1.2 DTO 迁移映射

`ctl/register` 不再沿用旧的裸 `lease_id` 字符串；server-issued lease 统一抽象为 `LeaseKey{instance_id, generation}`。

```json
// 旧: {"lease_id": "xxx"}
// 新: {"instance_id": "xxx", "generation": 123}
```

补充约束：

- 所有 `ctl/register` 之后的方法都把 `LeaseKey` 展开为两个顶层 JSON 字段：`instance_id` 和 `generation`；不再使用 `lease: { ... }` 嵌套对象。
- `RegisterResponse.accepted_generation` 就是 lease 的 `generation`；客户端据此重建本地 `LeaseKey`。
- `RegisterRequest` 新增字段固定为：`session_token`、`boot_id`、`capabilities_offered`、`capabilities_required`、`resume_from_generation`、`peer_kind`。

## 2. 架构边界

| 位置 | 职责 | 禁止事项 |
| --- | --- | --- |
| `cmd/mcp-*` | 本地工具执行、stdio MCP server、本地 business logic | 不得承载核心 lease registry 或 UI approval restore |
| `internal/mcpserver/runtime/bootstrap/*` | 工具侧 bootstrap client、register/heartbeat/reconnect/shutdown 协调 | 不得依赖核心 service、store 或 `internal/platform/mcpcontrol` 实现 |
| `internal/platform/rpc/*` | TCP `jrpc2` transport、连接 accept、`AllowPush`、通用 push helper | 不得继续堆放 `tool_registry` / `tool_callback` / lifecycle 业务路由 |
| `internal/platform/mcpcontrol/*` | `LeaseKey` 注册表、`ctl/*` handlers、selector fanout、peer 分类、错误码 | 不得承载 stdio server、manifest 生成或具体 tool family 逻辑 |
| `internal/platform/hooks/*` | 核心层 hooks 拦截点、合并、TTL、`pending_hook_review`、`resolve` 收敛 | 不得承载 DAG / 命令校验 / reviewer agent 等领域策略 |
| `internal/dto/mcp/*` | `ctl/*` DTO、判别联合、错误常量、selector 结构 | 不得承载 transport、handler、store |
| `internal/contract/*` | `mcpcontrol` 依赖的 service interfaces | 不得承载 `jrpc2` server/client 细节 |
| `internal/module/*` / `internal/store/*` | 核心 UI / store / domain service | 不得反向 import `cmd/mcp-*` |

补充规则：

- 工具执行通道只处理 MCP manifest、tool list、tool call、tool response
- 生命周期管理通道只处理 `ctl/*` 基线 9 方法，以及协商启用后的 `ctl/hook/subscribe`、`ctl/hook/before`、`ctl/hook/check`、`ctl/hook/after`、`ctl/hook/resolve`
- `ctl/context` scope 固定为 `agent.runtime`、`thread.binding`、`workspace.run`、`config.snapshot`
- hooks 不得偷偷给 `ctl/context` 扩 turn / wakeup 查询；这类恢复必须由 MCP 工具的本地 store 处理
- `ctl/event` 只做遥测 / 审计 fire-and-forget
- `ctl/report` 只做持久化状态迁移，`report_type=event` 删除，`progress` / `diagnostic` 仅预留扩展标签
- `ctl/log` 与 `ctl/event` 不合并：`ctl/log` 有 `level` / `seq` 语义并面向结构化 `slog` 消费者；`ctl/event` 有 `audit_class` 语义并面向审计消费者，消费路径不同
- hook 扩展仍属于 lifecycle/control plane，不进入 `stdio` 工具执行通道
- 核心层只负责 `ctl/hook/*` 的订阅、回调、合并和 pending review 状态；具体审批 / 策略判断仍在 MCP 工具侧
- `ctl/hook/after` 返回 `escalate` 时，只能通过生命周期通道上的 `ctl/hook/resolve` 异步收敛，`stdio` tool call 不得直接提交终态
- 核心层 hooks = 拦截点 + `ctl/hook/*` transport；MCP 侧 lifecycle adapter = 消费这些 hooks 的领域逻辑；两层抽象必须分开
- turn 相关事件进入 MCP 的唯一路径是核心主动发起 `ctl/hook/before` / `ctl/hook/check` / `ctl/hook/after` 回调；不新增旁路推送
- `SubmitTurn` 是核心内部 orchestration 动作；MCP 只能在 hook 返回里给出 `dispatch_intent`，由核心验证、持久化并执行
- `LeaseKey` 是 binary 身份与路由主键；hook payload 中的 `agent_id` / `thread_id` / `turn_id` 才是逻辑对象身份；MCP 进程是共享服务，`agent_id` 只从 tool call 参数传入
- `agent.process.exit` / `process_exited` 类 hook topic 只能由核心进程管理模块在检测到子进程退出后生成，不接受 MCP 工具自报

## 3. 依赖方向

### 3.1 `cmd/mcp-orch`

允许依赖：

- `internal/platform/config`
- `internal/platform/db`
- `internal/contract/*`
- `internal/dto/*`
- `internal/mcpserver/runtime/bootstrap/*`
- `internal/sidecar/orch/orchestration/*`
- `internal/sidecar/orch/store/sqlc/*`
- `internal/sidecar/orch/store/*`
- `internal/sidecar/orch/tools/*`
- `cmd/mcp-orch` 本地 stdio server / manifest 组装

禁止依赖：

- 其他 `cmd/*`
- `internal/app`
- `internal/ui/*`
- `internal/module/*`
- `internal/store/*`
- `internal/store/sqlc/*`
- `internal/platform/mcpcontrol/*`
- `internal/platform/rpc` 以外任何宿主 handler bridge

原因很直接：

- `cmd/mcp-orch` 是工具执行 binary，不是核心 lifecycle owner
- 它只消费 bootstrap client 暴露的 `ctl/*` client API，不直接依赖核心侧 registry/handler 实现

### 3.2 单向依赖与 cleanup 规则

- `internal/module/*`、`internal/store/*`、`internal/platform/*`、`internal/contract/*`、`internal/dto/*` 都不得反向 import `cmd/mcp-*`
- 所有 lifecycle 索引都必须以 `LeaseKey{instance_id, generation}` 为主键，不再以裸 `instance_id` 做活体身份
- 所有连接都必须带 `peer_kind`，取值固定为 `ui` 或 `tool`
- `rpc.Server` 的连接元数据必须持有 `peer_kind`；tool 连接从 `ctl/register` 写入，UI 连接沿用既有握手链路标记
- pending approval 恢复只允许投递给 `peer_kind=ui` 的连接
- `mcpcontrol` 只能通过 `contract.ApprovalBroker` 使用审批能力，不直接 import `rpc` 包实现；依赖方向固定为 `mcpcontrol -> contract.ApprovalBroker <- rpc.ApprovalManager`
- 新控制协议统一使用 `ctl/*` 命名；旧 `mcp/*` 仅作为迁移期别名
- 生命周期方法不做精确版本命中，而做 capabilities 协商

旧协议 deprecation 时间表：

- `2026-03-22`：文档冻结，新增实现只允许写 `ctl/*`
- `2026-04-15`：核心保留 `mcp/* -> ctl/*` 兼容别名，并对别名调用打 deprecation log
- `2026-05-31`：仓库内 first-party binaries 全部切换到 `ctl/*` 和 `GO_AGENT_CTL_*`
- `2026-06-30`：删除 `mcp/*` 别名；`GO_AGENT_MCP_*` 仅保留只读兼容，不再新增消费者

说明：

- 这里的时间表只针对 lifecycle/control plane
- 既有 `agent.*` / `orchestration.*` 核心 RPC 不在本次 deprecation 范围内

## 4. MCP 双通道模型

`cmd/mcp-*` 必须采用双通道：

| 通道 | 传输 | owner | 负责内容 | 明确不负责 |
| --- | --- | --- | --- | --- |
| 工具执行通道 | `stdio` | tool binary | MCP manifest、tool list、tool call、tool response | register、heartbeat、approval restore、shutdown control |
| 生命周期管理通道 | `jrpc2` TCP | bootstrap client + core control plane | `ctl/*` 基线 9 方法 + hook 扩展 5 方法、lease、config push、approval、durable report | 外部执行器的 tool call 执行 |

若外部执行器采用 manifest，这两条通道的协同顺序通常是：

1. Claude CLI 等外部执行器根据 manifest 拉起 `cmd/mcp-*`（或以其他等价方式启动）
2. binary 先拉起本地 `stdio` MCP server，保证工具执行面就绪
3. bootstrap client 读取 `GO_AGENT_CTL_*` env 和 boot snapshot
4. bootstrap client 连接核心 `jrpc2` TCP 生命周期通道
5. 工具发送 `ctl/register`，拿到 server-issued lease
6. 心跳循环开始，核心可下发 `ctl/config/changed` / `ctl/shutdown`

bootstrap env 主命名固定为：

- `GO_AGENT_CTL_RPC_ADDR`
- `GO_AGENT_CTL_INSTANCE_ID`
- `GO_AGENT_CTL_THREAD_ID`
- `GO_AGENT_CTL_BINARY_NAME`
- `GO_AGENT_CTL_BOOTSTRAP_JSON`

迁移期兼容读取但标记 deprecated：

- `RPC_ADDR`
- `GO_AGENT_MCP_INSTANCE_ID`
- `GO_AGENT_MCP_THREAD_ID`
- `GO_AGENT_MCP_BINARY_NAME`
- `GO_AGENT_MCP_BOOT_CONTEXT`

容错分层固定为：

- 工具侧：`live RPC > boot snapshot`
- 核心侧：`live registry > durable DB rebuild`

进一步约束：

- `ctl/config/changed` 的 selector 语义固定为 `intersection(subscription, capability, scope)`
- 下行发送端统一落在 `internal/platform/mcpcontrol/router.go`
- `ctl/config/changed` 与 `ctl/shutdown` 都由 router 负责发送
- fanout 必须按 peer 拷贝目标列表后并发发送，禁止持锁网络 I/O
- fanout 必须使用有界 worker pool，`parallelism=8`
- 每个 peer 都有独立 `send_timeout=2s`
- v1 不支持通用操作级 cancel；只有 deadline 超时和 `ctl/shutdown` 两种中止语义

`ctl/shutdown` 在 callback 成功时返回：

```text
ShutdownResponse {
    acknowledged: bool
    final_report_deadline_ms: int  // 工具有多少时间补发最终 report
}
```

退出语义固定为：

- 没有显式的 `ctl/unregister` 方法
- 正常退出：停 heartbeat，补最终 completion report，再断连；server 侧由 sweeper 清理
- 异常退出：依赖 sweeper 在 heartbeat timeout 后清理
- completion report 的 `status=done` 隐含“此 lease 已完成使命”语义

`ctl/report` handler 的分流固定为：

- `runtime` -> `orchestration.UpdateRuntime`
- `completion` -> `orchestration.SetReport`
- `progress` / `diagnostic` -> 预留，只有协商到对应 capability 后才能启用
- `report_id` 为幂等键；server 维护最近 `N` 个 `report_id` 去重

## 5. 守卫标准

新写 hand-written 代码默认纳入统一守卫：

- 单文件 `<= 400` 行
- 单函数 `<= 80` 行
- 圈复杂度 `<= 10`
- `MaxPackageFiles = 15`
- `MaxPackageLines = 3000`

P8.5 明确新增 `internal/platform/mcpcontrol/*` 的原因之一，就是避免继续把 lifecycle 业务塞进已经拥挤的 `internal/platform/rpc`。

### 5.1 Input enum 校验（ADR-003）

MCP server 不强制按 input_schema 验证调用入参（避免引 jsonschema 库 +
避免 wire breaking 70+ tool），因此 schema 里声明的 `enum` 不会在框架
层兜底。为防止脏值穿到 service / store / DB，所有 enum 字段必须由 handler
层显式校验，并满足以下规约：

- enum 候选值提到**包级 `var`** 单源（命名 `<tool>_<field>_Enum`），schema
  与 handler 共用同一切片；改动一处必须同步另一处，单测覆盖。
- handler 层用 `tools.requireEnum(value, field, allowed)` 兜底校验。可选
  字段需在 handler 内手动处理空串放行，必填字段直接 `requireEnum` 即可
  （内置必填检查）。
- enum 值同时落 DB CHECK（migration 0080/0081/0082 模板），形成
  「schema enum + handler requireEnum + DB CHECK」三层互锁；任何单层失守，
  其余两层兜底。
- 错误消息中英双语，列出 allowed 候选，对齐 P19 错误约定与
  `translateStartDAGError` 风格。

参见 `docs/decisions/ADR-003-mcp-input-enum-validation.md`。

## 6. 落地检查清单

- 是否明确区分了 `stdio` 工具执行通道和 `jrpc2` 生命周期管理通道
- 是否所有新 lifecycle 方法都使用 `ctl/*`，而不是 `mcp/*`
- 是否完整落地基线 9 个方法；若启用 hooks，是否补齐 5 个 hook 扩展方法，而不是分 Phase 1 / Phase 2
- 是否所有活体索引都升级为 `LeaseKey{instance_id, generation}`
- 是否给连接补了 `peer_kind=ui|tool`
- 是否把 `tool_registry` / `tool_callback` 放进 `internal/platform/mcpcontrol/*`
- 是否把 bootstrap client 放进 `internal/mcpserver/runtime/bootstrap/*`，且依赖只含 `jrpc2 + dto + stdlib`
- 是否把 DTO 放进 `internal/dto/mcp/*`，接口放进 `internal/contract/*`
- 是否删掉了 `report_type=event`，并把 `ctl/event` / `ctl/report` 职责彻底拆开
- 是否把 `ctl/context` scope 收窄到 `agent.runtime`、`thread.binding`、`workspace.run`、`config.snapshot`
- 是否采用 capabilities 协商，而不是精确版本命中
- 是否为每个方法定义了幂等键、重试、degrade 行为和标准错误码
- 是否只向 `ui` 连接恢复 pending approval
- 是否统一使用 `GO_AGENT_CTL_*`，并仅把 `GO_AGENT_MCP_*` / `RPC_ADDR` 作为 deprecated 兼容读取
- 是否明确没有 `ctl/unregister`，并以 final completion report + disconnect + sweeper 作为退出语义
- 是否补齐 reconnect / 重注册 lease 语义、heartbeat jitter、连续失败策略、per-peer timeout 和 fanout 并发
