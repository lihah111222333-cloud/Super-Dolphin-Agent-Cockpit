# V3 架构合规审查报告（2026-03-26 复核修订版）

> 审查时间：2026-03-26  
> 说明：本版基于 `docs/plans/迁移/` 下 10 份 `arch-*.md` 主审计文档，并结合 `verify-*.md` 复核文档，对旧版 `framework_audit.md` 做二次修订。  
> 注：`pgx` 项没有独立 `arch-pgx-*.md` 文档，本报告仅按“驱动选型与访问边界”口径归纳。

## 总览

### 6 大框架合规状态

| # | 框架 | 用途 | 合规状态 |
|---|------|------|----------|
| 1 | `go.uber.org/fx` | 依赖注入 | ⚠️ 图可验证，但有 19 个悬空 provider 与 9 个 optional 依赖设计 |
| 2 | `github.com/kelindar/event` | 事件总线 | ⚠️ 订阅合规，但有 9 个零发布事件 + LogSink lifecycle 偏差 |
| 3 | `github.com/qmuntal/stateless` | 状态机 | ❌ Agent SM 仍有 out-of-band 写；turn tracker 仍完全绕过 |
| 4 | `github.com/creachadair/jrpc2` | JSON-RPC | ❌ 输入兼容治理已有进展，但返回值 shape 与部分输出字段仍未收口 |
| 5 | `github.com/oklog/run` + fx lifecycle | 进程拉起 + 关闭 | ⚠️ 结构合规；原 3 个高优竞态已修，仍有 2 个中优结构点 |
| 6 | `github.com/jackc/pgx` | 数据库 | ✅ 驱动选型与访问边界合规 |

### 架构横切面合规状态

| # | 维度 | 合规状态 |
|---|------|----------|
| 7 | 错误处理 | ❌ 38 个生产吞错点；goroutine panic 防护仍明显不足 |
| 8 | Import 方向 | ⚠️ 10 处违规（9 处 dto/contract 边界问题 + 1 处 wails 跨层） |
| 9 | 代码守卫 | ⚠️ archtest 8/8 通过，但多处接近红线 |
| 10 | Two-Zone DRY | ⚠️ 三件套齐全；Projector/Route 复用仍弱，handler 去重未完全 |
| 11 | 测试覆盖 | ❌ `store/*` 仍全零；`thread` / `codexapp` 已脱零但迁移核心边界整体仍薄 |

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

## 2. kelindar/event（事件总线）— ⚠️ 订阅完整但有零发布事件

> 详见 [arch-event-contract.md](迁移/arch-event-contract.md)

- 6 族 Emitter 全部通过 fx.Provide 暴露
- 19 个已发布事件均至少有 1 个订阅者（最小消费者集是 `LogSink`）
- `LogSink` 覆盖全部 28 个事件类型

**问题**：
- **9 个零发布事件**：`TurnStalled`、`TurnResumed`、`TaskDagCreated`、`TaskNodeStatusChanged`、`TaskWakeupDispatched`、`TaskWakeupCompleted`、`UIProjectionUpdated`、`UITimelineAppended`、`UITokensUpdated`
- **LogSink lifecycle 偏差**：构造期即订阅，不满足“OnStart 订阅、OnStop 退订”契约
- 5 族 typed emitter（除 Workspace）当前仍主要停留在“已提供但未被业务模块显式注入”的状态

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

## 4. jrpc2（JSON-RPC）— ❌ 治理推进明显，但契约仍未收口

> 详见 [arch-json-wire.md](迁移/arch-json-wire.md)；复核见 [verify-arch-compliance.md](迁移/verify-arch-compliance.md)、[verify-align-fx-wails.md](迁移/verify-align-fx-wails.md)

### 内部 RPC（合规）
- `platform/rpc/`、`module/*/rpc.go`、`mcpserver/common/bootstrap/` 这一层已基于 jrpc2

