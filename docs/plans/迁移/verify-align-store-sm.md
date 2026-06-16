# 验证：store 层 + 状态机对齐修复

基线文档：
- `docs/plans/迁移/align-store-layer.md`
- `docs/plans/迁移/align-statemachine.md`

验证时间：`2026-03-21`

## 结论概览

| 项目 | 结论 | 结论摘要 |
| --- | --- | --- |
| WrapStoreError 全量覆盖 19/19 | ✅ | 当前 19 个 repo 都包了 `WrapStoreError`。 |
| 事件时间语义统一 | ⚠️ | provider translator / approval helper 已优先取 payload 时间，但内部事件生产者仍混用 `time.Now()` 与 runtime 时间。 |
| sqlc 生成层漂移是否修复 | ❌ | `threadbinding` 仍缺席，`ailog` 仍挂在 `system_log`，`dbquery` 仍是 placeholder，`prompt/commandcard` 等协议收缩仍在。 |
| 状态机严格模式（无 force fallback） | ❌ | `fireOrForceLocked` 已严格，但仍有状态机外直接写状态路径。 |
| `awaiting_user_input` 链路 | ❌ | provider -> approval 桥接存在，但 orchestration 没有消费 approval 事件，也没有触发 `user_input_requested/resolved`。 |
| recover 路径 | ⚠️ | manual / stall / thread recover 都在，但仍是“停旧进程后重启”，没有 replay 语义。 |

## 1. WrapStoreError 全量覆盖 19/19：✅

- `internal/store/module.go:28-49` 当前注册了 19 个 store repo。
- `internal/platform/db/errors.go:47-61` 定义了统一的 `WrapStoreError`。
- LSP `text_search` 对 `internal/store` 搜索 `WrapStoreError(` 命中 19 个 `store.go` 文件：
  - `internal/store/agentstatus/store.go:71`
  - `internal/store/ailog/store.go:54`
  - `internal/store/auditlog/store.go:65`
  - `internal/store/binding/store.go:105`
  - `internal/store/buslog/store.go:50`
  - `internal/store/commandcard/store.go:151`
  - `internal/store/cwdlock/store.go:84`
  - `internal/store/dbquery/store.go:19`
  - `internal/store/interaction/store.go:84`
  - `internal/store/prompt/store.go:102`
  - `internal/store/sharedfile/store.go:74`
  - `internal/store/systemlog/store.go:71`
  - `internal/store/taskack/store.go:80`
  - `internal/store/taskdag/store.go:291`
  - `internal/store/tasktrace/store.go:74`
  - `internal/store/thread/store.go:146`
  - `internal/store/topologyapproval/store.go:78`
  - `internal/store/uipreference/store.go:56`
  - `internal/store/workspace/store.go:153`
- 判定：这一项已经对齐，没有遗漏 repo。

## 2. 事件时间语义统一：⚠️

- 已统一的消费侧：
  - `internal/sidecar/orch/orchestration/event_time.go:10-32` 定义 `withEventTime/resolveEventTime`。
  - `internal/sidecar/orch/orchestration/module.go:33-41` 与 `internal/sidecar/orch/orchestration/turn_lifecycle.go:20-25` 会把 turn 事件时间写入上下文。
  - `internal/platform/rpc/approval_events.go:123-173` approval 事件优先取 payload 时间。
  - `internal/provider/codexapp/event_map.go:304-313` 与 `internal/provider/claudecli/event_map.go:136-144` translator 优先解析事件自带时间。
- 未统一的内部生产侧：
  - `internal/module/thread/service.go:320-357` 三类 thread 事件直接 `Timestamp: time.Now()`。
  - `internal/module/workspace/service_helpers.go:337-344` workspace 事件头直接 `time.Now()`。
  - `internal/sidecar/orch/orchestration/helpers.go:52-60` launch 准备阶段直接 `agent.updatedAt = time.Now()`。
  - `internal/sidecar/orch/orchestration/service.go:240-249`、`internal/sidecar/orch/orchestration/service.go:358-361` 进程启动/退出直接写 `now := time.Now()`。
- 判定：当前只是消费侧和部分 orchestration 内部使用了 event-time helper，还不是生产-消费全链路统一语义。

## 3. sqlc 生成层漂移是否修复：❌

