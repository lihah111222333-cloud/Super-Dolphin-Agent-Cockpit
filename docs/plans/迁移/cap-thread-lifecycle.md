# Thread 生命周期层能力+容错审查

## 总体结论

**结论：不通过。**

V3 的 `thread` 生命周期链路已经有基本骨架，但离“能力闭环 + 故障可恢复 + V2 等价”还有明显距离。主要阻塞点有 6 个：

1. `thread/stop` 公共能力缺失；当前只有私有 `stopAgent` 回滚路径，没有标准 thread 级 stop 入口。
2. `thread/start` / `thread/resume` / `thread/delete` / `thread/archive` 都是多步操作，但没有事务；一旦中途失败会留下半创建或半删除状态。
3. store 状态、运行时 orchestration 状态、provider session 状态三套状态没有真正对齐；活跃 thread 也会被持久化成 `created`。
4. 并发保护不足；没有按 `threadID` 串行化，`SessionManager` 还是按 `agentID` 单槽替换，会相互踢掉 session。
5. V2 的大量 thread 能力在 V3 只是路由壳，或者参数/返回结构已经不等价。
6. 事件只做到部分内部发布，没有把 thread 生命周期变化稳定地发布成对外可消费的 thread 事件。

## 审查方法

本次只用 LSP 做验证，实际用到的手段包括：

- `text_search`
- `workspace_symbol`
- `document_symbol`
- `references(compact)`
- `call_hierarchy`
- `read_file`

---

## 1. `thread/start`

**结论：部分闭环，不算完全闭环。**

**证据**

- RPC 入口存在，`thread/start` 从 handler 进入 service：`internal/module/thread/rpc.go:20-31`
- service 主链完整：规范化请求、拉起 agent、创建 provider session、读取 session threadID、落库：`internal/module/thread/lifecycle.go:44-76`
- provider session 创建链存在：`internal/module/thread/lifecycle.go:204-220` -> `internal/provider/unified/client.go:29-35,47-67`
- `codexapp` 驱动会做 `initialize` + `thread/start`：`internal/provider/codexapp/driver.go:78-94,114-144`
- `claudecli` 驱动也有 `StartSession`：`internal/provider/claudecli/driver.go:53-73,86-131`
- 持久化链存在：`internal/module/thread/lifecycle.go:238-271` -> `internal/store/thread/store.go:77-90`

**问题**

- 持久化时无论 thread 是否已启动成功，store 里都写成 `statusCreated`，不是运行态：`internal/module/thread/lifecycle.go:246-253`
- `persistThreadState` 是先写 thread store，再写 binding store，没有事务，失败会留下半状态：`internal/module/thread/lifecycle.go:244-270`
- `Start` 本身不发布 thread 生命周期事件；后续是否有事件只能依赖 provider/orchestration 侧的间接事件：`internal/module/thread/lifecycle.go:44-76`

**判断**

链路“存在”，但不是强闭环。它更像“能跑通 happy path”，还没有做到状态、事件、回滚三件事同时闭合。

---

## 2. `thread/stop`

**结论：不通过，公共能力缺失。**

**证据**

- `thread` handler map 中没有 `thread/stop`：`internal/module/thread/rpc.go:20-83`
- 当前只有私有 `stopAgent`，且只被 `Start` / `Resume` 的失败回滚使用：`internal/module/thread/lifecycle.go:54-55,59-60,72-73,89-90,94-95,309-313`
- orchestration 有 `StopAgent`，但不暴露成 thread 模块的公共 RPC：`internal/sidecar/orch/orchestration/service.go:127-141`

**判断**

V3 现在没有标准的 thread 停止语义。现有“停止”能力分散在：

- `thread/start` / `thread/resume` 的失败回滚
- orchestration 的 agent 级 stop
- `thread/delete` / `thread/archive` 的 best-effort `session.Close()`

这三者不是一个统一的 thread/stop 生命周期。

---

## 3. `thread/delete`

**结论：不通过。**

**证据**

