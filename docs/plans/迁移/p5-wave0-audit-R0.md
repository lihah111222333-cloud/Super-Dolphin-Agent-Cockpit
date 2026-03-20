# P5 波次 0 复审：整组 R0

## 审查方法

- 代码读取全部使用 LSP，范围包括：
  - `internal/platform/rpc/{server.go,module.go,errors.go,strict.go,handler.go,codec.go,push.go,transport_ws.go,approval.go,approval_support.go}`
  - `internal/app/modules.go`
  - `internal/module/{turn,orchestration}/*`
  - `internal/provider/{unified,claudecli,codexapp}/*`
  - `internal/dto/agent/state.go`
  - V2 对照与迁移计划文档
- 构建/守卫验证：
  - `go build ./...` 通过
  - `go test ./internal/archtest/... -count=1 -timeout 120s` 通过
  - 临时加入 `fx.ValidateApp(Module)` 测试后执行 `go test ./internal/app -run TestTmpValidateApp -count=1`，通过；测试文件已删除
- 行数验证：
  - `wc -l` 结果：
    - `server.go=73`
    - `approval.go=320`
    - `approval_support.go=255`
    - `strict.go=22`
    - `codec.go=22`
    - `transport_ws.go=105`
    - `push.go=63`
    - `module.go=44`
    - `handler.go=106`
    - `internal/app/modules.go=39`

## Findings

### High

1. `R0c` 仍未形成真正的“approval 状态机”闭环，缺少运行态状态迁移接口。

- `internal/dto/agent/state.go:28-29,90-93` 已定义：
  - `TriggerUserInputRequested`
  - `TriggerUserInputResolved`
  - `turn_running -> awaiting_user_input -> turn_running`
- 但全仓 LSP 搜索 `TriggerUserInputRequested` / `TriggerUserInputResolved`，除 `internal/dto/agent/state.go` 定义处外无任何使用点。
- `internal/platform/rpc/approval.go:74-152` 的 `ApprovalManager` 只编排：
  - pending 注册
  - callback 下发
  - wait / cleanup / restore
  - respond
- `ApprovalManager` 的外部依赖只有 `*PushBridge` 和 `*jrpc2.Server`，没有任何 `UserInputStatePort` 或等价状态恢复端口。
- 结果：
  - 当前代码能表示“有一个 approval pending manager”
  - 但不能证明“approval resolved 之后会把 agent 从 `awaiting_user_input` 拉回 `turn_running`”
- 对 `R0c: approval 状态机` 这个任务名来说，这个缺口是实质性的。

2. `R0c` 的公共回调契约仍有断层：当前实现按 `callId` 管理 pending，但迁移计划与 V2 对外入口仍是 `requestId/approved/decision`。

- `internal/platform/rpc/approval.go:19-23` 的 pending 表定义为 `map[string]*pendingApproval`，key 是 `callID`。
- `internal/platform/rpc/approval.go:108-119` 的 `Respond` 只接受 `callID string`。
- `internal/platform/rpc/approval_support.go:43-63` 会在缺省时用 `RequestID` 回填 `CallID`，但这是内部归一化，不是公开 API。
- 迁移计划仍明确：
  - `docs/plans/迁移/p5-execution-plan.md:93-94`：`approval/respond` 归 `module/turn/rpc.go`，状态迁移归 `platform/rpc/approval.go`
  - `docs/plans/迁移/p5-wave0-review-B.md:271`：当前 `approval/respond` 参数是 `requestId/approved/decision`
- V2 公开方法也仍是 `approval/respond`：
  - `go-agent-v2/internal/apiserver/methods.go:157-166`
  - `go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:227`
- 结果：
  - 现在的 `ApprovalManager` 内部可运行前提是调用方已经拿到 `callId`
  - 但后续 `approval/respond` 若按现有迁移计划继续走 `requestId` 入口，就还需要额外的 requestId->pending 查找层

3. R0 的自定义错误码族仍整体违反 jrpc2 契约保留区间，且 `R0c` 已开始主动使用这些错误。

- `internal/platform/rpc/errors.go:5-10` 定义：
  - `CodeNotFound=-31001`
  - `CodeInvalidState=-31002`
  - `CodeConflict=-31003`
  - `CodeCapabilityGate=-31004`
  - `CodeApprovalTimeout=-31005`
