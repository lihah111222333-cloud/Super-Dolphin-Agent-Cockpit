# P5 波次 0 审查 B — V2 对照 + push 通道 + 互辩结论验证

## 审查方法

- 代码读取全部使用 LSP：`internal/platform/rpc/{push.go,codec.go,transport_ws.go,module.go,server.go}`、`internal/app/modules.go`、`go-agent-v2/internal/apiserver/{notifications.go,server_conn_ws.go,server_payload.go,server_conn.go,server_state_groups.go,server_approval.go,server_transport.go}`。
- 编译验证：
  - `go test ./internal/platform/rpc ./internal/app` 通过。
  - 临时加入 `fx.ValidateApp(Module)` 测试后执行 `go test ./internal/app -run TestTmpValidateApp -count=1`，通过；测试文件已删除。
- 行数验证：
  - `git status --short -- internal/platform/rpc internal/app/modules.go` 显示 `internal/platform/rpc/` 与 `internal/app/modules.go` 当前均为未跟踪新增。
  - `wc -l internal/platform/rpc/*.go internal/app/modules.go` 结果为 `531` 行。

## 结论总表

| 维度 | 结论 | 说明 |
| --- | --- | --- |
| 1. R0a 源量基线 ~1,300 -> <=650 | 通过 | 当前可观测新增 `531` 行，预算内；但“预算内”不等于“能力已齐”。 |
| 2. `jrpc2 Notify + Callback` 双通道 | 部分通过 | `push.go` 同时提供 `NotifyClient` / `CallbackClient`；但仓内没有调用点，也没有 `OnNotify` / `OnCallback` 客户端配套。 |
| 3. codec 不复制 V2 | 通过 | `codec.go` 仅 `22` 行；TCP/WS 主 JSON-RPC 编解码已交给 `jrpc2`。 |
| 4. R0b optional 注入 | 通过 | `rpc.Module` 已形成 `handler.Map` value-group + `fx.Invoke` 闭环；`internal/app/modules.go` 可编译且 `fx.ValidateApp` 通过。未使用 `optional:"true"`，但空 group 仍可闭环。 |
| 5. V2 `notifications.go` 对照 | 未通过 | V3 `push.go` 没有落地 V2 的 event->method 映射表与 fallback。 |
| 6. V2 `server_conn_ws.go` 对照 | 部分通过 | V3 有最小 WS adapter，但未接入主运行链路，且缺连接上限、read deadline、pong/ping、背压/过载处理。 |
| 7. V2 `server_payload.go` 对照 | 未通过 | 当前 `codec.go` 只保留成功/失败包装；V2 的 payload shaping、UI/runtime/legacy mirror 逻辑均未落地。 |
| 8. 并发安全 | 部分通过 | `PushBridge` 本身无共享可变状态，底层 `jrpc2.Server` 与 `wsChannel` 有锁；但 V3 尚无 `approval.go` / pending manager，ApprovalManager 无从验证是否已落地。 |

## 1. R0a 源量基线 ~1,300 -> <=650

- 按当前工作树的可观测新增口径，`internal/platform/rpc/*.go` + `internal/app/modules.go` 共 `531` 行，低于 `<=650`。
- 若只看本轮直接对应互辩结论的文件，`push.go` + `codec.go` + `transport_ws.go` + `module.go` + `server.go` + `internal/app/modules.go` 共 `345` 行。
- 结论：行数预算成立。
- 备注：当前压缩主要来自两点，而不是“完整等价迁移”：
  - JSON-RPC 线协议已委托给 `jrpc2`，没有复制 V2 手写 envelope。
  - V2 `server_payload.go` 的 payload shaping / UI side effect / legacy mirror 没有迁入当前树。

## 2. `jrpc2 Notify + Callback` 双通道

- `internal/platform/rpc/push.go:25-47` 已同时提供：
  - `NotifyClient(ctx, server, method, params)` -> `server.Notify(...)`
  - `CallbackClient(ctx, server, method, params)` -> `server.Callback(...)` + `resp.UnmarshalResult(&raw)`
- `internal/platform/rpc/server.go:60-72` 的 `prepareServerOptions` 已统一打开 `AllowPush=true`。
- 但当前落地只到 API 层：
  - 全仓 LSP 搜索 `BindEventToNotify(`、`WSHandler(`、`PayloadEncoder` 仅命中定义处。
  - 全仓 LSP 搜索 `OnNotify` / `OnCallback`，`internal/` 无实现命中，只有文档命中。
- 结论：
  - “双通道 API 是否存在” -> 是。
  - “push 通道是否已形成端到端链路” -> 否。

## 3. codec 不复制 V2

- `internal/platform/rpc/codec.go:1-22` 只有 `PayloadEncoder.WrapSuccess` / `WrapError`，总计 `22` 行，满足 `<=100`。
- `jrpc2` 内置 JSON-RPC 已被正确使用：
  - TCP 主链路：`internal/platform/rpc/server.go:37-58` 通过 `server.Loop(..., channel.Line, ..., prepareServerOptions(nil))` 启动。
  - WS 适配：`internal/platform/rpc/transport_ws.go:22-43` 通过 `jrpc2.NewServer(...).Start(ch)` 直接把 `wsChannel` 交给 `jrpc2`。
