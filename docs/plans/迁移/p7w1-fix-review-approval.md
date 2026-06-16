# P7w1 Fix Review: approval / turn 视角复审

## dto-store Agent

1. `WrapStoreError` 扩展仍然不完整，离“统一 store 错误包装”还有明显缺口。
   证据：
   `internal/platform/db/errors.go:47-61` 是统一包装入口。
   目前只看到 `internal/store/agentstatus/store.go:70-72`、`internal/store/taskdag/store.go:290-292`、`internal/store/thread/store.go:145-147` 等部分 store 已接线。
   但 `internal/store/ailog/store.go:17-30`、`internal/store/auditlog/store.go:17-46`、`internal/store/buslog/store.go:17-31`、`internal/store/interaction/store.go:15-61` 仍直接返回裸 `err`。
   结论：这轮修复只是扩大覆盖面，不是全量收口；上层调用方依旧要同时处理 `StoreError` 和驱动原始错误。

2. 事件时间语义“统一”只覆盖了 provider translator，没有覆盖内部事件生产者，因此 turn / approval 事件在系统内仍不是同一套时间来源。
   证据：
   provider translator 已改为 helper：`internal/provider/claudecli/event_map.go:105-142`、`internal/provider/codexapp/event_map.go:153-308`。
   但内部事件发布仍直接写 `time.Now()`：`internal/sidecar/orch/orchestration/events.go:73-81`、`internal/module/workspace/service_helpers.go:286-294`、`internal/platform/rpc/approval_events.go:102-115`。
   结论：所谓“统一时间语义”目前只在 provider -> typed event 这一层成立；对 turn 发布链路整体并不成立。

3. 即便在 provider translator 内部，时间语义也没有真正统一，helper 接受的字段集合和回退策略仍然不一致。
   证据：
   `internal/provider/claudecli/event_map.go:134-142` 只解析 `timestamp/ts`，失败后直接 `time.Now()`。
   `internal/provider/codexapp/event_map.go:299-308` 解析 `timestamp/ts/createdAt/created_at`，失败后同样直接 `time.Now()`。
   结论：两边对“事件原始时间”的定义不同，而且解析失败时都会静默伪造当前时间；这会继续污染跨 provider 的 turn 时间线排序。

4. 与我这轮 approval/turn 修复的兼容性上，dto-store 这组改动没有修复关键交叉面。
   证据：
   我的 `approval/respond` 仍是 RPC wire，字段在 `internal/module/turn/rpc_types.go:31-69` 定义为 `call_id/request_id`，并兼容 camelCase。
   approval pending 也仍是纯内存态，见 `internal/platform/rpc/approval.go:74-93`。
   结论：store 错误包装扩展不会改善 approval pending 查找；事件时间 helper 扩展也不会缓解 approval/respond 的 wire 兼容问题。两边大体不冲突，但也没有形成闭环。

## provider Agent

1. recovery ReadLoop 竞态虽然补了状态机，但 pending 生命周期仍未和 recovery 对齐，`RestorePending/Cleanup` 对 provider recovery 重启没有实际帮助。
   证据：
   recovery 现在通过 `startReadLoop/prepareReadLoop/waitReadLoopStopped` 串行化 reader，见 `internal/provider/codexapp/recovery.go:69-158`。
   但 `RestorePending/PendingSnapshot/Cleanup` 的唯一调用点仍然只在 rpc lifecycle，见 `internal/platform/rpc/module.go:73-102` 和 `internal/platform/rpc/approval_lifecycle.go:10-43` 的引用关系。
   同时 codexapp approval 仍以 `RequestApproval(s.ctx, nil, nil, req)` / `RequestUserInput(s.ctx, nil, nil, req)` 注册 pending，见 `internal/provider/codexapp/session_approval.go:38-43`；而 `publishRequested` 在 `bridge == nil` 时直接不发，见 `internal/platform/rpc/approval.go:79-85` 与 `internal/platform/rpc/approval_events.go:22-31`。
   结论：provider recovery 重启 transport/read loop 时，approval pending 既不会走 `RestorePending`，也没有 replay/republish 机制；pending 一致性仍未纳入 recovery 方案。

2. callback method 补齐仍不完整，provider 侧依然不识别我这轮默认的 `approval/request` 和 legacy `tool/approval/request`。
   证据：
   当前默认 callback method 是 `approval/request`，legacy alias 是 `tool/approval/request`，见 `internal/platform/rpc/approval_events.go:13-20`。
   但 codexapp 侧没有任何 `approval/request` 或 `tool/approval/request` 消费点；`internal/provider/codexapp/session_approval.go:93-114` 只接受 `item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`skill/requestApproval`、`tool.approval.requested` 和若干 `request_user_input` 变体。
   `internal/provider/codexapp/event_map.go:114-149` 的 tool approval 翻译同样只覆盖这些 requestApproval 族 method。
   结论：response 方向和我新加的 snake_case `approval/respond` 是兼容的，但 callback method 方向仍未和我这轮默认 method 对齐，补齐是不完整的。

3. history metadata 修复仍然不完整，而且 codexapp 的远端 fallback 仍然接错 RPC 方法。
   证据：
   `internal/provider/codexapp/history.go:19-39` 在本地 rollout 缺失时仍调用 `thread/read` 并尝试解码 `{"history":[...]}`。
   但 thread 模块里 `thread/read` 走的是 `svc.Get(...)`，不是 history 接口，见 `internal/module/thread/rpc.go:47-53`。
   metadata 也仍是部分恢复：`internal/provider/codexapp/history_rollout.go:88-144` 只从本地 rollout 恢复 `input_*` 的局部信息；`internal/provider/claudecli/history.go:124-156` 仍然只靠注入文本恢复附件 metadata。
   结论：history 这轮修复并没有闭环，codexapp 的远端 fallback 依旧是坏的，metadata 也仍然是 best-effort。

4. 与我这轮 `request_user_input` 桥接的兼容性是“部分兼容”而不是完整兼容。
   证据：
   provider 侧现在确实能识别若干 user-input method，见 `internal/provider/codexapp/session_approval.go:102-114`。
   但 callback method 默认仍可能落到 `approval/request`，见 `internal/platform/rpc/approval_events.go:44-53`，而 codexapp 不消费这个 method。
   结论：`request_user_input` 族 method 现在能走进同一条 approval 链，但 provider 修复对 callback method 的兼容面仍窄于我这轮 approval 默认面，因此只能算部分兼容。
