# P5 波次 1 任务审查 A

## 1. 方法覆盖

- 基线文件：`go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113`。
- 该函数实际注册 `35` 个方法：
  - `29` 个 `thread/*`
  - `4` 个 `turn/*`
  - `1` 个 `review/start`
  - `1` 个 `mock/experimentalMethod`
- 本文按题面 `25 + 6 = 31` 的口径推导任务清单：
  - R1 `25`：`29` 个 `thread/*` 去掉 `thread/realtime/*` 四个路由
  - R2 `6`：`turn/start`、`turn/steer`、`turn/interrupt`、`turn/forceComplete`、`review/start`、`approval/respond`
- `approval/respond` 不在 `registerThreadTurnMethods` 内，而是在 `go-agent-v2/internal/apiserver/methods.go:157-163` 的 core 注册表中。

### 31 方法清单

- R1 `25`：`thread/start`、`thread/resume`、`thread/recover`、`thread/fork`、`thread/archive`、`thread/unarchive`、`thread/delete`、`thread/name/set`、`thread/compact/start`、`thread/rollback`、`thread/list`、`thread/loaded/list`、`thread/read`、`thread/resolve`、`thread/config/get`、`thread/config/set`、`thread/messages`、`thread/backgroundTerminals/clean`、`thread/undo`、`thread/model/set`、`thread/personality/set`、`thread/approvals/set`、`thread/mcp/list`、`thread/skills/list`、`thread/debugMemory`
- R2 `6`：`approval/respond`、`review/start`、`turn/start`、`turn/steer`、`turn/interrupt`、`turn/forceComplete`

### 对照结论

- 这 `31` 个方法不能“完全覆盖” `registerThreadTurnMethods`。
- 严格对照 `registerThreadTurnMethods`：
  - 已覆盖：`25` 个非 realtime 的 `thread/*` + `4` 个 `turn/*` + `review/start`，共 `30` 个该函数内的方法。
  - 未覆盖：`thread/realtime/start`、`thread/realtime/appendAudio`、`thread/realtime/appendText`、`thread/realtime/stop`。
  - 另外未纳入：`mock/experimentalMethod`。
  - 额外纳入：`approval/respond`，但它来自 `methods.go`，不来自 `registerThreadTurnMethods`。
- 结论：
  - 若按“V2 业务语义方法”口径，缺口主要是 `thread/realtime/*` 四个路由。
  - 若按“该注册函数全部条目”口径，缺口是 `thread/realtime/*` 四个路由加 `mock/experimentalMethod`。
- `thread/realtime/*` 四个路由不在这 `31` 个方法清单里；`25` 这个数字正是由 `29 - 4 = 25` 得出。

## 2. Service 闭环

- 读取文件：
  - `internal/module/thread/contract.go:9-26`
  - `internal/module/turn/contract.go:12-19`
  - `internal/contract/approval.go:5-13`
- 当前接口口径下，`31` 个方法里：
  - 明确闭环：`19`
  - 部分闭环：`1`
  - 未闭环：`11`