- 删除主链只有 4 步：规范化 ID、best-effort 关 session、删 binding、删 thread 行：`internal/module/thread/service.go:102-119`
- session 清理是 best-effort，binding/session 查找失败直接吞掉：`internal/module/thread/service.go:228-240`
- `Delete` 对 `closeSessionIfActive` 的返回值也直接忽略：`internal/module/thread/service.go:107-108`
- binding 删除只有 `DeleteByAgentID`：`internal/store/binding/store.go:60-62`
- thread 删除只有 `DeleteByThreadID`：`internal/store/thread/store.go:100-104`
- `Delete` 的调用图只碰 binding/session/thread store，没有任何 turn 清理分支：`internal/module/thread/service.go:102-119`
- 仓库内唯一 `DeleteByThreadID` 只存在于 thread store；没有 turn store 的按 thread 级联删除：`internal/store/thread/contract.go:16`, `internal/store/thread/store.go:100-104`

**问题**

- 没有 turn/history 级联清理。
- 没有事务；如果 binding 删了、thread row 删失败，会留下“thread 不存在但 binding 已丢”的不一致状态。
- 没有显式 `orchestration.StopAgent`；删除效果依赖 `session.Close()` 的副作用，而不是明确的生命周期操作。

**对比 V2**

- V2 的 `thread/delete` 还会停 inline manager、删 archive 目录、解绑、清理 archived prefs，并返回 `{ok, threadId}`：`go-agent-v2/internal/apiserver/methods.go:204-227`
- V3 只做 store/session 层面最小删除，且成功返回是 `null`：`internal/module/thread/rpc.go:39,92-95`, `internal/module/thread/service.go:102-119`

---

## 4. `thread/resume`

**结论：部分闭环。session 恢复有，history 加载没有。**

**证据**

- RPC 恢复入口存在：`internal/module/thread/rpc.go:118-130`
- `Resume` 逻辑会拉起 agent、恢复 provider session、再持久化 thread 状态：`internal/module/thread/lifecycle.go:77-107`
- 真正的 session 恢复发生在：`internal/module/thread/lifecycle.go:221-229`
- history 加载是另一条独立 API，只在 `ReadMessages` 里发生：`internal/module/thread/history.go:22-45`
- `Resume` 依赖 `svc.Get()` 返回的 `AgentID`，而 `threadStore.Upsert` 本身并不存 `AgentID`；读取时是靠 binding 反查出来的：`internal/module/thread/rpc.go:121-129`, `internal/module/thread/service.go:66-72`, `internal/store/thread/contract.go:24-35`, `internal/store/sqlc/query_agent_thread.go:6-12,64-66`
- 一旦 binding 缺失，`ResumeRequest` 会因为 `AgentID == ""` 直接失败：`internal/module/thread/lifecycle.go:189-200`

**判断**

- `session 恢复`：有。
- `history 加载`：没有，必须后续再走 `thread/messages`。
- `状态恢复鲁棒性`：弱；resume 对 binding 的依赖比 thread row 还强。

---

## 5. thread 状态一致性

**结论：不通过。**

**证据**

- thread store 持久化时总是写 `created`：`internal/module/thread/lifecycle.go:246-253`
- `Archive/Unarchive` 只在 `archived` 和 `created` 间切换：`internal/module/thread/archive.go:5-20`
- store 明明定义了 `ListRunning` / `ListRecoverable` / `ResetRunning` / `ExpireStale` / `RunningExists`，但引用搜索没有实际调用者：`internal/store/thread/contract.go:10-19`
- orchestration 自己维护独立运行时状态 `agentRuntime.state/threadID`：`internal/sidecar/orch/orchestration/service.go:41-65,220-231`
- thread row 自身不写 `AgentID`；`AgentID` 读取靠 binding 子查询推导：`internal/store/thread/contract.go:24-35`, `internal/store/sqlc/query_agent_thread.go:6-12,22-25`
- binding 持久化时把 `ProviderThreadID` 和 `CodexThreadID` 都写成同一个 `state.ThreadID`：`internal/module/thread/lifecycle.go:262-267`

**问题**

- store 状态不是运行态真相。
- runtime 状态在 orchestration 内存里，thread store 不回写。
- provider session 的真实状态也不回写到 thread store。
- 只要 binding 丢了，thread row 里的 `AgentID` 也就“消失”了。

**判断**

目前是三套状态并存：

