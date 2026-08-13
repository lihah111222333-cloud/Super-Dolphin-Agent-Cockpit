# 能力+容错审查：Approval 审批生命周期

## 审查方式

- 只用 LSP 取证：`text_search`、`workspace_symbol`、`references(compact)`、`call_hierarchy`、`read_file`
- 审查对象：当前 V3 代码；V2 仅用于逐步对照
- 结论口径：
  - `通过`：链路闭合且当前调用点真实可达
  - `部分通过`：实现存在，但当前接线/容错/覆盖面不完整
  - `不通过`：当前主路径缺失或与 V2 关键行为不等价

## 总结

| 维度 | 结论 | 核心判断 |
| --- | --- | --- |
| 1. `RequestApproval` 完整链路 | 部分通过 | 主实现完整，但当前真实调用点只有 `codexapp`，且它不走 live callback/push bridge |
| 2. `approval/respond` 完整链路 | 通过 | RPC 入站到 `ApprovalManager.Respond` 闭合；随后 provider goroutine 会回传 provider |
| 3. `requestId` 去重 | 通过 | `callID:requestID` 已分键；同 `callID` 不同 `requestID` 可正确并存 |
| 4. 超时处理 | 部分通过 | `waitForApproval` 支持 ctx deadline，但当前 `codexapp` 调用不带 deadline；`Cleanup` 未接线 |
| 5. 并发安全 | 部分通过 | 数据竞争基本受控，但重复 `respond` 的返回语义不严格幂等 |
| 6. `request_user_input` | 不通过 | API 包装存在，但当前无调用点，provider 也未桥接 |
| 7. callback method V2 兼容 | 部分通过 | command-exec/legacy 映射已补；`fileChange`/`skill` 方法族仍缺失，默认 method 仍非 V2 家族 |
| 8. `RestorePending / Cleanup` | 不通过 | 只有定义，没有调用点；重启后无法恢复 in-memory pending |
| 9. provider 回传 | 部分通过 | `sendApprovalDecision` 可回传 provider，但绕过 recovery wrapper |
| 10. V2 等价性 | 不通过 | 仅覆盖“在线 command approval + 前端 respond”子集，缺 `request_user_input`、fallback、恢复、完整 method family |

## 1. `RequestApproval` 完整链路

### 1.1 当前真实调用者

LSP `call_hierarchy(incoming)` 指向两个入口：

- `internal/provider/codexapp/session_approval.go:26-36` `requestToolApproval`
- `internal/platform/rpc/approval.go:101-106` `RequestUserInput`

其中当前仓内真正外部可达的只有 `codexapp`：

1. `internal/provider/codexapp/session.go:231-241` `onNotification` 先收到 provider notification
2. method 命中 `item/commandExecution/requestApproval` 或 `tool.approval.requested`
3. 进入 `internal/provider/codexapp/session_approval.go:14-24` `handleApprovalRequest`
4. goroutine 调 `requestToolApproval`
5. `internal/provider/codexapp/session_approval.go:38-64` `buildApprovalRequest` 从 provider payload 提取参数
6. `internal/provider/codexapp/session_approval.go:31` 调 `ApprovalManager.RequestApproval(s.ctx, nil, nil, req)`

### 1.2 参数来源

`buildApprovalRequest` 的参数来源明确：

- `requestId`：`payload["requestId"|"request_id"]`，且必须 `> 0`，否则直接丢弃，见 `internal/provider/codexapp/session_approval.go:42-45`
- `callId`：优先 `callId/call_id`，再 `approvalId/approval_id`，最后退化为 `strconv.FormatInt(requestID, 10)`，见 `internal/provider/codexapp/session_approval.go:46-50`
- `toolName`、`threadId`、`turnId`、`reason`、`SourceMethod`、`Payload`：同文件 `52-63`

`internal/platform/rpc/approval_support.go:18-32` `normalizeApprovalRequest` 还会补齐：

- `CallID`
- 默认 `Kind="tool"`
- 默认 `State="awaiting_user_input"`

### 1.3 pending 创建

