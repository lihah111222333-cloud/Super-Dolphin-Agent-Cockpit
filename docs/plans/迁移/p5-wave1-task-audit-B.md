# P5 波次 1 任务审查 B

## 1. 代码量

- V2 基线 `796` 行成立：
  - `go-agent-v2/internal/apiserver/methods_thread_turn.go:1-113`
  - `go-agent-v2/internal/apiserver/methods_thread.go:1-359`
  - `go-agent-v2/internal/apiserver/methods_turn.go:1-260`
  - `go-agent-v2/internal/apiserver/methods_thread_helpers.go:1-64`
- `R1 + R2 <= 600` 意味着要从这组基线里净减 `196` 行，压缩率约 `24.6%`。

- 仅靠 transport glue 去重，不足以稳定拿到 `196` 行：
  - `withRequiredThreadID(...)` 在这组面里只有 `11` 个直接调用点，全部位于 `go-agent-v2/internal/apiserver/methods_thread_turn.go:14,29,40,45,52,63,68,74,79,84,89`
  - `withRequiredThreadID` 本体只有 `7` 行：`go-agent-v2/internal/apiserver/methods.go:175-181`
  - 这部分即使全部被 `ThreadScope` 吃掉，回收量也更接近 `45-55` 行，不是 `80+`
  - `typedHandler` 到 `StrictHandler` 本身几乎不减少业务代码；变化主要是注册点样板，`typedHandler` 定义也只有 `11` 行：`go-agent-v2/internal/apiserver/server_transport.go:88-98`
  - `capabilityGuard` 到 `CapabilityGate` 同理，收益主要在注册点，守卫本体只有 `11` 行：`go-agent-v2/internal/apiserver/methods.go:109-119`

- 真正的压缩来源必须是 helper-heavy 逻辑下沉：
  - `thread/start` handler 本体 `33` 行：`go-agent-v2/internal/apiserver/methods_thread.go:44-76`
  - 其直接 helper 集群又占 `142` 行：`go-agent-v2/internal/apiserver/methods_thread.go:77-218`
  - 额外 thread-start 配置 helper 文件还有 `64` 行：`go-agent-v2/internal/apiserver/methods_thread_helpers.go:1-64`
  - `review/start` handler 本体只有 `14` 行：`go-agent-v2/internal/apiserver/methods_turn.go:173-186`
  - 但它依赖 `buildReviewStartArgs` 和 `normalizeReviewStartResponse` 共 `49` 行：`go-agent-v2/internal/apiserver/methods_turn.go:122-170`

- 因此，`25%` 压缩率不是“工厂化自动可得”，而是“工厂化 + helper 下沉”联合结果。

- 仍会保留在 handler 里的 V2 兼容转换逻辑，不会完全消失：
  - `thread/rollback` 仍要做 `turnIndex -> numTurns` 兼容归一化：`go-agent-v2/internal/apiserver/methods_thread.go:280-293`
  - `thread/list` 若保留 V2 `archived` 过滤面，仍要保留一个薄请求适配层，或把过滤直接下沉 service：`go-agent-v2/internal/apiserver/methods_thread.go:309-324`
  - `turn/start` / `turn/steer` 仍要把 `[]UserInput` 映射成 provider 输入：`go-agent-v2/internal/apiserver/methods_turn.go:44-66,74-82`
  - `review/start` 若维持 V2 契约，仍要保留 target 归一化和 success shape 归一化，除非单独做 compat facade：`go-agent-v2/internal/apiserver/methods_turn.go:122-186`

- 判断：
  - 若任务书把压缩来源主要归因于 `withRequiredThreadID`、`StrictHandler`、`CapabilityGate`，则结论过于乐观。
  - 若任务书前提是 `thread/start`、`review/start` 及其 helper 集群先下沉到 service/facade，则 `<= 600` 可以成立。

## 2. 参数类型

- V2 `methods_thread.go` 里并没有“每个 handler 一套 params struct”：
  - 命名 `*Params` 一共 `10` 个：`threadStartParams`、`threadIDParams`、`threadForkParams`、`threadResumeParams`、`threadNameSetParams`、`threadRollbackParams`、`threadMessagesParams`、`threadLoadedListParams`、`threadConfigGetParams`、`threadConfigSetParams`
  - 位置：`go-agent-v2/internal/apiserver/methods_thread.go:16,220,224,237,269,274,295,326,343,347`
  - 另有 `thread/list` 的匿名过滤 struct，仅 `3` 行：`go-agent-v2/internal/apiserver/methods_thread.go:310-312`