- thread store 状态
- orchestration 运行时状态
- provider session 状态

三者没有统一 source of truth，也没有同步协议。

---

## 6. 并发安全

**结论：不通过。**

**证据**

- thread 模块唯一的锁只保护 `threadAgents` 映射：`internal/module/thread/service.go:26-35`, `internal/module/thread/lifecycle.go:372-399`
- `Start` / `Resume` / `Delete` / `Archive` 本身没有按 `threadID` 的串行化：`internal/module/thread/lifecycle.go:44-107`, `internal/module/thread/service.go:102-119`, `internal/module/thread/archive.go:5-20`
- `RunningExists` 虽然存在，但完全没有调用者：`internal/store/thread/contract.go:19`
- `SessionManager` 是按 `agentID` 单槽管理 session；新的 session 注册会 `ForceStop` 旧 session：`internal/provider/unified/session.go:30-46`
- orchestration 的 `LaunchAgent` 也是按 `agentID` 加锁和判重，不是按 `threadID`：`internal/sidecar/orch/orchestration/service.go:110-125`
- fork 场景会复用同一个 `agentID`，但新 fork thread 默认不写新 binding：`internal/module/thread/lifecycle.go:122-131`
- fork thread 的 agent 关联只记在内存 map 里：`internal/module/thread/lifecycle.go:244,372-380`
- `resolveBinding` 会通过这张 map 回退到共享 `agentID`：`internal/module/thread/service.go:196-201`
- `closeSessionIfActive` 于是可能关闭共享 session：`internal/module/thread/service.go:232-240`

**判断**

两个典型竞态：

1. 同一个 `agentID` 上并发 `Start/Resume`，后注册 session 会把前一个 `ForceStop`。
2. fork 子 thread 做 `archive/delete`，可能通过共享 `agentID` 关掉父 thread 还在用的 session。

---

## 7. 超时处理

**结论：部分具备，但不一致。**

**证据**

- thread service 自己不设 deadline，只把 `nil` context 变成 `Background()`：`internal/module/thread/lifecycle.go:341-345`
- `internal/module/thread` 和 `internal/sidecar/orch/orchestration` 中都没有 `WithTimeout` 使用
- `codexapp` driver 有明确超时：
  - `initialize`: `10s`：`internal/provider/codexapp/driver.go:114-122`
  - `thread/start`: `30s`：`internal/provider/codexapp/driver.go:124-144`
  - `thread/resume`: `30s`：`internal/provider/codexapp/driver.go:146-157`
- `claudecli` driver 的 `StartSession/ResumeSession` 没有对应 deadline 包装：`internal/provider/claudecli/driver.go:53-84,86-131`
- `codexapp` 在超时/初始化失败时会 `ForceStop()`：`internal/provider/codexapp/driver.go:83-90,101-108`

**判断**

- 有超时，但只是 provider-specific。
- 没有 thread 生命周期层面的统一 deadline 策略。
- `codexapp` 和 `claudecli` 的行为并不一致。

---

## 8. 错误恢复

**结论：不通过。**

**证据**

- `Start` 在 `startSession` / `lookupSession` / `persistThreadState` 失败时会调用 `stopAgent` 回滚：`internal/module/thread/lifecycle.go:50-55,57-60,63-73`
- `Resume` 失败时也只做 `stopAgent`：`internal/module/thread/lifecycle.go:85-106`
- `codexapp` driver 在初始化失败或远端 `thread/start/resume` 失败时会 `ForceStop()`：`internal/provider/codexapp/driver.go:83-90,101-108`
- `persistThreadState` 先 `rememberThreadAgent`，再写 thread store，最后写 binding store：`internal/module/thread/lifecycle.go:244-270`
- 一旦 `bindingStore.Upsert` 失败，前面的内存映射和 thread row 都不会回滚：`internal/module/thread/lifecycle.go:244-270`
- thread row 又不真正存 `AgentID`，读取时必须依赖 binding 反查：`internal/store/thread/contract.go:24-35`, `internal/store/sqlc/query_agent_thread.go:6-12`

**典型半创建状态**

- `threadStore.Upsert` 成功，`bindingStore.Upsert` 失败：
  - thread row 已存在
  - `AgentID` 读出来会是空
  - `thread/resume` 之后会失败