- 契约文档 `docs/契约/jrpc2-convention.md:588` 明确要求业务错误码“必须避开保留区间 `-32768` 到 `-32000`”。
- 当前这些错误已不是“未使用常量”：
  - `internal/platform/rpc/approval.go:111,115` 使用 `ErrInvalidState` / `ErrNotFound`
  - `internal/platform/rpc/approval.go:137` 使用 `ErrApprovalTimeout`
  - `internal/platform/rpc/approval_support.go:138,145` 使用 `ErrInvalidState`
  - `internal/platform/rpc/handler.go:65-67` 使用 `CodeCapabilityGate`
- 结果：
  - 这不是单点 `CapabilityGate` 问题，而是整个 R0 基础设施错误码策略还未对齐契约。

### Medium

4. `R0a/R0c/R0d` 的新组件大多仍是“定义存在、运行时未消费”的状态。

- `internal/platform/rpc/push.go`
  - `NotifyClient` / `CallbackClient` 已实现
  - `BindEventToNotify` 全仓仅命中定义处
- `internal/platform/rpc/transport_ws.go`
  - `WSHandler` 全仓仅命中定义处
  - `internal/platform/rpc/server.go:37-58` 主运行路径仍是 `server.Loop(..., channel.Line, ...)`
- `internal/platform/rpc/codec.go`
  - `PayloadEncoder` 全仓仅命中定义处
- `internal/platform/rpc/handler.go` 与 `strict.go`
  - `StrictHandler` / `RawHandler` / `Wrap` / `ThreadScope` / `CapabilityGate` / `Logging` / `Validate` 全仓均只命中定义处
- `internal/platform/rpc/approval.go`
  - `ApprovalManager` 全仓仅命中定义与 module provider
- 结果：
  - 这些组件现在证明了“基础设施骨架已写出”
  - 但还不能证明“R0 基础设施已被公开 RPC 链路或业务模块消费”

5. `R0c/R0d` 已明显超出原计划预算。

- 迁移计划预算：
  - `docs/plans/迁移/p5-execution-plan.md:52-55`
  - `R0a <= 500`
  - `R0b <= 100`
  - `R0c <= 250`
  - `R0d <= 80`
- 当前体量：
  - `R0a` 相关最小集合：`server.go + codec.go + transport_ws.go + push.go = 263`，在预算内
  - `R0b` 相关：`module.go + internal/app/modules.go = 83`，在预算内
  - `R0c` 相关：`approval.go + approval_support.go = 575`，超预算
  - `R0d` 相关：`strict.go + handler.go = 128`，超预算
- 结果：
  - 如果仍按 `p5-execution-plan.md` 的预算口径验收，当前 `R0c` 与 `R0d` 不能算预算达标

6. `internal/platform/rpc` 仍无单测，新增的 approval/transport/middleware 逻辑目前只由构建成功证明“可编译”。

- `rg --files internal/platform/rpc -g '*_test.go'` 无结果。
- 当前能证明的只有：
  - `go build ./...` 通过
  - `go test ./internal/archtest/...` 通过
  - `fx.ValidateApp(app.Module)` 通过
- 结果：
  - approval 的并发、restore、timeout、重复请求、callback decode 等路径都还没有仓内自动化证据。

### Low

7. `ThreadScope(fields ...string)` 仍保留固定错误文案，和多 field 能力不完全一致。

- `internal/platform/rpc/handler.go:30-54` 支持自定义字段列表。
- 失败路径 `handler.go:51` 固定返回 `"threadId is required"`。
- 若调用方未来传入非 `threadId` 字段名，错误提示会与真实接受字段不一致。

## 分项复审

### R0a: `platform/rpc` 契约合规 + `codec` + `transport_ws` + `push`

结论：部分通过。

已完成：

- `internal/platform/rpc/server.go:60-73` 强制 `AllowPush=true`
- `internal/platform/rpc/push.go:25-47` 同时提供 `NotifyClient` 与 `CallbackClient`
- `internal/platform/rpc/transport_ws.go:22-105` 提供最小 `channel.Channel` 级别 WS 适配
- `internal/platform/rpc/codec.go:1-22` 保持薄封装，没有复制 V2 `server_payload.go`
- 体量 `263` 行，在 `<=500` 预算内

未闭环：