`internal/platform/rpc/approval.go:74-99` 的 `RequestApproval` 顺序是：

1. `normalizeApprovalRequest`
2. `registerPending`
3. `publishRequested`
4. `ensureDispatch`
5. `waitForApproval`

pending 创建在 `internal/platform/rpc/approval.go:127-152`：

- 存储键：`pendingStorageKey(callID, requestID)`，见 `130`
- 若 `requestID` 为空或 `<=0`，会分配 `nextRequestID`，见 `135-138`
- 同时写入：
  - `pending map[string]*pendingApproval`
  - `pendingByRequestID map[int64]map[string]*pendingApproval`

### 1.4 dispatch 到前端

这里要区分“代码能力”和“当前真实链路”。

`ApprovalManager` 本身具备 direct callback 能力：

- `internal/platform/rpc/approval.go:154-169` `ensureDispatch`
- `internal/platform/rpc/approval.go:171-191` `beginDispatch`
- `internal/platform/rpc/approval.go:193-205` `dispatchApproval`
- `internal/platform/rpc/push.go:42-57` `PushBridge.CallbackClient -> server.Callback(...)`

但当前真实调用点 `codexapp.requestToolApproval` 传的是 `nil, nil`：

- `internal/provider/codexapp/session_approval.go:31`

因此当前 `codexapp` 路径里：

- `publishRequested` 不会挂 `dispatcher`，见 `internal/platform/rpc/approval.go:80-85`
- `ensureDispatch` 会直接返回，不发 callback，见 `internal/platform/rpc/approval.go:154-159`

当前前端可见的“审批请求”不是 `ApprovalManager` 发出去的，而是 provider 原始事件链：

1. `internal/provider/codexapp/session.go:232` 先 `s.dispatch(dto.RawProviderEvent{...})`
2. `internal/provider/unified/event_map.go:43-66` 把 raw event 交给 translator
3. `internal/provider/codexapp/event_map.go:132-137` 把该 provider 事件翻成 `tooldto.ToolApprovalRequested`

而本地 pending 创建发生得更晚：

- `onNotification` 是先 `dispatch`，再进入 approval 分支，见 `internal/provider/codexapp/session.go:231-235`
- `handleApprovalRequest` 还会再起一个 goroutine，见 `internal/provider/codexapp/session_approval.go:18-23`
- 真正 `registerPending` 要到 `ApprovalManager.RequestApproval` 里才发生，见 `internal/platform/rpc/approval.go:79`

所以当前 `codexapp` 路径存在一个真实竞态：

- 前端或上层可能已经看到了 approval request
- 但本地 `ApprovalManager` 的 pending 还没注册完成
- 如果此时抢先打 `approval/respond`，可能返回 `approval is not pending`

但 `platform/rpc` 的 jrpc2 push bridge 只订阅 3 类事件：

- `internal/platform/rpc/push.go:82-90` 只有 `StateChanged` / `TurnStarted` / `TurnCompleted`

它没有把 `ToolApprovalRequested/Resolved` 转成 jrpc2 notify。也就是说：

- provider 事件翻译链存在
- `ApprovalManager` direct callback 能力存在
- 当前真实调用点没有使用它们来向 jrpc2 前端派发 approval request

## 2. `approval/respond` 完整链路

### 2.1 RPC 入口

`approval/respond` 注册于：

- `internal/module/turn/rpc.go:82-94`

参数解析在：

- `internal/module/turn/rpc_types.go:31-69`

这里有两个兼容点：

- 结构体标签主字段是 snake_case：`call_id` / `request_id`
- `UnmarshalJSON` 会补读 legacy camelCase：`callId` / `requestId`

### 2.2 响应链路

handler 行为：

1. 校验至少有 `approved` 或 `decision`，见 `internal/module/turn/rpc.go:87-89`
2. 调 `approver.Respond(p.CallID, p.RequestID, contract.ApprovalDecision{...})`，见 `90-93`

`ApprovalManager.Respond` 逻辑：

1. `lookupPending(callID, requestID)`，见 `internal/platform/rpc/approval.go:108-116`
2. 找不到时：
   - `callID` 和 `requestID` 都空 -> `approval call id is required`
   - 否则 -> `approval is not pending`
