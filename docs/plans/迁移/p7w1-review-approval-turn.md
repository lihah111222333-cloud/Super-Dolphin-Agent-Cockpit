# P7 Wave 1 Review: approval + turn 视角互审

## D5 + D11 + D12

1. `D5` 没有做到“统一 store 错误包装”，目前只覆盖了 `binding/thread/workspace` 三个 store，其他 store 仍然直接返回原始错误。
   证据：
   `internal/platform/db/errors.go:47-61` 定义了统一入口 `WrapStoreError`。
   `internal/store/binding/store.go:104-106`、`internal/store/thread/store.go:145-147`、`internal/store/workspace/store.go:152-154` 已接线。
   但 `internal/store/taskdag/store.go:21-34`、`internal/store/topologyapproval/store.go:15-34`、`internal/store/agentstatus/store.go:17-31` 仍然是裸 `err` 返回。
   结论：这不是“统一包装”，只是局部包装；后续调用方仍会同时面对 `StoreError` 和底层驱动错误两套形态。

2. `D11` 把 typed event DTO 改成 snake_case，但 approval/turn 的 RPC wire 仍是 lowerCamel，结果是同一系统内出现混合契约。
   证据：
   `internal/dto/shared/event.go:47-86` 把事件头字段定义成 `thread_id/agent_id/session_id/turn_id/call_id/tool_name/approval_id`。
   `internal/dto/agent/event.go:6-10` 继续把 `StateChanged` 定义成 `old_state/new_state`。
   与之相对，approval/turn RPC 仍然明确使用 `threadId/turnId/requestId/callId`，见 `internal/module/turn/rpc_types.go:19-40`、`internal/platform/rpc/approval.go:47-60`。
   同时，事件发布侧不会做命名转换：`internal/platform/rpc/push.go:35-73` 直接把 typed event 作为 `params` 推给 jrpc2，`internal/ui/wails/bridge.go:81-109` 也是直接 `json.Marshal` 后再回填成 map。
   结论：这轮 snake_case 治理没有收敛契约，反而把“RPC 方法面是 lowerCamel、事件推送面是 snake_case”的分裂固定下来了。它和我的 approval/turn 修复没有直接运行时冲突，但会让事件订阅端与 RPC 调用端看到两套字段命名。

3. `D12` 的 `EventHeader` 重构只统一了结构体嵌套，没有统一 turn 事件的时间语义；不同 provider 仍在构造不同来源的时间戳。
   证据：
   `internal/dto/shared/event.go:41-73` 把 `Timestamp` 上收到了 `EventHeader`。
   但 `internal/provider/claudecli/event_map.go:105-123` 构造 `AgentSessionHeader/TurnHeader` 时直接写 `time.Now()`。
   `internal/provider/codexapp/event_map.go:150-173` 则是用 `eventTime(payload)`。
   结论：header 形状虽然统一了，turn 事件发布时间语义并没有统一；跨 provider 看板、timeline 排序和归因仍可能出现不一致。这一项会直接影响 turn 事件发布链路，但不影响我这边 approval pending 的 in-memory 查找。

4. 从 approval/turn 交叉面看，`D5` 对 pending 查找没有直接影响，影响点主要落在 turn 的 session 解析路径。
   证据：
   `internal/platform/rpc/approval.go:19-24` 里的 pending 状态完全是内存 map，不走 store。
   `internal/provider/unified/session_resolver.go:23-46` 才是 turn RPC 真实依赖的 store 入口，其中 `GetByThreadID` 的错误会直接包到 `ResolveSession` 返回值里。
   结论：store 错误包装不会改变 approval pending 的命中逻辑；它只会改变 turn/session 解析时暴露给上层的错误外形。

## D7 + D8 + D9 + D10

1. `D9` 的 codexapp history fallback 仍然接错了 RPC 方法，远端回退路径实际上取不到 history。
   证据：
   `internal/provider/codexapp/history.go:19-39` 在本地 rollout 不可用时调用 `thread/read`，并尝试解码 `{"history": [...]}`。
   但 `internal/module/thread/rpc.go:47-53` 清楚地表明 `thread/read` 走的是 `svc.Get(...)`，真正的消息接口是 `thread/messages`。
   结论：一旦本地 rollout 缺失，codexapp 的远端 history fallback 会读到错误的 payload 结构，D9 在这个分支上仍然是坏的。

2. `D10` 的 recovery 接线存在读循环丢失竞态，reconnect 成功后可能根本没有活跃的 reader。
   证据：
   初始 session 在 `internal/provider/codexapp/session.go:71-98` 启动时就会 `go transport.ReadLoop(...)`。
   恢复后 `internal/provider/codexapp/session_recovery.go:32-51` 又会再次 `go s.transport.ReadLoop(...)`。
   但 `internal/provider/codexapp/transport.go:112-119` 的 `ReadLoop` 由 `looping.CompareAndSwap(false, true)` 串行化，已经有 loop 在跑时新的 loop 会直接返回。
   同时 `internal/provider/codexapp/transport.go:141-152` 的 `reconnect` 会先 `closeSocket()` 再 `connect(ctx)`。
   结论：如果 recovery 发生时旧 read loop 还没退出，新 loop 会因为 `looping=true` 立即返回；等旧 loop 因旧 socket 被关掉而退出后，新连接上反而没有 reader 了。

3. recovery telemetry 与实际重试次数不一致，`attempt` 字段被硬编码成了 1。
   证据：
   `internal/provider/codexapp/session_recovery.go:38-45` 发布 `recovery.attempt` 时固定写 `"attempt": 1`。
   `internal/provider/codexapp/recovery.go:28-44` 又把真正的 reconnect 包在 `shared.Retry(ctx, attempts, ...)` 里，且 `maxRetry` 在 `internal/provider/codexapp/session.go:83-88` 被配置为 `3`。
   结论：日志/UI/审计事件看到的 recovery attempt 数并不代表真实重试轮次，诊断价值被削弱。

4. provider 这轮 recovery 改动和我的 `approval/respond` 修复存在直接冲突：approval 回传仍绕过 recovery 通道。
   证据：
   普通 RPC 已经走 `callTransport`，见 `internal/provider/codexapp/session_recovery.go:12-21`，这里会在失败后触发 `attemptRecovery(...)`。
   但审批回传 `internal/provider/codexapp/session_approval.go:66-83` 仍然直接调用 `s.transport.Call(..., "approval/respond", ...)`。
   结论：一旦连接在 approval callback 之后、`approval/respond` 之前掉线，普通 turn RPC 会尝试恢复，而 approval 决议不会。这和我在 D4 做的 approval/request_user_input 桥接是直接冲突的，审批链路仍然比普通 RPC 脆弱。

5. `D9` 的“history metadata 修复”仍然是部分完成，两个 driver 都保留了明确的 TODO。
   证据：
   `internal/provider/codexapp/history_rollout.go:88-109` 只恢复 `input_image`，并在 `101-102` 明写“local rollout artifacts do not currently persist non-image attachment metadata”。
   `internal/provider/claudecli/history.go:124-156` 只从注入文本里恢复附件 metadata，并在 `127-128` 明写“recover structured attachment metadata directly ...”仍是 TODO。
   结论：D9 不是彻底收口，只是把最窄的一类 metadata 先补上了；带 mention/file 等结构化输入的历史恢复仍会退化。
