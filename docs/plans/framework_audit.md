# V3 框架与迁移契约合规审查报告（2026-03-26 修订版）

> 审查时间：2026-03-26  
> 说明：本版在 `arch-*.md` / `verify-*.md` 基础上，补充吸收 `align-*.md`、`cap-*.md`、`review-*.md` 中已落锤的契约结论，对旧版 `framework_audit.md` 重新评估。  
> 口径：本报告同时覆盖“框架选型/使用合规”与“迁移契约是否真正闭环”，因此结论会比旧版更严格。  
> 注：`pgx` 项没有独立 `arch-pgx-*.md` 文档，本报告仅按“驱动选型与访问边界”口径归纳；`store` 的 1:1 parity 单列评估。

## 总览

### 6 大框架合规状态

| # | 框架 | 用途 | 合规状态 |
|---|------|------|----------|
| 1 | `go.uber.org/fx` | 依赖注入 | ⚠️ 图可验证，但有 19 个悬空 provider 与 9 个 optional 依赖设计 |
| 2 | `github.com/kelindar/event` | 事件总线 | ❌ typed bus 存在，但对外事件契约、bridge 宽度与前端消费链均未对齐 |
| 3 | `github.com/qmuntal/stateless` | 状态机 | ❌ Agent SM 仍有 out-of-band 写；turn tracker 仍完全绕过 |
| 4 | `github.com/creachadair/jrpc2` | JSON-RPC | ❌ 内部基座可用，但方法覆盖率、approval、push、返回 shape 仍明显偏离 V2 |
| 5 | `github.com/oklog/run` + fx lifecycle | 进程拉起 + 关闭 | ❌ 可运行，但 shutdown 顺序、timeout、signal 与 panic hardening 未达契约要求 |
| 6 | `github.com/jackc/pgx` | 数据库 | ✅ 驱动选型与访问边界合规（不等于 store parity 已完成） |

### 架构横切面合规状态

| # | 维度 | 合规状态 |
|---|------|----------|
| 7 | 错误处理 | ❌ 自定义业务码基本合规，但吞错、panic 防护、ctx/Close 语义仍明显失衡 |
| 8 | Import 方向 | ⚠️ 10 处违规（9 处 dto/contract 边界问题 + 1 处 wails 跨层） |
| 9 | 代码守卫 | ⚠️ archtest 8/8 通过，但多处接近红线 |
| 10 | Two-Zone DRY | ⚠️ 三件套齐全；Projector/Route 复用仍弱，handler 去重未完全 |
| 11 | 测试覆盖 | ❌ `store/*` 仍全零；`thread` / `codexapp` / `claudecli` / `platform/rpc` 保护面仍接近空白 |
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

## 2. kelindar/event（事件总线）— ❌ 内部 bus 存在，但对外事件契约未闭环

> 详见 [arch-event-contract.md](迁移/arch-event-contract.md)；对齐/能力复核见 [align-event-flow.md](迁移/align-event-flow.md)、[cap-event-push.md](迁移/cap-event-push.md)

- 6 族 Emitter 全部通过 fx.Provide 暴露
- 19 个已发布事件都至少有 1 个订阅者，但其中大量事件仍只有 `LogSink` 消费
- `bridge-event` 后端桥与 `type + payload` envelope 已接上

**问题**：
- **对外 push/Wails 只桥接 3 个事件**：`ui/state/changed`、`turn/started`、`turn/completed`，远窄于 V2 public method 面
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
- **snake_case 主格式 + camelCase 兼容** 已扩展到多模块，旧版“只补少数点状类型”的判断过窄
- 但 **仍未完全统一**：
  - `thread/start` 仍保留 camelCase 主 tag
  - `turnForceCompleteResult` 仍输出 `forceCompleted`
  - 多个 thread / turn 路由返回 `null`、裸数组或大写字段，并非 V2 期望 shape
- 事件 DTO 层已统一 snake_case，`AgentSnapshot` 与 V2 一致

### 仍然成立的关键偏差
- `agent.launch`：V3 成功结果仍是 `null`，V2 期望 `{agent_id, name, status}`
- `RememberReportRequestResult`：V3 `{agent_id, requester_id}`，V2 `{sender_id, worker_id}`
- `ReportEventResult`：V3 额外带 `report` / `notified_requester_ids`，V2 无此字段
- `approval/respond`：V3 成功返回 `nil`，不是 V2 的结构化 ack
- `turn/start` / `turn/steer` / `thread/start` / `thread/resume` 等参数面与 V2 仍明显不兼容

