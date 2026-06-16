# P7w1 Fix Review（Provider 视角）

基线：
- 本文只批判另外 2 个 Agent 的修复
- 每条均基于当前仓库 LSP 命中，不做臆测

## 1. approval-turn Agent

### Finding 1：`RestorePending` 只在 server 启动时打一枪，后续新连接拿不到历史 pending

证据：
- `internal/platform/rpc/module.go:77-88` 的 `bindApprovalLifecycle.OnStart` 只在启动时遍历 `server.snapshotActive()` 调 `RestorePending`。
- `internal/platform/rpc/approval_lifecycle.go:23-34` 的 `RestorePending`，LSP `references` 只有 `module.go:83` 这一处。
- `internal/platform/rpc/server.go:107-126` 的连接生命周期里，`serveConn` 只做 `addActive/removeActive`；`internal/platform/rpc/server.go:128-155` 的 `addActive/snapshotActive` 也没有任何 `RestorePending` 触发点。

问题：
- server 启动时通常还没有 active client，`snapshotActive()` 很可能是空。
- 之后新建的 RPC 连接不会再触发 `RestorePending`，因此“恢复 pending approval” 实际上只覆盖极窄窗口。

结论：
- 这个接线不是完整修复，更像一次性补偿；对后到的 UI/client 无效。

### Finding 2：`request_user_input` 只桥到了审批决策路径，没有桥到 typed event 流

证据：
- `internal/provider/codexapp/session_approval.go:38-44` 命中 `request_user_input` 时会调用 `RequestUserInput`。
- 但 `internal/provider/codexapp/event_map.go` 对 `request_user_input` 做 LSP `text_search` 是 0 命中。
- 当前 `internal/provider/codexapp/event_map.go:132-140` 只把 `item/commandExecution/requestApproval` / `item/fileChange/requestApproval` / `skill/requestApproval` / `tool.approval.requested` 翻成 `ToolApprovalRequested`。
- 同时 `internal/provider/codexapp/session.go:231-240` 先 `dispatch(raw)` 再做 `handleApprovalRequest`。

问题：
- 这意味着 `request_user_input` 虽然能走审批决策，但 provider raw event 不会被翻译成统一的 typed approval event。
- 从 provider/UI 视角，这条桥仍然是不完整的，typed bus 看不到这类请求。

结论：
- “request_user_input 已桥接” 只成立于 respond 路径，不成立于 typed event 观察面。

### Finding 3：`respond` 的 snake_case 兼容是单向 shim，不是对称契约

证据：
- `internal/module/turn/rpc_types.go:31-69` 的 `approvalRespondParams.UnmarshalJSON` 同时接受 snake_case 和 legacy camelCase。
- 但 `internal/platform/rpc/approval.go:47-60` 的 `ApprovalRequest` 仍然全部是 camelCase tag。
- `internal/platform/rpc/approval_events.go:79-99` 的 `callbackParams` 仍然发 `requestId/callId/approvalId/threadId/turnId`。
- LSP `text_search` `callId` 命中 `approval.go:48` 和 `approval_events.go:83`，说明 outbound 侧仍是 camelCase。

问题：
- 现在只是 `approval/respond` 入参兼容 snake_case，callback/outbound payload 并没有同步收敛。
- 这会让 approval 契约继续保持“入口 snake_case 兼容，出口 camelCase 原样”的不对称状态。

结论：
- 该修复更接近“输入兼容补丁”，还不是完整的命名风格对齐。

### Finding 4：在 provider 路径里，`RestorePending/Cleanup` 对 `request_user_input` 基本不起作用

证据：
- `internal/provider/codexapp/session_approval.go:39-43` 调 `RequestUserInput(s.ctx, nil, nil, req)` / `RequestApproval(s.ctx, nil, nil, req)`，bridge/server 都是 `nil`。
- `internal/platform/rpc/approval.go:154-159` 的 `ensureDispatch` 在 `bridge == nil || server == nil` 时直接返回，不进入 callback dispatch。
- `internal/platform/rpc/module.go:73-102` 的 `RestorePending/Cleanup` 只绑定在 `rpc.Server` 生命周期。

问题：
- 我这边已经修了 `codexapp` 的单 ReadLoop 恢复，但 approval-turn 这轮的 lifecycle wiring 仍只覆盖 callback-capable RPC 客户端。
- provider 侧 `approval/respond` / `request_user_input` 这条链完全不经过它新增的 restore 路径，所以这轮修复对 provider 恢复链没有实质帮助。

结论：
- 从 provider 视角，这组修复没有覆盖真正相关的 request_user_input 恢复面。

