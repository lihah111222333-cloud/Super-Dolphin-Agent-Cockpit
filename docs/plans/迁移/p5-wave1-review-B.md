# P5 波次 1 审查 B

## 1. 代码量

- 先看方法数口径。V2 `go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113` 中共有 29 个 `thread/*` 路由。
- `~25` 这个数字只有在把 `thread/realtime/*` 4 个路由整体排除出 R1 时才成立。`29 - 4 = 25`。
- 因此，R1 `module/thread/rpc.go <= 350` 的前提不是“25 个方法都很短”，而是“先裁掉 realtime 类 provider-specific 路由”。

- `25` 个方法压到 `350` 行，平均每方法约 `14` 行。这个均值只在 `rpc.go` 退化成薄包装层时才成立。
- V2 反例很明确：
  - `threadStartTyped` 自身是 `33` 行：`go-agent-v2/internal/apiserver/methods_thread.go:44-76`
  - 同文件里支撑 `thread/start` 的 helper 还包括 `allocateFreshThreadID`、`resolveThreadStartConfig`、`threadStartSandbox`、`threadStartPref` 等，累计又占去约 `142` 行：`go-agent-v2/internal/apiserver/methods_thread.go:77-218`
  - `review/start` 虽然 handler 只有 `14` 行，但它依赖 `buildReviewStartArgs` 和 `normalizeReviewStartResponse` 两个 helper，再加约 `49` 行：`go-agent-v2/internal/apiserver/methods_turn.go:122-186`
- 结论：`14` 行/方法不是可靠指标。真正决定体积的是 lifecycle/config/review 逻辑是否已经下沉到 Service 或兼容层。

- 对 `796 -> 600` 的压缩率判断：
  - V2 基线为 `113 + 359 + 260 + 64 = 796` 行，这个算术成立。
  - 单靠去掉 `withRequiredThreadID` 不够。LSP 命中 `11` 个直接调用点，全部在 `methods_thread_turn.go`：`go-agent-v2/internal/apiserver/methods_thread_turn.go:14,29,40,45,52,63,68,74,79,84,89`
  - 每个调用点大致可回收 `4` 行重复 transport glue，合计约 `44` 行；再加 `withRequiredThreadID` 本体 `7` 行：`go-agent-v2/internal/apiserver/methods.go:175-181`
  - 保守估计，消除 `withRequiredThreadID` 只能省 `50` 行左右。
  - 再叠加 `typedHandler` / `capabilityGuard` 的工厂化，额外还能省一部分注册样板，但总收益仍更接近 `70-90` 行，不是 `196` 行。
- 结论：`R1 + R2 <= 600` 只有在下面两个条件同时成立时才可信：
  - R1 缩到“核心 thread 面”，不要把 `thread/realtime/*`、`thread/debugMemory` 之类兼容/诊断路由塞进 `module/thread/rpc.go`
  - `thread/start`、`thread/resume`、`thread/recover`、`thread/config/*`、`review/start` 这类 helper-heavy 逻辑先下沉

- 对 R2：
  - `module/turn/rpc.go <= 250`、`~6` 个方法，平均约 `41` 行/方法。
  - 这个目标比 R1 宽松得多。
  - 但它依赖现有 `turn.Service` 真能承接 `steer` / `forceComplete` / `review`。当前并不能。

## 2. thread.Service 差距

现有 `internal/module/thread/contract.go:9-21` 只有 11 个方法：

- `List`
- `Get`
- `ReadHistory`
- `ReadMessages`
- `Archive`
- `Unarchive`
- `ListByStatus`
- `ListByCWD`
- `SendCommand`
- `SetName`
- `Delete`

### 2.1 R1 核心 thread RPC 对照

| RPC 方法 | 需要的 Service 方法/门面 | 现有？ |
|---|---|---|
| `thread/start` | `Start(ctx, StartInput) (StartResult, error)` | 否 |
| `thread/resume` | `Resume(ctx, ResumeInput) (ResumeResult, error)` | 否 |
| `thread/recover` | `Recover(ctx, threadID) (RecoverResult, error)` | 否 |
| `thread/fork` | `Fork(ctx, threadID) (ForkResult, error)` | 否 |
| `thread/list` | `List(ctx)` 或 `List(ctx, ListFilter)` | 部分 |
| `thread/loaded/list` | `ListLoaded(ctx, cursor, limit)` 或 `ListRunning(ctx)` | 否 |
| `thread/archive` | `Archive(ctx, threadID)` | 是，语义不全 |
| `thread/unarchive` | `Unarchive(ctx, threadID)` | 是，语义不全 |
| `thread/delete` | `Delete(ctx, threadID)` | 是，语义不全 |
| `thread/name/set` | `SetName(ctx, threadID, name)` | 是，语义不全 |
| `thread/read` | `Read(ctx, threadID)` 或 `ReadHistory(ctx, ...)` | 部分 |
| `thread/resolve` | `Resolve(ctx, threadID) (ResolveResult, error)` | 否 |
| `thread/config/get` | `GetConfig(ctx, threadID)` | 否 |
| `thread/config/set` | `SetConfig(ctx, threadID, patch)` | 否 |
| `thread/messages` | `ReadMessages(ctx, threadID, limit, before)` | 部分 |
| `thread/rollback` | `Rollback(ctx, threadID, numTurns)` | 否 |