- `internal/store/sqlc/querier.go:13` 仍然暴露 `ListAILogSystemLogs`；AI log 还不是独立生成面。
- `internal/store/ailog/store.go:18-30` 仍直接调用 `ListAILogSystemLogs`。
- `internal/store/sqlc/query_system_log.go:8`、`internal/store/sqlc/query_system_log.go:25-27` 说明 AI log 仍从 `system_logs` 直接查询。
- `internal/store/sqlc/querier.go:109` 仍然只有 `PlaceholderDBQuery`。
- `internal/store/dbquery/contract.go:5-11`、`internal/store/dbquery/store.go:16-25`、`internal/store/sqlc/query_db_query.go:6-17` 仍是 placeholder 实现。
- `internal/store/module.go:28-49` 仍只有 19 个 repo；`internal/store/binding/contract.go:7-14` 和 `internal/store/thread/contract.go:7-21` 之间仍没有独立 `threadbinding` repo/contract。
- `internal/store/sqlc/querier.go:22-41` 只有 binding/thread 相关方法，没有任何 `ThreadBinding` 生成接口。
- `internal/store/sqlc/query_prompt.go:6-32`、`internal/store/sqlc/query_command_card.go:6-54` 仍然是当前收缩后的 query 面，没有把 V2 的协议面完全补回。
- 判定：生成层漂移没有修完，核心红项仍在。

## 4. 状态机严格模式（无 force fallback）：❌

- 正向变化：`internal/sidecar/orch/orchestration/service.go:261-274` 的 `fireOrForceLocked()` 现在只做严格 `FireCtx`，非法转换直接报错。
- 但仍存在状态机外直接写状态：
  - `internal/sidecar/orch/orchestration/helpers.go:52-60` 的 `prepareLaunchStateLocked()` 直接把状态写成 `provisioning`。
  - `internal/sidecar/orch/orchestration/turn_lifecycle.go:14`、`internal/sidecar/orch/orchestration/turn_lifecycle.go:29-59` 的 `forceIdleAfterCompletionError()` 直接把状态写回 `idle`。
- `internal/dto/agent/state.go:21-32` 的 trigger 定义里没有 `turn_completion_recovered`，但 `internal/sidecar/orch/orchestration/turn_lifecycle.go:14`、`internal/sidecar/orch/orchestration/turn_lifecycle.go:58` 仍发布这个未声明 trigger。
- 判定：strict mode 还没成立，只是 fallback 从 `fireOrForceLocked()` 函数里移走了。

## 5. `awaiting_user_input` 链路：❌

- `internal/dto/agent/state.go:14`、`internal/dto/agent/state.go:28-29`、`internal/dto/agent/state.go:96-104` 已声明 `awaiting_user_input` 和 `user_input_requested/user_input_resolved`。
- `internal/provider/codexapp/session_approval.go:38-43` 已把 `request_user_input` 桥接到 approval manager。
- `internal/platform/rpc/approval_support.go:26-31` 会把 approval 默认状态归一到 `awaiting_user_input`。
- `internal/platform/rpc/approval_events.go:23-43` 会发布 `ToolApprovalRequested/Resolved`。
- 但 `internal/sidecar/orch/orchestration/module.go:33-41` 只订阅 `TurnStarted` 和 `TurnCompleted`，没有消费 approval 事件。
- LSP 对 `internal/sidecar/orch/orchestration` 搜索 `TriggerUserInputRequested` / `TriggerUserInputResolved` 无命中；当前没有找到任何 fire 点。
- 判定：链路仍停在 approval 层，没有反向驱动 agent 状态机进入/退出 `awaiting_user_input`。

## 6. recover 路径：⚠️

- `internal/sidecar/orch/orchestration/recover.go:27-58` 有显式 recover；流程是 `publishAgentRecovering("manual") -> stopProcess -> 清空 activeTurnID -> fire recover_requested -> startProcessLocked`。
- `internal/sidecar/orch/orchestration/recover.go:16-25` 与 `internal/sidecar/orch/orchestration/runner_actor.go:68-77` 仍保留 stall detector 自动 recover。
- `internal/module/thread/lifecycle.go:151-187` 暴露 thread recover；`internal/module/thread/lifecycle.go:271-280` 会先调 orchestration recover，失败再 fallback 到 launch。
- 但 `internal/sidecar/orch/orchestration/recover.go:47-53` 明确会清空 `activeTurnID` 并直接重启；LSP 对 `internal/sidecar/orch/orchestration` 搜索 `replay` 无命中。
- 判定：recover 路径“存在”，但仍不是完整 replay/recover 语义，只能给 `⚠️`。

## 最终结论

- 当前结果：`1 个 ✅ / 2 个 ⚠️ / 3 个 ❌`
- 仍未对齐的主问题：
  1. 事件时间还不是生产-消费单语义。
  2. sqlc 漂移的几个核心缺口还在。
  3. 状态机仍非 strict。
  4. `awaiting_user_input` 还没形成闭环。
  5. recover 仍是窄语义重启，不是完整恢复。
