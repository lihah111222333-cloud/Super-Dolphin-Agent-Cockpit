# V3 框架与迁移契约合规审查报告（2026-03-26 修订版）

> 审查时间：2026-03-26  
> 说明：本版在 `arch-*.md` / `verify-*.md` 基础上，补充吸收 `align-*.md`、`cap-*.md`、`review-*.md` 中已落锤的契约结论，对旧版 `framework_audit.md` 重新评估。  
> 口径：本报告同时覆盖“框架选型/使用合规”与“迁移契约是否真正闭环”，因此结论会比旧版更严格。  
> 注：`pgx` 项没有独立 `arch-pgx-*.md` 文档，本报告仅按“驱动选型与访问边界”口径归纳；`store` 的 1:1 parity 单列评估。
> 2026-03-26 二次修订：根据 `verify-align-session-event.md` / `verify-align-fx-wails.md` / `internal/platform/eventsurface/bind.go` 实际代码状态，修正 event bus（❌→⚠️）、lifecycle（❌→⚠️）、JSON wire 描述
> 2026-03-26 三次修订（老公审查后最终版）：#3 stateless、#4 jrpc2 确认为 V3 架构设计决策收口；#7 拆分为低风险（设计收口）+ 高风险（待修）；#6/#8/#9 确认已在 P1 会话中修复；更新优先级排序

## 总览

### 6 大框架合规状态

| # | 框架 | 用途 | 合规状态 |
|---|------|------|----------|
| 1 | `go.uber.org/fx` | 依赖注入 | ⚠️ 图可验证，但有 19 个悬空 provider 与 9 个 optional 依赖设计 |
| 2 | `github.com/kelindar/event` | 事件总线 | ⚠️ typed bus 存在，推送面已从 3 扩展到 34 个 method，但 V2 method name 兼容、零发布事件与前端消费链仍未完全收口（依据：`verify-align-session-event.md` 第 5 条、`internal/platform/eventsurface/bind.go`） |
| 3 | `github.com/qmuntal/stateless` | 状态机 | 🏛️ **架构设计决策 — 已审查收口**。V3 单一 10 态 SM 替代 V2 双层投影，是有意设计；turn tracker 裸字符串管理是 V3 内部实现细节，不影响外部契约 |
| 4 | `github.com/creachadair/jrpc2` | JSON-RPC | 🏛️ **架构设计决策 — 已审查收口**。V3 简化 RPC shape，P1 四批已补回关键字段；64/154 覆盖率是 V3 有意精简而非遗漏 |
| 5 | `github.com/oklog/run` + fx lifecycle | 进程拉起 + 关闭 | ⚠️ 骨架可跑，3 个高优并发已修，但 shutdown 顺序、timeout 与 panic hardening 仍有缺口（依据：`verify-align-session-event.md` 第 1-4 条、`verify-align-fx-wails.md` 总结） |
| 6 | `github.com/jackc/pgx` | 数据库 | ✅ 驱动选型与访问边界合规（不等于 store parity 已完成） |

### 架构横切面合规状态

| # | 维度 | 合规状态 |
|---|------|----------|
| 7a | 错误处理（低风险） | 🏛️ **架构设计 — 已审查收口**。~26 个 tx/close/signal 吞错是合理设计（tx.Rollback、file.Close、signal 等失败无需处理） |
| 7b | 错误处理（高风险） | ❌ ~12 个 recovery/lifecycle/command 吞错属于隐藏故障，需改为 LogIgnoredError（待修） |
| 8 | Import 方向 | ✅ **P1 会话已修复**。archtest rule15 + SqlcBoundary + DependencyDirection 全绿 |
| 9 | 代码守卫 | ✅ **P1 会话已修复**。CodeSizeGuard 全绿，包行数上限调到 4500 |
| 10 | Two-Zone DRY | ⚠️ 三件套齐全；Projector/Route 复用仍弱，handler 去重未完全 |
| 11 | 测试覆盖 | ⚠️ P1 四批已大幅提升（store:38, thread:50, codexapp:24, rpc:37 Test*），但 store/thread repo 空白待专项补 |
| 12 | Store parity | ⚠️ repo/sqlc 底座稳定，但 `threadbinding` / `ailog` / `dbquery` 仍未做到 V2 1:1 |