- `rememberThreadAgent` 成功，DB 写失败：
  - 内存 map 留下脏映射
  - 没有对应回滚

**判断**

当前错误恢复更像“尽量停掉现场”，不是“把状态恢复到操作前”。

---

## 9. V2 等价性

**结论：不通过。**

先说结论：V3 目前只有 `thread/model/set`、`thread/personality/set`、`thread/approvals/set` 这 3 条命令链最接近闭环，其余 thread 能力大量存在“参数不等价 / 返回不等价 / 只有壳没有实现 / 语义回退”。

**逐项差距**

- `thread/start`
  - V2 参数有 `modelProvider`、`baseInstructions`、`developerInstructions`、`sandbox`、`summary`，返回 `{thread:{id,status}, model, modelProvider, cwd, approvalPolicy}`：`go-agent-v2/internal/apiserver/methods_thread.go:16-27,44-76`
  - V3 参数只剩 `provider/cwd/model/prompt/instructions/approvalPolicy/effort/personality`，返回 `StartResult{threadID, agentID}`：`internal/module/thread/rpc_types.go:7-16`, `internal/module/thread/contract.go:28-43`
  - V3 service 实际下传时还丢掉了 `modelProvider/developerInstructions/summary/sandbox`；但 `codexapp` driver 明明支持这些字段：`internal/module/thread/lifecycle.go:208-218`, `internal/provider/codexapp/driver.go:124-136`

- `thread/resume`
  - V2 参数有 `path/cwd/model`，返回 `{thread:{id,status}, model}`：`go-agent-v2/internal/apiserver/methods_thread.go:237-250`
  - V3 只有 `threadId/provider`，handler 成功返回 `null`：`internal/module/thread/rpc_types.go:18-21`, `internal/module/thread/rpc.go:118-130`
  - `ResumeSessionRequest` 本身还保留了 `Model` 字段，但 V3 service 没往下传：`internal/dto/provider/session.go:14-19`, `internal/module/thread/lifecycle.go:221-229`, `internal/provider/codexapp/driver.go:149-152`

- `thread/fork`
  - V2 支持 `turnIndex`，返回 `{thread:{id,forkedFrom}}`：`go-agent-v2/internal/apiserver/methods_thread.go:224-235`
  - V3 没有 `turnIndex` 参数，返回 `{newThreadID}`：`internal/module/thread/rpc.go:33-35`, `internal/module/thread/contract.go:51-53`

- `thread/recover`
  - V2 返回 `{thread:{id,status}, recovered, mode}`：`go-agent-v2/internal/apiserver/methods_thread.go:257-267`
  - V3 成功返回 `null`，丢失 `recovered/mode`：`internal/module/thread/rpc.go:36,92-95`, `internal/module/thread/lifecycle.go:137-169`

- `thread/archive` / `thread/unarchive`
  - V2 调 provider 的 archive/unarchive，并在 unarchive 时尝试恢复进程：`go-agent-v2/internal/apiserver/methods_thread_turn.go:13-26`, `go-agent-v2/internal/apiserver/methods.go:183-202`
  - V3 只改 thread store 状态、binding archived 标志，并在 archive 时 best-effort 关 session；没有 provider archive/unarchive 调用，成功返回 `null`：`internal/module/thread/archive.go:5-20`, `internal/module/thread/rpc.go:37-39,92-95`

- `thread/delete`
  - V2 还会停 inline manager、删 archive 目录、解绑、清 archived prefs，返回 `{ok,threadId}`：`go-agent-v2/internal/apiserver/methods.go:204-227`
  - V3 只做 session close + binding 删除 + thread row 删除，成功返回 `null`：`internal/module/thread/service.go:102-119`, `internal/module/thread/rpc.go:39,92-95`

- `thread/list`
  - V2 返回 `{data,nextCursor}`，并支持 archived 过滤：`go-agent-v2/internal/apiserver/methods_thread.go:301-324`
  - V3 返回裸 `[]Ref`，不支持 cursor/archived 过滤：`internal/module/thread/rpc.go:41-43`, `internal/module/thread/service.go:62-83`