3. 找到后 `finishPending`

### 2.3 解析 `callID/requestID`

`lookupPending` 的查找顺序见 `internal/platform/rpc/approval.go:272-293`：

1. 如果同时有 `callID + requestID`，先查精确键 `callID:requestID`
2. 如果有 `requestID`，再走 `pendingByRequestID`
3. 如果只有 `callID`，最后查 `callID` 单键

`lookupPendingByRequestIDLocked` 的语义见 `295-312`：

- 若同一个 `requestID` 下只有一个 pending，可用 `requestID` 单独解析
- 若同一个 `requestID` 下有多个 pending，则必须再给 `callID` 才能消歧

### 2.4 resolve 后如何唤醒等待方

`finishPending` 见 `internal/platform/rpc/approval.go:237-265`：

- 从 `pending` / `pendingByRequestID` 删除
- 取消 dispatch ctx
- 写入 `pending.decision` / `pending.err`
- `close(pending.done)`

`waitForApproval` 只等两个信号，见 `internal/platform/rpc/approval_support.go:34-47`：

- `ctx.Done()`
- `pending.done`

所以 `approval/respond` 一旦命中 pending，就能解锁等待中的 `RequestApproval`

### 2.5 回传 provider

这一步不在 `Respond` 内，而在等待方 goroutine 里：

1. `requestToolApproval` 卡在 `RequestApproval(...)`，见 `internal/provider/codexapp/session_approval.go:26-36`
2. `approval/respond` 命中 pending 后，`RequestApproval` 返回 `decision`
3. `requestToolApproval` 继续执行 `sendApprovalDecision(requestID, decision)`，见 `35`
4. `sendApprovalDecision` 调 provider transport 的 `"approval/respond"`，见 `internal/provider/codexapp/session_approval.go:66-83`

所以整条链是：

`approval/respond (前端 -> V3 RPC)`
`-> ApprovalManager.Respond`
`-> finishPending / close(done)`
`-> requestToolApproval 醒来`
`-> sendApprovalDecision`
`-> provider session`

## 3. `requestId` 去重

### 3.1 同 `callID` 不同 `requestID` 是否正确分开

是，D1 这部分可以判定为已修。

证据：

- 存储键是 `callID:requestID`，见 `internal/platform/rpc/approval_support.go:144-153`
- `registerPending` 用这个键入主表，见 `internal/platform/rpc/approval.go:130-151`

因此：

- `callID=A, requestID=1` -> `A:1`
- `callID=A, requestID=2` -> `A:2`

两者不会互相覆盖。

### 3.2 响应时是否会串单

不会串到错误 pending，但有“必须携带足够信息才能命中”的约束。

- 若 `callID + requestID` 都带上，命中精确键，见 `internal/platform/rpc/approval.go:279-283`
- 若只带 `requestID`：
  - 该 `requestID` 唯一 -> 可命中，见 `285-287` + `305-309`
  - 该 `requestID` 映射多个 pending -> 返回 `nil`，不会串错，见 `305-306`
- 若只带 `callID`：
  - 只能命中 `callID` 单键 pending，见 `292`
  - 如果真实待处理项都是 `callID:reqID` 形式，则 `callID` 单独不足以定位

### 3.3 仍存在的边界

同 `callID` 且没有有效 `requestID` 的请求，仍会共用 `callID` 单键：

- `internal/platform/rpc/approval.go:130`
- `internal/platform/rpc/approval_support.go:149-152`

所以“同 `callID` 不同 `requestID`”已正确分开；但“缺失 `requestID` 的重复 `callID` 请求”仍会折叠。

## 4. 超时处理

### 4.1 `waitForApproval` 本身

`waitForApproval` 没有自带 timer，只依赖调用方传入的 `ctx`：

- `internal/platform/rpc/approval_support.go:41-46`

所以它支持 `context deadline`，但不主动创建 deadline。

### 4.2 deadline 触发后的清理

`RequestApproval` 在 `waitForApproval` 返回错误后：

