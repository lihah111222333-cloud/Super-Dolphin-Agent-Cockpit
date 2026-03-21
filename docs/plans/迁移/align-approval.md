# V2↔V3 1:1 对齐：approval 完整生命周期

## 取证范围

- 只用 LSP 取证；未使用 `grep/find/cat/sed/awk`
- V2：
  - `go-agent-v2/internal/apiserver/server_event_handler.go`
  - `go-agent-v2/internal/apiserver/server_approval.go`
  - `go-agent-v2/internal/apiserver/server_conn.go`
  - `go-agent-v2/internal/apiserver/server_context.go`
  - `go-agent-v2/internal/apiserver/server_state_groups.go`
  - `go-agent-v2/cmd/agent-terminal/app_handlers.go`
  - `go-agent-v2/cmd/agent-terminal/app_helpers.go`
- V3：
  - `internal/provider/codexapp/session.go`
  - `internal/provider/codexapp/session_approval.go`
  - `internal/provider/codexapp/event_map.go`
  - `internal/provider/unified/event_map.go`
  - `internal/platform/rpc/approval.go`
  - `internal/platform/rpc/approval_support.go`
  - `internal/platform/rpc/approval_events.go`
  - `internal/platform/rpc/approval_lifecycle.go`
  - `internal/platform/rpc/module.go`
  - `internal/platform/rpc/push.go`
  - `internal/module/turn/rpc.go`
  - `internal/module/turn/rpc_types.go`

## 总表

| 对比项 | 结论 | 核心判断 |
| --- | --- | --- |
| request 触发点 | ✅ | V3 已覆盖 V2 的 approval/request_user_input 触发家族，入口从 V2 `apiserver` 挪到 V3 `codexapp session + ApprovalManager` |
| pending 注册 | ⚠️ | V2 保证“先注册 pending，再把请求发给前端”；V3 `codexapp` 主路径里 approval 事件会先经 translator 发布，再异步注册 pending |
| callback dispatch | ⚠️ | V3 `ApprovalManager` 具备比 V2 更强的 `server.Callback + RestorePending` 能力，但当前 `codexapp` 主路径传入 `nil bridge/server`，实际不走这条 direct callback 链 |
| respond 参数（decision/approved/requestId） | ✅ | V3 保留 `requestId + approved/decision`，并兼容 camelCase；同时新增可选 `callId/call_id` |
| resolve 回传 provider | ✅ | 两边都能闭环回传 provider，只是 V3 把“回传 provider”从 rpc manager 内部移到了等待方 goroutine |
| timeout/cleanup | ❌ | V2 是固定 5 分钟超时并即时清理 pending；V3 当前主路径没有每次 approval 的独立 TTL，只依赖调用方 ctx 或停机清理 |
| requestId 去重 | ⚠️ | V2 以 `agentID:method:requestID` 做 in-flight 去重；V3 以 `callID:requestID` 做 pending 键，同 `requestID` 不同 `callID` 可并存，语义不等价 |

## 1. request 触发点

### V2

- `go-agent-v2/internal/apiserver/server_event_handler.go:221-246` 把两类事件都桥接到统一 approval 流：
  - `request_user_input` -> `handleApprovalRequest(..., approvalMethodCommandExecution, ...)`
  - `EventExecApprovalRequest` / `EventFileChangeApprovalRequest` / `skill/requestApproval` -> `handleApprovalRequest(...)`

### V3

- `internal/provider/codexapp/session.go:237-249` 在 provider notification 入口先做 `dispatch`，再对 `isApprovalBridgeMethod(method)` 走 `handleApprovalRequest`
- `internal/provider/codexapp/session_approval.go:93-115` 的 `isApprovalBridgeMethod` 已覆盖：
  - `approval/request`
  - `tool/approval/request`
  - `item/commandExecution/requestApproval`
  - `item/fileChange/requestApproval`
  - `skill/requestApproval`
  - `tool.approval.requested`
  - `request_user_input` 系列方法
- `internal/provider/codexapp/session_approval.go:38-44` 对 `request_user_input` 单独转 `RequestUserInput(...)`

### 判断

- 就“哪些事件会触发 approval 生命周期”而言，V3 已覆盖 V2 主路径，且保留了 `request_user_input` 特例
- 结论：`✅`

## 2. pending 注册

### V2

- WS 路径：
  - `go-agent-v2/internal/apiserver/server_approval.go:425-433` 调 `sendRequestToAll`
  - `go-agent-v2/internal/apiserver/server_conn.go:196-229` 在真正发请求前先 `allocPendingRequestState(s)`，拿到 `reqID/ch/cleanup`
- Wails 路径：
  - `go-agent-v2/internal/apiserver/server_approval.go:351-382` 在通知 UI 前先 `allocPendingRequest(s)`，再把 `requestId` 写回 payload 并广播
- `go-agent-v2/internal/apiserver/server_state_groups.go:102-115` 把 pending 存进 `pending map[int64]chan *Response`

### V3

