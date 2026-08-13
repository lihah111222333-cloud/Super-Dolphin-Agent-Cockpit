# V2↔V3 1:1 对齐：thread stop/delete

审查时间：2026-03-21
审查方式：只用 LSP，实际使用了 `workspace_symbol`、`text_search`、`references(compact)`、`read_file`

## 结论摘要

- 总结论：`❌`
- `thread/stop`：`❌`
  - 在已读路由表里，V2/V3 都没有裸 `thread/stop`，只有 `thread/realtime/stop`：`go-agent-v2/internal/apiserver/methods_thread_turn.go:72-92`，`internal/module/thread/rpc.go:89-95`
  - V2 的 `thread/realtime/stop` 只是校验 `threadId` 后返回空结果：`go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:140-141`，`go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:161-163,359-368`
  - V3 虽然挂了同名路由，但运行时会落到 `SendCommand("realtime/stop") -> /realtime/stop -> unsupported command`：`internal/module/thread/rpc.go:89-95`，`internal/module/thread/command.go:22-45`
- `thread/delete`：`❌`
  - V2 是“停进程/会话 + 删 archive 目录 + 删 binding + 清 prefs”的 best-effort 删除：`go-agent-v2/internal/apiserver/methods.go:204-227`
  - V3 是“best-effort `session.Close` + 删 binding + 删 thread row”的最小删除：`internal/module/thread/service.go:102-119`
  - V3 没有接上 `orchestration.StopAgent` / `SessionManager.Remove`，所以事件、资源回收、僵尸防护都不等价：`cmd/mcp-orch/orchestration/service.go:127-140,155-164`，`internal/provider/unified/session.go:60-85`

## 0. 方法名对齐说明

本次按代码真实路由做 1:1 对齐：

- V2 “stop” 对应的是 `thread/realtime/stop`，不是裸 `thread/stop`
- V3 也只有 `thread/realtime/stop`
- `turn/interrupt` 在 V2/V3 都存在，但它是另一条 turn 级语义，不算本次 `thread stop/delete` 的 1:1 对齐对象

## 1. `thread/realtime/stop`

### 调用链

- V2：`methods_thread_turn.go` -> `inlineProvider.ThreadRealtimeStop` -> `codexadapter.Adapter.ThreadRealtimeStop` -> `RunThreadRealtimeStop`
- V3：`rpc.go` -> `newCapabilityThreadCommandHandler` -> `svc.SendCommand(ctx, threadID, "realtime/stop", ...)`

### 逐项对比

| 维度 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| session 关闭 | 没有；只校验 `threadId` | 没有；而且运行时直接 `unsupported command` | ❌ |
| store 清理 | 没有 | 没有 | ❌ |
| turn 级联清理 | 没有 | 没有 | ❌ |
| history 清理 | 没有 | 没有 | ❌ |
| 事件发布 | 没有 | 没有稳定事件；路由本身跑不通 | ❌ |
| 资源回收 | 没有 | 没有 | ❌ |
| 僵尸防护 | 没有 | 没有 | ❌ |

### 判断

`thread/realtime/stop` 在 V2 本身就是空壳能力；V3 连这个空壳也没有跑通，所以这里不是“等价缺省”，而是明确 `❌`。

## 2. `thread/delete`

### 调用链

- V2：`go-agent-v2/internal/apiserver/methods.go:204-227`
  - `stopInlineManager(threadID)`：`go-agent-v2/internal/apiserver/server.go:134-142`
  - `runner.AgentManager.Stop`：`go-agent-v2/internal/runner/manager_lifecycle.go:19-91`
  - 删除 archive 目录：`go-agent-v2/internal/apiserver/methods.go:211-217`
  - `bindingStore.Unbind`：`go-agent-v2/internal/store/agent_thread_binding.go:258-279`
  - 清理 archived prefs：`go-agent-v2/internal/apiserver/methods_ui_state.go:221-244`
- V3：`internal/module/thread/rpc.go:40,105-108` -> `internal/module/thread/service.go:102-119`
  - `closeSessionIfActive`：`internal/module/thread/service.go:228-240`
  - `bindingStore.DeleteByAgentID`：`internal/store/binding/store.go:60-62`
  - `forgetThreadAgent`
  - `threadStore.DeleteByThreadID`：`internal/store/thread/store.go:100-104`

### 逐项对比