- owner=true 才会走 `mapApprovalWaitErr + failPending`
- `context.DeadlineExceeded` 会映射成 `ErrApprovalTimeout(...)`

证据：

- `internal/platform/rpc/approval.go:92-98`
- `internal/platform/rpc/approval_support.go:89-98`

因此：

- 只要调用方真的传了 deadline，超时后 pending 会被 `failPending` 清理掉
- 这部分实现是闭合的

### 4.3 当前 `codexapp` 实际是否会超时

不会自然超时。

`codexapp` 调用用的是 `s.ctx`：

- `internal/provider/codexapp/session_approval.go:31`

而 `s.ctx` 来源于：

- `internal/provider/codexapp/session.go:82-93`

这里只是 `context.WithCancel(context.Background())`，没有 deadline。

因此当前真实路径里，approval 会一直等到：

- 前端 `approval/respond`
- session `Close/ForceStop`

### 4.4 `Cleanup(timeout)` 现状

`Cleanup` 已实现：

- `internal/platform/rpc/approval_lifecycle.go:10-21`

它会把超过 cutoff 的 pending 统一 `failPending(ErrApprovalTimeout("approval timed out"))`

但 LSP `references` 为 0：

- `Cleanup`
- `RestorePending`
- `PendingSnapshot`

当前都没有调用点。

### 4.5 额外说明：provider 回传超时

`sendApprovalDecision` 自己有 10 秒 cancel wrapper：

- `internal/provider/codexapp/session_approval.go:79-82`
- `internal/provider/codexapp/support.go:10-20`

但这只是“本地已 resolve 后向 provider 回传”的超时，不是 `waitForApproval` 的等待超时。

## 5. 并发安全

### 5.1 多个 approval request 同时到达

主状态受 `ApprovalManager.mu` 保护：

- `internal/platform/rpc/approval.go:20`
- `127-152`
- `175-190`
- `224-235`
- `242-252`
- `277-287`
- `343-349`

结果：

- 不同 key 的请求可以并发注册，彼此独立
- 同 key 的重复请求会返回同一个 pending，后来的调用方拿到 `owner=false`，见 `131-132`

这意味着当前实现更接近“按 key 合并等待”而不是“重复创建 pending”。

### 5.2 多个 respond 同时到达

`finishPending` 用 `pending.once.Do(...)` 包住最终收口：

- `internal/platform/rpc/approval.go:241`

所以不会双重删除 map、不会重复 close channel、不会出现明显 data race。

但返回语义不是严格幂等：

- 两个 goroutine 若都在删除前通过 `lookupPending` 拿到同一个 `pending`
- 两者都会继续调用 `finishPending`
- 只有一个真正生效，另一个 `once.Do` 为空操作
- 但 `Respond` 仍会返回 `nil`

也就是说：

- 内存安全：基本通过
- 业务语义：重复 `respond` 可能“第二次也返回成功”，不够严格

### 5.3 dispatch / respond 并发

这部分也基本受控：

- `beginDispatch` 会在锁内检查 `dispatching` 和 `isPendingDone`，见 `internal/platform/rpc/approval.go:177-190`
- `resetDispatch` 只在当前 pending 仍是 map 中现存对象时重置，见 `226-234`
- `finishPending` 会先删 map、清 cancel，再 `once` 收口，见 `242-255`

所以 direct callback、recoverable dispatch error、manual respond 三条路径同时竞争时，主要风险不是 data race，而是“谁先赢”的语义差异。

## 6. `request_user_input`

### 6.1 是否与 `RequestApproval` 走同一链路

在 API 层是。

`internal/platform/rpc/approval.go:101-106`：

- 若 `Kind` 为空，补成 `request_user_input`
- 直接调用 `RequestApproval`

### 6.2 当前是否有真实调用点

没有。

LSP `call_hierarchy(incoming)` / `references` 对 `RequestUserInput` 都没有命中，只有声明本身。

### 6.3 provider 是否桥接了 `request_user_input`

没有。

证据：

- `internal/provider/codexapp/session.go:233-235` 只处理：
  - `item/commandExecution/requestApproval`
  - `tool.approval.requested`
