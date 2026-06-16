# 系统级修复审查 — Agent B
## 1. S8 fireOrForceLocked（逐项验证+行号）

### 1.1 `fireOrForceLocked` 是否真的移除了 force fallback
- `OK`。`internal/sidecar/orch/orchestration/service.go:266-279` 的实现只做三件事：兜底 `context.Background()`、校验 `agent.sm` 非空、调用 `fireAndPublishLocked`。失败时仅用 `AllowedTriggers` 组装 `errIllegalStateTransition` 返回；没有任何直接写 `agent.state` 的 fallback 路径。真正的状态迁移仍只发生在 `internal/sidecar/orch/orchestration/service.go:281-289` 的 `agent.sm.FireCtx(...)`。

### 1.2 返回错误后，调用点是否正确处理非法转换错误
- `OK`。队列接单路径在 `internal/sidecar/orch/orchestration/service.go:301-309` 出错时会把 submission 放回队列并 `Warn`，不会静默丢 turn。
- `OK`。`CompleteTurn` 在 `internal/sidecar/orch/orchestration/service.go:337-345` 把非法转换直接向上返回；订阅端 `internal/sidecar/orch/orchestration/module.go:38-43` 只记录日志，不会 panic。
- `OK`。进程退出路径在 `internal/sidecar/orch/orchestration/service.go:378-389` 对失败只记 `Warn`，不会中断 runner。
- `OK`。ready-state reconcile 在 `internal/sidecar/orch/orchestration/helpers.go:123-136` 出错只记 `Warn`。
- `Warning`。`Recover` 路径先发布 recovering 事件，再做状态机迁移验证：`internal/sidecar/orch/orchestration/recover.go:35-37` 先调用 `publishAgentRecovering`，真正的 `RecoverRequested` 触发在 `internal/sidecar/orch/orchestration/recover.go:50-57`。如果这里返回非法转换，`internal/sidecar/orch/orchestration/events.go:45-53` 的 recovering 事件已经发出，事件流会先于状态机失败结果。

### 1.3 `state.go` 新增合法路径是否合理
- `OK`。turn 相关新增路径是自洽的：`StateTurnQueued -> TriggerTurnAccepted -> StateTurnStarting`、`StateTurnStarting -> TriggerTurnAccepted -> StateTurnRunning`、`StateTurnStarting/StateTurnRunning -> TriggerTurnCompleted -> StateIdle`，定义在 `internal/dto/agent/state.go:85-96`。这与 `claimTurnWork`/`startTurnExecution` 的两阶段推进一致，见 `internal/sidecar/orch/orchestration/service.go:305-317` 与 `internal/sidecar/orch/orchestration/helpers.go:140-150`。
- `OK`。恢复与进程退出路径也基本闭合：`StateRecovering -> TriggerLaunchSucceeded/TriggerLaunchFailed` 在 `internal/dto/agent/state.go:105-106`，`StateStopping -> TriggerProcessExited -> StateStopped` 在 `internal/dto/agent/state.go:107`，`StateStopped -> TriggerRecoverRequested/TriggerLaunchSucceeded` 在 `internal/dto/agent/state.go:108-109`。`handleProcessExitTransition` 还把 `Provisioning/Recovering` 的退出重映射为 `LaunchFailed`，见 `internal/sidecar/orch/orchestration/service.go:381-385`。

### 1.4 是否存在“去掉 fallback 后正常流程变成非法转换”的回归
- `Warning`。`StopAgent` 没有按状态预判，直接触发 `TriggerStopRequested`：`internal/sidecar/orch/orchestration/service.go:114-123`、`internal/sidecar/orch/orchestration/service.go:142-150`。但状态表没有 `Provisioning/Recovering/Stopping/Stopped -> StopRequested`，见 `internal/dto/agent/state.go:79-112`。同时 launch 前准备会把 agent 直接放到 `StateProvisioning`，见 `internal/sidecar/orch/orchestration/helpers.go:52-60`。因此“启动中/恢复中立刻 stop”在去掉 fallback 后会变成硬错误。
- `OK`。turn 主路径、进程退出主路径、恢复主路径没有再看到依赖 force fallback 才能走通的内部调用；现有内部调用点都已显式处理返回值，证据见 `internal/sidecar/orch/orchestration/service.go:240-243`、`internal/sidecar/orch/orchestration/service.go:255-259`、`internal/sidecar/orch/orchestration/service.go:305-309`、`internal/sidecar/orch/orchestration/service.go:387-389`、`internal/sidecar/orch/orchestration/helpers.go:124-136`、`internal/sidecar/orch/orchestration/recover.go:50-57`。

