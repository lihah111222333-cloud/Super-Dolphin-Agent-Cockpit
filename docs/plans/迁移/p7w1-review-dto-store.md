# P7 波次 1 互审：dto+store 视角批判 D1+D2+D4 / D7+D8+D9+D10

基于 LSP `text_search` / `workspace_symbol` / `references` / `call_hierarchy` / `read_file` 验证。

## Findings

### A. D1 + D2 + D4: approval-turn

1. `request_user_input` 桥接并没有真正接线完成。
   证据：
   `internal/platform/rpc/approval.go:101` 定义了 `ApprovalManager.RequestUserInput`，但对该符号做 `call_hierarchy(incoming)` 没有任何调用方。
   `internal/provider/codexapp/session.go:233` 只处理 `item/commandExecution/requestApproval` 和 `tool.approval.requested`。
   对当前仓库 `internal/provider/codexapp` 做 `text_search("request_user_input")` 没有命中。
   对照：
   `go-agent-v2/internal/apiserver/server_event_handler.go:221` 到 `go-agent-v2/internal/apiserver/server_event_handler.go:233` 明确把 `request_user_input` 事件桥接到统一审批通道。
   影响：
   D4 名义上修的是 callback method + `request_user_input` 桥接，但当前 V3 里这条桥并没有被任何 provider 触发。

2. `approval/respond` 仍然只接受 camelCase，和 V2 的 snake_case 兼容层不对齐。
   证据：
   `internal/module/turn/rpc_types.go:28` 到 `internal/module/turn/rpc_types.go:32` 的 `approvalRespondParams` 只有 `json:"callId"` 和 `json:"requestId"`，没有 `UnmarshalJSON` 兼容层。
   `internal/module/turn/rpc.go:82` 到 `internal/module/turn/rpc.go:94` 直接把这个结构体喂给 `approver.Respond(...)`。
   对照：
   `go-agent-v2/internal/apiserver/server_approval.go:98` 明确同时提取 `requestId` / `request_id` / `requestID`；
   `go-agent-v2/internal/apiserver/server_approval_hardening_test.go:101` 也专门守了 `request_id`。
   影响：
   如果前端或兼容客户端发 `request_id` / `call_id`，当前实现会静默丢掉 `requestID` / `callID`，直接把 D1 的 requestId 维度退化回“不可靠”。

3. outbound approval callback payload 仍然是纯 camelCase，和本轮 DTO snake_case 收口方向相冲突。
   证据：
   `internal/platform/rpc/approval_events.go:73` 到 `internal/platform/rpc/approval_events.go:92` 只写入 `requestId`、`callId`、`toolName`、`approvalId`、`sourceMethod`、`agentId`、`threadId`、`turnId`。
   对照：
   V2 的审批提取逻辑 `go-agent-v2/internal/apiserver/server_approval.go:98` 明确接受 snake/camel 双格式。
   影响：
   现在 approval 这条 wire 仍然是协议特例；即使 D11 已经把 typed event tag 收到 snake_case，approval callback 依旧迫使消费端保留 camelCase 特判。

4. requestId 索引路径没有接入 D5 的统一 NotFound 语义，只是“互不冲突”，不是“兼容归一”。
   证据：
   对 `internal/platform/rpc` 做 `text_search("internal/platform/db")` 没有任何命中。
   `internal/platform/rpc/approval.go:114` 在 miss 时返回的是 `rpc.ErrNotFound("approval is not pending")`。
   `internal/platform/rpc/errors.go:13` 到 `internal/platform/rpc/errors.go:15` 这是一层 jrpc2 错误，不是 `internal/platform/db.ErrNotFound`。
   影响：
   requestId 索引和我的 D5 store 包装没有直接冲突，但也没有共享错误语义。调用方无法用一套 `errors.Is(..., platformdb.ErrNotFound)` 统一处理 store miss 和 approval miss。

### B. D7 + D8 + D9 + D10: provider

1. Claude capability 声明仍然和 V2 不一致，`context_compact` 还没补回来。
   证据：
   `internal/provider/claudecli/driver.go:13` 到 `internal/provider/claudecli/driver.go:17` 只声明了 `message_send` / `model_switch` / `turn_override`。
   `internal/module/thread/rpc.go:63` 的 `thread/compact/start` 仍然依赖 `context_compact` capability。
   对照：
   `go-agent-v2/legacy-agentsdk/claude/capabilities.go:42` 到 `go-agent-v2/legacy-agentsdk/claude/capabilities.go:44` 明确把 `CapabilityContextCompact` 标成 `true`。
   影响：
   D8 号称修 capability 声明不一致，但当前 Claude 仍然会把 `thread/compact/start` 错误地 gate 掉。

