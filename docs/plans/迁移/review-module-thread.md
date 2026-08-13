# module/thread 后端总审查

审查时间：2026-03-21
审查范围：`internal/module/thread/` 全部文件
审查方式：只读审查，结合 LSP `text_search`、`workspace_symbol`、`references(compact)`、`call_hierarchy`、`read_file`、`document_symbol`

## 结论

当前 `internal/module/thread` 已经搭起了一个可编译的 thread 模块骨架，但离 V2 等价还很远，暂时不具备“迁移完成”条件。

核心结论只有三条：

1. `rpc.go` 虽然已经注册了 29 个 handler key，但大量 handler 只是接到了统一的 `SendCommand` 骨架，运行时会直接返回 `unsupported command`，或者返回与 V2 完全不同的结果结构。
2. `rpc_types.go` 与返回结构体的 JSON 形状和 V2 差异很大，尤其是 `thread/start`、`thread/resume`、`thread/list`、`thread/read`、`thread/resolve`、`thread/messages`、`thread/config/*`、`thread/debugMemory`、`thread/realtime/*`。
3. lifecycle、history/archive、command 三个面都只做了最小闭环，没有实现 V2 的关键语义：独立 thread identity、结构化返回、archive 文件归档/恢复、loaded list 分页、messages 分页与 total、slash route 全量支持、进程 stop/recover 语义。

如果以“迁移完成度”衡量，我的判断是：`module/thread` 目前更接近 `P4/P5 骨架`，不是 `V2 parity`。

## 1. 文件清单与行数

共 9 个文件，合计 1041 行。

| 文件 | 行数 | 说明 |
| --- | ---: | --- |
| `internal/module/thread/archive.go` | 20 | 归档/反归档最小实现 |
| `internal/module/thread/command.go` | 71 | 命令分发，当前只实做少数命令 |
| `internal/module/thread/contract.go` | 68 | Service 接口与请求/结果类型 |
| `internal/module/thread/history.go` | 92 | history/messages 读取 |
| `internal/module/thread/lifecycle.go` | 399 | start/resume/fork/recover 主逻辑 |
| `internal/module/thread/module.go` | 18 | fx 注册 |
| `internal/module/thread/rpc.go` | 143 | 29 个 RPC handler 注册 |
| `internal/module/thread/rpc_types.go` | 37 | RPC params 定义 |
| `internal/module/thread/service.go` | 293 | store/session/binding 基础能力 |

## 2. handler 完整性

`internal/module/thread/rpc.go:18-84` 共注册 29 个 handler key，数量完整，没有重复键。

| # | handler key | 当前绑定 |
| ---: | --- | --- |
| 1 | `thread/start` | `svc.Start` |
| 2 | `thread/resume` | `newResumeHandler -> svc.Get -> svc.Resume` |
| 3 | `thread/fork` | `svc.Fork` |
| 4 | `thread/recover` | `svc.Recover` |
| 5 | `thread/archive` | `svc.Archive` |
| 6 | `thread/unarchive` | `svc.Unarchive` |
| 7 | `thread/delete` | `svc.Delete` |
| 8 | `thread/list` | `svc.List` |
| 9 | `thread/loaded/list` | `svc.ListByStatus(statusCreated)` |
| 10 | `thread/read` | `svc.Get` |
| 11 | `thread/resolve` | `svc.Get` |
| 12 | `thread/messages` | `svc.ReadMessages` |
| 13 | `thread/name/set` | `svc.SetName` |
| 14 | `thread/config/get` | `svc.SendCommand("config/get")` |
| 15 | `thread/config/set` | `svc.SendCommand("config/set")` |
| 16 | `thread/model/set` | `svc.SendCommand("/model")` |
| 17 | `thread/personality/set` | `svc.SendCommand("/personality")` |
| 18 | `thread/approvals/set` | `svc.SendCommand("/approvals")` |
| 19 | `thread/compact/start` | `svc.SendCommand("/compact")` |
| 20 | `thread/rollback` | `svc.SendCommand("/rollback")` |
| 21 | `thread/undo` | `svc.SendCommand("/undo")` |
| 22 | `thread/backgroundTerminals/clean` | `svc.SendCommand("/clean")` |
| 23 | `thread/mcp/list` | `svc.SendCommand("/mcp")` |
| 24 | `thread/skills/list` | `svc.SendCommand("/skills")` |
| 25 | `thread/debugMemory` | `runtimeMemoryStats()` |
| 26 | `thread/realtime/start` | `svc.SendCommand("realtime/start")` |
| 27 | `thread/realtime/appendAudio` | `svc.SendCommand("realtime/appendAudio")` |
| 28 | `thread/realtime/appendText` | `svc.SendCommand("realtime/appendText")` |
| 29 | `thread/realtime/stop` | `svc.SendCommand("realtime/stop")` |