### 1.5 `handleProcessExit` 拆分后是否符合 CC≤10
- `OK`。`internal/sidecar/orch/orchestration/service.go:351-366` 的 `handleProcessExit` 只有一个 guard 条件分支，然后把副作用拆到 `recordProcessExitError` 与 `handleProcessExitTransition`；转移判断集中在 `internal/sidecar/orch/orchestration/service.go:378-389`，总复杂度明显低于 10。

## 2. S2 TurnCompleted→CompleteTurn（逐项验证+行号）

### 2.1 是否订阅了 `TurnStarted` 和 `TurnCompleted`
- `OK`。`fx.Invoke(registerTurnLifecycle)` 已注册在 `internal/sidecar/orch/orchestration/module.go:15-23`。`registerTurnLifecycle` 在 `internal/sidecar/orch/orchestration/module.go:33-44` 同时订阅 `turndto.TurnStarted` 和 `turndto.TurnCompleted`。

### 2.2 `TurnStarted` 是否把 provider 的 `turnID` 绑定回 orchestration
- `OK`。订阅端在 `internal/sidecar/orch/orchestration/module.go:33-36` 调用 `svc.BindActiveTurnID(...)`。`BindActiveTurnID` 会在校验 agent 存在、`turnID` 非空、当前已有 active turn 后，把 `agent.activeTurnID` 替换成事件里的 `turnID`，见 `internal/sidecar/orch/orchestration/helpers.go:81-97`。

### 2.3 `TurnCompleted` 是否调用 `CompleteTurn`，参数映射是否正确
- `OK`。订阅端直接把 `ev.AgentID`、`ev.TurnID`、`ev.Success`、`ev.Error` 传给 `CompleteTurn`，见 `internal/sidecar/orch/orchestration/module.go:38-43`。`CompleteTurn` 内部把 `success=true` 映射到 `TriggerTurnCompleted`，把 `success=false` 映射到 `TriggerTurnAborted` 并写入 `lastError`，见 `internal/sidecar/orch/orchestration/service.go:337-345`。

### 2.4 `agentID` 从事件中提取是否可靠
- `Blocker`。从 DTO 结构上看，`TurnHeader` 确实内嵌了 `AgentHeader.AgentID`，见 `internal/dto/shared/event.go:44-60`。但 `codexapp` 的 turn translator 只从 payload 读取 `agentId/agent_id`，见 `internal/provider/codexapp/event_map.go:150-165`；`codexapp` session 在收到通知时又是把原始 `params` 原样 dispatch，见 `internal/provider/codexapp/session.go:230-235`，没有像 `claudecli` 那样在 dispatch 前用 session 上下文回填 `AgentID`，对照 `internal/provider/claudecli/session_events.go:33-44`。因此 `module.go:33-43` 对 `ev.AgentID` 的依赖没有代码级保证；一旦 provider payload 不带 `agentId`，`CompleteTurn` 会在 `internal/sidecar/orch/orchestration/service.go:326-328` / `internal/sidecar/orch/orchestration/helpers.go:169-175` 查找失败，而订阅端又在 `internal/sidecar/orch/orchestration/module.go:40-43` 把 `errAgentNotFound` 直接吞掉，闭环会静默失效。

### 2.5 错误处理：`CompleteTurn` 失败时是否 log、不 panic
- `OK`。`internal/sidecar/orch/orchestration/module.go:38-43` 对 `CompleteTurn` 的返回值只做 `Warn`，没有 panic 路径。

## 3. S6 approval 闭环（逐项验证+行号）