| RPC 方法 | 目标 Service 方法/门面 | 状态 | 证据 |
| --- | --- | --- | --- |
| `thread/start` | `thread.Service.Start(ctx, StartRequest)` | 是 | `internal/module/thread/contract.go:10` |
| `thread/resume` | `thread.Service.Resume(ctx, ResumeRequest)` | 是 | `internal/module/thread/contract.go:11` |
| `thread/recover` | `thread.Service.Recover(ctx, threadID)` | 是 | `internal/module/thread/contract.go:13` |
| `thread/fork` | `thread.Service.Fork(ctx, threadID)` | 是 | `internal/module/thread/contract.go:12` |
| `thread/archive` | `thread.Service.Archive(ctx, threadID)` | 是 | `internal/module/thread/contract.go:19` |
| `thread/unarchive` | `thread.Service.Unarchive(ctx, threadID)` | 是 | `internal/module/thread/contract.go:20` |
| `thread/delete` | `thread.Service.Delete(ctx, threadID)` | 是 | `internal/module/thread/contract.go:25` |
| `thread/name/set` | `thread.Service.SetName(ctx, threadID, name)` | 是 | `internal/module/thread/contract.go:24` |
| `thread/compact/start` | 独立 facade 或显式 service 方法 | 否 | `thread.Service` 无对应方法；`SendCommand` 只支持 `/model`、`/personality`、`/approvals`、`/interrupt`，见 `internal/module/thread/command.go:18-35` |
| `thread/rollback` | `Rollback(...)` | 否 | `internal/module/thread/contract.go:9-26` 无该方法 |
| `thread/list` | `thread.Service.List(ctx)` | 是 | `internal/module/thread/contract.go:15` |
| `thread/loaded/list` | `thread.Service.ListByStatus(ctx, "running")` | 是 | `internal/module/thread/contract.go:21` |
| `thread/read` | `thread.Service.Get(ctx, id)` | 部分 | 仅有 `Get(ctx, id) (*Ref, error)`，见 `internal/module/thread/contract.go:16`；`Ref` 只有 `ID/Name`，见 `internal/module/thread/contract.go:60-63` |
| `thread/resolve` | `Resolve(...)` | 否 | `internal/module/thread/contract.go:9-26` 无该方法 |
| `thread/config/get` | `ConfigGet(...)` 或等价方法 | 否 | `internal/module/thread/contract.go:9-26` 无该方法 |
| `thread/config/set` | `ConfigSet(...)` 或等价方法 | 否 | `internal/module/thread/contract.go:9-26` 无该方法；`SendCommand` 不是结构化 config API |
| `thread/messages` | `thread.Service.ReadMessages(ctx, threadID, limit, before)` | 是 | `internal/module/thread/contract.go:18` |
| `thread/backgroundTerminals/clean` | 独立 facade 或显式 service 方法 | 否 | `SendCommand` 不支持 `/clean`，见 `internal/module/thread/command.go:18-35` |
| `thread/undo` | `Rollback(...)` 或兼容 facade | 否 | `SendCommand` 不支持 `/undo`，见 `internal/module/thread/command.go:18-35` |
| `thread/model/set` | `thread.Service.SendCommand(ctx, threadID, "/model", args)` | 是 | `internal/module/thread/contract.go:23` + `internal/module/thread/command.go:19-24,60-61` |
| `thread/personality/set` | `thread.Service.SendCommand(ctx, threadID, "/personality", args)` | 是 | `internal/module/thread/contract.go:23` + `internal/module/thread/command.go:19-24,62-63` |
| `thread/approvals/set` | `thread.Service.SendCommand(ctx, threadID, "/approvals", args)` | 是 | `internal/module/thread/contract.go:23` + `internal/module/thread/command.go:19-24,64-65` |
| `thread/mcp/list` | 独立 facade 或显式 service 方法 | 否 | `SendCommand` 不支持 `/mcp`，见 `internal/module/thread/command.go:18-35` |
| `thread/skills/list` | 独立 facade 或显式 service 方法 | 否 | `internal/module/thread/contract.go:9-26` 无该方法 |
| `thread/debugMemory` | debug/ops facade | 否 | `internal/module/thread/contract.go:9-26` 无该方法 |
| `approval/respond` | `contract.ApprovalResponder.Respond(callID, requestID, decision)` | 是 | `internal/contract/approval.go:5-7` |
| `review/start` | `review.Service` 或 `turn.Service` 中显式 review 方法 | 否 | `internal/module/turn/contract.go:12-19` 无 review 方法 |
| `turn/start` | `turn.Service.PrepareTurn(...) + StartTurn(...)` | 是 | `internal/module/turn/contract.go:13-14` |
| `turn/steer` | `turn.Service.SteerTurn(...)` | 是 | `internal/module/turn/contract.go:15` |
| `turn/interrupt` | `turn.Service.InterruptTurn(...)` | 是 | `internal/module/turn/contract.go:16` |
| `turn/forceComplete` | `turn.Service.ForceCompleteTurn(...)` | 是 | `internal/module/turn/contract.go:17` |

