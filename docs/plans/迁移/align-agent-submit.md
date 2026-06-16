# V2↔V3 1:1 对齐：`agent.submit` + `agent.submitPrompt`

## 审查方式

- 只用 LSP 取证：`text_search`、`workspace_symbol`、`read_file`
- V2 取证文件：
  - `go-agent-v2/internal/apiserver/methods_orchestration.go`
  - `go-agent-v2/internal/runner/manager_submission.go`
  - `go-agent-v2/legacy-agentsdk/agentcore/client.go`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go`
  - `go-agent-v2/legacy-agentsdk/codex/client_appserver_helpers.go`
- V3 取证文件：
  - `internal/sidecar/orch/orchestration/rpc.go`
  - `internal/sidecar/orch/orchestration/rpc_types.go`
  - `internal/sidecar/orch/orchestration/service.go`
  - `internal/sidecar/orch/orchestration/helpers.go`
  - `internal/sidecar/orch/orchestration/submission.go`
  - `internal/sidecar/orch/orchestration/runner_actor.go`
  - `internal/dto/turn/model.go`
  - `internal/module/turn/orchestration_starter.go`
  - `internal/module/turn/service.go`
  - `internal/module/turn/assembler.go`
  - `internal/provider/codexapp/session.go`
  - `internal/provider/codexapp/input_map.go`
  - `internal/provider/codexapp/skill_prompt.go`

## 结论摘要

| 对项 | 结论 | 核心判断 |
| --- | --- | --- |
| `agent.submit` / `agent.submitPrompt` 同名入口与 alias 语义 | ✅ | V2 两个方法都绑到 `agentSubmitTyped`；V3 两个方法都走 `submissionFromParams(...) -> svc.SubmitTurn(...)` |
| 基础参数 `agent_id/prompt/images/files` | ✅ | V3 保留这四个旧字段，并把 `prompt/images/files` 转成 typed input 后继续下游执行 |
| V3 `queue -> claim -> TurnStarter -> provider` 完整链路 | ⚠️ | 代码链已闭合；但 `TurnStarter` 第一跳就是 `sessions.GetSession(agentID)`，所以运行前提是 session 已经注册 |
| `SelectedSkills` 透传 | ✅ | RPC -> `TurnSubmission` -> `PrepareInput` -> `dto.TurnRequest` -> provider `turn/start.selectedSkills` 全链保留；但只保留 skill name |
| `ManualSkillSelection` 透传 | ✅ | RPC -> `TurnSubmission` -> `PrepareInput` -> `dto.TurnRequest` -> provider `turn/start.manualSkillSelection` 全链保留 |
| `OutputSchema` 透传 | ✅ | RPC -> `TurnSubmission` -> `PrepareInput` -> `dto.TurnRequest` -> provider `turn/start.outputSchema` 全链保留 |
| 与 V2 lower layer 的技能载荷丰富度 | ⚠️ | V2 `SubmitWithSkillsAndOverrides` 可下传 `Skill{Name, Path}`；V3 orchestration submit 只有 `[]string selectedSkills`，没有 path/prompt 级载荷 |
| 与 V2 RPC 面是否“字面同构” | ⚠️ | V3 是“兼容旧参 + 扩展新参”，不是只保留 V2 那四个字段的纯同构实现 |

## 1. V2 实际行为

### 1.1 RPC 入口

- `go-agent-v2/internal/apiserver/methods_orchestration.go:14-17` 注册：
  - `agent.submit -> agentSubmitTyped`
  - `agent.submitPrompt -> agentSubmitTyped`
- `go-agent-v2/internal/apiserver/methods_orchestration.go:73-78` 的 `agentSubmitParams` 只有四个字段：
  - `agent_id`
  - `prompt`
  - `images`
  - `files`
- `go-agent-v2/internal/apiserver/methods_orchestration.go:80-90` 只做一件事：`s.mgr.Submit(p.AgentID, p.Prompt, p.Images, p.Files)`

结论：

- V2 `agent.submit` 和 `agent.submitPrompt` 是同一个 handler，同一个参数面
- 这一层没有 `input`
- 这一层没有 `selectedSkills`
- 这一层没有 `manualSkillSelection`
- 这一层没有 `outputSchema`

### 1.2 V2 下游 submit 链

- `go-agent-v2/internal/runner/manager_submission.go:514-518`
  - `AgentManager.Submit(...)` 直接包装为 `client.Submit(prompt, images, files, nil)`
- `go-agent-v2/internal/runner/manager_submission.go:243-274`
  - `SubmitOrQueueWithMetadata(...)` 负责“直接 dispatch 或入本地 queue”
- `go-agent-v2/internal/runner/manager_submission.go:286-297`
  - 忙态时排队
- `go-agent-v2/internal/runner/manager_submission.go:140-205`
  - 空闲时 drain queue，直接调用 submission 的 `dispatch(client)`
- `go-agent-v2/legacy-agentsdk/agentcore/client.go:7-19`
  - client 合约是 `Submit(prompt, images, files []string, outputSchema json.RawMessage) error`

结论：

- V2 有 queue / drain
- 但没有 V3 这种显式 `claimTurnWork`
- 也没有独立 `TurnStarter`
- orchestration 侧最终是“队列后直接 dispatch 到 provider client”

### 1.3 V2 provider 侧真正收到什么

- `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:214-220`
  - `Submit(...) -> SubmitWithSkills(...) -> SubmitWithSkillsAndOverrides(...)`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:222-249`
  - 真正 RPC 是 `turn/start`
  - `buildTurnStartInputs(prompt, images, files, skills)` 负责把参数转成 provider `input`
  - `outputSchema` 只有在 `len(outputSchema) > 0` 时才写入 `turn/start`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_helpers.go:39-48`
  - `prompt/images/files/skills` 会被拼成 typed `input`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_helpers.go:106-123`
  - V2 lower layer 的 `skills` 是 `{name, path}` 级别，不只是名字