### 3.1 `approval.go` 放宽 pending 进入条件后是否仍然安全
- `OK`。放宽点在 `internal/platform/rpc/approval.go:149-164`：`bridge==nil || server==nil` 时允许 pending 保留，不再直接报错。`RequestApproval` 仍会先注册 pending 再等待，见 `internal/platform/rpc/approval.go:71-96`、`internal/platform/rpc/approval.go:125-147`。
- `Warning`。这里没有超时兜底；等待只依赖 `ctx.Done()` 或 `pending.done`，见 `internal/platform/rpc/approval_support.go:34-47`。`codexapp` 传入的是 session 级 context，见 `internal/provider/codexapp/session_approval.go:31`，其生命周期从 `internal/provider/codexapp/session.go:81-93` 创建，到 `internal/provider/codexapp/session.go:204-213` 的 `Close/ForceStop` 才取消。因此 pending 不会永久泄漏到 session 生命周期之外，但可以在活跃 session 中无限期挂起。

### 3.2 `codexapp` session 收到审批请求时，是否正确创建 pending
- `Blocker`。当前顺序不是“provider event -> pending -> respond”，而是“provider event -> 对外发布 -> 异步创建 pending”。`internal/provider/codexapp/session.go:230-235` 先 `dispatch` 原始事件，再进入 `handleApprovalRequest`；translator 会立刻把该事件转成 `ToolApprovalRequested`，见 `internal/provider/codexapp/event_map.go:132-137`。但 `handleApprovalRequest` 在 `internal/provider/codexapp/session_approval.go:18-23` 又异步起 goroutine，真正的 `RequestApproval`/`registerPending` 发生在 `internal/provider/codexapp/session_approval.go:26-35` 与 `internal/platform/rpc/approval.go:76-83`、`internal/platform/rpc/approval.go:125-147`。这意味着外部若在收到 `ToolApprovalRequested` 后立即调用 `approval/respond`，`internal/platform/rpc/approval.go:110-115` 仍可能返回 `approval is not pending`。
- `OK`。除上述竞态外，请求内容本身构造完整：`internal/provider/codexapp/session_approval.go:38-64` 会提取 `callId/approvalId/requestId/toolName/agentID/threadID/turnID`，并在缺失 `callId` 时回退到 `approvalId` 或 `requestId` 字符串。

### 3.3 `approval/respond` 收到后，决策是否正确回传给 provider
- `OK`。RPC 入口 `internal/module/turn/rpc.go:79-91` 把 `callId/requestId/approved/decision` 传给 `approver.Respond(...)`。`ApprovalManager.Respond` 在 `internal/platform/rpc/approval.go:105-116` 查找并完成 pending。随后 `requestToolApproval` 从 `RequestApproval` 返回决策并调用 `sendApprovalDecision`，见 `internal/provider/codexapp/session_approval.go:31-35`；`sendApprovalDecision` 最终调用 provider 的 `approval/respond`，见 `internal/provider/codexapp/session_approval.go:66-83`。

### 3.4 整条链路：provider event → pending → respond RPC → resolve → 回传 provider
- `Warning`。除 3.2 的竞态外，闭环主链路本身是完整的：provider 事件入口在 `internal/provider/codexapp/session.go:230-235`，pending 注册在 `internal/platform/rpc/approval.go:125-147`，RPC resolve 在 `internal/platform/rpc/approval.go:105-116`，wait 完成后回传 provider 在 `internal/provider/codexapp/session_approval.go:31-35` 与 `internal/provider/codexapp/session_approval.go:66-83`。
- `Warning`。`codexapp` 这条路径调用 `RequestApproval(s.ctx, nil, nil, req)`，见 `internal/provider/codexapp/session_approval.go:31`，因此 `ApprovalManager` 自己不会通过 `publishRequested/publishResolved` 向 bridge 侧补事件，见 `internal/platform/rpc/approval_events.go:15-35`。当前外部可见性依赖 `internal/provider/codexapp/event_map.go:132-144` 对 provider 原始 approval 事件的翻译，而不是 `ApprovalManager` 的事件回放。

### 3.5 `claudecli` driver 是否也需要接入；若未接入是否标了 TODO
- `Warning`。`claudecli` 当前没有接入 approval manager：`internal/provider/claudecli/module.go:12-25` 的工厂没有 `*rpc.ApprovalManager` 参数，`internal/provider/claudecli/driver.go:36-48` 的 driver 构造也没有 approval 依赖，`internal/provider/claudecli/session.go:19-37` 的 session 结构体同样不存在 approvals 字段或 approval 处理入口。代码中未看到与这处缺口相邻的迁移占位。