### 小结

- 题面点名的 5 项里，当前代码树的状态是：
  - `thread/start -> Start(ctx, StartRequest)`：存在
  - `thread/loaded/list -> ListByStatus("running")`：存在
  - `turn/steer -> SteerTurn`：存在
  - `turn/forceComplete -> ForceCompleteTurn`：存在
  - `approval/respond -> contract.ApprovalResponder.Respond`：存在
- 但 `thread/read` 仍只有 `Get(*Ref)` 的弱版本；`review/start`、`thread/config/*`、`thread/rollback`、`thread/resolve`、`thread/debugMemory` 以及若干 compat/debug/provider-specific 路由仍未闭环。

## 3. handler 工厂

- `internal/platform/rpc/handler.go:75-77` 存在 `ThreadHandler[Req, Resp any]`：

```go
func ThreadHandler[Req, Resp any](fn func(context.Context, Req) (Resp, error)) handler.Func
```

- `internal/platform/rpc/handler.go:80-82` 存在 `CapabilityThreadHandler[Req, Resp any]`：

```go
func CapabilityThreadHandler[Req, Resp any](cap string, resolver CapabilityResolver, fn func(context.Context, Req) (Resp, error)) handler.Func
```

- `StrictHandler[Req, Resp any]` 存在，但不在 `handler.go`，而在 `internal/platform/rpc/strict.go:11-17`：

```go
func StrictHandler[Req, Resp any](fn func(context.Context, Req) (Resp, error)) handler.Func
```

- `RawHandler` 存在，也不在 `handler.go`，而在 `internal/platform/rpc/strict.go:20-22`：

```go
func RawHandler(fn func(context.Context, *jrpc2.Request) (any, error)) handler.Func
```

### 结论

- handler 工厂基础件齐全。
- 需要补一条文档级说明：`StrictHandler` / `RawHandler` 在 `strict.go`，不是 `handler.go`。

## 4. HandlerMapResult

- `internal/platform/rpc/module.go:30-34` 已定义：

```go
type HandlerMapResult struct {
	fx.Out
	Handlers handler.Map `group:"rpc_handlers"`
}
```

- `internal/platform/rpc/module.go:36-45` 已存在收集与注册链路：
  - `serverParams` 通过 `[]handler.Map \`group:"rpc_handlers"\`` 注入
  - `registerAllHandlers` 调用 `server.Register(p.Handlers...)`
- 这说明 `HandlerMapResult` 的输出机制本身已闭环。

### 当前仓库内是否已有范例

- 以 LSP 搜索 `HandlerMapResult`、`group:"rpc_handlers"`、`fx.ResultTags(\`group:"rpc_handlers"\`)` 的结果看，当前树内没有其他模块实际产出 `HandlerMapResult` 或带该 group tag 的 `handler.Map`。
- 现状是：
  - 收集口已存在
  - 消费口已存在
  - 生产方范例还没有

### 结论

- `HandlerMapResult` 机制可用。
- 当前没有现成生产范例；R1/R2 会成为第一批生产方。

## 5. session 获取路径

- `turn.Service` 的关键方法都要求调用方传入 `contract.Session`：
  - `PrepareTurn(ctx, session, input)`，见 `internal/module/turn/contract.go:13`
  - `StartTurn(ctx, session, req)`，见 `internal/module/turn/contract.go:14`
  - `SteerTurn(ctx, session, prompt)`，见 `internal/module/turn/contract.go:15`
  - `InterruptTurn(ctx, session, source)`，见 `internal/module/turn/contract.go:16`
  - `ForceCompleteTurn(ctx, session)`，见 `internal/module/turn/contract.go:17`