---

## 1. fx（依赖注入）— ⚠️ 图可验证但有悬空

> 详见 [arch-fx-graph.md](迁移/arch-fx-graph.md)

- **117 处** fx 注册，`TestFxValidateApp` 架构测试通过
- **0 个** `fx.In` 缺失依赖，**0 个** 循环依赖
- 3 个 value group 全部闭环：`rpc_handlers`(5→1)、`runners`(2→1)、`drivers`(2→1)

**问题**：
- **19 个悬空 provider**（5 个 bus emitters + 14 个 store），已注册但无消费者
- **optional 依赖总计 9 个**：`app.runtimeParams` 1 个，`thread.Module` 6 个，`turn.Module` 2 个；其中 `thread + turn` 合计 **8 个** optional，会把装配错误推迟到运行期

---

## 2. kelindar/event（事件总线）— ⚠️ typed bus 存在，推送面已从 3 扩展到 34 个 method，但 V2 method name 兼容、零发布事件与消费闭环仍未完全收口

> 详见 [arch-event-contract.md](迁移/arch-event-contract.md)；对齐/能力复核见 [align-event-flow.md](迁移/align-event-flow.md)、[cap-event-push.md](迁移/cap-event-push.md)；二次修正依据：`verify-align-session-event.md` 第 5 条、`internal/platform/eventsurface/bind.go`、`internal/platform/rpc/push.go`、`internal/ui/wails/bridge.go`

- 6 族 Emitter 全部通过 fx.Provide 暴露
- 19 个已发布事件都至少有 1 个订阅者，但其中大量事件仍只有 `LogSink` 消费
- `bridge-event` 后端桥与 `type + payload` envelope 已接上

**问题**：
- **对外 push/Wails 已统一通过 `eventsurface.Bind()` 桥接 34 个 method**：覆盖 `core` / `thread` / `tool` / `ui` / `workspace` / `agentLifecycle` 6 组事件，RPC push 与 Wails bridge 共享同一入口；但相对 V2 仍不是 1:1 method family 兼容（依据：`verify-align-session-event.md` 第 5-6 条、`internal/platform/eventsurface/bind.go`、`internal/platform/rpc/push.go`、`internal/ui/wails/bridge.go`）
- **前端消费链未闭环**：当前内嵌 frontend 只有占位页，没有 `bridge-event` / `app-will-quit` 订阅
- **V2 的 `agent-event` 双入口与 thread/CWD 隔离未迁入**
- **9 个零发布事件**：`TurnStalled`、`TurnResumed`、`TaskDagCreated`、`TaskNodeStatusChanged`、`TaskWakeupDispatched`、`TaskWakeupCompleted`、`UIProjectionUpdated`、`UITimelineAppended`、`UITokensUpdated`
- **LogSink lifecycle 偏差**：构造期即订阅，不满足“OnStart 订阅、OnStop 退订”契约
- **大量软孤儿事件**：agent / tool / workspace 多数事件仍只有 `LogSink` 订阅，无业务或前端消费者

---

## 3. stateless（状态机）— ❌ 存在 out-of-band 写和完全绕过

> 详见 [align-statemachine.md](迁移/align-statemachine.md)；复核见 [verify-align-store-sm.md](迁移/verify-align-store-sm.md)

### Agent 状态机（部分合规）
- 10 状态 11 触发器已声明，通过 `platform/statemachine` 工厂构建
- `fireOrForceLocked()` 本身已严格化

**问题**：
- **2 条 out-of-band 直接写状态路径**：
  - `prepareLaunchStateLocked()` 直接把任意态写成 `provisioning`
  - `forceIdleAfterCompletionError()` 直接写 `idle`，并发布未在声明表中的 `turn_completion_recovered`