## 2. dto-store Agent

### Finding 1：`WrapStoreError` 扩展仍不完整，很多 store 还在裸返回

证据：
- `internal/platform/db/errors.go:47-61` 的 `WrapStoreError`，LSP `references` 命中已扩展到多处 store。
- 但对 `internal/store` 做 LSP `text_search` `return nil, err`，仍能命中：
  - `internal/store/ailog/store.go:17-30`
  - `internal/store/auditlog/store.go:17-33`
  - `internal/store/buslog/store.go:17-32`
  - `internal/store/cwdlock/store.go:53-64`
  - `internal/store/dbquery/store.go:15-25`
  - `internal/store/interaction/store.go:15-61`
  - `internal/store/topologyapproval/store.go:15-50`

问题：
- 这说明 store error 域仍然是混合状态：一部分返回 `*db.StoreError`，另一部分直接透传底层错误。

结论：
- “WrapStoreError 扩展” 仍然是局部推进，不是统一治理。

### Finding 2：事件时间语义并没有统一，provider 事件和平台事件仍然双轨

证据：
- provider translator 已经用 payload 时间：
  - `internal/provider/codexapp/event_map.go:153-165` 用 `eventTime(payload)`
  - `internal/provider/claudecli/event_map.go:105-116` 用 `eventTime(data)`
- 但仍有模块继续手写 `time.Now()`：
  - `internal/platform/rpc/approval_events.go:102-120`
  - `internal/sidecar/orch/orchestration/events.go:73-81`
- 对 `internal/dto/shared/event.go:42` 的 `EventHeader` 做 LSP `references`，上述两类构造点同时存在。

问题：
- approval/orchestration 发出的 typed event 时间仍是本地构造时间，而 provider translator 走的是事件原始时间。
- 这会造成跨源事件的时间语义继续分裂，尤其在 provider 流和平台流混排时更明显。

结论：
- “事件时间语义统一” 这个结论站不住，当前只是局部统一。

### Finding 3：history metadata 路径与 `EventHeader` 重构仍是两套体系，没有真正对齐

证据：
- `internal/provider/claudecli/session_history.go:13-27` 和 `internal/provider/codexapp/history.go:19-39` 都是 `ReadHistory -> []dto.Message`。
- 对 `internal/provider/claudecli` 和 `internal/provider/codexapp` 搜 `EventHeader`，只命中各自 `event_map.go`，不命中 history 相关文件。
- 也就是说 history 仍然靠 `dto.Message.Timestamp + Metadata` 输出，而不是任何 `EventHeader` 派生结构。

问题：
- dto-store 这轮即使整理了 `EventHeader`，也没有把 history path 纳入统一时间/头部语义。
- 我这边刚补的 history metadata 恢复逻辑，仍然完全绕开 EventHeader 层。

结论：
- history metadata 与 EventHeader 重构当前并不对齐，仍然是并行模型。

### Finding 4：扩展后的 store error 对 provider recovery 仍然没有可见收益

证据：
- `internal/provider/codexapp/transport.go:253-255` 会把远端错误压成 `fmt.Errorf("rpc error %d: %s", ...)`。
- `internal/provider/codexapp/recovery.go:96-104` 的 `shouldReconnect` 继续把 `"rpc error "` 视为不可重连。
- 因而不管 server 侧 store error 是 `*db.StoreError` 还是原始 `pgx` 错误，过 RPC 后 provider 侧都只看到字符串。

问题：
- 这意味着 dto-store 这轮做得再细，provider recovery 仍然无法基于 store error kind 做决策。

结论：
- 从 provider 视角，WrapStoreError 扩展目前没有落到恢复语义上。

## 3. Provider 视角总结

### 对 recovery ReadLoop 修复后的 `request_user_input` 桥接影响

结论：
- 我的单 ReadLoop 修复保证了 `codexapp` 恢复后不会再并发跑多个读循环。
- 但 approval-turn 这轮的 `request_user_input` 修复主要停在 approval decision path，没有补 typed event 映射；`RestorePending/Cleanup` 也只覆盖 RPC callback 路径，不覆盖 provider `approval/respond` 路径。
- 所以 ReadLoop 竞态修好后，`request_user_input` 桥接依然是不完整的，尤其在 UI/typed bus 可见性上没有补齐。

### 对 history metadata 与 EventHeader 的对齐情况

结论：
- dto-store 的 EventHeader/事件时间修复没有直接帮助 history metadata。
- 当前 provider event 走 `event_map.go + EventHeader`，history 走 `ReadHistory -> dto.Message{Timestamp, Metadata}`；两条链还是分离的。