- V2 `methods_turn.go` 也主要靠复用，不是“每方法一套”：
  - 命名 `*Params` `9` 个：`go-agent-v2/internal/apiserver/methods_turn.go:24,30,68,84,91,97,102,116,188`
  - alias `2` 个：`go-agent-v2/internal/apiserver/methods_turn.go:89,107`
  - 嵌套辅助类型 `reviewTarget` `1` 个：`go-agent-v2/internal/apiserver/methods_turn.go:109`

- 所以，“25 个方法 -> 50 个 request/response struct”这个担心不成立。V2 已证明：
  - 多个 handler 可以共享一个 request type，例如 `threadIDParams`
  - response 也不需要每方法单独 struct；V2 在这组代码里只显式定义了 `threadInfo` 和 `turnInfo` 两个轻量 response helper：`go-agent-v2/internal/apiserver/methods_thread.go:29-33`、`go-agent-v2/internal/apiserver/methods_turn.go:39-42`

- V3 类型放置建议应拆两层：
  - service-facing DTO 放 `internal/module/thread/contract.go`、`internal/module/turn/contract.go`
  - RPC transport DTO 放 `module/thread/rpc_types.go`、`module/turn/rpc_types.go`

- 这样拆的原因是当前 contract 已经证明 service DTO 和公开 RPC params 不是一回事：
  - `thread.StartRequest` / `ResumeRequest` 已存在，但字段是 service 视角，无 JSON tag，也不等价于 V2 `threadStartParams` / `threadResumeParams`：`internal/module/thread/contract.go:28-45`
  - `turn.PrepareInput` 也已存在，但它是模块编排输入，不等价于 V2 `turnStartParams`：`internal/module/turn/contract.go:23-37`

- 因此不建议把 V2 兼容的 JSON-tagged request struct 全塞进 `contract.go`：
  - `contract.go` 应继续承载模块公开服务接口和服务级 DTO
  - `rpc.go` 不应堆 20+ 个 transport struct
  - 最合适的是 `rpc.go + rpc_types.go`

- 建议的 V3 transport 复用方式：
  - `threadIDParams` -> 共享 `ThreadIDRequest`
  - `threadMessagesParams` -> `ThreadMessagesRequest`
  - `threadRollbackParams` -> `ThreadRollbackRequest`
  - `threadLoadedListParams` -> `ThreadLoadedListRequest`
  - `turnStartParams` / `turnSteerParams` 共用一层 `TurnInputRequest`
  - `reviewStartParams` + `reviewTarget` 保持在 turn/review transport 层，不要污染 thread contract

- 估算：
  - R1 若按 transport 复用设计，request type 更接近 `12-16` 个，不是 `25-50` 个
  - R2 更接近 `5-7` 个
  - response type 不必每方法单独定义；`StrictHandler[Req, Resp]` 并不强迫引入新 response struct

## 3. 工厂重复

- 当前 V3 已有可用工厂/基件：
  - `StrictHandler`：`internal/platform/rpc/strict.go:11-17`
  - `RawHandler`：`internal/platform/rpc/strict.go:20-22`
  - `ThreadHandler`：`internal/platform/rpc/handler.go:75-77`
  - `CapabilityThreadHandler`：`internal/platform/rpc/handler.go:80-82`

- 这套分层已经覆盖了 Wave 1 里最主要的重复面：
  - typed + no scope -> `StrictHandler`
  - typed + thread scope -> `ThreadHandler`
  - typed + thread scope + capability -> `CapabilityThreadHandler`
  - truly raw request -> `RawHandler`

- 不建议再导出第四种 `SimpleHandler[Req, Resp]`：
  - 若它的实现只是 `return StrictHandler(fn)`，那只是别名，不增加语义
  - no-thread-scope typed handler 数量不会像 thread-scoped 路由那么密集
  - 新增一个 exported factory 会增加框架表面积，但几乎不减少代码量

- 真正需要注意的是 `ThreadHandler` 不能被误用到所有 `thread/*`：
  - `thread/start` 没有 `threadId`
  - `thread/list` 没有 `threadId`
  - `thread/loaded/list` 没有 `threadId`
  - `approval/respond` 也不是 thread-scoped
  - 这些都应该直接用 `StrictHandler`