- `turn.Module` 当前只 `fx.Provide(NewService)`，见 `internal/module/turn/module.go:5-7`；没有任何 session resolver 注入。

### 当前代码树里唯一可行的 session 解析链

1. `rpc.ThreadScope` 从请求参数抽出 `threadId` 并写入上下文，见 `internal/platform/rpc/handler.go:29-54`。
2. 现有代码里，真正能把 `threadID` 解析到 session 的逻辑在 `thread.service.resolveSession(ctx, threadID)`，见 `internal/module/thread/service.go:212-225`。
3. `resolveSession` 的前置步骤是 `resolveBinding(ctx, threadID)`，见 `internal/module/thread/service.go:182-210`。它按以下顺序把 `threadID` 解析到 `binding.AgentID`：
   - `bindingStore.GetByAgentID(ctx, threadID)`
   - `threadAgents` 内存索引 `threadID -> agentID`
   - `bindingStore.GetByProviderThread(ctx, "codex", threadID)`
   - `bindingStore.GetByProviderThread(ctx, "claude", threadID)`
4. 取到 `binding.AgentID` 后，再通过 `SessionProvider.GetSession(agentID)` 取 session，见：
   - `internal/module/thread/service.go:22-24`
   - `internal/provider/unified/session_adapter.go:14-16`
   - `internal/provider/unified/session.go:48-57`

### 对题面问题的直接回答

- `通过 thread.SessionProvider.GetSession(agentID)？`
  - 可以，但那只是最后一步。
  - 前面必须先做 `threadID -> binding.AgentID` 的解析。
- `rpc handler 只有 threadID，不一定有 agentID`
  - 正确。
  - 仅凭 `turn.Service` 无法完成这一步。
- `需要什么中间步骤？`
  - 需要一个显式的 binding/session resolver，把 `threadID` 解析成 `agentID`，再取 session。

### 当前 blocker

- `resolveSession` 是 `thread` 模块私有方法，不在 `thread.Service` 接口里。
- `turn/rpc.go` 若只注入 `turn.Service`，拿不到这个能力。
- 因此，`turn/start` / `turn/interrupt` / `turn/steer` / `turn/forceComplete` 目前都无法仅靠 `turn.Service` 落地。
- 另外，当前 `resolveSession` 只查“已存在的活动 session”；如果 `GetSession(agentID)` 失败，它不会自动 `Recover` 或 `Resume`。这意味着 turn RPC 还需要定义“线程未加载时”的恢复策略。

### 最小可行补法

- 方案 A：新增窄接口，例如：

```go
type SessionResolver interface {
	ResolveSession(ctx context.Context, threadID string) (contract.Session, error)
}
```

- 由 `module/thread` 提供该接口实现，内部直接复用现有 `resolveBinding + GetSession` 链。
- `module/turn/rpc.go` 注入 `turn.Service + SessionResolver + contract.ApprovalResponder`，这是最小闭环。
- 方案 B：让 `module/turn/rpc.go` 直接注入 `binding.Store + SessionProvider`。
  - 技术上可行。
  - 但会把线程绑定解析逻辑泄漏到 transport 层，边界更差。

## 结论（Blocker / Improvement）

### Blocker

- `31` 方法口径不能完全覆盖 `registerThreadTurnMethods`；缺 `thread/realtime/*` 四个路由，严格口径下还缺 `mock/experimentalMethod`。
- `31` 个方法中，当前只有 `19` 个明确闭环，`1` 个部分闭环，`11` 个未闭环。
- 未闭环的核心缺口集中在：
  - `thread/read`
  - `thread/compact/start`
  - `thread/rollback`
  - `thread/resolve`
  - `thread/config/get`
  - `thread/config/set`
  - `thread/backgroundTerminals/clean`
  - `thread/undo`
  - `thread/mcp/list`
  - `thread/skills/list`
  - `thread/debugMemory`
  - `review/start`