- `thread/loaded/list`
  - V2 返回 `{data,nextCursor}`：`go-agent-v2/internal/apiserver/methods_thread.go:326-341`
  - V3 只是 `ListByStatus(statusCreated)`，语义变成“查本地 store 中 status=created 的 thread”：`internal/module/thread/rpc.go:44-46`, `internal/module/thread/service.go:75-83`

- `thread/read`
  - V2 返回 `{history:[...]}`：`go-agent-v2/internal/apiserver/methods_thread_turn.go:39-43`, `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:80-82,420-423`
  - V3 只返回 `Ref{id,name,agentId}`：`internal/module/thread/rpc.go:47-50,133-137`, `internal/module/thread/service.go:66-72,275-284`

- `thread/resolve`
  - V2 返回 `threadId/state/port/providerThreadId/uuid/hasHistory`：`go-agent-v2/internal/apiserver/methods_thread_turn.go:44-47`, `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:83-89,426-428`
  - V3 仍然只返回 `Ref{id,name,agentId}`：`internal/module/thread/rpc.go:48-50,133-137`, `internal/module/thread/service.go:66-72,275-284`

- `thread/messages`
  - V2 参数里的 `before` 是 `int64`，返回 `{messages,total}`：`go-agent-v2/internal/apiserver/methods_thread.go:295-299`, `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:431-434`
  - V3 参数里的 `before` 变成 `string`，返回裸 `[]dto.Message`：`internal/module/thread/rpc_types.go:23-27`, `internal/module/thread/history.go:22-45`

- `thread/config/get` / `thread/config/set`
  - V2 是直接 provider config 读写，返回 `threadId/provider/supportsThreadOverride/override/effective`：`go-agent-v2/internal/apiserver/methods_thread.go:343-359`, `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:59-70,437-449`
  - V3 虽然暴露了路由，但都落到 `SendCommand`；而 `SendCommand` 根本不支持 `/config/get` 和 `/config/set`，会直接 `unsupported command`：`internal/module/thread/rpc.go:58-59`, `internal/module/thread/command.go:19-38`

- `thread/name/set`
  - V2 调 providerAdapter 的 `ThreadNameSet`：`go-agent-v2/internal/apiserver/methods_thread_turn.go:28-31`
  - V3 只是把本地 thread row 的 `Prompt` 改掉，并不触达 provider：`internal/module/thread/service.go:92-100`

- `thread/compact/start`
  - V2 走 slash command pass-through：`go-agent-v2/internal/apiserver/methods_thread_turn.go:33-35`
  - V3 路由存在，但 `SendCommand` 不支持 `/compact`：`internal/module/thread/rpc.go:63`, `internal/module/thread/command.go:19-38`

- `thread/rollback`
  - V2 有专门 `ThreadRollback`：`go-agent-v2/internal/apiserver/methods_thread.go:274-293`
  - V3 路由存在，但 `SendCommand` 不支持 `/rollback`：`internal/module/thread/rpc.go:64`, `internal/module/thread/command.go:19-38`

- `thread/undo`
  - V2 走 `/undo` pass-through：`go-agent-v2/internal/apiserver/methods_thread_turn.go:95-97`
  - V3 路由存在，但 `SendCommand` 不支持 `/undo`：`internal/module/thread/rpc.go:65`, `internal/module/thread/command.go:19-38`

- `thread/backgroundTerminals/clean`
  - V2 走 `/clean` pass-through：`go-agent-v2/internal/apiserver/methods_thread_turn.go:56-58`
  - V3 路由存在，但 `SendCommand` 不支持 `/clean`：`internal/module/thread/rpc.go:66`, `internal/module/thread/command.go:19-38`

- `thread/mcp/list`
  - V2 走 `/mcp` pass-through：`go-agent-v2/internal/apiserver/methods_thread_turn.go:107-109`
  - V3 路由存在，但 `SendCommand` 不支持 `/mcp`：`internal/module/thread/rpc.go:67`, `internal/module/thread/command.go:19-38`

- `thread/skills/list`
  - V2 有专门 `ThreadSkillsList`：`go-agent-v2/internal/apiserver/methods_thread_turn.go:110`, `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:437-439`
  - V3 路由存在，但 `SendCommand` 不支持 `/skills`：`internal/module/thread/rpc.go:70`, `internal/module/thread/command.go:19-38`