### 外部协议适配（设计选择）
- `codexapp/transport.go`：WebSocket 上手写 JSON-RPC（对接外部协议）
- `mcpserver/common/server.go`：MCP stdio server 手写 JSON-RPC

### JSON wire format 当前状态
- 早期审计确认 `rpc_types.go` 曾存在 **camelCase / snake_case / 单词小写** 混用
- 后续复核显示：**snake_case 主格式 + camelCase 兼容** 已扩展到 orchestration DAG、thread、turn、workspace 等多模块，已经不再是“只补 6 个点状类型”
- 但 **仍未完全统一**：
  - `thread/start` 仍保留 camelCase 主 tag
  - `turnForceCompleteResult` 仍输出 `forceCompleted`
  - 输出字段 shape 尚未全量统一
- 事件 DTO 层已统一 snake_case，`AgentSnapshot` 与 V2 一致

### V2 返回值 shape 仍有偏差
截至现有审计/复核文档，以下问题仍未被新的 verify 文档推翻：

- `agent.launch`：V3 成功结果仍是 `null`，V2 期望 `{agent_id, name, status}`
- `RememberReportRequestResult`：V3 `{agent_id, requester_id}`，V2 `{sender_id, worker_id}`
- `ReportEventResult`：V3 额外带 `report` / `notified_requester_ids`，V2 无此字段

---

## 5. oklog/run + fx lifecycle（进程拉起+关闭）— ⚠️ 高优竞态已修，但仍有中优结构点

> 详见 [arch-concurrency.md](迁移/arch-concurrency.md)；复核见 [verify-arch-compliance.md](迁移/verify-arch-compliance.md)、[verify-align-session-event.md](迁移/verify-align-session-event.md)

- `oklog/run` 负责启动 Actor，fx lifecycle 负责 graceful shutdown
- `platform/runner/group.go` 正确封装 `run.Group`
- 原审计列出的 **3 个高优并发问题**（`SessionManager` 代际、`codexapp.threadID` data race、`ApprovalManager.pending.dispatcher` 并发窗口）已在复核文档中判定为 **已修**
- 本轮仍未发现确定性死锁链；锁顺序基本单向

**剩余结构点**：

| 优先级 | 问题 | 风险 |
|--------|------|------|
| 中 | `rpc.Server.methods` 依赖启动期约束而非构造安全 | 运行态注册会产生竞态 |
| 中 | 2 处“超时返回但后台 goroutine 可能残留” | `unified.closeSession`、`wails.runner.Run` |

---

## 6. pgx（数据库）— ✅ 驱动选型与访问边界合规

- 零 `database/sql` 导入
- 数据库访问统一通过 `pgx/v5` + sqlc 生成层
- `pgx` 相关的 ctx 继承 / timeout 问题归入“错误处理与关闭语义”维度，不归入驱动选型本身

---

## 7. 错误处理 — ❌ 吞错多，panic 防护仍薄

> 详见 [arch-error-handling.md](迁移/arch-error-handling.md)

| 检查项 | 状态 |
|--------|------|
| RPC 层错误码避开 `-32xxx` | ❌ `ThreadScope` 仍直接返回 `jrpc2.InvalidParams` (`-32602`) |
| store 层错误包装 | ✅ 19/19 repo 已用 `WrapStoreError` |
| `CapabilityError` 统一消费 | ❌ 生产调用方仍未形成统一 `errors.As`/映射约定 |
| 静默吞错 | ❌ 38 个生产吞错点（lifecycle/shutdown/recovery 路径风险最高） |
| goroutine panic 防护 | ❌ 仅 `bus.ResilientSubscribe` 有 recover；其余 goroutine 无统一防护 |
| context 传递与 Close timeout | ❌ `Session.Close(ctx)` 两个 provider 实现仍忽略 ctx；启动/推送/关闭三类长路径未统一继承 ctx/deadline |

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