- `module/turn/rpc.go` 若只注入 `turn.Service`，无法从 `threadID` 解出 `contract.Session`；必须补 `threadID -> agentID -> session` 的 resolver。
- 当前仓库里没有任何现成的 `rpc_handlers` 生产方范例；R1/R2 若采用 `HandlerMapResult`，需要自己成为首个实现样板。

### Improvement

- `StrictHandler` / `RawHandler` 实际在 `internal/platform/rpc/strict.go`；相关任务书若只指向 `handler.go`，应补齐文件定位。
- `thread/loaded/list -> ListByStatus("running")` 在接口层可行，但仍是通用状态过滤，不是专用的 loaded-list 门面。
- `turn/forceComplete` 虽然接口已存在，但实现仍是 `session.Interrupt(... Source: "force_complete")`，底层 `contract.Session` 没有独立的 force-complete API；语义仍依赖 provider 对 interrupt source 的解释。

## 互辩：对 audit-B 的批判

### 1. 关于“不要做通用 commandHandler”

- B 对“不要做覆盖 11 个方法的通用 `commandHandler`”这个结论，按当前代码树看基本成立，但它把可抽象面说得过窄了。
- 先看当前 V2 路由形态，实际上不是一个统一命令族：
  - `thread/config/get`、`thread/config/set` 是结构化 provider API，见 `go-agent-v2/internal/apiserver/methods_thread.go:353-358`
  - `thread/rollback` 有 `turnIndex -> numTurns` 兼容归一化，见 `go-agent-v2/internal/apiserver/methods_thread.go:280-293`
  - `thread/compact/start`、`thread/backgroundTerminals/clean` 走 `SendSlashCommandFromRawParamsRequireThreadID`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:33-35,56-58`
  - `thread/undo`、`thread/mcp/list` 走 `SendSlashCommandFromRawParams`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:95-109`
  - `thread/model/set`、`thread/personality/set`、`thread/approvals/set` 走 `SendSlashCommandWithArgs`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:98-105`
  - `thread/skills/list` 是零参数 `ThreadSkillsList()`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:110`
- 所以，`config/get config/set rollback clean undo mcp skills` 这组方法今天并不构成一个 transport 层可统一的单一 `SendCommand` 面。B 在这一点上没有低估当前实现。
- 但 B 把“适合抽象的只有 3 个方法”说得过头了。若后续 `thread.Service.SendCommand` 扩展到 `/compact`、`/clean`、`/undo`、`/mcp`，则至少会形成一个更大的“slash-compatible handler family”：
  - `thread/compact/start`
  - `thread/backgroundTerminals/clean`
  - `thread/undo`
  - `thread/model/set`
  - `thread/personality/set`
  - `thread/approvals/set`
  - `thread/mcp/list`
- 但即便如此，也仍不应把 `thread/config/get`、`thread/config/set`、`thread/rollback`、`thread/skills/list` 硬并进同一个 `commandHandler`。
- 另外，在当前可核实的迁移文档里，也没有证据表明 `config/get config/set rollback skills` 被定义为 `SendCommand` 面；相反，`v3-module-migration-details.md` 明确把：
  - `thread/config/get`、`thread/config/set` 放到 `module/thread/config.go`，见 `docs/plans/迁移/v3-module-migration-details.md:63-64`
  - `thread/rollback` 放到 `module/thread/service.go + archive.go`，见 `docs/plans/迁移/v3-module-migration-details.md:59`
  - `thread/skills/list` 下沉到 `module/skill/service.go` facade，见 `docs/plans/迁移/v3-module-migration-details.md:76`
- 互辩结论：
  - B 正确否定了“11 路通吃”的 `commandHandler`
  - 但把未来可抽象面缩到“只有 3 路”偏保守；更准确的说法应是“现在只闭环 3 路，将来可能扩到更大的 slash-family，但仍不会覆盖全部 11 路”