- `internal/platform/rpc/approval.go:74-99` 的 `RequestApproval` 会在等待前执行 `registerPending`
- `internal/platform/rpc/approval.go:127-152` 把 pending 写入：
  - `pending map[string]*pendingApproval`
  - `pendingByRequestID map[int64]map[string]*pendingApproval`
- 但 `codexapp` 主路径的时序并不等价：
  - `internal/provider/codexapp/session.go:237-243` 先 `dispatch(raw event)`，再异步 `handleApprovalRequest`
  - `internal/provider/unified/event_map.go:42-66` 会立刻把 raw event 交给 translator 发布 typed event
  - `internal/provider/codexapp/event_map.go:133-143` 对 approval 类方法直接翻译成 `ToolApprovalRequested`
  - `internal/provider/codexapp/session_approval.go:14-24` 又额外起 goroutine 才进入 `requestToolApproval`
  - 真正 `registerPending` 要到 `internal/platform/rpc/approval.go:79`

### 判断

- V2 的性质是“前端看到请求时，pending 已经就绪”
- V3 `codexapp` 主路径里存在“事件先发布，pending 后注册”的窗口
- 如果调用端抢先发送 `approval/respond`，V3 可能先命中 `approval is not pending`，V2 没有这个窗口
- 结论：`⚠️`

## 3. callback dispatch

### V2

- `go-agent-v2/internal/apiserver/server_approval.go:425-438`
  - 先尝试 `sendRequestToAll`
  - 没有 WS client 时退到 `handleWailsModeApproval`
- `go-agent-v2/internal/apiserver/server_approval.go:351-382`
  - Wails fallback 复用同一个 pending request channel
  - 广播通知后本地等待 `approval/respond`

### V3

- `internal/platform/rpc/approval.go:154-205` 具备 direct callback 链：
  - `ensureDispatch`
  - `beginDispatch`
  - `dispatchApproval`
- `internal/platform/rpc/push.go:42-57` 实际通过 `server.Callback(...)` 发起 callback
- `internal/platform/rpc/approval_events.go:45-74` 对 legacy / V2 method family 做了 callback method 归一化
- `internal/platform/rpc/module.go:74-104` + `internal/platform/rpc/approval_lifecycle.go:23-34` 还补了 `RestorePending`，支持 reconnect 后重新 dispatch

### 但当前主路径

- `internal/provider/codexapp/session_approval.go:38-44` 调的是 `RequestApproval(s.ctx, nil, nil, req)` / `RequestUserInput(s.ctx, nil, nil, req)`
- 也就是 `bridge/server` 都是 `nil`
- `internal/platform/rpc/approval.go:154-159` 这会让 `ensureDispatch` 直接返回，不走 `server.Callback`

### 判断

- 能力层面，V3 比 V2 更强
- 当前 `codexapp` 主路径，V3 实际不是“server 发 callback 等前端返回”，而是“provider 原始事件先发出，后续靠独立的 `approval/respond` RPC 解 pending”
- 所以它不是 V2 的 1:1 dispatch 复刻，只是结果上仍能闭环
- 结论：`⚠️`

## 4. respond 参数（decision/approved/requestId）

### V2

- 前端入口：
  - `go-agent-v2/cmd/agent-terminal/app_handlers.go:381-408` 要求 `requestId`，并至少带 `decision` 或 `approved`
  - `go-agent-v2/cmd/agent-terminal/app_helpers.go:507-516` 要求 `requestId` 是正整数
- 服务端入口：
  - `go-agent-v2/internal/apiserver/server_approval.go:456-483`
  - `approvalRespondParams` 只有：
    - `requestId`
    - `approved`
    - `decision`

### V3

- `internal/module/turn/rpc_types.go:31-69`
  - 主字段是 `call_id` / `request_id` / `approved` / `decision`
  - `UnmarshalJSON` 兼容 `callId` / `requestId`
- `internal/module/turn/rpc.go:82-94`
  - 仍要求至少有 `approved` 或 `decision`
  - 调 `approver.Respond(p.CallID, p.RequestID, ...)`
- `internal/platform/rpc/approval.go:108-118`
  - `Respond` 可按 `callID`
  - 或 `requestID`
  - 或二者组合查找 pending

### 判断

- 用户指定的三项核心参数 `decision / approved / requestId` 在 V3 仍然成立
- V3 只是在此基础上新增了 `callId/call_id`
- 结论：`✅`

### 附加差异

- V2 `approvalRespondTyped` 返回 `{ok,status}`，见 `go-agent-v2/internal/apiserver/server_approval.go:462-483`
- V3 `approval/respond` 成功返回 `nil`，失败返回 jrpc2 error，见 `internal/module/turn/rpc.go:82-94` + `internal/platform/rpc/errors.go:13-31`
- 这不是参数差异，但属于调用契约差异

## 5. resolve 回传 provider

### V2

- `go-agent-v2/internal/apiserver/server_approval.go:441-454`
- 优先走 `event.RespondResultFunc(decisionPayload)`
- 没有 callback 时退到 `submitApprovalLegacyDecision(...)`
- `go-agent-v2/internal/apiserver/server_approval.go:319-338` 最终是 `SubmitSystemPrompt(..., "yes"/"no", ...)`