- 判断：
  - 维持现在的 `StrictHandler + ThreadHandler + CapabilityThreadHandler + RawHandler` 足够
  - 若真要加新抽象，优先级也应低于 `command` 类 helper，不是 `SimpleHandler`

## 4. SendCommand 消除

- 当前 `thread.Service.SendCommand` 的支持面很窄，不支撑“11 个方法统一工厂”这个前提：
  - 只支持 `/model`、`/personality`、`/approvals`、`/interrupt`：`internal/module/thread/command.go:18-35`
  - command patch 也只覆盖这三类配置命令：`internal/module/thread/command.go:54-69`
  - 没有 `/clean`、`/compact`、`/undo`、`/mcp`

- 因此不应设计一个面向“所有 slash 兼容路由”的通用 `commandHandler`：
  - 这会把当前并不存在的统一性提前固化进 transport 层
  - 也会掩盖 `SendCommand` 真实支持面不足的问题

- 适合抽象的只有当前确实同构的 3 个方法：
  - `thread/model/set`
  - `thread/personality/set`
  - `thread/approvals/set`

- 这 3 个方法可以共享“thread-scoped + optional capability + svc.SendCommand(cmd, arg)”的 handler body，但 request type 仍然不会合并成一个：
  - `thread/model/set` 暴露字段是 `model`
  - `thread/personality/set` 暴露字段是 `personality`
  - `thread/approvals/set` 暴露字段是 `policy`

- 所以 `commandHandler` 真正能消掉的是 handler body，不是 transport type 数量。

- 判断：
  - 不要做覆盖 11 个方法的通用 `commandHandler`
  - 可以做一个局部 helper，仅服务这 3 个配置兼容路由
  - `/interrupt` 是否也走这条路，应由 turn/thread 模块边界决定，不应仅因为“代码像”就强行复用

## 5. review 骨架

- V2 `reviewStartTyped` 的签名是：
  - `func (s *Server) reviewStartTyped(_ context.Context, p reviewStartParams) (any, error)`
  - 位置：`go-agent-v2/internal/apiserver/methods_turn.go:173-186`

- 它的 success contract 不是空 map，而是规范化后的结构：
  - `normalizeReviewStartResponse` 固定返回 `turn` 和 `reviewThreadId`：`go-agent-v2/internal/apiserver/methods_turn.go:146-170`

- 因此骨架若返回：
  - 空成功：错误，会让客户端把未实现当已成功
  - `{"status":"not_implemented"}` 成功：也不合适，会破坏既有 success shape
  - 显式错误：正确

- 当前 V3 `internal/platform/rpc/errors.go:5-31` 还没有 `CodeNotImplemented` / `ErrNotImplemented`。

- 判断：
  - `review/start` 骨架应返回显式 not-implemented error
  - 最好先补一个专用错误码，例如 `CodeNotImplemented`
  - 若暂时不愿扩错误码，就不应在 Wave 1 注册一个“假成功”的 `review/start`

## 结论（Blocker / Improvement）

### Blocker

- `796 -> 600` 不能建立在“工厂化本身可以省出 196 行”这个假设上。真正的大头必须来自 `thread/start` 和 `review/start` helper 集群下沉。
- “25 个方法会带来 50 个 struct”这一估算过高；但若不显式区分 transport DTO 与 service DTO，`rpc.go` 或 `contract.go` 会迅速膨胀。
- 当前 `thread.Service.SendCommand` 支持面不足，不能支撑一个覆盖大量路由的通用 `commandHandler`。
- `review/start` 若只给空成功或自造 success status，会破坏 V2 已有 success contract。

### Improvement

- 在 `module/thread`、`module/turn` 新增 `rpc_types.go`，专放 JSON-tagged transport DTO。
- 继续把 `contract.go` 保持为 service-facing DTO 和模块接口，不混入 V2 兼容 transport shape。
- 工厂层维持 `StrictHandler`、`ThreadHandler`、`CapabilityThreadHandler`、`RawHandler` 四件套即可，不要再导出 `SimpleHandler`。
- 只为真实同构的 3 个配置兼容路由引入小型 `command` helper，不要过度抽象成“11 路通吃”。
- 在注册 `review/start` 骨架前，先补 `ErrNotImplemented` / `CodeNotImplemented`。