- 对 `internal/provider/codexapp` 做 `text_search("request_user_input")` 为 0 命中

### 6.4 callback method 归一化是否考虑了它

考虑了，但这只是“method 归一化能力”，不是“调用链已接线”。

`internal/platform/rpc/approval_events.go:42-67` 会把这些输入映射到 `item/commandExecution/requestApproval`：

- `codex/event/request_user_input`
- `item/tool/request_user_input`
- `item/tool/requestUserInput`
- `request_user_input`

### 6.5 与 V2 对比

V2 明确把 `request_user_input` 桥到统一 approval 流程：

- `go-agent-v2/internal/apiserver/server_event_handler.go:221-233`

并且在 `approval_policy=never` 时自动应答：

- `go-agent-v2/internal/apiserver/server_event_handler.go:223-229`
- `go-agent-v2/internal/apiserver/server_event_handler.go:497-519`
- `go-agent-v2/internal/apiserver/server_user_input_responder.go:9-41`

当前 V3 没有这条桥，也没有 auto-responder。

结论：`request_user_input` 当前不通过。

## 7. callback method V2 兼容

### 7.1 当前 V3 的 method 选择规则

`internal/platform/rpc/approval_events.go:42-52`：

1. 先看 `CallbackMethod`
2. 再看 `SourceMethod`
3. 若 `Kind=request_user_input`，返回 `item/commandExecution/requestApproval`
4. 否则默认 `approval/request`

### 7.2 已有的兼容修复

`normalizeApprovalCallbackMethod` 已补这些映射，见 `internal/platform/rpc/approval_events.go:54-67`：

- `tool/approval/request` -> `approval/request`
- `tool.approval.requested` -> `item/commandExecution/requestApproval`
- `request_user_input` 家族 -> `item/commandExecution/requestApproval`

另外 `approval/respond` 参数也兼容了 camelCase / snake_case：

- `internal/module/turn/rpc_types.go:38-69`

### 7.3 对 D4 的判断

只能判“部分修复”，不能判“已对齐 V2”。

理由：

1. `codexapp` 当前真实 approval 入口只覆盖：
   - `item/commandExecution/requestApproval`
   - `tool.approval.requested`
   见 `internal/provider/codexapp/session.go:233-235`
2. 当前 V3 `internal` 下对以下两个 V2 method 搜索为 0 命中：
   - `item/fileChange/requestApproval`
   - `skill/requestApproval`
3. 默认 callback method 仍是 `approval/request`，见 `internal/platform/rpc/approval_events.go:14`
4. 当前仓内对 `approval/request` 的 LSP `text_search` 只有常量定义，没有 receiver/handler 实现

所以：

- command-exec + legacy path：有部分兼容
- 完整 V2 method family：未对齐

## 8. `RestorePending / Cleanup`

### 8.1 实现是否存在

存在：

- `internal/platform/rpc/approval_lifecycle.go:10-21` `Cleanup`
- `internal/platform/rpc/approval_lifecycle.go:23-34` `RestorePending`
- `internal/platform/rpc/approval_lifecycle.go:36-43` `PendingSnapshot`

### 8.2 是否有调用点

没有。

对三个符号做 LSP `references` 均为 0。

### 8.3 应用重启后 pending 如何恢复

当前无法恢复。

原因：

- pending 只保存在 `ApprovalManager.pending / pendingByRequestID` 的内存 map 中，见 `internal/platform/rpc/approval.go:19-25`
- 没有持久化写出
- 没有启动时 restore hook
- 没有从 provider 重新拉回 pending snapshot 的代码

因此：

- 进程内连接中断后的“recoverable dispatch error”可以保留 pending 对象，见 `internal/platform/rpc/approval.go:212-215`
- 但一旦进程重启，所有 pending 都丢失

## 9. provider 回传

### 9.1 当前回传路径

provider 回传由 `codexapp` 完成：

- `internal/provider/codexapp/session_approval.go:66-83` `sendApprovalDecision`

发送内容：

- `requestId` 必带
- `approved` 可选
- `decision` 优先发原始 `Detail`，否则发 `Reason`