### 2.2 逐项说明

- `thread/start`
  - 当前 `thread.Service` 的构造函数只注入 `threadStore`、`bindingStore`、`SessionProvider`：`internal/module/thread/service.go:34-49`
  - 但启动线程需要 driver/unified client、启动配置解析、fresh thread id、binding 注册等能力。V2 `ThreadStart` 明确是生命周期编排，不是薄 RPC：`go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:40-81`
  - 结论：必须新增 `Start` 级接口，同时扩容依赖。

- `thread/resume`
  - V2 `ThreadResume` 依赖 provider-thread candidate 解析和 resume request 发送：`go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:83-98`
  - 现有 `thread.Service` 无对应能力。

- `thread/recover`
  - V2 `ThreadRecover` 会在“复用现存进程恢复”和“重新拉起”两条路径间分支：`go-agent-v2/internal/apiserver/codexadapter/thread_recover.go:163-195`
  - 这是生命周期服务，不应残留在 rpc handler。

- `thread/fork`
  - `contract.Session` 已有 `ForkThread`：`internal/contract/provider.go:30-37`
  - 但 `thread.Service` 没有暴露 `Fork`。现状只能通过内部 `resolveSession` 自己拼，contract 仍然缺。

- `thread/list`
  - `List` 存在，但当前返回 `[]Ref{ID, Name}`：`internal/module/thread/contract.go:23-26`
  - V2 `thread/list` 还支持 `archived` 过滤，且底层结果有 `Archived` 字段：`go-agent-v2/internal/apiserver/methods_thread.go:301-324`、`internal/dto/provider/thread.go:3-7`
  - 现有接口缺过滤语义，也缺 `Archived` 字段。

- `thread/loaded/list`
  - store 层已有 `ListRunning` / `ListRunningAgents`：`internal/store/thread/contract.go:11-13`
  - 但 `thread.Service` 没有公开对应方法，也没有 cursor/limit 门面。

- `thread/archive`
  - `Archive` 存在：`internal/module/thread/archive.go:5-13`
  - 但 V2 还会走 archive service、清 diff 状态、停 inline manager：`go-agent-v2/internal/apiserver/codexadapter/adapter_thread_listing.go:358-373`、`go-agent-v2/internal/apiserver/methods_thread_turn.go:13-25`
  - 现有实现只是“改状态 + 标 archived + close session”，不能视为同等语义。

- `thread/unarchive`
  - `Unarchive` 存在：`internal/module/thread/archive.go:15-20`
  - 但 V2 还会在进程不活跃时触发 `EnsureProcessAlive`：`go-agent-v2/internal/apiserver/methods.go:183-202`
  - 现有实现缺恢复语义。

- `thread/delete`
  - `Delete` 存在：`internal/module/thread/service.go:91-106`
  - 但 V2 还会删 archive 目录、解绑、清偏好，并返回 `{ok, threadId}`：`go-agent-v2/internal/apiserver/methods.go:204-227`
  - 现有实现只能算部分。

- `thread/name/set`
  - `SetName` 存在：`internal/module/thread/service.go:81-89`
  - 但 V2 `ThreadNameSet` 还涉及运行时 rename、历史校验、别名持久化：`go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:124-130`
  - 现有实现只是改 store prompt。

- `thread/read`
  - 现有只有 `ReadHistory`：`internal/module/thread/history.go:13-20`
  - V3 内部 `thread/read` 目前只是 codex history fallback transport：`internal/provider/codexapp/history.go:19-39`
  - 可以作为近似复用，但不是完整 facade。

- `thread/resolve`
  - 完全缺失。
  - V2 `thread/resolve` 需要运行态、providerThreadID、history 存在性等聚合结果：`go-agent-v2/pkg/agentsdk/service/lifecycle/thread_lifecycle_logic.go:253-290`