---

## 5. oklog/run + fx lifecycle（进程拉起+关闭）— ❌ 骨架可跑，但 lifecycle hardening 未达标

> 详见 [arch-concurrency.md](迁移/arch-concurrency.md)；能力复核见 [cap-fx-lifecycle.md](迁移/cap-fx-lifecycle.md)、[cap-wails-desktop.md](迁移/cap-wails-desktop.md)

- `oklog/run` 负责启动 Actor，fx lifecycle 负责 graceful shutdown
- `platform/runner/group.go` 正确封装 `run.Group`
- 原审计列出的 **3 个高优并发问题**（`SessionManager` 代际、`codexapp.threadID` data race、`ApprovalManager.pending.dispatcher` 并发窗口）已在复核文档中判定为 **已修**
- 本轮仍未发现确定性死锁链；锁顺序基本单向

**仍然不通过的原因**：

| 风险级别 | 问题 | 影响 |
|---|---|---|
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
| ✅ 合规 | pgx | 驱动选型与访问边界合规，但不等于 store parity 完成 |
| ⚠️ 部分问题 | fx | 19 悬空 provider，9 个 optional 依赖设计 |
| ⚠️ 部分问题 | import 方向 | dto/contract 边界不纯 + wails 跨层 |
| ⚠️ 部分问题 | 代码守卫 | 多处贴边，复杂度余量紧张 |
| ⚠️ 部分问题 | two-zone DRY | Projector/Route 复用弱，handler 去重不完整 |
| ⚠️ 部分问题 | store parity | repo/sqlc 底座稳定，但 `threadbinding` / `ailog` / `dbquery` 未 1:1 |
| ❌ 不合规 | event bus | 对外事件面只剩 3 个核心事件，前端消费链与双入口隔离未闭环 |
| ❌ 不合规 | stateless | out-of-band 写，turn tracker 绕过，`awaiting_user_input` 不可达 |
| ❌ 不合规 | jrpc2 | 仅 64/154 V2 同名方法对齐，approval / push / shape 仍未收口 |
| ❌ 不合规 | oklog/run + fx lifecycle | shutdown 顺序、timeout、signal、panic hardening 未达标 |
| ❌ 不合规 | 错误处理 | 吞错多，panic 防护不足，Close/ctx 语义不统一 |
| ❌ 不合规 | 测试覆盖 | `store/*` 全零；`thread` / `codexapp` / `claudecli` / `platform/rpc` 近零测试 |

### 建议优先处理项

**P0 — 影响正确性、契约一致性与生命周期安全**：

1. **stateless strict mode 收口** → 消除 `prepareLaunchStateLocked` / `forceIdleAfterCompletionError` 的状态机外写状态
2. **`awaiting_user_input` + approval bridge 打通** → orchestration 消费 approval 事件，驱动 `turn_running → awaiting_user_input → turn_running`
3. **RPC 契约收口** → 优先修 `agent.launch`、`approval/respond`、`turn/start`、`thread/start` / `resume` 的返回 shape 与参数面；同时补 V2 approval method family
4. **lifecycle hardening** → 补 app 级 timeout、修 shutdown 顺序、统一 goroutine panic policy、收敛双 signal 入口
5. **provider/thread session 一致性** → 修 Claude placeholder threadID、Archive/Delete 不 `Remove` session、Recover 误复用 closed session 等问题
6. **测试补齐** → 先补 `store/*`、`thread`、`codexapp`、`claudecli`、`platform/rpc`

**P1 — 影响事件面、维护性与迁移完整度**：

7. **事件契约收口** → 决定补齐 V2 public method / `agent-event` / 前端 runtime 消费，还是明确降级并删死定义
8. **9 个零发布事件 + 软孤儿事件** → 决定补发布链路与消费者，或删除死定义/死桥接
9. **fx optional 与悬空 provider 收紧** → 能改强依赖的改强依赖；悬空 provider 要么接消费点，要么删除
10. **turn tracker** → 迁移到 stateless 状态机
11. **Store parity 深化** → 补 `threadbinding` / `ailog` / `dbquery` 与相关事务/兼容层
12. **Two-Zone DRY 深化** → 补足 Projector/Route 复用与跨模块 handler 工厂
