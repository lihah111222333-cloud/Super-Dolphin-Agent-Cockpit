# P5 方案审查 C（基础设施+契约+风险）

证据边界：
- 仓内未发现独立的 P5 RPC 方案文件；本审查以 `docs/plans/迁移/v3-migration-plan.md`、`docs/plans/迁移/v3-module-migration-details.md`、`docs/契约/jrpc2-convention.md` 和当前 V3 实现为准。
- `module/core`、`module/debug` 只出现在外部口径中，主迁移文档未给出正式模块定义；以下将其视为“待证实设计”，不会假定其边界已成立。

## 1. Server 基础设施归宿

### 已有覆盖

| 项 | 当前 V3 证据 | 结论 |
|---|---|---|
| 基本 `handler.Map` 容器 | `internal/platform/rpc/server.go:15-35`、`internal/platform/rpc/registry.go:5-13` | 只覆盖了“统一注册表”的最外层容器。 |
| 最小请求上下文 | `internal/platform/rpc/request_context.go:5-14` | 只覆盖了 `cwd` 读取，没有覆盖 V2 的连接、pending request、approval wait、UI bridge 上下文。 |
| 最小启动注入 | `internal/platform/rpc/module.go:11-18` | 只 `Provide(NewServer)`，还不是完整 RPC 平台装配。 |

### 主要遗漏

| V2 资产 | V2 职责证据 | 方案归宿 | 当前 V3 状态 | 结论 |
|---|---|---|---|---|
| `server_bootstrap.go` | `go-agent-v2/internal/apiserver/server_bootstrap.go:29-56` 初始化 provider adapter、DAG watcher、LSP tools；`:86-120` 初始化 store/workspace manager | `internal/app/app.go` + `internal/app/lifecycle.go` | `internal/app/modules.go:15-25` 只装配 `config/db/bus/rpc/thread`；未接入 `turn`、`orchestration`、`workspace`、`uistate`、`dashboard`、`skill` | 启动骨架未承接。P5 不能只迁 handler，不补启动装配。 |
| `server_conn.go` | `go-agent-v2/internal/apiserver/server_conn.go:40-101` 连接 outbox/write loop；`:136-257` notification 广播、server->client request、pending response 相关 | `internal/platform/rpc/server.go` | `internal/platform/rpc/server.go:37-52` 只有 `net.Listen + server.Loop(channel.Line)`；无连接表、无 outbox、无 pending request、无 backpressure | 连接管理和 server push 请求/响应相关逻辑整体缺失。 |
| `server_conn_ws.go` | `go-agent-v2/internal/apiserver/server_conn_ws.go:15-174` 负责 WS upgrade、ping/pong、read loop、过载分类、client response 解析 | `internal/platform/rpc/transport_ws.go` | 仓内不存在 `internal/platform/rpc/transport_ws.go` | WebSocket transport 未落位。 |
| `server_transport.go` | `go-agent-v2/internal/apiserver/server_transport.go:184-212` 提供 SSE；`:133-149` 提供 HTTP JSON-RPC | `internal/platform/rpc/server.go` | 当前 V3 无 HTTP/SSE handler | 传输层只剩 TCP line channel，浏览器/Wails 场景未承接。 |
| `server_payload.go` | `go-agent-v2/internal/apiserver/server_payload.go:15-46` 定义 `Request/Response/Notification/RPCError`；`:84-149` HTTP-RPC；`:184-212` SSE | `internal/platform/rpc/codec.go` | `internal/platform/rpc/codec.go:1-3` 只有占位注释 | codec 与 HTTP/SSE 语义没有落地。 |
| `notifications.go` | `go-agent-v2/internal/apiserver/notifications.go:10-89` 维护 provider event -> public method 的映射；配合 `server_payload.go:32-46,114-231` 做 UI mirror、节流、刷新 | `internal/platform/rpc/server.go` + `internal/module/uistate/*` | 当前无 `internal/module/uistate` 包；`internal/platform/bus/sink.go:61-85` 只记录 bus log，不做 RPC 推送 | 通知“内部事件语义”与“外部推送通道”都未完整承接。 |
| `server_approval.go` | `go-agent-v2/internal/apiserver/server_approval.go:339-483` 包含 auto-approve、dedup、pending request、Wails fallback、legacy submit | `internal/platform/rpc/*` + `internal/module/turn/service.go` | 当前 `platform/rpc` 无 push/pending request；`module/turn` 无 `review.go`/approval bridge | approval 不是一个独立 handler，可用归宿尚未形成。 |
| `internal_messages.go` | `go-agent-v2/internal/apiserver/internal_messages.go:68-162` 负责系统消息入线程/UI | `internal/module/turn/service.go` | 当前 `internal/module/turn/service.go:37-179` 只覆盖 prepare/start/interrupt/tracker | turn 服务没有承接内部消息路由。 |