### V3

- `internal/platform/rpc/approval.go:237-265`
  - `finishPending` 会写入 `pending.decision/pending.err`
  - `close(pending.done)` 唤醒等待方
- `internal/platform/rpc/approval_support.go:34-47`
  - `waitForApproval` 等 `pending.done`
- `internal/provider/codexapp/session_approval.go:26-36`
  - `requestToolApproval` 在 `RequestApproval(...)` 返回后继续执行 `sendApprovalDecision(...)`
- `internal/provider/codexapp/session_approval.go:74-91`
  - `sendApprovalDecision` 再把结果回写 provider transport 的 `"approval/respond"`

### 判断

- V3 的“resolve 后回传 provider”是闭合的
- 只是 V2 把这一步放在 `apiserver` finalize 阶段，V3 把它放在等待 `RequestApproval` 的 goroutine 里
- 语义对齐，位置变了
- 结论：`✅`

## 6. timeout/cleanup

### V2

- `go-agent-v2/internal/apiserver/server_conn.go:222-229`
  - WS request 等待固定 `5 * time.Minute`
- `go-agent-v2/internal/apiserver/server_approval.go:369-380`
  - Wails fallback 也等固定 `5 * time.Minute`
  - 超时后直接保留默认 `decline`
- 两条路径都 `defer cleanup()` 删除 pending 注册

### V3

- `internal/platform/rpc/approval_support.go:34-47`
  - `waitForApproval` 只等 `ctx.Done()` 或 `pending.done`
- `internal/platform/rpc/approval_support.go:89-98`
  - 只有当调用方 ctx 自带 deadline 时，才会映射成 `ErrApprovalTimeout`
- `internal/provider/codexapp/session_approval.go:38-44`
  - 当前 `codexapp` 主路径直接传 `s.ctx`
  - 这里没有像 V2 那样单次 approval 的 `5 min` deadline
- `internal/platform/rpc/module.go:100-117`
  - 停机时只会等 `5s grace`
  - 超过后 `Cleanup(grace)`
- `internal/platform/rpc/approval_lifecycle.go:10-21`
  - `Cleanup` 按创建时间淘汰旧 pending

### 判断

- V2 是“每个 approval 自带固定 TTL + 清理”
- V3 当前主路径是“没有 per-request TTL；运行期只要 session ctx 不结束就会一直挂着；停机时再统一清理”
- 虽然 V3 额外拥有 `RestorePending`，但这属于恢复能力，不等于 V2 的超时语义
- 结论：`❌`

## 7. requestId 去重

### V2

- `go-agent-v2/internal/apiserver/server_approval.go:90-112`
  - `approvalRequestIdentity` 从 `event.RequestID` / `RequestIDRaw` / payload `requestId` 提取 identity
- `go-agent-v2/internal/apiserver/server_approval.go:104-112`
  - `approvalInflightKey = agentID + method + requestID`
- `go-agent-v2/internal/apiserver/server_approval.go:413-423`
  - 有 `requestID` 时才做 in-flight 去重
- `go-agent-v2/internal/apiserver/server_state_groups.go:399-420`
  - `runtimeGuardState.approvalInFlight.LoadOrStore/Delete`

### V3

- `internal/platform/rpc/approval_support.go:144-153`
  - pending 主键是 `callID:requestID`
- `internal/platform/rpc/approval.go:127-152`
  - `registerPending` 以这个键建 pending
- `internal/platform/rpc/approval.go:136-138`
  - 只有完全同 key 才视为已存在
- `internal/platform/rpc/approval_test.go:5-26`
  - 同 `callID` 且未显式提供 `requestID` 时，V3 会自动分配不同 `requestID`
- `internal/platform/rpc/approval.go:295-312`
  - `pendingByRequestID` 主要用于 respond 查找，不是 V2 那种“收到请求时先做 in-flight 去重”

### 判断

- V2 的 dedup 轴心是 `agentID + method + requestID`
- V3 的 dedup 轴心是 `callID + requestID`
- 这意味着：
  - 同 `requestID`、同 agent/method、不同 `callID`：V2 倾向视为同一 in-flight；V3 可并存
  - 同 `callID`、缺 `requestID`：V2 没有这类自动补号语义；V3 会分配唯一 `requestID`
- 不是 1:1 等价，只能算部分对齐
- 结论：`⚠️`

## 迁移结论

- 已对齐的核心面：
  - trigger family
  - `approval/respond` 的 `requestId + approved/decision`
  - resolve 后回传 provider
- 未 1:1 对齐的关键面：
  - pending 注册时机
  - callback dispatch 模式
  - per-request timeout/cleanup 语义
  - requestId 去重轴心
- 如果目标是“V2 approval 生命周期按行为 1:1 复刻到 V3”，当前最大缺口不是 `approval/respond` 参数，而是：
  - V3 `codexapp` 主路径没有把 pending 注册与 approval 请求发布做成同一原子时序
  - V3 当前主路径没有 V2 同等级的 5 分钟 approval TTL
  - V3 的 dedup 维度已经从“requestID 驱动”改成“callID+requestID 驱动”