补充观察：

- `thread/read` 和 `thread/resolve` 当前都落到 `svc.Get`，这两个路由在现实现里几乎是同义的；V2 中二者语义明确不同。
- `thread/debugMemory` 直接返回宿主进程 `runtime.MemStats`，`rpc.go:72-75` 还留了 TODO，明确承认这不是 V2 行为。

## 3. V2 方法对照

本节严格按用户要求，只对照 `go-agent-v2/internal/apiserver/methods_thread.go`。
如果 V2 路由真实实现位于 `methods_thread_turn.go` 或 `methods.go`，这里仍按“`methods_thread.go` 内是否存在等价方法”记为 `❌缺失`，并在备注里说明。

结论先说：`29/29` 里没有一个可以给 `✅等价`。

| handler key | V2 `methods_thread.go` 对应 | 结论 | 备注 |
| --- | --- | --- | --- |
| `thread/start` | `threadStartTyped` | ⚠️骨架 | 当前要求 `provider`，缺 `modelProvider/baseInstructions/developerInstructions/sandbox/summary`，返回也不是 V2 的 `{thread, model, modelProvider, cwd, approvalPolicy}` |
| `thread/resume` | `threadResumeTyped` | ⚠️骨架 | 当前要求 `provider`，缺 `path/cwd/model`，返回 `null`，不是 `{thread, model}` |
| `thread/fork` | `threadForkTyped` | ⚠️骨架 | 当前不支持 `turnIndex`，返回 `ForkResult{NewThreadID}` 而不是 `{thread:{id,forkedFrom}}` |
| `thread/recover` | `threadRecoverTyped` | ⚠️骨架 | 当前只返回 `null`，没有 `{thread,recovered,mode}` |
| `thread/archive` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go`，而且底层走 archive service，不是当前的状态位切换 |
| `thread/unarchive` | 无 | ❌缺失 | V2 路由在 `methods.go` |
| `thread/delete` | 无 | ❌缺失 | V2 路由在 `methods.go` |
| `thread/list` | `threadList` | ⚠️骨架 | 当前返回 `[]Ref`，V2 返回 `{data,nextCursor}`，且支持 `archived` 过滤 |
| `thread/loaded/list` | `threadLoadedList` | ⚠️骨架 | 当前无 `cursor/limit`，也没有 V2 的分页结构 |
| `thread/read` | 无 | ❌缺失 | V2 真实路由在 `methods_thread_turn.go`，结果应为 `{history:[...]}`；当前只是 `svc.Get` |
| `thread/resolve` | 无 | ❌缺失 | V2 真实能力在 lifecycle service；当前只是 `svc.Get` |
| `thread/messages` | 只有 `threadMessagesParams` | ❌缺失 | `methods_thread.go` 里无 handler，V2 路由在 `methods_thread_turn.go` |
| `thread/name/set` | 只有 `threadNameSetParams` | ❌缺失 | `methods_thread.go` 里无 handler，V2 路由在 `methods_thread_turn.go` |
| `thread/config/get` | `threadConfigGetTyped` | ⚠️骨架 | 当前走 `SendCommand("/config/get")`，运行时直接 unsupported |
| `thread/config/set` | `threadConfigSetTyped` | ⚠️骨架 | 当前走 `SendCommand("/config/set")`，运行时直接 unsupported |
| `thread/model/set` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go`，支持 `model/args` 双入参；当前只认 `args` |
| `thread/personality/set` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go` |
| `thread/approvals/set` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go` |
| `thread/compact/start` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go`，底层是 slash helper |
| `thread/rollback` | `threadRollbackTyped` | ⚠️骨架 | 当前参数被压平成 `args`，底层还会 unsupported；V2 需要 `numTurns/turnIndex` |
| `thread/undo` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go` |
| `thread/backgroundTerminals/clean` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go` |
| `thread/mcp/list` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go` |
| `thread/skills/list` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go`，而且 V2 无需 `threadId` params |
| `thread/debugMemory` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go`，参数是 `action`，不是 `threadId` |
| `thread/realtime/start` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go`，参数是 `{threadId,prompt,sessionId}` |
| `thread/realtime/appendAudio` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go`，参数是 `{threadId,audio}` |
| `thread/realtime/appendText` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go`，参数是 `{threadId,text}` |
| `thread/realtime/stop` | 无 | ❌缺失 | V2 路由在 `methods_thread_turn.go` |

## 4. Service 接口覆盖

`internal/module/thread/contract.go:9-26` 共 15 个接口方法。`rpc.go` 实际可达 13 个，`2` 个接口方法没有任何 RPC 调用。

| Service 方法 | `rpc.go` 是否调用 | 调用来源 | 结论 |
| --- | --- | --- | --- |
| `Start` | 是 | `thread/start` | 覆盖 |
| `Resume` | 是 | `thread/resume` | 覆盖 |
| `Fork` | 是 | `thread/fork` | 覆盖 |
| `Recover` | 是 | `thread/recover` | 覆盖 |
| `List` | 是 | `thread/list` | 覆盖 |
| `Get` | 是 | `thread/read`、`thread/resolve`、`newResumeHandler` | 覆盖，但语义被复用过度 |
| `ReadHistory` | 否 | 无 | 未覆盖，死接口/死实现 |
| `ReadMessages` | 是 | `thread/messages` | 覆盖 |
| `Archive` | 是 | `thread/archive` | 覆盖 |
| `Unarchive` | 是 | `thread/unarchive` | 覆盖 |
| `ListByStatus` | 是 | `thread/loaded/list` | 覆盖，但 loaded 语义不对 |
| `ListByCWD` | 否 | 无 | 未覆盖，死接口/死实现 |
| `SendCommand` | 是 | 15 个 command/realtime/config handler | 覆盖过重，问题集中点 |
| `SetName` | 是 | `thread/name/set` | 覆盖 |
| `Delete` | 是 | `thread/delete` | 覆盖 |

结论：

- interface 和 `rpc.go` 不是“一一对应”的。
- `ReadHistory`、`ListByCWD` 目前只有声明和实现，没有 RPC 入口，也没有别的引用。
- `SendCommand` 反而承接了 15 个路由，是当前模块最明显的“接口过度塌缩”点。

## 5. service 实现质量

| 方法 | 质量判断 | 说明 |
| --- | --- | --- |
| `Start` | 真实但简化 | 会启动 agent、start session、落 thread/binding；但 thread identity、参数、返回结构都和 V2 不同 |
| `Resume` | 真实但简化 | 会重启 agent、resume session、落库；但强依赖 `provider + agentID`，不兼容 V2 `path/cwd/model` |
| `Fork` | 真实但简化 | 确实调用 `session.ForkThread`；但不支持 `turnIndex`，返回结构不兼容 |
| `Recover` | 真实但简化 | 确实做 recover/launch/resume；但返回为空，也没有 V2 的 mode/recovered 信息 |
| `List` | 真实但简化 | 直接列 DB thread，返回 `[]Ref` |
| `Get` | 真实但简化 | 仅返回 `Ref{ID,Name,AgentID}` |
| `ReadHistory` | 真实实现，但未接线 | 只在 `history.go` 被定义，没有调用者 |
| `ReadMessages` | 真实但简化 | 只做 `session.ReadHistory + before 过滤`，没有 total、没有 hydration、没有 V2 response envelope |
| `Archive` | 极简实现 | 仅改 status、binding archived、关 session |
| `Unarchive` | 极简实现 | 仅改 status、binding archived=false |
| `ListByStatus` | 真实实现 | 纯 DB filter |
| `ListByCWD` | 真实实现，但未接线 | 纯 DB filter，无调用者 |
| `SendCommand` | 骨架/stub | 只有 `/model`、`/personality`、`/approvals`、`/interrupt` 四条路径，其他全部 unsupported |
| `SetName` | 真实但简化 | 直接把 `thread.Prompt` 当 name 存回去 |
| `Delete` | 真实但简化 | 会删 binding/thread 记录，但没有 stop orchestration、没有 archive 清理、没有 V2 ack 结构 |

最关键的质量判断：

- `SendCommand` 不能算“完整实现”；它只是统一入口骨架。
- `Archive`/`Unarchive`/`Delete` 只能算最小状态管理，不算 V2 等价实现。
- `ReadHistory` 是真代码，但当前属于“死代码”。

## 6. history/archive

### history

现状：

- `ReadHistory` 存在，但无 RPC 入口，`references` 结果只有声明与实现本身。
- `thread/read` 没走 `ReadHistory`，而是直接走 `svc.Get`，所以当前根本没有 V2 `thread/read -> {history:[...]}` 语义。
- `thread/messages` 直接返回 `[]dto.Message`，没有 V2 的 `{messages,total}` envelope。
- `messagesParams.Before` 是 `string`，而 V2 `threadMessagesParams.Before` 是 `int64`。
- 一旦传 `before`，当前实现会把 `historyLimit` 置 0，再调用 `session.ReadHistory(...)` 后在本地过滤；这对大历史线程是明显的扩展性风险。

对照 V2：

- V2 `Adapter.ThreadMessages` 会加载 provider rollout history、做分页、返回 total，并带 runtime hydration 逻辑，见 `go-agent-v2/internal/apiserver/codexadapter/thread_messages.go:77-102`。

结论：

- history 能力不等价。
- 当前只实现了“能从 session 拉一点历史”，没有实现 V2 的消息分页协议和 thread/read 语义。

### archive

现状：

- `Archive` 只做三件事：改 thread status、改 binding archived、`session.Close()`。
- `Unarchive` 只做两件事：改 thread status、改 binding archived=false。
- 没有 archive 目录、manifest、provider artifact 归档、restore、partial/warning、rollout_path rebind。
- `Delete` 也没有清理 archive 目录。

对照 V2：

- V2 `ThreadArchive` 会真正归档 provider artifacts、保存 manifest、回传 `archiveDir/rolloutPath/files/partial/warnings`，见 `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_ops.go:54-91`。
- V2 `ThreadUnarchive` 会恢复归档文件、回写 binding、清理 archive dir、回传 restore 结果，见 `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_ops.go:94-175`。

结论：

- archive/unarchive 与 V2 完全不等价，当前只是“逻辑归档标记”，不是“归档功能”。

## 7. command 处理

当前 `command.go:14-43` 的命令矩阵如下。

| 路由组 | 当前底层命令 | 结果 |
| --- | --- | --- |
| `thread/model/set` | `/model` | 部分可用，最终走 `session.Configure(Model)` |
| `thread/personality/set` | `/personality` | 部分可用，最终走 `session.Configure(Personality)` |
| `thread/approvals/set` | `/approvals` | 部分可用，最终走 `session.Configure(Approvals)` |
| 无公开路由 | `/interrupt` | 代码支持，但没有任何 handler 暴露，实际不可达 |
| `thread/config/get` | `/config/get` | 直接 unsupported |
| `thread/config/set` | `/config/set` | 直接 unsupported |
| `thread/compact/start` | `/compact` | 直接 unsupported |
| `thread/rollback` | `/rollback` | 直接 unsupported |
| `thread/undo` | `/undo` | 直接 unsupported |
| `thread/backgroundTerminals/clean` | `/clean` | 直接 unsupported |
| `thread/mcp/list` | `/mcp` | 直接 unsupported |
| `thread/skills/list` | `/skills` | 直接 unsupported |
| `thread/realtime/*` | `/realtime/...` | 直接 unsupported |

更细的兼容性问题：

1. 当前所有 command handler 统一使用 `commandParams{threadId,args}`，见 `internal/module/thread/rpc_types.go:34-37`。
2. V2 对 `/model`、`/personality`、`/approvals` 允许专用参数键，例如 `model`、`personality`、`policy`，也兼容 `args`，见 `go-agent-v2/legacy-agentsdk/service/command/slash_command_logic.go:104-119`。
3. 当前 handler 使用 `StrictHandler`，所以如果前端按 V2 发送 `{"threadId":"...","model":"gpt-5.4"}`，会因为 `model` 不是 `commandParams` 字段而直接判定为 invalid params。
4. `thread/skills/list` 在 V2 是无参路由，当前却用了 `rpc.ThreadHandler`，会强制要求 `threadId`。
5. `thread/debugMemory` 在 V2 参数是 `{"action":"drop|update"}`；当前却走 `threadId` 路径，完全不是同一个协议。

结论：

- 命令机制不完整。
- 真正闭环的只有 3 个 set 命令，而且参数兼容还不对。
- 其余 slash/realtime/config 路由目前只能视为占位。

## 8. lifecycle

### 已有部分

- 创建/启动：`Start`
- 恢复启动：`Resume`
- fork：`Fork`
- recover：`Recover`
- archive/unarchive：`Archive` / `Unarchive`
- delete：`Delete`

### 缺口

1. 没有独立的 app-level thread id 分配逻辑。
   V2 `thread/start` 先 `allocateFreshThreadID`，然后单独跟 provider thread id 对齐；当前 `Start` 直接用 `session.ThreadID()` 或 `agentID` 当 thread id，见 `lifecycle.go:62` 与 V2 `methods_thread.go:44-76`。

2. 没有“running”状态。
   模块内只定义了 `created` 和 `archived` 两个状态，`persistThreadState` 永远写 `statusCreated`，`thread/loaded/list` 却把 `created` 当 loaded 用，见 `service.go:16-19`、`lifecycle.go:251`、`rpc.go:44-46`。

3. 没有真正的 stop 生命周期。
   对外没有 `thread/stop`；`Archive` 和 `Delete` 也没有调用 `orchestration.StopAgent`，只做 `session.Close()` 或纯删表。V2 的 archive/delete 都会停进程。

4. `Delete` 生命周期不完整。
   当前 `Delete` 不清理 archive 目录、不回传 `{ok,threadId}`、不显式 stop orchestration agent。

5. `Unarchive` 生命周期不完整。
   当前只把状态改回 `created`，不恢复 artifacts、不 ensure process alive。V2 `threadUnarchiveTyped` 至少会尝试 `EnsureProcessAlive`。

6. `Resume` 语义偏弱。
   当前 `newResumeHandler` 先 `svc.Get(threadID)` 取 `AgentID`，再要求 `provider`；如果 binding / agent id 缺失，就不能 resume。V2 `threadResumeTyped` 的入口参数和恢复策略更宽。

7. `Fork` 语义偏弱。
   没有 `turnIndex`，只能 fork 整个 thread。

结论：

- 当前 lifecycle 只有“能开起来/能删记录”的最小闭环。
- 缺少 V2 的状态机、identity、分页/resolve 信息、archive/recover 语义。

## 9. import 方向

结论：这一项基本通过。

证据：

- 未发现 `internal/provider/*` 或其它 provider 实现层 import。
- `rpc.go` 合规 import 了 `internal/platform/rpc`。
- thread 模块依赖的主要是 `internal/contract`、`internal/dto/provider`、`internal/store/*`、`internal/platform/rpc`，保持了 provider-neutral 方向。

补充：

- `dto/provider` 是 DTO 层，不属于禁止的 provider 实现 import，方向上可接受。

## 10. fx 注册

结论：形式上通过。

`internal/module/thread/module.go:7-18` 已提供：

- `NewService`
- `NewThreadHandlers`

注意点：

- 所有依赖都被 `optional:"true"` 标记了，这让模块很容易在“半接线”状态下成功启动。
- 结果是：模块虽然能被 fx 注册，但很多路径会在运行时才报 `thread store is not configured`、`session starter is not configured`、`session provider is not configured`。

所以这一项可以判为“已注册，但防错性弱”。

## 11. 参数类型

这一项问题很多，而且不止是 `rpc_types.go`，还包括返回结构体缺少 JSON tag。

### 参数不兼容

| 路由 | 当前参数 | V2 参数 | 结论 |
| --- | --- | --- | --- |
| `thread/start` | `provider,cwd,model,prompt,approvalPolicy,instructions,effort,personality` | `model,modelProvider,cwd,approvalPolicy,baseInstructions,developerInstructions,sandbox,summary,effort,personality` | 不兼容 |
| `thread/resume` | `threadId,provider` | `threadId,path,cwd,model` | 不兼容 |
| `thread/fork` | `threadId` | `threadId,turnIndex` | 不兼容 |
| `thread/list` | 空 struct | `archived` | 不兼容 |
| `thread/loaded/list` | 空 struct | `cursor,limit` | 不兼容 |
| `thread/messages` | `threadId,limit,before:string` | `threadId,limit,before:int64` | 不兼容 |
| `thread/config/get` | `threadId,args` | `threadId` | 不兼容 |
| `thread/config/set` | `threadId,args` | `threadId,model,effort` | 不兼容 |
| `thread/rollback` | `threadId,args` | `threadId,numTurns,turnIndex` | 不兼容 |
| `thread/model/set` | `threadId,args` | `threadId,args/model` | 部分不兼容 |
| `thread/personality/set` | `threadId,args` | `threadId,args/personality` | 部分不兼容 |
| `thread/approvals/set` | `threadId,args` | `threadId,args/policy` | 部分不兼容 |
| `thread/skills/list` | `threadId,args` | 无参 | 不兼容 |
| `thread/debugMemory` | `threadId` | `action` | 不兼容 |
| `thread/realtime/start` | `threadId,args` | `threadId,prompt,sessionId` | 不兼容 |
| `thread/realtime/appendAudio` | `threadId,args` | `threadId,audio` | 不兼容 |
| `thread/realtime/appendText` | `threadId,args` | `threadId,text` | 不兼容 |
| `thread/realtime/stop` | `threadId,args` | `threadId` | 不兼容 |

### 返回不兼容

1. `contract.go` 里的 `StartResult`、`ForkResult`、`Ref` 都没有 JSON tag。
   这意味着输出键会是 `ThreadID`、`AgentID`、`NewThreadID`、`ID`、`Name`，不是 V2 的 `threadId/id/name/...`。

2. `thread/archive`、`thread/unarchive`、`thread/delete`、`thread/name/set`、`thread/recover`、`thread/resume` 当前大多返回 `null`。
   V2 对应路由普遍返回对象。

3. `thread/list` / `thread/loaded/list` 当前直接返回数组。
   V2 返回 `{data,nextCursor}`。

4. `thread/read` 当前返回 `Ref`。
   V2 返回 `{history:[...]}`。

5. `thread/resolve` 当前返回 `Ref`。
   V2 返回 `{threadId,state,port,providerThreadId,uuid,hasHistory}`。

6. `thread/messages` 当前返回 `[]dto.Message`。
   V2 返回 `{messages,total}`。

7. `thread/debugMemory` 当前返回整个 Go `runtime.MemStats`。
   V2 走 slash route，返回的是 provider 语义结果，不是宿主运行时结构。

结论：

- `rpc_types.go` 本身不合理的地方已经很多。
- 更大的问题是：整个 RPC 输入/输出协议都没有做到 V2 兼容。

## 12. 错误处理

结论：service 层错误处理偏弱，远不如 V2 的 `apperrors.Wrap/New` 风格。

主要问题：

1. 大多数方法直接把底层错误原样返回。
   `Start`、`Resume`、`Fork`、`Recover`、`ReadMessages`、`Delete`、`Archive`、`Unarchive` 基本都没有方法名上下文包装。

2. 有多处错误被静默吞掉。
   `Delete` 忽略 `resolveBinding` 错误，也忽略 `closeSessionIfActive` 错误。
   `closeSessionIfActive` 自己也会吞掉 binding/session lookup 错误。
   `stopAgent` 明确丢弃 `orchestration.StopAgent` 错误。

3. `SendCommand` 的默认失败是 `fmt.Errorf("unsupported command: %s", cmd)`。
   这类错误既没有 route 上下文，也没有结构化错误码。

4. `thread/debugMemory` 的 TODO 已明确说明当前行为不是 V2，但没有做任何兼容保护。

5. `newResumeHandler` 会先 `svc.Get(threadID)`，如果 `AgentID` 缺失，最终错误会在 `Resume` 内部表现为 `agent id is required`，调用方看不到更清晰的恢复上下文。

综合判断：

- 当前 error path “能报错”，但“不可诊断、不成体系”。
- 对迁移阶段很不友好。

## 13. 并发安全

### 做对了的部分

- 共享 map `threadAgents` 有 `threadAgentsMu` 保护，`rememberThreadAgent` / `lookupThreadAgent` / `forgetThreadAgent` 没有明显 data race。

### 仍然存在的风险

1. 没有 per-thread 生命周期锁。
   `Start`、`Resume`、`Recover`、`Archive`、`Delete` 都可能同时改 threadStore、bindingStore、session、orchestration，顺序并不原子。

2. 没有事务边界。
   `persistThreadState` 会先 upsert thread，再 upsert binding；`Delete` 会先删 binding 再删 thread。中间失败时可能留下半状态。

3. `thread/loaded/list` 依赖 `statusCreated`，但状态更新本身没有更细粒度同步，无法表达 running/recovering/stopping 等过渡状态。

4. `rememberBinding` 把多个 thread id 映射到一个 agent id，这个缓存虽有 mutex，但没有失效策略，逻辑上可能陈旧。

结论：

- 没有明显 Go data race。
- 但有明显“多存储、多阶段操作的逻辑并发一致性风险”。

## 14. 函数复杂度

按“函数行数”看，当前 top 3 如下。

| 排名 | 函数 | 位置 | 行数 |
| ---: | --- | --- | ---: |
| 1 | `NewThreadHandlers` | `internal/module/thread/rpc.go:18-84` | 67 |
| 2 | `(*service).persistThreadState` | `internal/module/thread/lifecycle.go:238-271` | 34 |
| 3 | `(*service).Start` | `internal/module/thread/lifecycle.go:44-76` | 33 |

补充：

- `(*service).Recover` 也是 33 行，和 `Start` 并列第三。
- 真正的复杂性不只在行数，而在“`SendCommand` 过载 + lifecycle 多阶段 side effect + 协议不兼容”。

## 15. 测试覆盖

结论：`internal/module/thread` 当前没有单元测试覆盖。

证据：

- `internal/module/thread/` 目录下没有任何 `_test.go` 文件。
- 针对全仓 `_test.go` 文本搜索，没有找到 `package thread`、`NewThreadHandlers(` 的测试引用。
- 全仓 `_test.go` 文本搜索，也没有找到 `internal/module/thread` 的直接测试入口。

因此当前缺失的关键测试路径包括：

- 29 个 handler 的 strict JSON contract 与返回结构
- `thread/start` / `thread/resume` / `thread/recover` / `thread/fork` 的 orchestration + session 协同
- `thread/list` / `thread/read` / `thread/resolve` / `thread/messages` 的 V2 协议对齐
- `SendCommand` 的命令矩阵
- `Archive` / `Unarchive` / `Delete` 的生命周期副作用
- capability gate 生效路径
- 并发下的 thread/binding 一致性

## 总体判断

`module/thread` 当前的价值主要在于：

- 已经把 thread 相关代码从 provider 实现层抽离出来了。
- handler/service/store/orchestration 的依赖骨架已经成型。

但从迁移完成度看，当前最主要的问题不是“少几个细节”，而是“三层同时未对齐”：

1. RPC 协议层未对齐 V2。
2. command/lifecycle/archive 语义层未对齐 V2。
3. 测试层几乎为空。

如果后续继续迁移，优先级建议是：

1. 先把 RPC 输入/输出协议做成 V2 兼容。
2. 再把 `SendCommand` 拆回命令专用 handler/params，而不是继续用 `args string` 硬压平。
3. 然后补 lifecycle/archive/messages 的真实 V2 语义。
4. 最后再补单测和 guardrail。