## 附录：对 audit-A 的互辩

### 1. 关于“11 个方法未闭环”与 `SendCommand`

- A 在这一点上不算根本性误判，但表述偏硬。
- 当前 `thread.Service.SendCommand` 真实只支持 `/model`、`/personality`、`/approvals`、`/interrupt`，见 `internal/module/thread/command.go:12-41`。
- 所以这些方法不能因为“任务书里出现了 `SendCommand`”就算已闭环：
  - `thread/config/get`
  - `thread/config/set`
  - `thread/rollback`
  - `thread/backgroundTerminals/clean`
  - `thread/undo`
  - `thread/mcp/list`
  - `thread/skills/list`
- 规划文档本身也没有把它们统统映射到 `SendCommand`：
  - `thread/config/set` 在迁移细节里被定义为“结构化 config 写入”，不是 slash command wrapper，见 `docs/plans/迁移/v3-module-migration-details.md:63-74`
  - `thread/backgroundTerminals/clean`、`thread/undo` 被定义为 provider session-control facade，下沉而非并入 `thread.Service.SendCommand`，见 `docs/plans/迁移/v3-module-migration-details.md:66,71`
  - `thread/mcp/list`、`thread/skills/list` 也分别下沉到 provider/tool facade 与 skill facade，见 `docs/plans/迁移/v3-module-migration-details.md:75-76`
- 但 A 的问题在于把“当前 `thread.Service` 不能直接承接”写成了近似“不可做”。
- 更精确的说法应是：
  - 对 `thread/model/set`、`thread/personality/set`、`thread/approvals/set`，当前已可由 `SendCommand` 直接闭环
  - 对其余 compat/provider-specific 路由，当前不是 `thread.Service.SendCommand` 问题域，应归为“需 facade 或 skeleton”，不应一概当成硬 blocker

### 2. 关于 `turn/rpc.go` 的 session resolver

- 用户对“A 没给方案”的指控不成立。A 实际给了最小补法：
  - 新增窄接口 `SessionResolver.ResolveSession(ctx, threadID)`，见 `docs/plans/迁移/p5-wave1-task-audit-A.md:191-205`
- 但 A 少讲了一条决定性约束：
  - 选项“`turn/rpc.go` 注入 `thread.Service.Get(threadID)` 先拿 agentID”在当前代码树里不可行
  - 因为 `thread.Service.Get` 返回的是 `*Ref`，而 `Ref` 只有 `ID/Name`，没有 `AgentID`，见 `internal/module/thread/contract.go:15-16,60-63`
- 当前 `turn.Service` 的签名已经把 `contract.Session` 设为调用方责任：
  - `PrepareTurn(ctx, session, input)`，见 `internal/module/turn/contract.go:13`
  - `StartTurn(ctx, session, req)`，见 `internal/module/turn/contract.go:14`
  - `SteerTurn` / `InterruptTurn` / `ForceCompleteTurn` 同样如此，见 `internal/module/turn/contract.go:15-17`
- 这意味着：
  - 若不改 `turn.Service` contract，resolver 必须在 `rpc.go` 外围或独立 facade 中提供
  - 若要把 resolver 放进 `turn.Service` 内部，就不是接线问题，而是 contract 改造
- 结合现有实现，最干净的最小方案仍是窄接口：
  - `thread.service.resolveSession(ctx, threadID)` 已有完整链路：`resolveBinding -> SessionProvider.GetSession(agentID)`，见 `internal/module/thread/service.go:182-225`
  - `SessionProvider.GetSession(agentID)` 已有窄适配器，见 `internal/provider/unified/session_adapter.go:5-16`
- 所以在“不改 `turn.Service` 签名”的前提下，最干净方案不是 `thread.Service.Get`，而是：
  - `turn/rpc.go` 注入一个 `SessionByThread` / `SessionResolver` 窄接口
  - 由 `thread` 模块或 unified provider 层实现

### 3. 关于 `19 / 1 / 11` 的数字

- 用户给出的 `31 - (15 + 6) = 10` 这个反驳不成立。
- 原因是 `Service` 方法数与 RPC 方法数不是 `1:1`：
  - `SendCommand` 一项覆盖了至少 `3` 个 RPC：`thread/model/set`、`thread/personality/set`、`thread/approvals/set`
  - `PrepareTurn + StartTurn` 两个 service 调用共同闭环 `turn/start`
  - `ListByStatus("running")` 被 A 视为 `thread/loaded/list` 的一个可用落点