- `thread/config/get`
  - 完全缺失。
  - V2 需要解析 launch config：`go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:47-59`

- `thread/config/set`
  - 完全缺失同名方法。
  - 现有 `SendCommand` 只能覆盖 `/model`、`/personality`、`/approvals`：`internal/module/thread/command.go:12-41`
  - V2 `thread/config/set` 参数还有 `effort`：`go-agent-v2/internal/apiserver/methods_thread.go:347-358`
  - 因此 `SendCommand` 只能算兼容层局部能力，不能替代 `SetConfig`。

- `thread/messages`
  - `ReadMessages` 存在：`internal/module/thread/history.go:22-45`
  - 但 V2 `ThreadMessages` 会补运行时 timeline/diff：`go-agent-v2/internal/apiserver/codexadapter/thread_messages.go:77-103,150-189`
  - 当前返回 `[]dto.Message`，与 V2 的前端 payload 不是同一个层级。

- `thread/rollback`
  - 完全缺失。
  - V2 本质是 `/undo` 语义包装：`go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:109-113`

### 2.3 不应驱动 thread.Service 扩容的兼容路由

下列 route 不是 `thread.Service` 主面，应该进 provider facade、compat 层或 debug 模块，而不是继续把 `thread.Service` 扩成巨型接口：

| RPC 方法 | 现有可复用能力 | 结论 |
|---|---|---|
| `thread/model/set` | `SendCommand("/model")` | 可复用 |
| `thread/personality/set` | `SendCommand("/personality")` | 可复用 |
| `thread/approvals/set` | `SendCommand("/approvals")` | 可复用 |
| `thread/compact/start` | 无 | 不应进 `thread.Service` 主面 |
| `thread/backgroundTerminals/clean` | 无 | 不应进 `thread.Service` 主面 |
| `thread/undo` | 无同名接口 | 更适合并入 `Rollback` 或 compat slash 层 |
| `thread/mcp/list` | 无 | 不应进 `thread.Service` 主面 |
| `thread/skills/list` | 无 | 不应进 `thread.Service` 主面 |
| `thread/realtime/start` | 无 | 不应进 `thread.Service` 主面 |
| `thread/realtime/appendAudio` | 无 | 不应进 `thread.Service` 主面 |
| `thread/realtime/appendText` | 无 | 不应进 `thread.Service` 主面 |
| `thread/realtime/stop` | 无 | 不应进 `thread.Service` 主面 |
| `thread/debugMemory` | 无 | 应独立到 debug/ops 路由 |

## 3. turn.Service 差距

现有 `internal/module/turn/contract.go:12-17` 只有：

- `PrepareTurn`
- `StartTurn`
- `InterruptTurn`
- `TrackTurn`

### 3.1 R2 对照

| RPC 方法 | 需要的 Service 方法/门面 | 现有？ |
|---|---|---|
| `turn/start` | `PrepareTurn` + `StartTurn` | 是 |
| `turn/steer` | `SteerTurn(ctx, session, SteerInput)` 或 `PrepareSteer + SteerTurn` | 否 |
| `turn/interrupt` | `InterruptTurn` | 是 |
| `turn/forceComplete` | `ForceCompleteTurn` | 否 |
| `review/start` | `ReviewStart` 或独立 `review.Service` | 否 |

### 3.2 逐项说明

- `turn/start`
  - 当前 `turn.Service` 已具备两段式能力。
  - `PrepareTurn` 负责输入组装、skills、manifest、override：`internal/module/turn/service.go:37-59`
  - `StartTurn` 负责 tracker、启动 handle、绑定 providerID、watch turn：`internal/module/turn/service.go:61-92`
  - 结论：`turn/start` 本身不是 blocker，R2 handler 只要做 orchestration glue。

- `turn/steer`
  - V2 `turnSteerTyped` 只是把 `ThreadID`、`ExpectedTurnID`、`Input`、`SelectedSkills`、`ManualSkillSelection` 交给 `providerAdapter.TurnSteerFromInputAligned`：`go-agent-v2/internal/apiserver/methods_turn.go:74-82`
  - 真正的对齐逻辑在 runtime/service：`go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:413-418`
  - 当前 `PrepareTurn` 没有 `ExpectedTurnID` 入口，也没有 steer 对齐能力。
  - 结论：需要新增 `SteerTurn` 级接口；仅靠 `PrepareTurn` 不够。