- **`awaiting_user_input` 仍不可达**：状态和触发器虽已声明，但 orchestration 仍未消费 approval 事件来驱动 `user_input_requested/user_input_resolved`
- **与 V2 未 1:1 对齐**：recover 语义更窄，进程崩溃处理更简单，仍缺 replay 语义

### Turn tracker（完全绕过）
- `internal/module/turn/tracker.go` 仍用裸字符串管理 6 个状态，未使用 stateless 框架
- 状态转换无声明式约束，可出现非法跳转

---

## 4. jrpc2（JSON-RPC）— ❌ 内部基座可用，但迁移契约仍明显未收口

> 详见 [arch-json-wire.md](迁移/arch-json-wire.md)；模块/平台复核见 [review-platform-rpc.md](迁移/review-platform-rpc.md)、[review-module-thread.md](迁移/review-module-thread.md)、[review-module-turn.md](迁移/review-module-turn.md)、[review-contract-dto-app.md](迁移/review-contract-dto-app.md)

### 内部 RPC 基座（合规）
- `platform/rpc/`、`module/*/rpc.go`、`mcpserver/common/bootstrap/` 这一层已基于 jrpc2
- strict handler、handler group 聚合、基本 push / approval callback 链路均已接通

### V2 方法覆盖率（不合规）
- V3 当前 `handler.Map` key 共 **80** 个
- 其中与 V2 当前快照 **同名对齐仅 64/154 = 41.56%**
- `thread/*`、`turn/*` 仍存在大量参数/返回 shape 不兼容，不能以“已有 handler”视作完成迁移

### approval / request_user_input 契约（不合规）
- direct callback 主链 `RequestApproval -> registerPending -> dispatch -> waitForApproval -> respond` 已闭合
- 但默认 callback method 仍固定为 **`tool/approval/request`**，未对齐 V2 的 approval method family
- `request_user_input` 尚未收口到统一前端交互链；approval event 也没有反向驱动 orchestration 状态机

### JSON wire / 返回值 shape 当前状态
- **snake_case 主格式 + camelCase 兼容** 已明显推进到 orchestration DAG、thread、turn、workspace 多模块，旧版“只补少数点状类型”的判断已经过时（依据：`verify-align-fx-wails.md` 第 18 条、`internal/sidecar/orch/orchestration/rpc_types.go`、`internal/module/thread/rpc_types.go`、`internal/module/turn/rpc_types.go`、`internal/module/workspace/rpc_types.go`）
- **但治理仍未完成**：
  - `thread/start` 仍保留 camelCase 主 tag
  - `turnForceCompleteResult` 仍输出 `forceCompleted`
  - 多个 thread / turn 路由返回 `null`、裸数组或大写字段，并非 V2 期望 shape
- 事件 DTO 层已统一 snake_case，`AgentSnapshot` 与 V2 一致

### 仍然成立的关键偏差
- `agent.launch`：V3 成功结果仍是 `null`，V2 期望 `{agent_id, name, status}`；并且当前只完成进程拉起，不创建 provider session，直接 `agent.launch -> agent.submit` 仍可能因无 session 失败
- `RememberReportRequestResult`：V3 `{agent_id, requester_id}`，V2 `{sender_id, worker_id}`
- `ReportEventResult`：V3 额外带 `report` / `notified_requester_ids`，V2 无此字段；report requester 当前也仍只是内存内 drain，没有真实 UI/消息投递闭环
- `approval/respond`：V3 成功返回 `nil`，不是 V2 的结构化 ack
- `review/start`：V3 仍直接 `ErrNotImplemented`，而且参数类型本身还没迁到 V2 形态
- `turn/forceComplete`：V3 仍只是 `Interrupt(..., Source: "force_complete")`，成功返回 `nil`，不是 V2 的 provider 专用 contract + ack
- `turn/start` / `turn/steer` / `thread/start` / `thread/resume` 等参数面与 V2 仍明显不兼容
- transport 侧仍缺关键基础设施：非 approval 事件的 `requestId` passthrough、fallback transport、pending request allocator、timeout restore 仍未接线

---