### 9.2 是否回到 provider session

是。

最终调用：

- `s.transport.Call(callCtx, "approval/respond", params)`，见 `internal/provider/codexapp/session_approval.go:81`

### 9.3 当前缺口

`sendApprovalDecision` 没走 `callTransport`：

- 普通带恢复包装的路径：`internal/provider/codexapp/session_recovery.go:12-21`
- approval 回传当前直接调：`internal/provider/codexapp/session_approval.go:81`

这意味着：

- 如果连接在“本地 pending 已 resolve”和“provider 收到 approval/respond”之间断掉
- 当前 approval 决策不会走 `attemptRecovery + retry`

另外，`codexapp` 路径里 `ApprovalManager` 不会自己发布 resolved 事件：

- `pending.dispatcher` 只有 `bridge != nil` 时才设置，见 `internal/platform/rpc/approval.go:80-82`
- 当前 `codexapp` 传 `nil, nil`

所以 resolved 的前端可见性主要仍依赖 provider 后续发回的：

- `approval/resolved`
- `tool.approval.resolved`

翻译入口见 `internal/provider/codexapp/event_map.go:138-144`

## 10. V2 等价性：逐步对照

| 步骤 | V2 | V3 当前现状 | 结论 |
| --- | --- | --- | --- |
| 事件识别 | `server_event_handler.go:221-245` 识别 `request_user_input`、exec/file/skill approval | `codexapp/session.go:233-235` 只识别 exec + legacy tool approval | 不等价 |
| `request_user_input` 桥接 | `request_user_input` 非 auto-respond 时桥到 `handleApprovalRequest(..., approvalMethodCommandExecution, ...)` | `RequestUserInput` 仅 API 包装，无调用点；provider 也不识别该事件 | 不等价 |
| 前端请求派发 | `handleApprovalRequest -> sendRequestToAll(method, payload)`，见 `go-agent-v2/internal/apiserver/server_approval.go:425-439` | 当前真实路径不走 `ApprovalManager` direct callback；前端可见性依赖 provider event translation | 不等价 |
| 无 WS fallback | V2 有 Wails/notifyHook fallback，见 `go-agent-v2/internal/apiserver/server_approval.go:351-381` | V3 无 approval 专用 fallback；`platform/rpc/push.go:75-92` 也未桥接 approval notify | 不等价 |
| pending registry | V2 `allocPendingRequest/ResolvePendingRequest`，见 `go-agent-v2/internal/apiserver/server_conn.go:241-260` | V3 `ApprovalManager.pending + pendingByRequestID`，见 `internal/platform/rpc/approval.go:19-25` | 架构不同，但本地闭环成立 |
| 前端响应 RPC | V2 `approval/respond` 只吃 `requestId`，见 `go-agent-v2/internal/apiserver/server_approval.go:456-483` | V3 同 method，可吃 `requestId` 或 `callId+requestId`，且兼容 camel/snake，见 `internal/module/turn/rpc_types.go:31-69` | 部分等价，V3 更宽 |
| provider 恢复执行 | V2 `finalizeApprovalRequestDecision` 用 `event.RespondResultFunc` 或 legacy submit，见 `go-agent-v2/internal/apiserver/server_approval.go:441-454` | V3 `RequestApproval` 返回后由 `sendApprovalDecision` 回传 provider | 部分等价 |
| 自动应答 | V2 有 `approval_policy=never` 自动应答 + TTL 去重，见 `go-agent-v2/internal/apiserver/server_event_handler.go:223-229,497-519` 和 `server_user_input_responder.go:9-41` | V3 无对应实现 | 不等价 |
| 恢复/重启 | V2 至少有 Wails pending/fallback；V3 `Cleanup/RestorePending/PendingSnapshot` 未接线 | 当前重启丢 pending | 不等价 |
| method family | V2 明确有 exec/file/skill 三类 method，见 `go-agent-v2/internal/apiserver/server_approval.go:15-17` | V3 `internal` 下仅有 exec/legacy 映射；缺 file/skill | 不等价 |

## 最终判断