2. Claude 的 runtime `Configure` 仍然是显式失败，D7 实际上没修完。
   证据：
   `internal/provider/claudecli/session_config.go:13` 到 `internal/provider/claudecli/session_config.go:27` 对任何非空 patch 都返回
   `claudecli: runtime Configure is not supported for active sessions`。
   `call_hierarchy(incoming)` 显示它是从 `internal/module/thread/command.go:26` 调进来的。
   同时 `internal/provider/claudecli/driver.go:13` 到 `internal/provider/claudecli/driver.go:17` 又继续宣称 `model_switch` / `turn_override` 可用。
   影响：
   `/model` 这类 live thread 配置仍然会在活跃 session 上失败，能力声明和实际行为继续打架。

3. Claude resume 路径丢了自定义 history root，D9 在非默认 `CLAUDE_HOME` 下会退化。
   证据：
   `internal/provider/claudecli/driver.go:75` 到 `internal/provider/claudecli/driver.go:84` 的 `ResumeSession` 没有把 `historyDir` 带回 `startSpec`。
   `internal/provider/claudecli/driver.go:108` 用 `spec.historyDir` 构建 `historyBackend`。
   `internal/provider/claudecli/history.go:70` 到 `internal/provider/claudecli/history.go:81` 在 `sessionDir` 为空时回退到 `CLAUDE_HOME` / `~/.claude`。
   影响：
   如果 session 是用自定义 `history_dir` / `claude_home` 启动的，resume 之后 `ReadHistory` 会去错误目录找历史。

4. Claude history metadata 恢复仍然是半成品，native attachment metadata 继续丢。
   证据：
   `internal/provider/claudecli/history.go:109` 到 `internal/provider/claudecli/history.go:117` 的 `extractHistoryText` 只保留 `type=="text"`。
   `internal/provider/claudecli/history.go:124` 到 `internal/provider/claudecli/history.go:156` 只会从注入到文本里的“附件提示头”重建 metadata。
   `internal/provider/claudecli/history.go:127` 还留着 `TODO(P7): recover structured attachment metadata directly if Claude history adds non-text items.`。
   影响：
   D9 还没有真正覆盖 Claude 的 structured attachment metadata 恢复。

5. Codex rollout history metadata 恢复也还是半成品，只保留图片。
   证据：
   `internal/provider/codexapp/history_rollout.go:88` 到 `internal/provider/codexapp/history_rollout.go:109` 只在 `input_image` 上写 metadata。
   `internal/provider/codexapp/history_rollout.go:101` 到 `internal/provider/codexapp/history_rollout.go:102` 明确写着
   `TODO(P7): local rollout artifacts do not currently persist non-image attachment metadata.`。
   影响：
   D9 在 Codex 本地 rollout 路径上也没有完成，文件/mention 类输入照样丢失。

6. Codex recovery 只是“能重连”，不是“恢复闭环”；健康检查没接线，attempt 计数也是假的。
   证据：
   `internal/provider/codexapp/recovery.go:18` 到 `internal/provider/codexapp/recovery.go:26` 的 `CheckHealth` 做 `call_hierarchy(incoming)` 没有任何调用方。
   `internal/provider/codexapp/session_recovery.go:38` 到 `internal/provider/codexapp/session_recovery.go:45` 每次 recovery event 都固定发 `"attempt": 1`。
   但 `internal/provider/codexapp/recovery.go:32` 到 `internal/provider/codexapp/recovery.go:39` 实际会在 `Reconnect` 里做多次 retry。
   影响：
   D10 仍然是部分接线：没有真正用健康检查闭环恢复，恢复事件里的 `attempt` 也不能反映真实重试次数。

## Compatibility Notes

1. approval requestId 索引与 D5 store 错误包装没有直接冲突，但它也没有接入统一错误语义。
   结论：
   “不冲突”成立；
   “完全兼容”不成立，因为 approval miss 仍是 `rpc.ErrNotFound`，不是 `platformdb.ErrNotFound`。

2. provider 的 live event path 已经跟上了新的 `EventHeader` 层级，但 history metadata 恢复路径其实绕开了 typed event。
   证据：
   `internal/provider/claudecli/event_map.go:105` 到 `internal/provider/claudecli/event_map.go:130`、
   `internal/provider/codexapp/event_map.go:150` 到 `internal/provider/codexapp/event_map.go:188`
   都在构造新的 `shared.ThreadHeader` / `shared.TurnIDHeader`。
   但 history 路径是
   `internal/provider/claudecli/session_history.go:13` /
   `internal/provider/codexapp/session_history.go:13`
   直接返回 `[]dto.Message`，再由 `internal/module/thread/history.go:13` 原样透传。
   结论：
   live event 映射和我的 EventHeader 重构是对齐的；
   history metadata 恢复则与 EventHeader 基本正交，不能算“已经跟着重构一起收口”。