### 判断

- P5 方案当前更像“方法迁移表”，不是“server 基础设施迁移表”。
- 现有 V3 `internal/platform/rpc/server.go` 只覆盖了 V2 `server.go`/`server_conn*.go`/`server_transport.go`/`server_payload.go` 的最外层监听壳，不覆盖连接、push、pending request、HTTP/SSE、approval wait、UI bridge。
- 如果不把上述基础设施显式列为 P5 交付物，P5 完成后仍无法替代 V2 app-server。

## 2. jrpc2 契约

### handler 注册方式

- 契约要求公共方法最终汇总成一个扁平 `handler.Map`，不再保留并行注册链，见 `docs/契约/jrpc2-convention.md:22-26`、`:167-170`、`:228-230`。
- 当前 `internal/platform/rpc/server.go:29-35` 与 `internal/platform/rpc/registry.go:5-13` 具备“合并 `handler.Map`”的基础形态。
- 但 `internal/platform/rpc/module.go:11-18` 没有收集 `handler.Map` 片段；`internal/platform/rpc/server.go:29-35` 的 `Register` 在仓内没有调用点；`internal/module/*` 里也没有 `handler.Map` provider。
- 结论：注册方式的设计方向对，但实现未进入“可验证”状态；当前还不能声称符合契约。

### strict binding