## 11. 测试覆盖 — ❌ 已有改善，但迁移核心边界仍不足

> 详见 [arch-test-coverage.md](迁移/arch-test-coverage.md)；复核见 [verify-arch-compliance.md](迁移/verify-arch-compliance.md)

### P0 缺口

| 包 | 当前状态 | 结论 |
|----|---------|------|
| `internal/store/*` | 根包 + 20 个子包仍为零测试 | 目前最大的真实缺口 |
| `internal/module/thread` | 已脱零，但 thread 生命周期/归档/历史/handler 面仍偏薄 | 迁移核心边界仍需补强 |
| `internal/provider/codexapp` | 已脱零，但 concrete provider 仍只有很薄的验证面 | start/resume/transport/history 仍缺系统覆盖 |

### P1 缺口
- `workspace`：仍只有少量测试，`build run`、文件同步、删除策略、dry-run、冲突细节仍缺覆盖
- `claudecli`：已不再是零测试，但主 provider 行为面仍偏薄
- `platform/rpc`：已有补测，但 handler 主流程、错误映射、事件桥接仍需加强
- V2 `schema_contract_test` 在 V3 仍无对应物

---

## 总结

| 类别 | 框架/维度 | 核心问题 |
|------|----------|----------|
| ✅ 合规 | pgx | 驱动选型与访问边界合规 |
| ⚠️ 部分问题 | fx | 19 悬空 provider，9 个 optional 依赖设计 |
| ⚠️ 部分问题 | event bus | 9 个零发布事件，LogSink lifecycle 偏差 |
| ⚠️ 部分问题 | oklog/run + fx lifecycle | 3 个高优竞态已修，仍有 2 个中优结构点 |
| ⚠️ 部分问题 | import 方向 | dto/contract 边界不纯 + wails 跨层 |
| ⚠️ 部分问题 | 代码守卫 | 多处贴边，复杂度余量紧张 |
| ⚠️ 部分问题 | two-zone DRY | Projector/Route 复用弱，handler 去重不完整 |
| ❌ 不合规 | stateless | out-of-band 写，turn tracker 绕过，`awaiting_user_input` 不可达 |
| ❌ 不合规 | jrpc2 | 返回值 shape 偏离 V2，wire/output 仍未完全统一 |
| ❌ 不合规 | 错误处理 | 38 个吞错点，panic 防护不足，Close/ctx 语义不统一 |
| ❌ 不合规 | 测试覆盖 | `store/*` 全零；`thread` / `codexapp` 虽已脱零但仍明显偏薄 |

### 建议优先处理项

**P0 — 影响正确性和契约一致性**：

1. **stateless strict mode 收口** → 消除 `prepareLaunchStateLocked` / `forceIdleAfterCompletionError` 的状态机外写状态
2. **`awaiting_user_input` 打通** → orchestration 消费 approval 事件，驱动 `turn_running → awaiting_user_input → turn_running`
3. **错误处理收口** → 优先清理 lifecycle/shutdown/recovery 路径吞错点；为 goroutine 和 direct bus subscriber 建统一 panic policy
4. **JSON-RPC 返回值 shape 收口** → 优先修 `agent.launch`、`RememberReportRequestResult`、`ReportEventResult`
5. **测试补齐** → 先补 `store/*`，再补 `thread` / `codexapp` / schema contract

**P1 — 影响合规性和维护性**：

6. **9 个零发布事件** → 决定补发布链路还是删除死定义
7. **fx optional 与悬空 provider 收紧** → 能改强依赖的改强依赖；悬空 provider 要么接消费点，要么删除
8. **并发中优结构点收尾** → 给 `rpc.Server.methods` 加注册期保护，清理超时返回但 goroutine 残留的包装函数
9. **turn tracker** → 迁移到 stateless 状态机
10. **Two-Zone DRY 深化** → 补足 Projector/Route 复用与跨模块 handler 工厂
