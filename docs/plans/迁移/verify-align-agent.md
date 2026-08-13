# 验证：orchestration agent 1:1 对齐修复（launch/submit/list）

审查时间：2026-03-21
审查方式：仅使用 LSP `read_file` / `text_search` / `references`

已读取：

- `docs/plans/迁移/align-agent-launch.md`
- `docs/plans/迁移/align-agent-submit.md`
- `docs/plans/迁移/align-agent-list.md`

## 结论总表

| 项目 | 结论 | 依据 |
| --- | --- | --- |
| `agent.launch` 参数面是否补齐 `prompt/instructions` | ✅ | `launchParams` 已声明 `prompt` / `instructions`，并映射到 `LaunchRequest`：`cmd/mcp-orch/orchestration/rpc_types.go:8-17`、`cmd/mcp-orch/orchestration/rpc.go:79-89`、`cmd/mcp-orch/orchestration/contract.go:41-50`。注意：当前启动链并不消费这两个字段，只消费 `name/parentID/cwd/command/env`：`cmd/mcp-orch/orchestration/helpers.go:34-50`、`cmd/mcp-orch/orchestration/service.go:229-233`。 |
| `agent.submit` 完整链路是否闭合；`SelectedSkills` / `ManualSkillSelection` 是否透传 | ⚠️ | handler -> queue -> claim -> `TurnStarter` -> provider 的代码链已闭合：`cmd/mcp-orch/orchestration/rpc.go:20-38,92-105`、`cmd/mcp-orch/orchestration/service.go:161-181,286-319`、`cmd/mcp-orch/orchestration/runner_actor.go:62-65`、`internal/module/turn/orchestration_starter.go:22-52`、`internal/module/turn/service.go:47-74`、`internal/provider/codexapp/session.go:377-399`。`SelectedSkills` / `ManualSkillSelection` 都能进 `turn/start`：`cmd/mcp-orch/orchestration/rpc.go:98-104`、`internal/module/turn/orchestration_starter.go:55-59,82-90`、`internal/module/turn/service.go:63-70`、`internal/provider/codexapp/session.go:52-60,377-399`。但 `TurnStarter` 第一跳依赖 `sessions.GetSession(agentID)`，session 未注册时链路会失败：`internal/module/turn/orchestration_starter.go:33-36`。 |
| `agent.list` json tag / 排序 / 字段完整 | ✅ | `AgentSnapshot` 使用 snake_case json tag：`cmd/mcp-orch/orchestration/contract.go:52-61`；`ListAgents()` 按 `ID -> Name -> Port` 做稳定排序：`cmd/mcp-orch/orchestration/service.go:184-201`；`snapshotLocked()` 返回 `id/name/parent_id/port/thread_id/cwd/state/provider/last_report` 全量字段：`cmd/mcp-orch/orchestration/service.go:215-227`。 |
| `AgentSnapshot` 所有字段是否正确填充 | ❌ | `id/name/parent_id/cwd/state/last_report` 有明确写入来源：`cmd/mcp-orch/orchestration/helpers.go:34-50`、`cmd/mcp-orch/orchestration/report.go:135-152`、`cmd/mcp-orch/orchestration/service.go:215-227`。但 `port/provider` 仅由 launch 请求推断，不是 runtime 实测值：`cmd/mcp-orch/orchestration/helpers.go:45-46,233-253`。`thread_id` 在 launch/stop 时会被清空，只在 claim queued turn 时从 submission 写回：`cmd/mcp-orch/orchestration/helpers.go:52-60`、`cmd/mcp-orch/orchestration/service.go:150-159,229-259,307-309`；而 submission 缺省又会回退成 `agentID`：`cmd/mcp-orch/orchestration/rpc.go:141-147`。provider 虽然会在真正发 turn 时改用 session thread id，但并不会回写 `AgentSnapshot.thread_id`：`internal/module/turn/orchestration_starter.go:70-79`。 |

## 1. `agent.launch`

结论：`✅`

直接证据：

- `cmd/mcp-orch/orchestration/rpc_types.go:8-17`
  `launchParams` 现在已有 `Prompt string` 和 `Instructions string`。
- `cmd/mcp-orch/orchestration/rpc.go:79-89`
  `launchRequestFromParams(...)` 会把 `p.Prompt`、`p.Instructions` 复制到 `LaunchRequest`。
- `cmd/mcp-orch/orchestration/contract.go:41-50`
  `LaunchRequest` contract 已包含 `Prompt`、`Instructions`。

限制：

- `cmd/mcp-orch/orchestration/helpers.go:34-50`
  `agentForLaunchLocked(...)` 只把 `Name`、`ParentID`、`Cwd`、`Command`、`Env` 写入 runtime。
- `cmd/mcp-orch/orchestration/service.go:229-233`
  `startProcessLocked(...)` 也只用 `command/cwd/env` 启动进程。

判断：

- 就 `agent.launch` 参数面是否“补齐 `prompt/instructions`”这个问题，答案是 `✅`。
- 但这还是“参数面补齐”；当前 orchestration 启动链没有消费这两个字段，所以不能把它理解成 launch 语义已经完整对齐。

## 2. `agent.submit`

结论：`⚠️`

直接证据：

- `cmd/mcp-orch/orchestration/rpc.go:20-38`
  `agent.submit` 和 `agent.submitPrompt` 都经 `submissionFromParams(...)` 后进入 `svc.SubmitTurn(...)`。