关键点：

- 虽然 V2 codex lower layer 支持 `skills + outputSchema + model + effort`
- 但 orchestration submit 这条链并没有调用那条 richer surface
- 因为 `AgentManager.Submit(...)` 固定传的是 `client.Submit(..., nil)`，也就是：
  - `outputSchema` 被置空
  - `selectedSkills/manualSkillSelection` 根本没有入口

## 2. V3 实际行为

### 2.1 RPC 参数与兼容层

- `internal/sidecar/orch/orchestration/rpc.go:20-39`
  - `agent.submit` 与 `agent.submitPrompt` 都走 `submissionFromParams(...) -> svc.SubmitTurn(...)`
- `internal/sidecar/orch/orchestration/rpc_types.go:70-83`
  - V3 仍保留 V2 风格字段：
    - `agent_id`
    - `prompt`
    - `images`
    - `files`
  - 同时新增：
    - `selected_skills`
    - `manual_skill_selection`
    - `output_schema`
- `internal/sidecar/orch/orchestration/rpc_types.go:85-115`
  - `UnmarshalJSON` 还兼容 camelCase / 新面：
    - `agentId`
    - `input`
    - `selectedSkills`
    - `manualSkillSelection`
    - `outputSchema`

结论：

- 对 V2 caller 而言，旧的 `agent_id/prompt/images/files` 还能用
- 对 V3 caller 而言，还能直接提交 `input + selectedSkills + manualSkillSelection + outputSchema`
- 所以这是“向后兼容 + 向前扩展”，不是“只保留 V2 四字段”的字面同构

### 2.2 V3 如何把旧参数折成统一 submission

- `internal/sidecar/orch/orchestration/rpc.go:90-103`
  - `submissionFromParams(...)` 构造 `TurnSubmission`
  - 把 `SelectedSkills / ManualSkillSelection / OutputSchema` 直接复制进 submission
- `internal/sidecar/orch/orchestration/rpc.go:106-124`
  - `prompt -> {type:"text"}`
  - `images -> {type:"image"}`
  - `files -> {type:"mention"}`