- `turn/forceComplete`
  - 当前 `turn.Service` 没有方法。
  - 更严重的是 `contract.Session` 也没有 turn-level force-complete，只提供 `ForceStop()`：`internal/contract/provider.go:23-37`
  - `ForceStop()` 在两个 provider 中都是“停整个 session/进程”，不是“完成当前 turn”：
    - `internal/provider/codexapp/session.go:201-205`
    - `internal/provider/claudecli/session.go:262-264`
  - 结论：这是 contract 级 blocker，不是单纯 `turn.Service` 少一个方法。

- `review/start`
  - V2 `reviewStartTyped` 实际是 `/review` 指令包装：`go-agent-v2/internal/apiserver/methods_turn.go:173-186`
  - 参数归一化和 response 归一化 helper 又占去 `49` 行：`go-agent-v2/internal/apiserver/methods_turn.go:122-171`
  - 结论：如果 `review/start` 进 R2，不建议挂到 `turn.Service`；更合理的是独立 `review.Service` 或 compat facade。

- `turn/interrupt`
  - 当前已有 `InterruptTurn`，而且比 V2 更接近领域语义：`internal/module/turn/service.go:94-118`
  - 这一项不是缺口。

## 4. 工厂模式

- 会有大量重复。
- V2 注册点已经给出数量级：
  - `typedHandler(...)` 命中 `23` 次：`go-agent-v2/internal/apiserver/methods_thread_turn.go`
  - `withRequiredThreadID(...)` 命中 `11` 次：`go-agent-v2/internal/apiserver/methods_thread_turn.go`
  - `capabilityGuard(...)` 命中 `8` 次：`go-agent-v2/internal/apiserver/methods_thread_turn.go`
- 如果 Wave 1 把这些 thread/turn 路由重写到 V3，而不做工厂，`ThreadScope + StrictHandler + CapabilityGate` 的重复会非常明显。

- 需要工厂，但不应只有一个万能工厂。
- 现有 V3 RPC 框架可支持小型工厂族：
  - `rpc.Wrap`：`internal/platform/rpc/handler.go:19-27`
  - `rpc.ThreadScope`：`internal/platform/rpc/handler.go:30-54`
  - `rpc.CapabilityGate`：`internal/platform/rpc/handler.go:56-72`
  - `rpc.Validate`：`internal/platform/rpc/handler.go:96-100`
  - `rpc.StrictHandler`：`internal/platform/rpc/strict.go:10-17`

- 建议至少有 3 类工厂：
  - `threadHandler[Req, Resp]`
    - 适用于 `thread/archive`、`thread/read`、`thread/messages` 这类“typed + thread scope”的路由
  - `threadCapHandler[Req, Resp]`
    - 适用于 `thread/compact/start`、`thread/model/set`、`thread/realtime/*` 这类“typed/raw + capability gate”的路由
  - `rawHandler` / `rawThreadHandler`
    - 适用于 `thread/list`、`thread/loaded/list`、`thread/mcp/list`、slash command proxy 这类不适合直接映射为 `func(ctx, Req) (Resp, error)` 的路由

- 因此，对“是否需要一个 `threadHandler[Req, Resp]` 工厂”的判断是：
  - 需要
  - 但它只能覆盖一部分 typed thread-scoped 路由
  - 不能指望一个工厂吃掉所有 `thread/*`

- 额外注意：
  - 当前 `rpc.Validate()` 还是 no-op：`internal/platform/rpc/handler.go:96-100`
  - 所以工厂的真实收益主要来自 `StrictHandler + ThreadScope (+ CapabilityGate)` 去重，不是“自动业务校验”

## 5. V2 复杂度抽查

### 5.1 严格按文件内函数行数统计

`methods_thread.go` + `methods_turn.go` 中 Top 3 为：

| 函数 | 行数 | 判断 |
|---|---:|---|
| `debugRuntime` | 35 | 不属于 thread/turn 领域，不能下沉到这两个 Service |
| `threadStartTyped` | 33 | 应下沉核心逻辑到 `thread.Service` |
| `resolveThreadStartConfig` | 33 | 应下沉到 `thread.Service` 或其配置解析依赖 |

对应位置：

- `go-agent-v2/internal/apiserver/methods_turn.go:200-234`
- `go-agent-v2/internal/apiserver/methods_thread.go:44-76`
- `go-agent-v2/internal/apiserver/methods_thread.go:107-139`

### 5.2 仅看直接 RPC handler

如果只看直接 handler，Top 3/Top 4 更接近 Wave 1 关注面：