| 维度 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| session 关闭 | `stopInlineManager -> AgentManager.Stop -> Client.Shutdown/Kill`，是真停 runtime：`go-agent-v2/internal/runner/manager_lifecycle.go:19-57` | `closeSessionIfActive -> session.Close(ctx)`，但 binding/session 查找失败直接吞掉，`Delete` 也忽略 close 错误：`internal/module/thread/service.go:107-108,228-240` | ⚠️ |
| store 清理 | 只清 binding store；`thread/delete` 主链不触达 `AgentThreadStore.Delete`，但 `Unbind` 会事务性删 `agent_provider_binding` + `agent_codex_binding`：`go-agent-v2/internal/store/agent_thread_binding.go:258-279` | 删 binding + `agent_threads` 行，但没有事务，binding 删了而 thread row 删失败会留下半状态：`internal/module/thread/service.go:109-118`，`internal/store/binding/store.go:60-62`，`internal/store/thread/store.go:100-104` | ⚠️ |
| turn 级联清理 | `AgentManager.Stop` 会清 `activeSubmission`，并在有活跃提交时发 synthetic `turn_aborted`：`go-agent-v2/internal/runner/manager_lifecycle.go:68-85` | provider session 关闭会结束 in-flight handle：`internal/provider/codexapp/session.go:211-220`，`internal/provider/claudecli/session.go:240-283`；但 thread 模块没有显式 turn tracker/store 级联清理 | ⚠️ |
| history 清理 | 会删 archive 目录，并把 archived prefs 里的 thread 移除：`go-agent-v2/internal/apiserver/methods.go:211-217`，`go-agent-v2/internal/apiserver/methods_ui_state.go:221-244` | 没有 archive/history/rollout 清理；`agent_interactions`、`system_logs` 都是按 `thread_id` 存储，但只有 insert/list，没有 delete：`migrations/0001_initial_schema.sql:95-114`，`sql/queries/interaction.sql:1-31`，`migrations/0009_system_logs_v2.sql:7-22`，`sql/queries/system_log.sql:1-24` | ❌ |
| 事件发布 | 没有显式 `thread_deleted`；但 active turn 会通过 `runner.Stop` 间接发 `turn_aborted`：`go-agent-v2/internal/runner/manager_lifecycle.go:77-84` | `thread.Delete` 自己没有任何 publish；它也不调用 `orchestration.StopAgent`，因此不会走 `publishAgentStopped`：`internal/module/thread/service.go:102-119`，`cmd/mcp-orch/orchestration/service.go:127-140`，`cmd/mcp-orch/orchestration/events.go:35-43` | ❌ |
| 资源回收 | 会停 client，失败时 fallback `Kill`，并删 archive 目录：`go-agent-v2/internal/runner/manager_lifecycle.go:39-57`，`go-agent-v2/internal/apiserver/methods.go:211-217` | provider session 的 transport 大多会被关闭，但 thread 删除不走 `StopAgent`，也不走 `SessionManager.Remove`，资源回收链不完整：`internal/provider/codexapp/transport.go:121-135`，`internal/provider/claudecli/session.go:248-283`，`internal/provider/unified/session.go:60-85` | ⚠️ |
| 僵尸防护 | `Stop` 有 `Shutdown -> Kill` 兜底，并清 active submission；但 delete 路由吞掉 stop 错误，也不清 thread store/diff 状态，所以只是部分防护：`go-agent-v2/internal/runner/manager_lifecycle.go:19-91`，`go-agent-v2/internal/apiserver/methods_inline_residual_guard_test.go:51-65` | 没有 `StopAgent`、没有 `stopRequested`、没有 queue clear、没有 `SessionManager.Remove`；thread row 删掉后 runtime/session 仍可能残留 | ❌ |

### 额外差异

- V2 `thread/delete` 成功返回 `{ok, threadId}`：`go-agent-v2/internal/apiserver/methods.go:225-226`
- V3 `thread/delete` 通过 `newThreadEffect` 返回 `null`：`internal/module/thread/rpc.go:105-108`
- V2 的 delete 是典型 best-effort；`stop`/`unbind`/prefs 失败都吞掉并继续返回 ack：`go-agent-v2/internal/apiserver/methods.go:210-225`，`go-agent-v2/internal/apiserver/methods_inline_residual_guard_test.go:51-65`
- V3 虽然比 V2 多删了 `agent_threads` 行，但没有把删除变成事务，也没有补上 turn/history/event/orchestration 侧的闭环

## 3. 最终判断

### `thread/realtime/stop`

- 1:1 对齐结论：`❌`
- 原因：V2 是空壳 stop；V3 是未落地 stop。二者都不做清理，但 V3 连调用语义都没跑通。

### `thread/delete`

- 1:1 对齐结论：`❌`
- 原因：V3 只做了“关 session + 删 binding + 删 thread row”的最小删除，没有对齐 V2 的 runtime stop、archive/prefs 清理、事件发布与僵尸防护。

## 4. 最小补齐项

如果要把这两条路径补到可接受的迁移水平，最少需要：

1. 明确公共 stop 语义。
   - 要么补真 `thread/stop`
   - 要么把 `thread/realtime/stop` 从空壳/unsupported 收敛成统一的 thread stop
2. `thread/delete` 先显式 `orchestration.StopAgent`，再 `SessionManager.Remove`，不要只做 best-effort `session.Close`
3. 把 binding/thread/history/interaction/system_log 清理收敛到一个事务化删除编排
4. 补稳定事件。
   - 至少有 `thread deleted/stopped`
   - 不要只依赖 provider-specific 或 synthetic turn 事件