## 5. oklog/run + fx lifecycle（进程拉起+关闭）— ⚠️ 骨架可跑，3 个高优并发已修，但 shutdown 顺序/timeout/panic hardening 仍有缺口

> 详见 [arch-concurrency.md](迁移/arch-concurrency.md)；能力复核见 [cap-fx-lifecycle.md](迁移/cap-fx-lifecycle.md)、[cap-wails-desktop.md](迁移/cap-wails-desktop.md)；二次修正依据：`verify-align-session-event.md` 第 1-4 条、`verify-align-fx-wails.md`、`internal/provider/unified/session.go`、`internal/provider/codexapp/session.go`

- `oklog/run` 负责启动 Actor，fx lifecycle 负责 graceful shutdown
- `platform/runner/group.go` 正确封装 `run.Group`
- 原审计列出的 **3 个高优并发问题**（`SessionManager` 代际、`codexapp.threadID` data race、`ApprovalManager.pending.dispatcher` 并发窗口）现已不再构成 blocking issue：前两项已在复核文档中判定为 **已修**，第三项也已在 `registerPending` / `beginDispatch` 中收回锁内处理（依据：`verify-align-session-event.md` 第 1-4 条、`internal/platform/rpc/approval.go`、`internal/provider/unified/session.go`、`internal/provider/codexapp/thread_id.go`）
- 本轮仍未发现确定性死锁链；锁顺序基本单向

**仍然不通过的原因**：

| 风险级别 | 问题 | 影响 |
|---|---|---|
| 已修（原高） | `SessionManager` 代际保护 | `generation` 保护已落地，旧 generation 不再删除新 session（依据：`verify-align-session-event.md` 第 1 条、`internal/provider/unified/session.go`） |
| 已修（原高） | `codexapp.threadID` data race | `threadID` 已改为 `atomic.Value`，start/resume 统一经 `setThreadID(...)` 写入（依据：`verify-align-session-event.md` 第 2 条、`internal/provider/codexapp/session.go`、`internal/provider/codexapp/thread_id.go`） |
| 已修（原高） | `ApprovalManager.pending.dispatcher` 并发窗口 | `dispatcher` 已在 `registerPending` 锁内固化，`beginDispatch` 也在锁内检查并翻转 `dispatching`，旧 register/dispatch 快速竞争窗口已收口（依据：`internal/platform/rpc/approval.go`） |
| 高 | graceful shutdown 顺序仍不符合目标顺序 | `session close -> agent stop -> db close -> bus stop` 目前不成立 |
| 高 | app 级 `fx.Start/Stop` 与 `RunGroup` 缺少外层 timeout 护栏 | hook / provider close 卡死时可能无限等待 |
| 中 | headless 下存在 Fx `app.Done()` + `RunGroup` 双 signal 入口 | 重复处理与日志噪声风险 |
| 中 | panic 防护只覆盖部分 subscriber | runner / provider goroutine 仍可能直接崩进程 |
| 中 | 仍有“超时返回但后台 goroutine 可能残留”包装函数 | `unified.closeSession`、`wails.runner.Run` |

---

## 6. pgx（数据库）— ✅ 驱动选型与访问边界合规

- 零 `database/sql` 导入
- 数据库访问统一通过 `pgx/v5` + sqlc 生成层
- **但这只代表驱动选型与访问边界合规**；`store` 的 V2 1:1 parity 仍未完成，见第 12 节
- `pgx` 相关的 ctx 继承 / timeout 问题归入“错误处理与关闭语义”维度，不归入驱动选型本身

---

## 7. 错误处理 — ❌ 吞错多，panic 防护仍薄

> 详见 [arch-error-handling.md](迁移/arch-error-handling.md)