- `thread/debugMemory`
  - V2 是 provider 级 `/debug-m-drop` / `/debug-m-update`：`go-agent-v2/internal/apiserver/methods.go:373-388`
  - V3 现在直接返回宿主 Go 进程的 `runtime.MemStats`，代码里自己也标了 TODO：`internal/module/thread/rpc.go:72-77`

- `thread/realtime/start` / `thread/realtime/appendAudio` / `thread/realtime/appendText` / `thread/realtime/stop`
  - V2 都有 provider 能力守卫和专门 pass-through：`go-agent-v2/internal/apiserver/methods_thread_turn.go:72-92`
  - V3 路由存在，但都会落到 `SendCommand` 的 unsupported：`internal/module/thread/rpc.go:79-82`, `internal/module/thread/command.go:19-38`

**补充**

- `thread/model/set` / `thread/personality/set` / `thread/approvals/set` 是目前 V3 少数接近闭环的命令：`internal/module/thread/rpc.go:60-62`, `internal/module/thread/command.go:21-38`
- `thread/stop` 在 V2 也不存在；这一项不是“V3 回退”，而是“V3 仍未补齐 thread 生命周期 stop 能力”

---

## 10. provider 无关性

**结论：不通过，只有接口表面无关，内部有明显隐式耦合。**

**证据**

- 顶层 driver/session contract 看起来是 provider-neutral：`internal/contract/provider.go:10-37`
- 但 binding store 直接有 `CodexThreadID` 字段：`internal/store/binding/contract.go:16-25,39-49`
- thread service 持久化时把 `ProviderThreadID` 和 `CodexThreadID` 同时写成一个值：`internal/module/thread/lifecycle.go:262-267`
- `resolveBinding` 里直接写死 provider 候选 `codex/claude`：`internal/module/thread/service.go:203-208`
- thread 命令通道本质依赖 slash command 语义：`internal/module/thread/rpc.go:58-67,79-82`, `internal/module/thread/command.go:19-38`
- `codexapp` 和 `claudecli` 的启动/恢复语义差异很大：
  - `codexapp`：`initialize` + 远端 `thread/start/resume` + 明确 timeout：`internal/provider/codexapp/driver.go:78-157`
  - `claudecli`：直接拉 CLI，会用 fallback threadID，没有统一 timeout：`internal/provider/claudecli/driver.go:53-131`

**判断**

接口是统一的，但实现层并不真正 provider-independent，尤其是：

- 绑定模型带 Codex 特化字段
- 绑定解析带硬编码 provider 名称
- 命令语义假设所有 provider 都接受 slash command

---

## 11. 事件发布

**结论：部分具备，但 thread 生命周期事件没有正确对外发布。**

**证据**

- orchestration 会发布 agent 级事件：`internal/sidecar/orch/orchestration/events.go:13-64`
- `codexapp` 会把 `thread/started`、`thread/status/changed`、`shutdown.complete` 等翻译成 `agentdto.*`：`internal/provider/codexapp/event_map.go:39-74`
- RPC push 只订阅并转发：
  - `ui/state/changed`
  - `turn/started`
  - `turn/completed`
  见：`internal/platform/rpc/push.go:16-20,75-90`
- log sink 也只做 agent/turn/tool/task/workspace/ui 事件日志镜像：`internal/platform/bus/sink.go:43-87`
- thread service 自己的 `Start/Resume/Recover/Delete/Archive/Unarchive` 完全不发布 thread 生命周期事件：`internal/module/thread/lifecycle.go:44-169`, `internal/module/thread/service.go:102-119`, `internal/module/thread/archive.go:5-20`

**判断**

- 内部总线上“有事件”。
- 但这些事件主要是 agent 级，不是 thread 级。
- 对 RPC/UI 来说，目前稳定推送的只有 `ui/state/changed` 和 turn 事件，没有 `thread/started` / `thread/deleted` / `thread/archived` / `thread/resumed` 这一类 thread 生命周期通知。

---

## 12. store 事务

**结论：不通过。**

**证据**