### 2. 关于“796 -> 600 不能靠去重达成”

- B 的总判断大体对，但论证口径过于绝对。
- 按题面要求，仅看 `go-agent-v2/internal/apiserver/methods_thread.go` 中 3 个典型方法，并把 `typedHandler` 注册行计作样板，按“非空、非花括号、非注释”的粗粒度行数统计：

| 方法 | 业务逻辑行 | 样板行 | 样板占比 |
| --- | --- | --- | --- |
| `thread/start` | 约 `27`（`methods_thread.go:45-75`） | `1`（`methods_thread_turn.go:9`） | 约 `3.6%` |
| `thread/resume` | 约 `4`（`methods_thread.go:245-249`） | `1`（`methods_thread_turn.go:10`） | 约 `20%` |
| `thread/config/get` | `1`（`methods_thread.go:354`） | `1`（`methods_thread_turn.go:49`） | `50%` |

- 这说明两件事：
  - B 把“`typedHandler` 本身几乎不减少业务代码”说得太满了。对 `thread/config/get` 这种 thin pass-through，样板已经到 `50%`。
  - 但 B 的总量判断仍然站得住。因为一旦看 `thread/start` 这种大头路由，样板占比立刻降到很低，单靠去重拿不到 `196` 行净减。
- 更进一步，如果把视野扩到 `methods_thread_turn.go` 的内联 pass-through route，样板占比还会更高：
  - `thread/read` 的 wrapper 在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:39-43`
  - `thread/messages` 的 wrapper 在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:51-55`
  - 这类路由的确是“glue 明显多于业务”
- 但这些高样板占比路由并不是 `796` 行基线的主要体积来源。主要体积仍然在：
  - `thread/start` 及其 helper 集群，见 `go-agent-v2/internal/apiserver/methods_thread.go:44-218`
  - `review/start` 及其 helper 集群，见 `go-agent-v2/internal/apiserver/methods_turn.go:122-186`
- 互辩结论：
  - B 对“仅靠工厂化不够”这个总结是对的
  - 但它低估了 thin pass-through 路由上样板去重的收益，原文应该改成“对总体预算不够，但对局部薄路由可达 50%+”

### 3. 关于“参数 struct 放 rpc_types.go”

- B 的方向基本正确，但判据说得不够精确。
- 当前代码已经证明 service DTO 和 transport DTO 不是一回事：
  - `thread.StartRequest` / `ResumeRequest` 是 service-facing DTO，无 JSON tag，见 `internal/module/thread/contract.go:28-45`
  - `turn.PrepareInput` 是模块编排 DTO，也不等价于 V2 `turnStartParams`，见 `internal/module/turn/contract.go:23-37`
- 所以，把 V2 兼容的 JSON 请求结构直接塞进 `contract.go`，确实会把 transport 兼容层污染到模块服务面。B 这一点成立。
- 但 B 若把“带 JSON tag 的 struct 应该放到 `rpc_types.go`”理解成硬规则，就过头了。现有代码树里已经存在带 JSON tag 的非 transport-only 类型：
  - `internal/module/turn/contract.go:39-43` 的 `TurnStatus`
  - `internal/dto/provider/turn.go:9-49` 的 `TurnRequest`、`TurnOverrides`、`TurnResult`、`InterruptRequest`、`ForkRequest`、`ForkResult`
- 因而真正的边界不是“有没有 JSON tag”，而是“这个类型表达的是模块服务契约，还是 V2/V3 transport 兼容形状”。
- 更准确的分层应该是：
  - transport-only、带 V2 兼容字段名/字段别名/宽松参数面的类型：放 `rpc_types.go`
  - 真正的服务级 canonical DTO：放 `contract.go` 或共享 `dto/*`