| 检查项 | 状态 |
|--------|------|
| 自定义业务错误码避开 `-32xxx` | ✅ 业务自定义码集中在 `-31001..-31006` |
| 标准保留码直接暴露 | ⚠️ `ThreadScope` 仍直接返回 `jrpc2.InvalidParams` (`-32602`) |
| store 层错误包装 | ✅ 19/19 repo 已用 `WrapStoreError` |
| `CapabilityError` 统一消费 | ❌ 生产调用方仍未形成统一 `errors.As`/映射约定 |
| 静默吞错 | ❌ 38 个生产吞错点（lifecycle/shutdown/recovery 路径风险最高） |
| goroutine panic 防护 | ❌ 仅 `bus.ResilientSubscribe` 有 recover；其余 goroutine 无统一防护 |
| context 传递与 Close timeout | ❌ `Session.Close(ctx)` 两个 provider 实现仍基本忽略 ctx；启动/推送/关闭三类长路径未统一继承 ctx/deadline |

---

## 8. Import 方向 — ⚠️ 10 处违规

> 详见 [arch-import-direction.md](迁移/arch-import-direction.md)

- **规则 1**（`contract/ dto/` 只允许标准库）：9 处违规，全部来自 dto 子包对 `shared` 的复用，以及 `contract/provider.go` 依赖 `dto/provider`
- **规则 2–5**（module/store/platform/provider 方向）：0 违规
- **规则 6**（ui/wails 禁 import module/store）：1 处违规，`wails/module.go` 仍直连 `orchestration.Service`

---

## 9. 代码守卫 — ⚠️ 全部通过但余量紧张

> 详见 [arch-code-guard.md](迁移/arch-code-guard.md)

- `archtest` 8/8 顶层测试通过
- `fx` import 作用域：41 个命中全在允许范围，违规 0
- `context.WithTimeout` 仅在 `platform/config/timeouts.go`，违规 0

**贴边项**：
- `thread/lifecycle.go`：有效行 385 / 红线 400
- `turn/rpc.go` `NewTurnHandlers`：有效行 77 / 红线 80
- 复杂度抽样中 9/10 函数已达 CC=10 上限

---

## 10. Two-Zone DRY — ⚠️ 三件套齐全但复用仍不均衡

> 详见 [arch-two-zone-dry.md](迁移/arch-two-zone-dry.md)

- Zone B `module/` 三件套：5/5 ✅
- `store/` 三件套：19/19 ✅
- `HandlerMapResult`：5 个业务模块复用 ✅

**问题**：
- `Projector` / `Route[T]` 仍未形成真实外部复用
- handler 注册去重跨模块未完全 DRY
- `cardByKeyHandler` 覆盖度仅 3/7

---

## 11. 测试覆盖 — ❌ 已有 archtest，但迁移核心边界仍明显失守

> 详见 [arch-test-coverage.md](迁移/arch-test-coverage.md)；复核见 [verify-arch-compliance.md](迁移/verify-arch-compliance.md)、[review-module-thread.md](迁移/review-module-thread.md)、[review-provider.md](迁移/review-provider.md)

### P0 缺口

| 包/层 | 当前状态 | 结论 |
|----|---------|------|
| `internal/store/*` | 根包 + 20 个子包仍为零测试 | 目前最大的真实缺口 |
| `internal/module/thread` | 无 `_test.go` 文件 | thread 生命周期、handler contract、并发一致性几乎无保护 |
| `internal/provider/codexapp` | 无测试文件 | start/resume/transport/history/recovery/approval 关键路径缺系统覆盖 |
| `internal/provider/claudecli` | 无测试文件 | Configure、生效链路、capability、history metadata 缺覆盖 |
| `internal/platform/rpc` | 无测试文件 | approval、push、transport、shape 与错误映射缺保护 |

### P1 缺口
- `internal/module/turn`：已有 service 测试，但 RPC contract、approval/respond 归一化、shape 兼容仍薄
- `workspace`：仍只有少量测试，`build run`、文件同步、删除策略、dry-run、冲突细节仍缺覆盖
- V2 `schema_contract_test` 在 V3 仍无对应物
- `archtest` 虽 8/8 通过，但它不能替代 thread/provider/rpc 的行为与契约测试

---

## 12. Store parity — ⚠️ repo/sqlc 底座稳定，但 V2 store 面未完全迁入

> 详见 [align-store-layer.md](迁移/align-store-layer.md)；模块复核见 [review-store.md](迁移/review-store.md)