- `Start/Resume` 的持久化是多步写：`internal/module/thread/lifecycle.go:238-271`
- `Delete` 是多步写：`internal/module/thread/service.go:102-119`
- `Archive/Unarchive` 也是多步写：`internal/module/thread/archive.go:5-20`
- thread store 和 binding store 的接口都没有 `WithTx`：`internal/store/thread/contract.go:7-21`, `internal/store/binding/contract.go:7-14`
- 底层 `sqlc.Queries` 其实已经支持 `WithTx`：`internal/store/sqlc/db.go:51-58`

**判断**

基础设施有事务能力，但 thread 生命周期层没有接上，所以所有跨 store 的状态更新都暴露在半成功/半失败窗口里。

---

## 最终判定

`Thread 生命周期层` 当前可以支撑基本演示链路，但还不满足“迁移完成”的标准。按阻塞程度排序，最先要补的是：

1. 为 `Start/Resume/Delete/Archive` 补事务或补幂等回滚。
2. 明确 `thread/stop` 的公共语义，并统一 session/orchestration/store 的 stop 路径。
3. 统一 thread store 状态机，至少把 `created/archived/running/failed/stopped` 和 orchestration/runtime 对齐。
4. 给 thread 生命周期操作加按 `threadID` 或 `agentID` 的串行化策略。
5. 补齐 V2 能力缺口，尤其是 `config/*`、`compact`、`rollback`、`undo`、`mcp/list`、`skills/list`、`realtime/*`。
6. 建立 thread 级事件发布，而不是只靠 agent 级状态变化侧带。

## 互审

### 1. 对 `docs/plans/迁移/cap-turn-execution.md`

1. `turn/start` 被评成“✅ 真正落地” (`docs/plans/迁移/cap-turn-execution.md:37-52`) 口径偏高。当前 `turn/start` handler 先强依赖 `resolver.ResolveSession(ctx, threadID)` 成功，见 `internal/module/turn/rpc.go:20-30,33-46`；而 resolver 又要求 thread store 能反查出非空 `AgentID`，且 `SessionManager` 里已经有活跃 session，见 `internal/provider/unified/session_resolver.go:34-45`、`internal/provider/unified/session.go:48-57`。也就是说，V3 现状不是“turn/start 自己闭环”，而是“已有 thread/session 前提下的 turn 执行链”。报告没有把这个前置条件记成能力缺口。
2. `TurnCompleted` 被评成“⚠️ 仅 direct RPC 不闭环” (`docs/plans/迁移/cap-turn-execution.md:104-117`) 仍然偏乐观。即便走 orchestration queue，失败完成路径也可能不闭环：状态机只允许 `turn_starting --turn_completed--> idle`，不允许 `turn_starting --turn_aborted--> ...`，见 `internal/dto/agent/state.go:89-95`；但 `CompleteTurn(success=false)` 明确选择 `TriggerTurnAborted`，见 `internal/sidecar/orch/orchestration/service.go:341-348`。报告把这个问题只放在“额外风险” (`docs/plans/迁移/cap-turn-execution.md:241-242`)，没有反映进主判定，轻了一档。
3. approval 集成一节 (`docs/plans/迁移/cap-turn-execution.md:137-151`) 只批到了“统一事件交付/状态机闭环不完整”，但漏了更强的 correctness bug。`codexapp` 在收到审批通知时，会先 `dispatch` 原始 provider 事件，再异步 goroutine 进入 `requestToolApproval`，见 `internal/provider/codexapp/session.go:233-239`、`internal/provider/codexapp/session_approval.go:14-23`；而 pending 直到 `ApprovalManager.registerPending` 才建立，见 `internal/platform/rpc/approval.go:74-85,127-152`。这意味着前端可能先看到 approval request，但随后抢先发 `approval/respond` 仍然会命中不到 pending。这个竞态没有进入报告主结论。

### 2. 对 `docs/plans/迁移/cap-approval-lifecycle.md`