- 这的确会形成“两层参数类型”，但这不是设计缺陷，而是兼容层与服务层分离的代价。真正应该避免的是“机械复制两套完全相同的 DTO”。
- 互辩结论：
  - B 对“不要把 V2 transport params 全塞进 contract”是对的
  - 但它把问题表述成“文件放哪儿”略显表面；更根本的是“canonical DTO 和 compat transport DTO 是否混层”

### 4. 关于“review/start 骨架应该返回 error”

- B 正确抓到了一个关键事实：V2 `review/start` 的成功契约不是空 map。
  - schema contract 要求返回 `reviewThreadId` 和 `turn`，见 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:451-455`
  - golden 响应也固定是 success shape，见 `go-agent-v2/internal/guards/golden/rpc_response/review_start.golden.json:1-11`
- 因此，假成功返回确实会破坏现有 success contract。B 在这点上是对的。
- 但 B 把“显式错误：正确”说成兼容结论，证据并不充分：
  - side-effect golden 对 `review/start` 显式允许 RPC error，见 `go-agent-v2/internal/guards/golden/side_effect_trace_misc_test.go:23-27,85-94`
  - 这说明测试体系至少容忍某些路径报错
  - 但它并不能证明“前端/客户端长期收到 `review/start` error 也完全兼容”
- 反过来，现有前端通用 RPC 层在收到错误时会直接抛出异常，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:174-229`。
  - 这说明“不会 crash”并没有被代码证明
  - 只是当前仓库里我没有搜到前端直接调用 `review/start` 的代码点，因此也不能证明“一定会 crash”
- 所以更严谨的结论应是：
  - `review/start` 骨架返回假成功：不兼容成功契约
  - `review/start` 骨架返回显式错误：语义上更诚实，但客户端兼容性未被证明
  - 若要稳妥，Wave 1 要么不注册骨架，要么先补完整 caller 侧错误处理验证
- 互辩结论：
  - B 对“假成功不行”是对的
  - B 对“显式错误就是正确做法”说得过满；更准确应是“显式错误比假成功更诚实，但兼容性仍需验证”

### 5. 关于“是否遗漏 thread/start 不应套 ThreadScope”

- 这一条对 B 的指控不成立。B 没有遗漏，反而明确写了。
- `audit-B` 原文在 `docs/plans/迁移/p5-wave1-task-audit-B.md:98-103` 已明确指出：
  - `thread/start` 没有 `threadId`
  - `thread/list` 没有 `threadId`
  - `thread/loaded/list` 没有 `threadId`
  - `approval/respond` 不是 thread-scoped
- V2 schema 也验证了这一点：
  - `thread/start` 参数键不含 `threadId`，见 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:361-371`
  - `thread/loaded/list` 参数只有 `cursor` / `limit`，见 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:415-417`
  - `thread/skills/list` 甚至是无 params，见 `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:346-348`
- 真要挑剔，只能说 B 这一段还可以再补上：
  - `thread/skills/list`
  - `thread/debugMemory`
- 但它已经抓住了最关键的 `thread/start` 争议点，不能算遗漏。
- 互辩结论：
  - 这一条批评 B 不成立
  - B 在 ThreadScope 误用风险上说得是对的，只是枚举还可以更完整

### 互辩总评

- B 最稳的判断有两条：
  - 不能把 `ThreadHandler`/`ThreadScope` 套到所有 `thread/*`
  - 不能把今天并不统一的 `11` 个方法硬塞进一个通用 `commandHandler`
- B 最值得收紧的判断有三条：
  - `commandHandler` 的未来抽象面不应被过早锁死在 `3` 路；更准确的是“当前只闭环 3 路，未来可能扩到更大的 slash-family”
  - “工厂化收益很小”说得太绝对；对 thin pass-through route，样板占比确实可到 `50%+`
  - `review/start` 骨架“应返回 error”在语义上比假成功更好，但兼容性并未被当前代码证明