- `WSHandler` 无使用点
- `BindEventToNotify` 无使用点
- `PayloadEncoder` 无使用点
- `internal/platform/rpc/server.go:37-58` 仍只跑 TCP `channel.Line`
- 全仓无 `OnNotify` / `OnCallback` 命中，仓内看不到 callback-capable client 侧实现

判断：

- 作为“基础设施组件已写出”，R0a 成立
- 作为“push/WS 已形成可验证运行链”，证据仍不足

### R0b: fx 图闭环

结论：通过，但只是依赖图闭环，不是 RPC 路由闭环。

证据：

- `internal/app/modules.go:20-35` 已接入：
  - `rpc.Module`
  - `thread.Module`
  - `turn.Module`
  - `orchestration.Module`
  - `unified.Module`
  - `claudecli.Module`
  - `codexapp.Module`
- `internal/platform/rpc/module.go:12-19,28-44` 已提供：
  - `NewServer`
  - `NewPushBridge`
  - `NewApprovalManager`
  - `handler.Map` value-group 收集
  - `registerAllHandlers` 注入
- `go test ./internal/app -run TestTmpValidateApp -count=1` 验证 `fx.ValidateApp(app.Module)` 通过
- `module.go + modules.go = 83` 行，在 `<=100` 预算内

仍未闭环的部分：

- 全仓 `group:"rpc_handlers"` 只命中 `internal/platform/rpc/module.go`
- 说明当前没有任何业务模块向该 group 真正出值
- 因而 `Server.Register(...)` 的链路虽已成立，但仍是空注册

判断：

- 如果 `R0b` 的目标是“app 级依赖图闭环”，当前通过
- 如果目标是“RPC 注册链已有真实 producer”，当前仍是骨架态

### R0c: approval 状态机

结论：部分通过。

已完成：

- `internal/platform/rpc/approval.go:18-320`
  - `ApprovalManager`
  - pending 注册/查询/快照
  - cleanup / restore
  - callback dispatch
  - respond / auto-approve
- `internal/platform/rpc/approval_support.go:21-255`
  - request normalize
  - callback params
  - result decode
  - requested/resolved 事件发布
- 并发面：
  - `ApprovalManager.mu` 保护 `pending`
  - `pending.once` 防重入 finish
  - `nextRequestID atomic.Int64`

未完成或未证实：

- 没有任何使用点；当前只是 provider-injected singleton
- 没有 user-input 状态迁移端口
- 公开回调契约仍与计划中的 `approval/respond(requestId, approved, decision)` 不对齐
- 错误码族仍用保留区间
- 无单测
- 体量 `575` 行，明显超出 `<=250`

判断：

- `R0c` 现在更像“approval awaiter/manager 基础组件”
- 若按“状态机已完成”验收，当前证据不足

### R0d: `ThreadScope` 多 field + `StrictBind` 兼容

结论：部分通过。

已完成：

- `internal/platform/rpc/strict.go:10-22`
  - `StrictHandler`
  - `RawHandler`
- `internal/platform/rpc/handler.go:14-106`
  - `Middleware`
  - `Wrap`
  - `ThreadScope(fields ...string)`
  - `CapabilityGate`
  - `Logging`
  - `Validate`
- `ThreadScope` 默认支持：
  - `threadId`
  - `threadID`
  - `thread_id`

未完成或未证实：

- 所有工厂/中间件全仓无使用点
- `CapabilityGate` 仍走保留区间错误码
- 自定义字段模式的错误文案仍固定为 `threadId is required`
- 体量 `128` 行，超出 `<=80`

判断：

- 组件能力已具备
- 但兼容策略还没有被任何真实 handler 路径验证

## 最终结论

整组 R0 的当前状态不是“未做”，而是“基础设施组件大多已经进树，但只有 R0b 的 app 级闭环可以明确判定通过；R0a/R0c/R0d 仍停留在组件完成、多处未接入运行链的状态”。

可明确通过的部分：

- 构建通过
- 架构守卫通过
- `app.Module` 的 fx 图闭环通过
- `R0a` 与 `R0b` 的行数预算仍成立

仍阻塞“整组 R0 已完成”结论的关键点：

- `R0c` 没有 user-input 状态恢复链
- `R0c` 的 pending/resolve API 仍与 `approval/respond` 的 `requestId` 契约存在断层
- R0 错误码族整体仍落在 jrpc2 保留区间
- `R0a/R0c/R0d` 的大部分新增组件尚无调用点或测试证据
- `R0c`、`R0d` 都已超出原计划预算