- V2 的 JSON-RPC wrapper 实际集中在 `go-agent-v2/internal/apiserver/server_transport.go:15-86`，当前 V3 没有复制这层；这部分由 `jrpc2` 取代，方向正确。
- 但当前 `codec.go` 也是“极薄 helper”，不是 V2 `server_payload.go` 的能力替代。

## 4. R0b optional 注入

- `internal/platform/rpc/module.go:12-18` 已将 `NewServer`、`NewPushBridge` 注入 `rpc.Module`，并增加 `fx.Invoke(registerAllHandlers)`。
- `internal/platform/rpc/module.go:27-43` 已引入 `handler.Map` value-group：
  - `HandlerMapResult.Handlers handler.Map 'group:"rpc_handlers"'`
  - `serverParams.Handlers []handler.Map 'group:"rpc_handlers"'`
  - `registerAllHandlers(server, p)` 统一执行 `server.Register(p.Handlers...)`
- `internal/app/modules.go:20-35` 已接入 `rpc.Module`。
- 当前全仓 LSP 搜索 `group:"rpc_handlers"` 只命中 `internal/platform/rpc/module.go`，说明还没有业务模块产出 handler 片段。
- 尽管如此：
  - `go test ./internal/platform/rpc ./internal/app` 通过。
  - `fx.ValidateApp(app.Module)` 验证通过。
- `optional:"true"` 现状：
  - `internal/platform/rpc/module.go` 与 `internal/app/modules.go` 都没有 `optional:"true"`。
  - 全仓唯一相关命中是 `internal/module/thread/module.go:5-11` 的 `fx.ParamTags("", 'optional:"true"', 'optional:"true"', 'optional:"true"')`，与 `rpc_handlers` group 无关。
- 结论：
  - R0b 的闭环已经落地。
  - 这里不需要 `optional:"true"`；空 `group:"rpc_handlers"` 仍可闭环，验证结果已证明这一点。

## 5. V2 `notifications.go` 对照

- V2 `go-agent-v2/internal/apiserver/notifications.go:10-100` 的核心能力是：
  - `eventMethodMap` 维护 provider event -> public RPC method 映射。
  - `mapEventToMethod(eventType)` 提供 fallback：
    - 命中映射则返回映射值。
    - 事件名本身含 `/` 则直接透传。
    - 否则退回 `agent/event/` 前缀。
- V3 `internal/platform/rpc/push.go:50-62` 只有 `BindEventToNotify[T](..., method string)`，要求调用方预先给出 method 字符串。
- 当前 V3 `push.go` 不具备以下能力：
  - 没有集中式 event->method 映射表。
  - 没有未知事件 fallback。
  - 没有任何现成绑定调用点。
- 备注：`internal/provider/codexapp/event_map.go:17-146` 确实存在“provider 原始事件 -> typed DTO”翻译器，但那是入站归一化，不是 `push.go` 的出站 public method 选择层，不能替代 V2 `notifications.go` 的职责。
- 结论：V3 `push.go` 目前只覆盖“发送原语”，没有覆盖 V2 `notifications.go` 的核心 event->method 映射能力。

## 6. V2 `server_conn_ws.go` 对照

- V2 `go-agent-v2/internal/apiserver/server_conn_ws.go` 覆盖的能力分三层：
  - `:15-50`：连接上限、upgrade、read limit/deadline、pong handler、write loop、ping loop、连接生命周期。
  - `:72-130`：`rpcEnvelope`、ID 解析、`jsonrpc=2.0` 校验、入站分类。
  - `:133-230`：read loop、请求/通知/客户端响应分流、过载拒绝、慢方法异步化。
- V3 `internal/platform/rpc/transport_ws.go:22-105` 只覆盖了最小 adapter：
  - upgrade -> `newWSChannel(conn)` -> `jrpc2.NewServer(...).Start(ch)`
  - `wsChannel.Send` / `Recv` / `Close`
  - close error 到 `io.EOF` / `channel.ErrClosed` 的映射
- 已覆盖的最小项：
  - WS 连接建立
  - WS 消息收发
  - WS 连接关闭
- 未覆盖的项：
  - 没有连接数上限
  - 没有 `SetReadLimit`
  - 没有 `SetReadDeadline` / `SetPongHandler`
  - 没有 ping loop
  - 没有显式 outbox/backpressure 管理
  - 没有 origin 策略
- 可以删除但已由 `jrpc2` 接管的 V2 能力：
  - 手写 JSON-RPC envelope decode/dispatch
  - request/notification/client-response 相关性恢复
- 另外一个更关键的现状：
  - 全仓 LSP 搜索 `WSHandler(` 仅命中 `internal/platform/rpc/transport_ws.go` 定义处。
  - `internal/platform/rpc/server.go:37-58` 当前主运行路径仍是 TCP `channel.Line`，不是 WS。
- 结论：V3 `transport_ws.go` 只实现了“可用的最小 WS channel adapter”，尚未覆盖 V2 运行态 WS 连接管理，也尚未接入主运行链路。