- 契约要求公共方法使用 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`，见 `docs/契约/jrpc2-convention.md:117`、`:238-249`、`:274-285`、`:671-673`。
- 当前 `internal/platform/rpc/handler.go:23-25` 的 `StrictBind` 直接返回 `handler.New(fn)`。
- 当前 `internal/platform/rpc/handler.go:52-56` 的 `Validate()` 是空实现。
- 当前 `internal/platform/rpc/handler.go:27-45` 的 `ThreadScope` 仍在手工反序列化 `map[string]json.RawMessage`，继续沿用 V2 风格的 request-time 手工字段校验。
- 仓内 `handler.Check(` 与 `SetStrict(true)` 的使用次数均为 0。
- 结论：当前实现不符合 strict/object-only 契约。

### 错误码映射

- 契约要求：
  - 协议层使用 `jrpc2` 标准码，见 `docs/契约/jrpc2-convention.md:579-595`。
  - 业务层使用稳定自定义 code，且避开保留区间 `-32768` 到 `-32000`，见 `docs/契约/jrpc2-convention.md:586-589`。
- 当前 `internal/platform/rpc/errors.go:5-20` 定义：
  - `CodeNotFound = -31001`
  - `CodeInvalidState = -31002`
  - `CodeConflict = -31003`
- 这些值全部落在契约明确要求避开的保留负区间。
- 结论：错误码映射与契约直接冲突。

### 方法命名

- 契约要求公共方法继续沿用 V2 的斜杠命名，见 `docs/契约/jrpc2-convention.md:22`、`:167-168`、`:230`。
- 主文档与模块文档中的示例方法名仍然是斜杠风格，如 `thread/start`、`turn/start`、`review/start`、`skills/list`。
- 当前 V3 还没有实际注册任何公共方法，因此该项只能判为“方案口径正确，代码未验证”。

### 额外契约缺口

- 契约对 server push 的要求是 `Server.Notify`/`ClientOptions.OnNotify`，且 transport 必须支持并打开 `AllowPush=true`，见 `docs/契约/jrpc2-convention.md:482-495`。
- 当前 `internal/platform/rpc/server.go:46-52` 没有设置 `AllowPush`，也没有任何 `Notify`/`OnNotify`/`OnCallback` 配套代码。
- 结论：notification/push 契约当前也未满足。

## 3. 新模块风险

### 模块必要性判断

| 模块 | 是否需要完整 `module + contract + service + rpc` | 依据 | 风险与建议 |
|---|---|---|---|
| `skill` | 需要 | `docs/plans/迁移/v3-module-migration-details.md:190-205` 明确给出 `module.go/contract.go/service.go/loader.go/matcher.go/helpers.go/rpc.go`；该域本身有独立公共方法面 | 合理，但当前仓内不存在 `internal/module/skill` 包，仍是纯规划。 |
| `config` | 不需要作为业务模块独立存在 | 全局配置在 `docs/plans/迁移/v3-module-migration-details.md:921-974` 明确属于 `platform/config`；线程配置在 `docs/plans/迁移/v3-module-migration-details.md:63-74` 明确属于 `module/thread/config.go`；当前 `internal/module/thread/command.go:19-25,54-65` 也已把 `/model`、`/personality`、`/approvals` 收敛到 `ThreadConfigPatch` | 不应新建 `module/config`；应保留 `platform/config` + `module/thread/config` 双层边界。 |
| `uistate` | 不适合机械套用完整业务四件套 | 文档把它定义为 projection/runtime 适配层，见 `docs/plans/迁移/v3-module-migration-details.md:387-401`；其核心不是“事务型领域服务” | 更像 `runtime + projection + rpc_bridge`，而不是“有独立真相状态和 store 的业务模块”。 |
| `dashboard` | 部分需要，但不是传统领域四件套 | 文档把它定义成 read-model 聚合查询，见 `docs/plans/迁移/v3-module-migration-details.md:564-577`；并明确“读模型跨 10+ store，最容易重新形成隐式 god service”，见 `:584-587` | 需要 `service + projection + rpc`，但不应强行伪造“独立领域真相”或“独立 store”。 |
| `core` | 不合理 | 主迁移文档和模块明细中不存在 `module/core` 定义；approval/review/event/config 已分别归入 `turn`、`thread`、`platform/rpc`、`platform/config` | 极易把零散横切逻辑重新吸成 God module。 |
| `debug` | 不应作为正式业务模块 | 模块文档只把 `thread/debugMemory` 标成“删除，不进入正式 V3 RPC 契约”，见 `docs/plans/迁移/v3-module-migration-details.md:77` | debug 更适合作为本地入口或 `platform/rpc` 下的受限 surface，不应扩展成独立业务模块。 |

### store 支撑判断

- 当前 `internal/store` 下存在 `thread`、`binding`、`workspace`、`commandcard`、`prompt`、`auditlog` 等包，但不存在 `uistate`、`dashboard`、`config` 对应的 store 包。
- 与 UI 偏好最接近的现状只有 `internal/store/sqlc/query_ui_preference.go:9-11`，即底层 SQL 常量；没有 UI preference facade/store 包。
- `docs/plans/迁移/v3-module-migration-details.md:398` 也只写了 `uistate` “间接依赖 UI preference store 和可能的 lightweight read-model store”，没有给出现成持久层落点。
- `docs/plans/迁移/v3-module-migration-details.md:574` 明确写了 `dashboard` “通过 dashboard 相关 store 接口读取聚合数据”，但仓内当前并无这类 store 包。
- 结论：
  - `skill` 不依赖 DB，为文件系统域，可独立。
  - `config` 不需要 store 驱动的业务模块。
  - `uistate` 和 `dashboard` 现阶段没有“完整 store 支撑”；如果强行上完整四件套，只会制造假边界和新 God service。

## 4. approval

### V2 不是“只有一个 approval RPC”

- V2 对外公开 RPC 的确只有 `approval/respond`，见 `go-agent-v2/internal/apiserver/methods.go:157-163`。
- 但 `approval/respond` 只是前端回调入口，见 `go-agent-v2/internal/apiserver/server_approval.go:456-483`。
- 真正的 approval 复杂度在请求侧状态机：
  - 三类 approval method：`item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`skill/requestApproval`，见 `go-agent-v2/internal/apiserver/server_approval.go:15-17`。
  - decision 归一化与 fallback：`:148-310`。
  - 子 agent 自动审批：`:339-349`。
  - Wails 无 WS 客户端时的 pending request + 5 分钟等待：`:351-382`。
  - 去重与 inflight 状态：`:413-423`。
  - 通过 WS client 请求前端做决策：`:425-439`。
  - `RespondResultFunc` 与 legacy submit 双路径收尾：`:441-454`。
- 结论：V2 approval 是完整状态机，不是单一 RPC method。

### `request_user_input` 与 approval 直接耦合

- `go-agent-v2/internal/apiserver/server_event_handler.go:221-233` 明确把 `request_user_input` 桥接到统一 approval 通道：
  - 自动响应时直接 `notify + autoRespondUserInput`
  - 非自动响应时调用 `handleApprovalRequest(... approvalMethodCommandExecution ...)`
- 这说明 V2 中“用户输入请求”和“approval”共享同一条前端等待/回调链路。

### review 与 approval 的关系

- 主迁移文档明确写了：
  - `review` 推迟到 P5，见 `docs/plans/迁移/v3-migration-plan.md:1174`
  - `request_user_input` 是 `service/turn/review.go` 可消费事件，见 `docs/plans/迁移/v3-migration-plan.md:914`
- 模块文档明确把：
  - `internal/module/turn/review.go` 列为目标文件，见 `docs/plans/迁移/v3-module-migration-details.md:123-131`
  - `review/start` 定义为 turn 派生流程，见 `docs/plans/迁移/v3-module-migration-details.md:138-149`
  - turn 模块订阅 approval 事件，见 `docs/plans/迁移/v3-module-migration-details.md:139-170`
- 当前 V3 现状：
  - `internal/module/turn` 下没有 `review.go`
  - `internal/contract/provider.go:23-37` 的 `Session` 没有 `ReviewStart` 或等价接口
  - `internal/dto/agent/state.go:14,28-29,90-95` 已经存在 `awaiting_user_input` 和 `user_input_requested/resolved` 状态机
  - `internal/dto/tool/event.go:20-38` 已定义 `ToolApprovalRequested/Resolved`
- 结论：
  - review 与 approval 不是两个可独立迁移的面。
  - 把 `approval/respond` 单独放入 `module/core` 无法覆盖 request side state machine，也无法覆盖 `request_user_input -> review -> approval resolve -> turn resume` 这条链。
  - 正确归宿应至少横跨 `platform/rpc`（push/回调）、`module/turn`（review/恢复运行）、`dto/agent` 状态机。

## 5. notification

### V2 notification 的真实语义

- V2 notification 不是 bus event；它是 server -> client push。
- `go-agent-v2/internal/apiserver/server_event_handler.go:19-59` 把 provider event 标准化后调用 `notify(...)`。
- `go-agent-v2/internal/apiserver/notifications.go:10-89` 只负责 `event.Type -> public RPC method` 映射。
- 真正的推送由 `go-agent-v2/internal/apiserver/server_conn.go:136-170` 完成：
  - 调用 `notifyHook`
  - 广播给 SSE clients
  - 广播给 WebSocket connections
- 需要客户端响应时，V2 还会通过 `go-agent-v2/internal/apiserver/server_conn.go:196-257` 发起 server -> client request，并等待 `ResolvePendingRequest`。
- `go-agent-v2/internal/apiserver/server_transport.go:184-212` 提供 SSE 长连接。
- 结论：V2 notification 本质是 transport 层的 server push，不是纯内存事件总线。

### `kelindar/event` 只能替换内部分发，不能替换外部推送

- 当前 V3 确实已有 typed bus：
  - `internal/platform/bus/bus.go:5-23`
  - `internal/platform/bus/module.go:10-35`
  - `internal/platform/bus/sink.go:61-85` 订阅 tool/UI 等事件
- 当前 V3 也已有 approval/UI typed event：
  - `internal/dto/tool/event.go:20-38`
  - `internal/dto/ui/event.go`（事件类型由 `internal/dto/shared/event.go:34-37` 标识）
- 但 `kelindar/event` 只解决“服务内谁收到事件”，不解决“远端客户端如何收到事件”。
- 当前 `internal/platform/rpc`：
  - 不依赖 `*event.Dispatcher`
  - 不存在 push bridge
  - 不存在 `transport_ws.go`
  - 不存在 `AllowPush`
  - 不存在 `ClientOptions.OnNotify`/`OnCallback` 对应支持

### jrpc2 视角下的缺口

- 契约要求 server push 使用 `Server.Notify`/`ClientOptions.OnNotify`，并打开 `AllowPush=true`，见 `docs/契约/jrpc2-convention.md:482-495`。
- 当前 `internal/platform/rpc/server.go:46-52` 只设置了 `Logger`，没有 `AllowPush`。
- 当前 transport 是 `channel.Line` over TCP，不是浏览器/Wails 友好的 WS 通道。
- 结论：
  - “V3 用 `kelindar/event` 替代 notification”只能成立一半。
  - 若不补 `platform/rpc` push bridge，V3 无法替代 V2 的 server->client notification 和 approval request。

## 6. 结论

### Blocker

- P5 目前只把焦点放在“129 个方法进入 `handler.Map`”，没有把 V2 app-server 的基础设施主体列为独立交付物。缺口至少包括：启动装配、WS/SSE transport、pending request 相关、approval wait loop、backpressure/overload、HTTP/SSE/codec、server push。
- 当前 `internal/platform/rpc` 与 `docs/契约/jrpc2-convention.md` 明确冲突：`StrictBind` 仍用 `handler.New`，`Validate()` 为空，仓内 0 次使用 `handler.Check(...).AllowArray(false).SetStrict(true)`，业务错误码还占用了保留负区间。
- approval 不能按“只迁 `approval/respond`”来定义范围。V2 真实复杂度在 request side state machine；`request_user_input`、review、approval、turn resume 是同一条链。
- `module/core`、`module/debug` 在主文档中没有正式边界。若以它们为归宿吸收 approval/debug/misc surface，极高概率重建 V2 的 God Server/God Service。
- `config` 作为独立业务模块与现有文档冲突：全局配置属于 `platform/config`，线程配置属于 `module/thread/config.go`。再建一层 `module/config` 会形成双重真相源。

### Improvement

- 把 P5 的 Done 标准从“129 methods migrated”改成“两张表同时完成”：
  - 方法表：method -> request/response -> owner module -> contract test
  - 基础设施表：V2 infra file -> V3 owner package -> transport contract -> protocol test
- `platform/rpc` 需要先补齐最小平台骨架，再谈方法迁移：
  - 严格 binder：统一改为 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`
  - 错误码：协议层用标准码，业务层改为正向稳定自定义码，如 `1001/1002/1003`
  - push bridge：明确 `AllowPush`、WS transport、pending request manager、client notify/callback 契约
  - handler 收集：用 FX value-group 收集各模块 `handler.Map`，禁止 `Server.Register` 手工散调
- approval 的归宿应明确拆成三段：
  - `platform/rpc`：server->client push、pending request、response correlation
  - `module/turn`：review、`request_user_input` 消费、resume/resolve 流程
  - `dto/agent`/bus：`awaiting_user_input` 状态与 approval event 映射
- 模块边界建议收敛为：
  - 保留 `module/skill`
  - 保留 `module/uistate` 和 `module/dashboard`，但明确它们是 projection/read-model，不要求伪造独立真相 store
  - 保留 `platform/config`，不要再建 `module/config`
  - 不新增 `module/core`、`module/debug`