当前 V3 的审批生命周期可以覆盖一条较窄的主路径：

- `codexapp` 在线收到 command approval
- 前端通过 `approval/respond` 回到 V3
- V3 解除本地 pending
- `codexapp` 再把决策回传 provider

但它还不是 V2 等价的 approval lifecycle，主要缺口有四个：

1. `request_user_input` 没接到真实链路
2. approval callback / method family 只覆盖 command-exec 子集
3. `Cleanup / RestorePending / PendingSnapshot` 完全未接线
4. provider 回传 `sendApprovalDecision` 绕过 recovery wrapper

如果按“能力+容错”口径评估，当前状态应判为：

- live command-approval 闭环：`部分通过`
- approval lifecycle 全量能力：`不通过`
- V2 等价性：`不通过`

## 互审

### 1. 对 `docs/plans/迁移/cap-thread-lifecycle.md` 的批判

1. 对 `Start/Resume` 半创建窗口的判断偏窄。报告把风险主要压在 `threadStore` / `bindingStore` 半成功上，但漏掉了更直接的 live session side effect：`thread.Start` / `thread.Resume` 都是在 `persistThreadState(...)` 之前先调 `startSession` / `resumeSession`，而 `unified.Client.open(...)` 成功后会立即 `sessions.Register(agentID, session)`；后续失败只能走 `stopAgent(...)`，而 `orchestration == nil` 时它直接是 no-op。证据：`internal/module/thread/lifecycle.go:53-73,88-106,298-313`，`internal/provider/unified/client.go:47-67`。这比“半落库”更早暴露了真实运行态副作用。

2. `Archive/Delete` 段落把问题写成 “best-effort Close”，力度还不够。当前 `closeSessionIfActive(...)` 对 `resolveBinding(...)` 和 `GetSession(...)` 的错误都是直接 `return nil`，也就是操作可以成功返回，但实际根本没有执行到 `session.Close(ctx)`。证据：`internal/module/thread/service.go:102-119,228-240`，`internal/module/thread/archive.go:5-13`。报告如果只写“Close 不 Remove”，会低估“甚至可能没有 Close”这层风险。

3. `thread/loaded/list` 的退化程度被低估了。报告抓到了它只是 `ListByStatus(statusCreated)`，但没继续指出：`persistThreadState(...)` 在 `Start/Resume/Fork/Recover` 一律写 `statusCreated`，`Unarchive()` 也会写回 `statusCreated`；当前只有 `Archive()` 会把状态切到 `archived`。证据：`internal/module/thread/service.go:75-83`，`internal/module/thread/lifecycle.go:122-131,160-168,246-253`，`internal/module/thread/archive.go:15-19`。所以它现在更接近“列出所有未归档 thread”，而不是“loaded/list”。

4. 并发/清理分析漏掉了 `threadAgents` 的脏 alias 残留。`rememberBinding(...)` 会把 `ProviderThreadID`、`CodexThreadID`、`AgentID` 全部写入同一张内存映射；但 `Delete(...)` 只 `forgetThreadAgent(id)` 当前请求的 `threadID`。证据：`internal/module/thread/lifecycle.go:363-380`，`internal/module/thread/service.go:102-118,183-201`。这意味着删除后其他 alias 仍可能命中旧 `agentID`，报告没有把这个内存态污染窗口单列出来。

### 2. 对 `docs/plans/迁移/cap-provider-session.md` 的批判

1. `Close(ctx)` 的问题被归因得过窄。报告主要批判两个 driver 不 honor ctx，但 `SessionManager.Remove(...)` 自己就已经在 manager 层把调用方 ctx 丢掉，直接硬编码 `session.Close(context.Background())`。证据：`internal/provider/unified/session.go:59-81`。也就是说，上层即便传了 deadline，走 `Remove` 这条链时也先在 manager 层失效，不只是 driver 层的问题。