## 7. V2 `server_payload.go` 对照

- V2 `go-agent-v2/internal/apiserver/server_payload.go` 的关键职责不是单纯 codec：
  - `:32-41`：`notify()` 串联 UI runtime 同步、UI patch、legacy mirror staging、broadcast。
  - `:43-121`：UI thread/sidebar changed 派生与节流。
  - `:204-230`：向 UI runtime 回放 workspace/thread 事件。
  - `:316-412`：`parseMapAny`、payload alias merge、error 字段整理、nested item merge。
- V3 `internal/platform/rpc/codec.go:1-22` 仅保留：
  - `WrapSuccess(data)`
  - `WrapError(code, msg)`
- 当前全仓 LSP 搜索 `parseMapAny(`、`raw_event_data`、`additional_details`，`internal/` 均无命中。
- 因此当前 `codec.go` 的真实状态是：
  - 对“不要复制 V2 412 行 server_payload”这一互辩结论，方向正确。
  - 对“是否覆盖 V2 payload shaping/包装逻辑”这一问题，答案是否定的。
- 进一步判断：
  - 若 Wave 0 的定义是“JSON-RPC 线协议由 jrpc2 接管，V2 payload shaping/UI mirror 全部延期”，那当前 `codec.go` 合理。
  - 若验收口径仍要求 `server_payload.go` 中的 payload alias/error normalization 保留，当前 `codec.go` 明显过度精简。
- 另一个直接信号：`PayloadEncoder` 全仓无调用点，当前仍是占位实现。

## 8. 并发安全

### PushBridge

- `internal/platform/rpc/push.go` 本身没有共享可变状态；`dispatcher` / `logger` 只在构造时注入。
- 底层 `jrpc2.Server` 的 server-push 状态是带锁的：
  - 本机模块缓存 `github.com/creachadair/jrpc2@v1.3.5/server.go:59-88` 显示 `Server` 持有 `mu *sync.Mutex` 和 `call map[string]*Response`。
  - `server.go:474-518` 的 `pushReq` 在 `s.mu.Lock()` 下分配 callback ID、写入 `call` map，并执行 `encode(s.ch, ...)`。
- V3 `internal/platform/rpc/transport_ws.go:45-105` 的 `wsChannel.Send` 也使用 `sendMu sync.Mutex` 串行化 websocket 写入。
- 因此：
  - 并发 `NotifyClient` / `CallbackClient` 不会在 V3 自己的代码里形成明显 data race。
  - 这部分并发安全主要由 `jrpc2.Server` 和 `wsChannel.sendMu` 提供，而不是 `PushBridge` 自己加锁。

### Callback 进度风险

- `go doc github.com/creachadair/jrpc2.Server.Callback` 明确指出：若客户端不支持 push callback，且 `ctx` 没有 deadline，调用可能一直阻塞。
- `internal/platform/rpc/push.go:32-47` 只是透传调用，没有超时包装。
- 当前仓内没有 `OnCallback` 客户端配套，也没有 `WSHandler` 使用点；所以“不会数据竞争”不等于“回调一定可完成”。

### Approval / pending map

- 当前 V3 `internal/platform/rpc/` 下不存在 `approval.go`，全仓也不存在 `ApprovalManager` 类型。
- 因而：
  - “ApprovalManager pending map 是否加锁”在当前树上无从验证，因为对象尚未落地。
  - 当前只能确认 V2 对应等待态是线程安全的：
    - `go-agent-v2/internal/apiserver/server_state_groups.go:21-27` 定义 `pendingMu sync.Mutex` + `pending map[int64]chan *Response`
    - `:102-115` 在分配时加锁写入
    - `:117-137` 在投递时持锁查找并发送，注释明确避免 TOCTOU
    - `:394-420` 的 approval 去重使用 `approvalInFlight sync.Map`
- 结论：V3 `push.go` 未见直接并发缺陷；但 approval shared awaiter/pending manager 还没有迁入当前树，互辩里“R0a 先提供 pending request manager 供 R0c 复用”的结论尚未落地。

## 最终判断

- 已落地：
  - 行数预算压缩成立
  - `NotifyClient` / `CallbackClient` API 已存在
  - `AllowPush=true` 已打开
  - `codec` 没有复制 V2
  - `handler.Map` value-group + `fx.Invoke` 闭环已成立，且 Fx 图可验证通过
- 未落地或只落到半成品：
  - `push.go` 没有承接 V2 `notifications.go` 的 event->method 映射表
  - `transport_ws.go` 没有接入主运行链路，且只实现最小 adapter
  - `codec.go` 没有覆盖 V2 payload shaping
  - R0c 所需的 pending request manager / approval orchestration 还未迁入 V3
- 结论：
  - 互辩里“不要复制 V2 codec、先把 R0b 做成 value-group 闭环、push 需要 Notify+Callback 双通道”这几条，代码里已经部分或大体落地。
  - 互辩里“R0a 已足以支撑后续 approval / WS 主链路 / V2 notifications 覆盖”的部分，在当前代码里还不能成立。