| 函数 | 行数 | 判断 |
|---|---:|---|
| `threadStartTyped` | 33 | 核心逻辑应下沉 |
| `turnStartTyped` | 19 | 可保留少量 orchestration glue 在 rpc |
| `reviewStartTyped` | 14 | 不建议塞进 `turn.Service` |
| `threadRollbackTyped` | 14 | 参数归一化可留 rpc，真正执行应下沉 |

### 5.3 结论

- `threadStartTyped`
  - 不应继续留在 rpc handler。
  - handler 里最多保留 request/response 适配。
  - fresh ID、默认值决议、偏好读取、provider 启动、binding 注册，都应下沉。
  - 因此 `thread.Service` 至少要新增：
    - `Start(ctx, StartInput) (StartResult, error)`
    - 以及内部可复用的 config resolver

- `resolveThreadStartConfig`
  - 本质是业务策略，不是 transport glue。
  - 如果还放在 `rpc.go`，R1 的 350 行目标基本失真。

- `turnStartTyped`
  - 当前已经很接近理想形态。
  - 在 V3 中它应保留为薄 orchestration：调用 `PrepareTurn`，再调用 `StartTurn`，最后做 response shape。
  - 不需要把这层也硬塞回 Service。

- `reviewStartTyped`
  - 不建议扩进 `turn.Service`。
  - 更合理的是独立 review/compat facade。
  - 否则 `turn.Service` 会开始吸收 slash-command 兼容逻辑。

- `debugRuntime`
  - 不应成为 thread/turn 模块设计的依据。
  - 应单独归到 debug/ops 路由。

## 6. noop

- V2 noop 注册点是集中的，不在 thread/turn 领域内部：
  - `initialized`
  - `fuzzyFileSearch/sessionStart`
  - `fuzzyFileSearch/sessionUpdate`
  - `fuzzyFileSearch/sessionStop`
  - `feedback/upload`
  - 位置：`go-agent-v2/internal/apiserver/methods.go:157-166`

- `mock/experimentalMethod` 不是集中 noop，而是在 thread-turn 注册点单独用 `stubHandler(map[string]any{})` 挂进去：
  - `go-agent-v2/internal/apiserver/methods_thread_turn.go:112`

- 对 Wave 1 的建议：
  - 不要在 `module/thread/rpc.go` 或 `module/turn/rpc.go` 里注册这些 noop
  - 统一放到 Wave 4 的 compat/noop 层，或者 core registry
  - 原因是这些方法不提供 thread/turn 领域价值，只会污染模块边界和方法计数

- 如果为了前端兼容必须尽早注册：
  - 可以有一个统一 `noop` 工厂
  - 但位置应在 core/compat registry，不应计入 R1/R2 的 thread/turn 方法数

## 7. 结论（Blocker / Improvement）

### Blocker

- `R1 ~25` 的方法数只有在排除 `thread/realtime/*` 时才成立；如果把全部 `thread/*` 都塞进来，真实数量是 `29`
- 当前 `thread.Service` 不足以承接 R1。缺少至少：
  - `Start`
  - `Resume`
  - `Recover`
  - `Fork`
  - `Rollback`
  - `Resolve`
  - `GetConfig`
  - `SetConfig`
  - `ListLoaded`
- 当前 `thread.Service` 的 `Archive` / `Unarchive` / `Delete` / `SetName` / `List` / `ReadHistory` / `ReadMessages` 也只是部分覆盖，不是 V2 同等语义
- 当前 `turn.Service` 不足以承接完整 R2。缺少：
  - `SteerTurn`
  - `ForceCompleteTurn`
  - `ReviewStart` 或独立 `review.Service`
- `turn/forceComplete` 还是 contract 级 blocker，因为 `contract.Session` 没有 turn-level force-complete
- 单靠 `withRequiredThreadID` 消除和工厂化，无法把 `796` 稳定压到 `600`

### Improvement

- R1 先缩面：
  - 把 `thread/realtime/*`、`thread/debugMemory`、`thread/mcp/list`、`thread/skills/list`、`thread/backgroundTerminals/clean` 移出 `module/thread/rpc.go`
- `thread/model/set`、`thread/personality/set`、`thread/approvals/set` 这类 slash-compat 路由优先复用 `SendCommand`，不要驱动 `thread.Service` 继续膨胀
- `turn/start` 保持薄 orchestration，利用现有 `PrepareTurn + StartTurn`
- 引入小型工厂族，而不是一个万能 handler 工厂
- noop 统一后移到 Wave 4 compat/noop 层，不计入 R1/R2 代码量与方法数