1. `approval/respond` 在总结表里被判成“通过” (`docs/plans/迁移/cap-approval-lifecycle.md:17`) 与报告正文自己暴露的竞态相冲突。报告在 `§1` 已承认当前 `codexapp` 路径里“前端可能已经看到了 approval request，但本地 pending 还没注册完成，因此 `approval/respond` 可能返回 `approval is not pending`” (`docs/plans/迁移/cap-approval-lifecycle.md:109-114`)。代码也确实是这个顺序：`onNotification` 先 `dispatch` 原始事件，后起 goroutine 进入 `requestToolApproval`，而 pending 则在 `RequestApproval -> registerPending` 才建立，见 `internal/provider/codexapp/session.go:233-239`、`internal/provider/codexapp/session_approval.go:14-23,26-43`、`internal/platform/rpc/approval.go:74-85,127-152`。既然端到端可见路径仍可失败，这项不能简单判“通过”。
2. `request_user_input` 一节声称“当前是否有真实调用点：没有”“provider 是否桥接：没有” (`docs/plans/迁移/cap-approval-lifecycle.md:376-392`) 是错误的。`codexapp` 的 `requestApprovalDecision` 明确会对 `request_user_input` 家族调用 `s.approvals.RequestUserInput(...)`，见 `internal/provider/codexapp/session_approval.go:38-43`；`isApprovalBridgeMethod` / `isRequestUserInputMethod` 也显式纳入了 `request_user_input`、`codex/event/request_user_input`、`item/tool/requestUserInput` 等方法，见 `internal/provider/codexapp/session_approval.go:93-113`；而 `onNotification` 是通过 `isApprovalBridgeMethod(method)` 进入这条链的，见 `internal/provider/codexapp/session.go:233-239`。更准确的结论应是“已有 provider 入口，但 UI/auto-responder/恢复链不完整”，不是“无调用点、无桥接”。
3. callback method 兼容一节说 `codexapp` 当前真实入口只覆盖 `item/commandExecution/requestApproval` 与 `tool.approval.requested`，并据此判定 `item/fileChange/requestApproval` / `skill/requestApproval` 仍缺 (`docs/plans/迁移/cap-approval-lifecycle.md:449-458`)；这同样不成立。真正的入口判定不在 `session.go` 的表面 `switch`，而在 `isApprovalBridgeMethod`，它明确包含了 `item/fileChange/requestApproval` 和 `skill/requestApproval`，见 `internal/provider/codexapp/session_approval.go:93-99`。报告把门面 switch 当成了能力全集，漏看了 predicate 内部展开。
4. `requestId` 去重在总结表中被判成“通过” (`docs/plans/迁移/cap-approval-lifecycle.md:18`)，但正文 `§3.3` 自己已经承认“缺失 `requestID` 的重复 `callID` 请求仍会折叠” (`docs/plans/迁移/cap-approval-lifecycle.md:234-241`)。代码上这是结构性问题，不是轻微边角：`pendingStorageKey` 在 `requestID` 为空时直接退化成 `callID`，见 `internal/platform/rpc/approval_support.go:144-153`；而 `registerPending` 是在算完 key 之后才补分配 `nextRequestID`，见 `internal/platform/rpc/approval.go:130-138`。因此“同 `callID` 不同 `requestID` 可并存”成立，但“去重整体通过”并不成立。

### 3. 对 `docs/plans/迁移/cap-orchestration-agent.md`

1. 这份报告在用户给定路径上根本不存在，无法进入正文复核。对 `docs/plans/迁移/cap-orchestration-agent.md` 直接执行 LSP `read_file` 返回的是 `path not found`，因此当前仓库里没有可供互审的目标文档。
2. 这不是简单的标题偏差。对 `docs/plans/迁移` 做 LSP `text_search("cap-orchestration-agent")`、`text_search("能力+容错审查：Orchestration Agent")`、`text_search("能力+容错审查：orchestration")` 都是 0 命中，说明迁移文档矩阵里没有同名报告或同主题标题可供替代复核。
3. 更糟的是，迁移结论文档已经把这个主题当成已复核内容在引用了。`docs/plans/迁移/final-verdict-2.md:92` 直接写了“`orchestration agent.submit* 真执行链`”已经确认修复，但仓库里却没有对应的 standalone 报告正文可供追溯。这意味着结论层与证据层断链，当前这份“报告”最主要的问题不是内容瑕疵，而是交付物缺失。