结论：

- 若按“R0 的基础组件已经铺出”验收：可以给到“基本完成，但未闭环”
- 若按“R0 已可作为后续波次稳定底座”验收：当前不能直接判定为完成

## 互辩：对 audit-A 的批判

### 1. A 的 "`R0c` 不通过" 判定是否过严

结论：过严，但不是完全没有依据。

- `docs/plans/迁移/p5-execution-plan.md:51-55` 对波次 0 的定义是“前置基础设施”，其中 `R0c` 只写成 `approval 状态机（≤250）`。
- 同一文档 `:57-59,92-94` 又明确把 `module/turn/rpc.go` 放到波次 1 的 `R2`，且 `approval/respond` 归 `module/turn/rpc.go`，`platform/rpc/approval.go` 只承接等待/确认状态迁移。
- 这意味着：对 `R0c` 的最低验收口径，应先看 approval infra 是否成立，而不是要求波次 0 就已经出现公开 RPC 接入点。
- A 在 [p5-wave0-audit-A.md](/Volumes/bot/super-agent-v3/docs/plans/迁移/p5-wave0-audit-A.md#L76) 把“没有调用点、没有 route、没有 provider/orchestration 端口闭环”直接上升为 `R0c 不通过`，这里把“基础设施建设”与“后续波次集成”混在了一起。
- 但 A 也不是完全错：
  - `internal/dto/agent/state.go:28-29,90-93` 的 `TriggerUserInputRequested/Resolved` 仍无使用点；
  - `ApprovalManager` 也没有显式状态恢复端口。
- 更准确的判定应该是：
  - “按波次 0 基础设施口径：`R0c` 至少应是部分通过，不能因为没有 `module/turn/rpc.go` 调用点就直接不通过。”
  - “按整轮 P5 端到端口径：`R0c` 当然还未闭环。”

### 2. A 的注册链批判是否合理

结论：事实正确，但用于否定 `R0b` 完成度时过界了。

- A 在 [p5-wave0-audit-A.md](/Volumes/bot/super-agent-v3/docs/plans/迁移/p5-wave0-audit-A.md#L53) 把“151 个方法完成注册”作为 `R0` 未完成的高优先级 finding。
- 但任务拆分明确写的是：
  - 波次 1：`module/thread/rpc.go`、`module/turn/rpc.go`
  - 波次 2：`module/skill/rpc.go`、`module/workspace/rpc.go`、`module/orchestration/rpc.go`
  - 波次 3：`module/uistate/rpc.go`、`module/dashboard/rpc.go`
  - 波次 4：`R8: 151 方法注册完整性测试`
  - 见 `docs/plans/迁移/p5-execution-plan.md:57-71`
- 因而：
  - `group:"rpc_handlers"` 目前没有 producer，这个观察是对的。
  - 但这正是因为 producer 被计划到波次 1-3，不是波次 0 的交付物。
- 对 `R0b` 来说，真正相关的是：
  - `internal/platform/rpc/module.go:12-19,28-44` 的 value-group 收集和注入机制是否成立；
  - `internal/app/modules.go:20-35` 的 app assembly 是否闭环；
  - `fx.ValidateApp(app.Module)` 是否通过。
- 这些点当前都成立。
- 所以 A 的注册链批判更适合作为“当前系统仍未进入公开 RPC 运行态”的状态说明，不适合作为否定 `R0b` 的核心理由。

### 3. A 是否低估了 `R0c` 的实际进展

结论：是，A 明显低估了 `approval.go` 已经落地的状态机密度。

- A 在 [p5-wave0-audit-A.md](/Volumes/bot/super-agent-v3/docs/plans/迁移/p5-wave0-audit-A.md#L76) 用的是“只是实现了一个孤立的 manager”。
- 如果“孤立”只是指“尚无外部调用点”，这个表述成立。
- 但如果暗含“只是编排壳、没有核心能力”，那就不公平。`approval.go`/`approval_support.go` 已经具备的能力包括：
  - pending 去重：
    - `internal/platform/rpc/approval.go:163-185`
    - 同 `callId` 二次注册时直接返回已有 pending 和 `owner=false`
  - `requestId` 分配：
    - `approval.go:169-173`
    - 缺省时用 `nextRequestID atomic.Int64` 分配
  - 等待与超时：
    - `internal/platform/rpc/approval_support.go:65-78` 的 `waitForApproval`
    - `approval_support.go:161-169` 的 `mapApprovalWaitErr`
    - `approval.go:128-139` 的 `Cleanup(timeout)`
  - 手动 resolve：
    - `approval.go:108-119` 的 `Respond`
  - auto-approve 能力：
    - `approval.go:121-126` 的 `AutoApprove`
  - 异步 callback dispatch：
    - `approval.go:187-235`
  - recoverable transport error 后保留 pending、供 restore 重发：
    - `approval.go:237-266`
    - `approval.go:141-152` 的 `RestorePending`
  - 回调 payload 解码与 decision 标准化：
    - `approval_support.go:125-158`
  - requested/resolved 事件发布：
    - `approval_support.go:21-41`
- 所以，A 对 `R0c` 更公平的描述应是：
  - “已经实现了一个有去重、等待、恢复、响应和事件发布能力的 approval manager，但尚未接入公开 RPC / provider / orchestration 主链。”
- 这比“只是孤立 manager”更准确。

### 4. A 的错误码批判与 B 重复，但是否更准确

结论：该批判成立，而且精度足够。

- 契约原文 `docs/契约/jrpc2-convention.md:586-588` 写得很直接：
  - 业务层使用应用自定义 code
  - 必须避开保留区间 `-32768` 到 `-32000`
- 当前错误码定义见 `internal/platform/rpc/errors.go:5-10`：
- `-31001`
- `-31002`
- `-31003`
- `-31004`
- `-31005`
- 这些值都满足：
  - `-32768 <= code <= -32000`
- 因而按照仓内契约，它们明确处于禁用区间内。
- 当前实现使用的 `-31001..-31005` 已经移出仓内契约禁止的 `-32768..-32000` 区间。
- 所以 A 与 B 在这一点上的批评都成立；A 的说法没有失真。

### 5. A 是否忽略了“增量完成”的合理性

结论：是。这是 A 报告里最明显的逻辑偏差。

- `docs/plans/迁移/p5-execution-plan.md:51-78` 已经把 P5 明确拆成：
  - 波次 0：前置基础设施
  - 波次 1-3：各 module 的 `rpc.go`
  - 波次 4：151 方法完整性验证
- 但 A 在 [p5-wave0-audit-A.md](/Volumes/bot/super-agent-v3/docs/plans/迁移/p5-wave0-audit-A.md#L53) 直接拿 `docs/plans/迁移/p5-execution-plan.md:140-147` 的整轮 Done 标准来判定“整个 `R0` 不完成”。
- 这里存在明显的 scope drift：
  - `151 个方法全部完成注册` 是整个 P5 的最终 Done 标准，不是波次 0 的 Done 标准；
  - `server -> client push 通道全链路打通` 也依赖后续模块和 UI 消费方，不是波次 0 单独能完成的交付。
- 这会把原本合理的“增量完成”误判成“未完成”。
- 更合理的审法应分两层：
  - 波次 0：审“基础设施是否具备后续接入条件”
  - 整轮 P5：审“151 方法、approval 闭环、push 全链路是否都成立”
- 因此，A 的报告在“现状描述”层面有价值，但在“完成度判级”上，明显把波次 0 和整轮 P5 的标准混用了。

## 互辩结论

- A 对以下事实的观察是准确的：
  - 当前没有 `rpc_handlers` producer
  - `ApprovalManager` 还没有外部调用点
  - `user_input_resolved` 状态恢复链未落地
  - 错误码仍处于契约保留区间
- 但 A 在完成度判定上存在两类过严：
  - 把波次 0 基础设施任务和波次 1-4 的运行时集成/最终验证混为一谈
  - 低估了 `R0c` 作为基础设施组件本身已经实现的状态机能力
- 因而，对 A 更公平的修正应是：
  - `R0b` 不应因“暂无 producer”被降级，它的本职交付已经完成
  - `R0c` 不应因“暂无 `module/turn/rpc.go` 调用点”直接判定不通过，更准确的状态是“基础设施完成度高，但主链未接入”
  - A 对错误码问题的批判则应完整保留，因为这属于波次 0 范围内的真实契约缺陷