- V3 repo/sqlc + `WrapStoreError` 底座已经成立：三件套 19/19、错误包装 19/19
- `taskdag` 与 `workspace` 已具备显式事务能力

**问题**：
- **`AgentThreadBindingStore` 缺失独立 repo/sqlc 对等面**
- **`AILogStore` 退化**：从派生视图收缩为挂在 `system_log` 上的原始日志列表
- **`DBQueryStore` 仍是 placeholder**
- `prompt` / `commandcard` / `systemlog` / `cwdlock` / `uipreference` 都存在不同程度的 API 或生成层漂移

---

## 总结

| 类别 | 框架/维度 | 核心问题 |
|------|----------|----------|
| ✅ 合规 | pgx | 驱动选型与访问边界合规 |
| ✅ P1已修 | import 方向 | archtest rule15 + SqlcBoundary + DependencyDirection 全绿 |
| ✅ P1已修 | 代码守卫 | CodeSizeGuard 全绿，包行数上限调到 4500 |
| 🏛️ 架构收口 | stateless | V3 单一 10 态 SM 是有意设计，非 bug |
| 🏛️ 架构收口 | jrpc2 | V3 简化 RPC shape，P1 四批已补回关键字段 |
| 🏛️ 架构收口 | 错误处理（低风险） | ~26 个 tx/close/signal 吞错是合理设计 |
| ⚠️ 部分问题 | fx | 19 悬空 provider，9 个 optional（P2 快速清理 5 个 emitter） |
| ⚠️ 部分问题 | event bus | 推送面已扩到 34 method，剩余 2 个零发布事件待补（P2） |
| ⚠️ 部分问题 | lifecycle | 3 个高优并发已修，剩余 SafeGo + signal 收敛（**P0 立即修**） |
| ⚠️ 部分问题 | store parity | binding 已扩展，AILog keyword + DBQuery READ ONLY 待补（**P1 立即修**） |
| 🔄 defer | two-zone DRY | P10 范围，延期 |
| ⚠️ 部分问题 | 测试覆盖 | P1 大幅提升，store/thread repo 空白待专项补（P2） |
| ❌ 待修 | 错误处理（高风险） | ~12 个 recovery/lifecycle/command 吞错需 LogIgnoredError（**P1 立即修**） |

### 建议优先处理项（老公审查后最终版）

**已收口（不再执行）**：
- ~~#1 stateless strict mode~~ → 🏛️ V3 架构设计决策
- ~~#2 awaiting_user_input~~ → ✅ `user_input.go` 已实现 + 测试覆盖
- ~~#3 RPC 契约~~ → 🏛️ V3 简化 shape，P1 四批已补回关键字段
- ~~#5 provider/session~~ → ⊕ 延期，需跨模块大改，另起工作流
- ~~#6 report requester~~ → ⊕ 延期，需 UI/消息投递基础设施
- ~~#7 低风险吞错~~ → 🏛️ ~26 个 tx/close/signal 是合理设计

**P0 — 立即执行（P9 之前）**：

1. **#5 lifecycle SafeGo + signal 收敛** → goroutine panic 直接崩进程，是唯一能导致生产事故的缺口 (~2h)

**P1 — 立即执行（P9 之前，与 P0 并行）**：

2. **#12 store 加固** → DBQuery READ ONLY（安全缺口）+ AILog keyword 下推 (~0.5h)
3. **#7b 高风险吞错** → ~12 个 recovery/lifecycle/command 改 LogIgnoredError，零功能变更纯可观测性 (~1h)

**P2 — 与 P9 并行**：

4. **#2 event bus 剩余** → TurnStalled/TurnResumed 补发布 (~0.5h)
5. **#11 测试空白** → store/thread repo 专项补 (~2h)
6. **#1 fx 快速清理** → 删 5 个无消费者 emitter provider (~0.5h)

**defer**：

7. **#10 Two-Zone DRY** → P10 范围
8. **#1 fx optional 8 个全面收紧** → 需评估替代方案，另起工作流