### 3.6 `session_approval.go` 行数是否符合守卫
- `OK`。`internal/provider/codexapp/session_approval.go:1-83` 总长 83 行；最大函数 `buildApprovalRequest` 仅 `internal/provider/codexapp/session_approval.go:38-64`，其余函数分别是 `14-24`、`26-36`、`66-83`，文件仍在可维护守卫内。

## 4. 跨修复一致性

- `OK`。S2 的 `TurnStarted/TurnCompleted` 订阅与 S1 的 push 订阅不存在竞争消费问题。push 侧单独在 `internal/platform/rpc/push.go:75-91` 订阅同一批 typed events；底层 `internal/platform/bus/resilient.go:10-23` 只是对 `event.Subscribe` 做 panic 保护，语义是 fan-out，不是独占消费。
- `Warning`。S8 去掉 force fallback 后，S2 的 `CompleteTurn` 并不能保证“所有路径都成功转移”。如果 `ev.AgentID` 为空或 turn 已不活跃，`CompleteTurn` 会在 `internal/sidecar/orch/orchestration/service.go:331-345` 返回 `errTurnNotActive`/`errAgentNotFound`，而订阅端 `internal/sidecar/orch/orchestration/module.go:40-43` 会直接忽略这两类错误；这会让 S2 回调静默失效，而不是显式暴露问题。
- `OK`。`BindActiveTurnID` 无重复定义。LSP `text_search` 仅命中定义 `internal/sidecar/orch/orchestration/helpers.go:77-98` 和唯一调用点 `internal/sidecar/orch/orchestration/module.go:34`。

## 5. 编译守卫

- `OK`。`go build ./...` 通过。
- `OK`。`go vet ./...` 通过。
- `OK`。`go test ./internal/archtest/... -count=1` 通过，结果为 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.372s`。
- `OK`。LSP `diagnostics` 在本次审查文件中未发现 error/warning 级别问题；仅有 `internal/provider/codexapp/session.go:345` 的 `maps.Copy` 优化提示，不影响本次结论。

## 结论（Blocker / Warning / OK）

- `Blocker`
  - S2：`codexapp` turn 事件没有代码级 `AgentID` 回填保证，`CompleteTurn/BindActiveTurnID` 对 `ev.AgentID` 的依赖可能被静默吞掉，证据见 `internal/provider/codexapp/session.go:230-235`、`internal/provider/codexapp/event_map.go:150-165`、`internal/sidecar/orch/orchestration/module.go:33-43`。
  - S6：approval 事件先对外发布、后异步 `registerPending`，`approval/respond` 存在 “先响应、后注册” 竞态，证据见 `internal/provider/codexapp/session.go:230-235`、`internal/provider/codexapp/session_approval.go:18-35`、`internal/platform/rpc/approval.go:110-147`。
- `Warning`
  - S8：`Recover` 先发 recovering 事件、后做状态机验证，失败时会产生误导性事件，证据见 `internal/sidecar/orch/orchestration/recover.go:35-37`、`internal/sidecar/orch/orchestration/recover.go:50-57`、`internal/sidecar/orch/orchestration/events.go:45-53`。
  - S8：`StopAgent` 在 `Provisioning/Recovering/Stopping/Stopped` 上会因缺少 `StopRequested` 转移而报非法转换，证据见 `internal/sidecar/orch/orchestration/service.go:114-123`、`internal/sidecar/orch/orchestration/service.go:142-150`、`internal/sidecar/orch/orchestration/helpers.go:52-60`、`internal/dto/agent/state.go:79-112`。
  - S6：`claudecli` 未接入 approval manager，当前只有 `codexapp` 形成闭环，证据见 `internal/provider/claudecli/module.go:12-25`、`internal/provider/claudecli/driver.go:36-48`、`internal/provider/claudecli/session.go:19-37`。
- `OK`
  - `fireOrForceLocked` 本体已不再 bypass 声明式状态表，证据见 `internal/sidecar/orch/orchestration/service.go:266-289`。
  - `handleProcessExit` 拆分后复杂度显著下降，主函数与辅助函数职责清晰，证据见 `internal/sidecar/orch/orchestration/service.go:351-389`。