- `internal/dto/turn/model.go:11-19`
  - `TurnSubmission` 明确保存：
    - `AgentID`
    - `ThreadID`
    - `ExpectedTurnID`
    - `Inputs`
    - `SelectedSkills`
    - `ManualSkillSelection`
    - `OutputSchema`

结论：

- V3 不是把旧参数直接丢给 provider
- 而是先统一折成 typed `TurnSubmission`
- 这个统一形态就是后面队列与 `TurnStarter` 的输入契约

## 3. V3 完整链路：queue -> claim -> TurnStarter -> provider

### 3.1 queue

- `internal/sidecar/orch/orchestration/service.go:166-187`
  - `SubmitTurn(...)` 校验 agent 运行态后，把 submission 放进 `agent.queue.Enqueue(req)`
  - 如果 agent 当前 `idle`，还会把状态推进到 `turn_queued`
- `internal/sidecar/orch/orchestration/submission.go:9-29`
  - `SubmissionQueue` 是简单 FIFO：`Enqueue` append，`Dequeue` 取头

### 3.2 claim

- `internal/sidecar/orch/orchestration/runner_actor.go:62-65`
  - runner 每轮调用 `claimTurnWork(ctx)`，随后立刻 `startTurnExecution(ctx, work)`
- `internal/sidecar/orch/orchestration/service.go:291-324`
  - `claimTurnWork(...)` 会：
    - 只挑 `state == turn_queued` 的 agent
    - `Dequeue()` 取出 submission
    - 触发 `turn_queued -> turn_starting`
    - 生成 / 绑定 `ExpectedTurnID`
    - 把完整 `submission` 塞进 `turnWork.submission`

关键判断：

- V3 不是“出队后只剩 agentID/turnID”
- 出队后的 `turnWork` 仍然带着完整 `TurnSubmission`
- 所以后面的 `TurnStarter` 能看到全部输入与透传字段

### 3.3 TurnStarter

- `internal/sidecar/orch/orchestration/helpers.go:140-150`
  - `startTurnExecution(...)` 直接调用 `s.turnStarter.StartTurn(ctx, work.submission)`
- `internal/module/turn/orchestration_starter.go:22-52`
  - `StartTurn(...)` 顺序是：
    - `sessions.GetSession(agentID)`
    - `turns.PrepareTurn(...)`
    - `turns.StartTurn(...)`
- `internal/module/turn/orchestration_starter.go:54-63`
  - `prepareQueuedTurnInput(...)` 把 submission 映射成 `PrepareInput`
  - 这里直接填入：
    - `Inputs`
    - `Skills`
    - `ManualSkillSelection`
    - `OutputSchema`

### 3.4 provider

- `internal/module/turn/service.go:47-74`
  - `PrepareTurn(...)` 生成 `dto.TurnRequest`
  - `ManualSkillSelection` 与 `OutputSchema` 直接写入 request
  - `SelectedSkills` 经 `skillResolver.Resolve(...)` 后成为 `dto.SkillRef`
- `internal/module/turn/service.go:76-106`
  - `StartTurn(...)` 最终调用 `session.StartTurn(ctx, req)`
- `internal/provider/codexapp/session.go:110-130`
  - codex provider session 真正发 RPC `turn/start`
- `internal/provider/codexapp/session.go:334-356`
  - `buildTurnStartParams(...)` 把 `dto.TurnRequest` 落成 provider RPC 参数：
    - `input`
    - `selectedSkills`
    - `manualSkillSelection`
    - `outputSchema`
- `internal/provider/codexapp/input_map.go:10-52`
  - provider 侧把 typed input 再映射成 app-server 需要的 `text/image/localImage/mention`

结论：

- V3 的 `queue -> claim -> TurnStarter -> provider` 是闭合的真实执行链
- 这条链不是只改本地状态
- 它最终确实会进入 provider `turn/start`
- 但运行前提是 `sessions.GetSession(agentID)` 能命中；这一点比 V2 “manager 直接持有 client” 多了一层外部依赖

## 4. 参数逐项对比

