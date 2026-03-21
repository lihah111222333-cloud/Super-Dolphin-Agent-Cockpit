# P7w1 Fix Review: dto/store 视角复审

基于 LSP `text_search` / `workspace_symbol` / `references` / `call_hierarchy` / `read_file` 验证。

## Approval-turn Findings

1. `RestorePending` 已接线，但在当前实现里几乎是空操作。
   证据：
   `internal/platform/rpc/module.go:77` 到 `internal/platform/rpc/module.go:87` 在 Fx `OnStart` 调 `approvals.RestorePending(...)`。
   `internal/platform/rpc/approval.go:63` 到 `internal/platform/rpc/approval.go:71` 的 `NewApprovalManager` 每次启动都新建空的 `pending` / `pendingByRequestID` map。
   对 `internal/platform/rpc` 做 `text_search("PendingSnapshot(")` 只有 `internal/platform/rpc/module.go:93` 的 `OnStop` 使用，没有任何持久化/回灌入口。
   结论：
   这条修复“函数被调用”了，但并不能在真实进程重启后恢复 pending approval。

2. `Cleanup` 与 `RestorePending` 是不对称的，停止时会直接把全部 pending 强制超时。
   证据：
   `internal/platform/rpc/module.go:89` 到 `internal/platform/rpc/module.go:99` 在 `OnStop` 先拿 `PendingSnapshot()`，然后直接 `approvals.Cleanup(time.Nanosecond)`。
   `internal/platform/rpc/approval_lifecycle.go:10` 到 `internal/platform/rpc/approval_lifecycle.go:20` 的 `Cleanup` 只按 `createdAt` 和 cutoff 判定，`time.Nanosecond` 等价于“立即清空全部历史 pending”。
   结论：
   这并不是“生命周期恢复”，而是“启动时尝试恢复、停止时无条件抹掉”。它只对运行中 callback 断连有帮助，对真正的 stop/start 恢复没有闭环。

3. `request_user_input` bridge 仍然只覆盖 codexapp 这一路。
   证据：
   `call_hierarchy(incoming)` 显示 `internal/platform/rpc/approval.go:101` 的 `RequestUserInput` 只有一个调用方：`internal/provider/codexapp/session_approval.go:38` 的 `requestApprovalDecision(...)`。
   对 `internal/provider` 做 `text_search("RequestUserInput(")` 只有 `internal/provider/codexapp/session_approval.go:41` 一处命中。
   结论：
   这次修复把 codexapp 的 `request_user_input` 接进来了，但没有形成 provider 级通用桥接。

4. 入站 `approval/respond` 已兼容 snake_case，但出站 callback payload 仍然只发 camelCase。
   证据：
   `internal/module/turn/rpc_types.go:31` 到 `internal/module/turn/rpc_types.go:69` 的 `approvalRespondParams.UnmarshalJSON` 已兼容 `callId/requestId` 和 `call_id/request_id`。
   但 `internal/platform/rpc/approval_events.go:79` 到 `internal/platform/rpc/approval_events.go:99` 的 `callbackParams(...)` 仍然只写 `requestId`、`callId`、`toolName`、`approvalId`、`sourceMethod`、`agentId`、`threadId`、`turnId`。
   结论：
   这次修的是“respond 入站兼容”，不是“approval wire 全链路兼容”。协议还是混用 camel/snake。

## Provider Findings

1. ReadLoop 竞态没有完全收口，`attemptRecovery` 仍可能被重复触发两次以上，只是从并发变成串行。
   证据：
   `call_hierarchy(incoming)` 显示 `internal/provider/codexapp/recovery.go:69` 的 `attemptRecovery(...)` 仍有两个入口：`callTransport(...)` 和 `handleConnectionDead(...)`。
   `internal/provider/codexapp/recovery.go:21` 的 `CheckHealth(...)` 没有任何 incoming caller。
   `internal/provider/codexapp/recovery.go:73` 到 `internal/provider/codexapp/recovery.go:93` 只用 `recoveryMu` 串行化，没有在恢复前做健康短路判断。
   结论：
   这次修复避免了 read loop 并发重启，但没有避免同一故障被“调用路径 + 通知路径”重复恢复。