2. 对 `Archive/Delete` 的风险概括为“只 Close 不 Remove”还不够。实际 `closeSessionIfActive(...)` 先后吞掉 `resolveBinding(...)` / `GetSession(...)` 失败，导致 `Archive/Delete` 甚至可能在完全没碰 session 的情况下返回成功。证据：`internal/module/thread/service.go:228-240`，`internal/module/thread/archive.go:5-13`，`internal/module/thread/service.go:102-119`。这比“留 stale session”更前一步，是“关闭动作本身就可能被静默跳过”。

3. Claude placeholder 分析仍不够深。报告抓到了 fresh start 后真实 session ID 不回填，但漏掉了 `historyTargetID(...)` 的优先级问题：它先选请求侧 `threadID`，再选 `ProviderThreadID/CodexThreadID/AgentID`；而 `ReadHistory/ReadMessages` 都直接用这个 target。证据：`internal/module/thread/service.go:258-272`，`internal/module/thread/history.go:13-20,22-28`。所以即便后面补了 binding repair，只要调用层继续传本地 alias，history 仍会优先打到 alias，而不是 provider thread ID。

4. `codexapp` recovery 段落把普通 RPC retry 和 session-level recovery 混在了一起。当前代码里，普通 provider RPC 已经会经 `callTransport(...)` 做一次 `attemptRecovery(...) + retry`；`sendApprovalDecision(...)` 现在也走这条 wrapper。证据：`internal/provider/codexapp/recovery.go:49-58`，`internal/provider/codexapp/session_approval.go:87-90`。因此“只修到了 transport reconnect”这个判断对“session 状态重建”是对的，但对“单次 RPC 是否会在断线后自动重试”则说得过粗。

### 3. 对 `docs/plans/迁移/cap-event-push.md` 的批判

1. 把 `ToolApprovalRequested/Resolved` 写成当前 live 的“双源发布”过宽。`ApprovalManager.publishRequested(...)` 要求 `bridge != nil && bridge.dispatcher != nil`，`publishResolved(...)` 要求 `pending.dispatcher != nil`；而当前 `codexapp` 审批入口 `requestApprovalDecision(...)` 调的是 `RequestApproval/RequestUserInput(s.ctx, nil, nil, req)`。证据：`internal/platform/rpc/approval_events.go:22-41`，`internal/platform/rpc/approval.go:79-85`，`internal/provider/codexapp/session_approval.go:38-43`。因此在当前 live `codexapp` 路径里，translator 是活跃 publisher，但 `ApprovalManager` 这一路并不是同时在线的第二个 live publisher。

2. 整份报告把“审批前端交互”过度压缩进 bus/push/Wails 三段式里，漏掉了 direct callback side channel。`ApprovalManager` 在有 `bridge + server` 时会 `dispatchApproval(...)`，再走 `PushBridge.CallbackClient(...) -> server.Callback(...)` 直接向前端发 callback 请求。证据：`internal/platform/rpc/approval.go:154-205`，`internal/platform/rpc/push.go:42-57`。这是独立于 typed bus `NotifyAll(...)` 和 Wails bridge 的另一条交互链，报告全文没有覆盖。

3. “`bridge-event` 负载形状与 V2 一致”的判断过宽。V3 `EventBridge.publish(...)` 只是单路 `EmitEvent("bridge-event", {"type":..., "payload": payloadToMap(...)})`；而 V2 在真正 emit 前还有窗口级 CWD 过滤，随后同时发 `bridge-event` 和 `agent-event` 两路。证据：`internal/ui/wails/bridge.go:81-109`，`go-agent-v2/cmd/agent-terminal/app_bridge.go:40-60,62-65,78-115`。所以最多只能说“外层 envelope 相似”，不能说桥接契约整体等价。

4. “tool approval 事件只有 `LogSink` 订阅，因此是软孤儿”这个表述缺少一层限定。对 typed bus 来说这句话成立；但当前 `codexapp` 真实审批请求还会走 `onNotification(...) -> handleApprovalRequest(...) -> requestApprovalDecision(...)`，进入独立的审批交互链。证据：`internal/provider/codexapp/session.go:233-242`，`internal/provider/codexapp/session_approval.go:14-43`。如果不把“typed bus 孤儿”和“端到端不可达”区分开，读者很容易把两者误读成同一件事。