- `cmd/mcp-orch/orchestration/rpc.go:92-105`
  `TurnSubmission` 会直接保存 `SelectedSkills`、`ManualSkillSelection`、`OutputSchema`。
- `cmd/mcp-orch/orchestration/service.go:161-181`
  `SubmitTurn(...)` 把 submission 入本地 queue。
- `cmd/mcp-orch/orchestration/service.go:286-319`
  `claimTurnWork(...)` 出队时仍携带完整 `submission`，并推进到 `turnWork.submission`。
- `cmd/mcp-orch/orchestration/runner_actor.go:62-65`
  runner 会立刻把 `turnWork` 交给 `startTurnExecution(...)`。
- `cmd/mcp-orch/orchestration/helpers.go:140-150`
  `startTurnExecution(...)` 直接调用 `s.turnStarter.StartTurn(ctx, work.submission)`。
- `internal/module/turn/orchestration_starter.go:54-60`
  `prepareQueuedTurnInput(...)` 把 `Inputs`、`SelectedSkills`、`ManualSkillSelection`、`OutputSchema` 都带入 `PrepareInput`。
- `internal/module/turn/orchestration_starter.go:82-90`
  `SelectedSkills` 会被映射成 `[]dto.SkillRef{Name: ...}`。
- `internal/module/turn/service.go:63-70`
  `PrepareTurn(...)` 把 `Skills`、`ManualSkillSelection`、`OutputSchema` 写入 `dto.TurnRequest`。
- `internal/provider/codexapp/session.go:52-60`
  provider `turnStartParams` 明确包含 `selectedSkills` 和 `manualSkillSelection`。
- `internal/provider/codexapp/session.go:377-399`
  `buildTurnStartParams(...)` 最终把这些字段落到 provider `turn/start` RPC。

风险点：

- `internal/module/turn/orchestration_starter.go:33-36`
  `StartTurn(...)` 的第一步是 `sessions.GetSession(agentID)`。
- 这意味着 submit 链路“代码上闭合”，但运行前提是 session 已经先注册好。
- 如果只是直接调用 orchestration `agent.launch`，当前 launch 链不会创建 session；此时 submit 会在这里失败。

补充：

- `SelectedSkills` 的透传是 `✅`，但当前只保留 `name`，没有更丰富的 skill payload；证据见 `internal/module/turn/orchestration_starter.go:82-90`。
- `ManualSkillSelection` 的透传是 `✅`。

## 3. `agent.list`

结论：`✅`

直接证据：

- `cmd/mcp-orch/orchestration/contract.go:52-61`
  `AgentSnapshot` 的 json tag 全是 snake_case：
  `id` / `name` / `parent_id` / `port` / `thread_id` / `cwd` / `state` / `provider` / `last_report`。
- `cmd/mcp-orch/orchestration/service.go:184-201`
  `ListAgents()` 会收集所有 snapshot，并按 `ID -> Name -> Port` 做 `sort.SliceStable(...)`。
- `cmd/mcp-orch/orchestration/service.go:215-227`
  `snapshotLocked()` 返回的字段集合完整，没有漏掉 `AgentSnapshot` 中声明的字段。

判断：

- 只看 `agent.list` 的 wire shape、json tag、排序规则、字段集合，当前实现是 `✅`。
- 字段“值是否权威/正确”是下一节 `AgentSnapshot` 自身填充语义的问题，不影响这里的 shape 结论。

## 4. `AgentSnapshot`

结论：`❌`

明确正确的字段：

- `cmd/mcp-orch/orchestration/helpers.go:34-44`
  `id/name/parent_id/cwd` 都直接来自 launch request。
- `cmd/mcp-orch/orchestration/service.go:223`
  `state` 来自 runtime 当前状态机状态。
- `cmd/mcp-orch/orchestration/report.go:135-152`
  `last_report` 来自 `SetReport(...)` / `HandleReportEvent(...)` 写入的 `agent.lastReport`。

不成立的字段：

- `cmd/mcp-orch/orchestration/helpers.go:45-46,233-253`
  `port/provider` 不是 runtime/client 的实测值，而是从 `Env` / `command flags` 推断出来的。
- `cmd/mcp-orch/orchestration/helpers.go:52-60`
  launch 前会把 `threadID` 清空。
- `cmd/mcp-orch/orchestration/service.go:150-159`
  stop 时会再次把 `threadID` 清空。
- `cmd/mcp-orch/orchestration/service.go:307-309`
  `threadID` 只在 claim queued turn 时，按 `submission.ThreadID` 写回 runtime。
- `cmd/mcp-orch/orchestration/rpc.go:141-147`
  如果 snapshot 里当前没有 `threadID`，submit 默认回退成 `agentID`。
- `internal/module/turn/orchestration_starter.go:70-79`
  provider 真正发 turn 时会把这个占位 `agentID` 替换成 session 的真实 thread id，但这个真实值不会回写到 orchestration runtime。

判断：

- `AgentSnapshot` 的字段集合完整，但“所有字段正确填充”这个命题不成立。
- 目前至少有两个确定问题：
  - `thread_id` 可能为空，也可能只是 `agentID` 占位值，而不是真实 session thread id。
  - `port/provider` 只是 launch 参数推断值，不是运行态真值。

## 最终结论

- `agent.launch` 参数面补齐 `prompt/instructions`：`✅`
- `agent.submit` 链路闭合且 skills/selection 透传：`⚠️`
- `agent.list` 的 json tag / 排序 / 字段集合：`✅`
- `AgentSnapshot` 所有字段正确填充：`❌`