| 参数 | V2 `agent.submit/submitPrompt` | V3 `agent.submit/submitPrompt` | 结论 |
| --- | --- | --- | --- |
| `agent_id` | 有 | 有 | ✅ |
| `prompt` | 有 | 有 | ✅ |
| `images` | 有 | 有 | ✅ |
| `files` | 有 | 有 | ✅ |
| `agentId` | 无 | 兼容 | ⚠️ V3 扩展 |
| `input` | 无 | 兼容 | ⚠️ V3 扩展 |
| `selectedSkills` / `selected_skills` | 无 | 有 | ⚠️ V3 扩展 |
| `manualSkillSelection` / `manual_skill_selection` | 无 | 有 | ⚠️ V3 扩展 |
| `outputSchema` / `output_schema` | V2 submit handler 无；manager path 也固定传 `nil` | 有，且真透传到 provider | ⚠️ V3 能力更强，不是字面同构 |

## 5. `SelectedSkills` / `ManualSkillSelection` / `OutputSchema` 透传核对

### 5.1 `SelectedSkills`

- `internal/sidecar/orch/orchestration/rpc.go:96-102`
  - RPC 入 `TurnSubmission.SelectedSkills`
- `internal/sidecar/orch/orchestration/service.go:316-320`
  - `claimTurnWork(...)` 把完整 submission 带进 `turnWork`
- `internal/module/turn/orchestration_starter.go:54-63,82-91`
  - 变成 `PrepareInput.Skills []dto.SkillRef{Name}`
- `internal/module/turn/service.go:63-73`
  - 进入 `dto.TurnRequest.Skills`
- `internal/provider/codexapp/session.go:335-355`
  - 变成 provider `selectedSkills`

结论：

- `SelectedSkills` 名字列表透传是完整的，结论 `✅`
- 但 V3 orchestration submit 只保留 `name`
- 如果目标是对齐 V2 lower layer `Skill{Name, Path}` 的 richer payload，这里只有 `⚠️`，不是完全同构

### 5.2 `ManualSkillSelection`

- `internal/sidecar/orch/orchestration/rpc.go:100-102`
  - RPC 入 `TurnSubmission.ManualSkillSelection`
- `internal/module/turn/orchestration_starter.go:57-59`
  - 入 `PrepareInput.ManualSkillSelection`
- `internal/module/turn/service.go:59-70`
  - 入 `dto.TurnRequest.ManualSkillSelection`
- `internal/provider/codexapp/session.go:348-355`
  - 入 provider `turn/start.manualSkillSelection`

结论：

- 这条链是完整的，结论 `✅`

### 5.3 `OutputSchema`

- `internal/sidecar/orch/orchestration/rpc.go:100-102`
  - RPC 入 `TurnSubmission.OutputSchema`
- `internal/module/turn/orchestration_starter.go:57-60`
  - 入 `PrepareInput.OutputSchema`
- `internal/module/turn/service.go:64-72`
  - 入 `dto.TurnRequest.OutputSchema`
- `internal/provider/codexapp/session.go:348-355`
  - 入 provider `turn/start.outputSchema`

结论：

- 这条链是完整的，结论 `✅`
- 这点甚至强于 V2 orchestration submit，因为 V2 manager submit path 固定把 `outputSchema` 置成 `nil`

## 6. 最终判断

- ✅ 如果目标是“让 V2 旧调用方继续用 `agent_id/prompt/images/files` 调 `agent.submit` / `agent.submitPrompt`”，V3 已经对齐；在 session 已注册前提下，这条链也能真实到达 provider
- ⚠️ 如果目标是“把 V3 submit 看成完全自洽的 orchestration 内链路”，还差一个运行前提：`SessionManager` 里必须已经有该 agent 的 session；否则会在 `TurnStarter.GetSession(...)` 处失败
- ✅ `SelectedSkills`、`ManualSkillSelection`、`OutputSchema` 在 V3 submit 链里都能透传到 provider
- ⚠️ 这不是字面意义上的 V2 RPC 同构；V3 是“兼容旧面 + 扩展新面”
- ⚠️ `SelectedSkills` 当前只有名字列表；若迁移目标要求对齐 V2 lower layer `Skill{Name, Path}` 的技能载荷，还差一层 richer skill ref 表达