- 从 A 自己的表格逐行求和，`19 + 1 + 11 = 31`，算术本身没有错，见 `docs/plans/迁移/p5-wave1-task-audit-A.md:45-77`
- 但 A 的数字也不是铁板一块，因为它混入了“语义近似可用”的判断：
  - `thread/loaded/list` 被算作 `是`，证据是 `ListByStatus("running")`，见 `docs/plans/迁移/p5-wave1-task-audit-A.md:58`
  - 这其实偏乐观，因为当前 `ListByStatus` 并不提供 V2 `threadLoadedList` 的 `cursor/limit` 面，`loaded` 与 `running` 也不是严格同义
- 更严谨的写法应是：
  - A 没有算错
  - 但 `19 / 1 / 11` 属于“当前语义口径下的分类结果”，不是由接口数量直接推出的硬事实

### 4. 关于 realtime 4 方法是否应排除

- A 在这里结论过强。
- 从 V2 代码看，`thread/realtime/*` 四个方法明确注册在 `registerThreadTurnMethods` 内，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:72-92`
- 从执行计划看，Wave 1 的 `module/thread/rpc.go` 方法表也明确包含这 4 个方法，见 `docs/plans/迁移/p5-execution-plan.md:88-90`
- 但迁移细节同时说明：
  - 这 4 个方法“下沉”到 `provider/unified` realtime 子 facade
  - `phase 1` 不作为 `thread` 模块主 contract，见 `docs/plans/迁移/v3-module-migration-details.md:67-70`
- 所以正确表述应拆开两层：
  - 它们不应成为 `thread.Service` 主 contract 的扩张理由
  - 但它们仍属于 Wave 1 的 RPC surface，至少要有 compat wrapper、provider facade 路由或显式 skeleton
- A 直接把它们从 Wave 1 清单里裁掉，只对“thread 主 service contract”口径成立；对“Wave 1 RPC surface”口径不成立

### 5. A 是否过于悲观

- 结论不是“完全悲观”或“完全不悲观”，而是悲观与乐观都不均匀。
- A 抓住的真正硬 blocker 主要只有一类：
  - 在当前 `turn.Service` 签名下，`threadID -> session` resolver 必须被显式补出来
- 其余不少项更像“当前实现空缺”或“scope/facade 决策”，不应全部提升为 blocker：
  - `thread/backgroundTerminals/clean`
  - `thread/undo`
  - `thread/mcp/list`
  - `thread/skills/list`
  - `thread/realtime/*`
  - `thread/debugMemory`
- 这几项里有些本来就被规划为下沉到 provider/skill/debug facade，不是 `thread.Service` 自身缺方法
- 另外，“当前仓库没有 `rpc_handlers` 生产方范例”也不应算 blocker：
  - 收集口和注册口已存在，见 `docs/plans/迁移/p5-wave1-task-audit-A.md:122-147`
  - 这只意味着 R1/R2 会成为首个生产样板，不意味着方案不可落地
- 反过来看，A 也并非处处悲观：
  - 它把 `thread/loaded/list` 记为 `是`
  - 它把 `approval/respond` 记为 `是`
  - 这些都带有“接口层可接”而非“V2 语义完全等价”的乐观假设
- 因此更准确的总体评价是：
  - A 最有价值的判断是 session resolver 这个结构性问题
  - A 最薄弱的地方是把很多 facade/skeleton/TODO 事项与真正 blocker 混写
  - A 对 realtime 的排除口径也过强，应改成“退出 thread 主 contract，但不退出 Wave 1 RPC surface”

### 互辩结论

- A 对 `SendCommand` 的判断大体方向正确：除 `model/personality/approvals` 外，不能把一批 provider-specific 路由直接算成已闭环。
- A 对 session resolver 的问题抓得最准，但“没给方案”这个指控不成立；它其实已经给了窄接口方案。
- A 的 `19 / 1 / 11` 不是算错，而是分类口径需要写得更明确。
- A 对 realtime 的排除不够严谨；它们不应扩进 `thread.Service` 主 contract，但仍应保留在 Wave 1 RPC surface 的 skeleton/wrapper 范围内。
- A 的 blocker 口径偏重，应把“真正结构性阻断”和“可 defer 的 compat/facade 缺口”分层。