2. callback method 补齐只补到了审批桥，没补到 typed event 翻译器。
   证据：
   `internal/provider/codexapp/session_approval.go:102` 到 `internal/provider/codexapp/session_approval.go:114` 的 `isRequestUserInputMethod(...)` 已支持 `codex/event/request_user_input`、`item/commandExecution/request_user_input`、`item/tool/requestUserInput` 等变体。
   但 `internal/provider/codexapp/event_map.go:132` 到 `internal/provider/codexapp/event_map.go:144` 的 `translateToolEvent(...)` 仍然只识别 `item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`skill/requestApproval`、`tool.approval.requested`。
   对 `internal/provider/codexapp/event_map.go` 做 `text_search("request_user_input")` / `text_search("requestUserInput")` 都没有命中。
   结论：
   callback bridge 修了，typed event/UI 观察面没有同步修。

3. codex history metadata 修复引入了新的非协议字段，形状和现有 `InputItem` 不对齐。
   证据：
   `internal/provider/codexapp/history_rollout.go:130` 到 `internal/provider/codexapp/history_rollout.go:143` 会在 metadata 里写 `todo` 和 `missing_input_types`。
   `internal/dto/shared/input.go:3` 到 `internal/dto/shared/input.go:9` 的输入项形状只有 `type/content/path/name/url`。
   对全仓库做 `text_search("missing_input_types")` 只有 `internal/provider/codexapp/history_rollout.go:137` 一处命中；
   做 `text_search("\"todo\"")` 也只有 `internal/provider/codexapp/history_rollout.go:136` 一处命中。
   结论：
   这不是恢复 metadata，而是把内部 TODO 泄漏成了外部 payload，而且没有任何消费方。

4. Claude history metadata 依然没有真正恢复 native non-text items。
   证据：
   `internal/provider/claudecli/history.go:109` 到 `internal/provider/claudecli/history.go:117` 的 `extractHistoryText(...)` 仍然只拼接 `type=="text"`。
   `internal/provider/claudecli/history.go:124` 到 `internal/provider/claudecli/history.go:156` 仍然只从注入文本里的附件提示重建 metadata。
   `internal/provider/claudecli/history.go:127` 还保留着 `TODO(P7): recover structured attachment metadata directly if Claude history adds non-text items.`。
   结论：
   provider Agent 的 “history metadata” 修复只覆盖了 Codex 本地 rollout 的一部分，Claude 路径仍然是半成品。

5. 事件时间统一后，provider event_map 仍然没有完全对齐，因为 codexapp 的合成恢复事件仍不带 payload 时间。
   证据：
   `internal/provider/codexapp/recovery.go:75` 到 `internal/provider/codexapp/recovery.go:82` 发出的 `recovery.attempt` payload 只有 `agentId/threadId/reason/attempt`，没有 `timestamp`。
   `internal/provider/codexapp/event_map.go:296` 到 `internal/provider/codexapp/event_map.go:305` 的 `eventTime(...)` 仍是“先读 payload 时间，否则 fallback 到 `time.Now()`”。
   结论：
   event_map 已经支持统一策略，但 producer 端没有全部补齐 payload 时间，`recovery.attempt` 这一类事件仍然走 fallback 时间。

## Compatibility Notes

1. `WrapStoreError` 扩展后，对 approval pending 查找没有直接影响，因为 approval 仍然完全是内存索引。
   证据：
   对 `internal/platform/rpc` 做 `text_search("internal/platform/db")` 没有命中。
   `internal/platform/rpc/approval.go:19` 到 `internal/platform/rpc/approval.go:25` 仍是 `pending` / `pendingByRequestID` 内存 map。
   `internal/platform/rpc/approval.go:108` 到 `internal/platform/rpc/approval.go:118` miss 时仍返回 `rpc.ErrNotFound(...)`。
   结论：
   “不会被我这轮 store 包装改坏”成立；
   “能共享统一 not found 语义”仍然不成立。

2. 事件时间统一后，provider event_map 只算“消费侧对齐”，不是“生产-消费双端对齐”。
   证据：
   `internal/provider/claudecli/event_map.go:105` 到 `internal/provider/claudecli/event_map.go:136` 已经统一走 `eventTime(...)`；
   `internal/provider/codexapp/event_map.go:296` 到 `internal/provider/codexapp/event_map.go:305` 也统一了 payload-time-first。
   但 `internal/provider/codexapp/recovery.go:75` 到 `internal/provider/codexapp/recovery.go:82` 这类合成事件仍无时间字段。
   结论：
   event_map 本身基本跟上了；
   provider 侧事件生产器还没有全部对齐。
