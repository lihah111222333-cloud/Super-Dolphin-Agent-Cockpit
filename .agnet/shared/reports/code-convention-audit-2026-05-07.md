# Super-Dolphin 全面契约合规审查报告

**日期**: 2026-05-07
**审查依据**: `docs/架构/` (6份) + `docs/契约/` (8份)，共 14 份规范文档
**审查范围**: 项目全部 Go 源码 (cmd/, internal/, pkg/)
**DAG**: `code-convention-audit-2026-05-07`

---

## 一、总览

| 审查领域 | 高严重 | 中严重 | 低严重 | 合计 |
|----------|--------|--------|--------|------|
| fx 契约 | 0 | 3 | 5 | 8 |
| rungroup 契约 | 2 | 2 | 7 | 11 |
| jrpc2 契约 | 12 | 25+ | 9+ | 46+ |
| stateless+event 契约 | 3 | 3 | 4 | 10 |
| 模块化契约 | 30+ | 20+ | 16+ | 66+ |
| 洋葱架构 | 0 | 1 | 4 | 5 |
| sqlc 契约 | 3 | 4 | 5 | 12 |
| MCP 服务契约 | 2 | 3 | 3 | 8 |
| **合计** | **~52** | **~61** | **~53** | **~166** |

---

## 二、P0 紧急修复项（影响正确性或守卫失效）

### P0-1 ⚠️ archtest rule4 因尾部斜杠 BUG 完全失效
- **文件**: `internal/archtest/dependency_direction_test.go:105`
- **问题**: `internalPrefix("internal/module/")` 末尾带 `/`，拼接后变 `"...internal/module//"` (双斜杠)，永远不匹配
- **后果**: `platform_cannot_import_module` 规则名存实亡，`toolbridge` 5 处反向依赖完全逃逸
- **修复**: 去掉末尾斜杠 → `internalPrefix("internal/module")`

### P0-2 ⚠️ Event Type ID 1400 冲突 (Cron vs Workspace)
- `internal/dto/shared/event.go:38` — `EventTypeCronJobRunStateChanged = 1400`
- `cmd/mcp-orch/workspace/event.go:12` — `EventTypeWorkspaceRunCreated = 1400`
- **后果**: 同一 dispatcher 上两个事件类型 ID 相同，会导致错误分发、运行时 panic 或数据损坏

### P0-3 ⚠️ hookConsumer 绕过状态机直接赋值 agent.state
- `cmd/mcp-orch/orchestration/hook_consumer.go:227` — `agent.state = nextState`
- `cmd/mcp-orch/orchestration/hook_consumer.go:248` — `agent.state = agentdto.StateStopped`
- **后果**: 状态机内部状态与 agent.state 脱节，Guard/Permit 校验失效

---

## 三、各领域详细违规

### 3.1 fx 契约 (8 项)

| # | 严重度 | 文件 | 描述 |
|---|--------|------|------|
| 1 | 中 | `internal/app/app.go:24-43` | NewLogger constructor 做文件 I/O (os.UserHomeDir, InitWithFile) |
| 2 | 中 | `cmd/mcp-orch/runtime.go:61-72` | newLogger constructor 调用 os.OpenFile |
| 3 | 中 | `internal/app/app.go:115` | 生产代码使用 fx.Populate (RunDesktop) |
| 4 | 低 | `internal/module/fbsd/module.go:34-46` | NewTrackerFromEnv constructor 调用 os.UserHomeDir/Hostname |
| 5 | 低 | `cmd/mcp-orch/runtime.go:234-248` | newRegistry 5 参数未用 fx.In |
| 6 | 低 | `cmd/mcp-orch/notify/module.go:100` | provideOrchFlusher 4 参数未用 fx.In |
| 7 | 低 | `internal/module/notify/module.go:76` | provideFlusher 4 参数未用 fx.In |
| 8 | 低 | `internal/module/memory/module.go:407` | provideMemoryService 4 参数未用 fx.In |

### 3.2 rungroup 契约 (11 项)

| # | 严重度 | 文件 | 描述 |
|---|--------|------|------|
| 1 | 高 | `cmd/mcp-orch/orchestration/service.go:335` | WakeupDispatcher 在 fx.OnStart 启动长跑 goroutine 绕过 run.Group |
| 2 | 高 | `cmd/mcp-orch/orchestration/wakeup_reclaim.go:157` | WakeupReclaimer 同上（已实现 Runner 接口但未注入） |
| 3 | 中 | `cmd/mcp-orch/notify/subscribers.go:77` | DAGNotifier worker 绕过 run.Group |
| 4 | 中 | `cmd/mcp-orch/tools/orchestration_tools.go:92` | HandleLaunchAgent async go func 无 drain |
| 5 | 低 | `cmd/mcp-ida/fx.go:118` | sidecar context.Background() 脱离 owner |
| 6 | 低 | `cmd/mcp-lsp/fx.go:237` | 同上 |
| 7 | 低 | `cmd/mcp-orch/runtime.go:365` | 同上 |
| 8 | 低 | `cmd/mcp-orch/orchestration/service_launcher_bridge.go:150` | submitInitialLaunchPromptAsync 无 drain |
| 9 | 低 | `internal/module/skill/events.go:65` | safego.Go(context.Background()) |
| 10 | 低 | `internal/module/thread/service.go:406` | safego.Go(context.Background()) |
| 11 | 低 | `pkg/logger/fields.go:61` | pkglogger.Fatal 间接 os.Exit（当前无调用者） |

### 3.3 jrpc2 契约 (46+ 项)

**裸 error — 12 处 (高)**:
- `internal/module/turn/rpc_helpers.go:65,87,292,295` — 返回 errors.New 未包装 jrpc2.Error
- `internal/module/thread/rpc.go:469,483` — errModelSetArgsConflict/errApprovalsSetArgsConflict
- `internal/platform/rpc/handler.go:25,29,36,137` — CapabilityResolver 返回裸 error
- `cmd/mcp-orch/workspace/factory.go:333` — updateRunStatusAndEmit
- `internal/module/thread/rpc_types.go:329` — decodeMessagesBefore

**使用 jrpc2 保留码 — 8 处 (中)**:
- `internal/module/skill/rpc.go:127,131,146`
- `internal/module/cron/rpc.go:193,243`
- `internal/module/dashboard/insights_rpc.go:76,79`
- `internal/mcpserver/common/bootstrap/lifecycle.go:174`

**无 typed response (map[string]any) — 17+ 处 (中)**:
- `internal/module/thread/rpc.go` — buildStartResponse, buildPromoteTaskResponse, newForkHandler, newHandoffHandler, newRecoverHandler, newResumeHandler 等 6 处
- `cmd/mcp-orch/orchestration/rpc.go` — 3 处
- `internal/module/cron/rpc.go` — 4 处
- `internal/module/skill/rpc.go:287`, `internal/module/feedback/rpc.go:42`, `internal/module/dashboard/rpc.go` 多处

**json.RawMessage — 7 处 (低-中)**:
- `internal/module/thread/rpc_types.go:38,43`
- `internal/module/cron/rpc.go:30`, `internal/module/feedback/rpc.go:19`
- `internal/platform/rpc/server.go:188,255`, `internal/platform/rpc/handler.go:62`

### 3.4 stateless + event 契约 (10 项)

| # | 严重度 | 文件 | 描述 |
|---|--------|------|------|
| 1 | **高** | `hook_consumer.go:227,248` | 绕过状态机直接赋值 (= P0-3) |
| 2 | **高** | `event.go:38` vs `workspace/event.go:12` | EventTypeID 1400 冲突 (= P0-2) |
| 3 | **高** | `orchestration/service.go:227-275` | 事件 handler 中直接操作状态机 |
| 4 | 中 | `internal/dto/agent/state.go` | 状态/触发器常量用裸 string 非命名类型 |
| 5 | 中 | `cmd/mcp-orch/workspace/event.go` | 事件 ID 未纳入共享常量文件 |
| 6 | 中 | `orchestration/helpers.go:296-304` | Publish 在持有 s.mu 锁期间调用 |
| 7 | 低 | `turn/tracker.go:215,236,267` | sm.Fire 错误被 `_ =` 静默丢弃 |
| 8 | 低 | platform/statemachine vs util/statemachine | factory 代码完全重复两份 |
| 9 | 低 | `statemachine/factory.go:28-44` | accessor/mutator 无自身锁保护 |
| 10 | 低 | workspace/service.go vs contract/bus.go | newEmitter 三份重复 |

### 3.5 模块化契约 (66+ 项) — 最严重领域

**依赖方向违规 (30+ 处)**:
- `platform/toolbridge` → module (5处)、provider (4处)、store (3处) — **三重违反**
- `platform/cachekeepalive` → store (2处)
- `provider/claudecli` → module (4处)
- `provider/codexapp` → module (7处)
- `provider/unified` → module (2处) + store (2处)
- `cmd/mcp-orch` → module (2处) + store (2处)

**module 间横向 import (14 对)**:
cron→thread/turn, dashboard→insight/skill, fbsd→skilllibrary, insight→turn/observation, prompt→skilllibrary, skilllibrary→skillforge, thread→prompt/turn, turn→memory/skill, uistate→skilllibrary/thread

**包文件数超限**: memory(33), thread(32), turn(31), codexapp(31), mcp-orch/orchestration(27)
**文件行数超限**: 12 个文件 (cron/scheduler.go 719行最严重)

**archtest 缺陷**:
1. rule4 尾部斜杠 bug 致检测完全失效 (= P0-1)
2. mcp-orch 白名单含 module/store 与契约矛盾
3. rule3 遗漏 unified 子目录

### 3.6 洋葱架构 (5 项偏差)

archtest 全部 PASS。基于契约文档零违规。基于更严格解读有 5 个偏差:
- contract 导入 pkg/logger (1处)
- contract 导入 dto/* (13处，语义合理)
- dto 子包间互引 (合理)
- module 导入 internal/util/* (12个子包)
- module 导入 pkg/* (logger/skillmetrics)

### 3.7 sqlc 契约 (12 项)

| # | 严重度 | 描述 |
|---|--------|------|
| 1 | 高 | `sqlc.yaml` query_parameter_limit=1 应为 0 |
| 2 | 高 | `skill_candidate.sql` 全部 7 个查询用 SELECT*/RETURNING* |
| 3 | 高 | `hookstore` 8 个 raw SQL 操作绕过 sqlc |
| 4 | 中 | schema 逐文件枚举，35 个 migration 未纳入 |
| 5 | 中 | 缺少 strict_order_by: true |
| 6 | 中 | initialisms 缺 dag/rpc/sql/cwd |
| 7 | 中 | prompt store 用 reflect 调用 WithTx |
| 8 | 低 | 缺少 uuid 类型 override |
| 9 | 低 | 额外 omit_unused_structs 偏离标准 |
| 10 | 低 | ClaimDueJobs 含 FOR UPDATE 无后缀 |
| 11 | 低 | Querier 未通过 fx Provide |
| 12 | 低 | 缺少 repositories/ 目录 |

### 3.8 MCP 服务契约 (8 项)

| # | 严重度 | 描述 |
|---|--------|------|
| 1 | 严重 | cmd/mcp-orch → internal/store (runtime.go:30-32) |
| 2 | 严重 | cmd/mcp-orch → internal/module (notify/module.go:13-14) |
| 3 | 中等 | LeaseKey 使用嵌套 lease 对象，未展平 |
| 4 | 中等 | mcp-ida 缺失 stdio 工具执行通道 |
| 5 | 中等 | 双通道启动顺序不可控（并发启动无保序） |
| 6 | 轻微 | bootstrap client 依赖超出 jrpc2+dto+stdlib |
| 7 | 轻微 | 9 个文件超 400 行限制 |
| 8 | 轻微 | orchestration 包 27 文件/7665 行严重超标 |

---

## 四、修复优先级建议

### 立即修复 (P0)
1. **archtest rule4 斜杠 bug** — 1 行改动，恢复守卫能力
2. **EventType ID 1400 冲突** — workspace 事件 ID 改为不冲突值并纳入共享常量
3. **hookConsumer 绕过状态机** — 改为通过 sm.Fire/sm.FireCtx 驱动

### 短期修复 (P1)
4. jrpc2 裸 error → 全部包装为 jrpc2.Errorf
5. jrpc2 保留码 → 替换为自定义 CodeInvalidParams
6. WakeupDispatcher/Reclaimer → 改为 Runner 注入 run.Group
7. archtest mcp-orch 白名单 → 与契约对齐
8. archtest rule3 → 覆盖 unified 子目录
9. skill_candidate.sql → 显式列清单替换 SELECT*

### 中期治理 (P2)
10. map[string]any response → 逐步替换为 typed struct
11. toolbridge 依赖方向 → 重构为接口依赖
12. module 间横向 import → 抽取到 contract 层
13. 超限文件/包 → 拆分重构
14. sqlc.yaml → 对齐契约标准配置

---

## 五、合规亮点

以下方面项目做得很好，值得保持:
- **fx value group 收集 Runner** — 统一模式，无手动注册
- **handler.Map 声明式路由** — 全部 7 个模块合规
- **StrictHandler 封装** — 生产代码无裸 handler.New
- **run.Group execute/interrupt** — 核心模型正确实现
- **信号处理 actor 化** — 主进程合规，sidecar 合理禁用
- **事件总线注入式 Dispatcher** — 无全局 event.Default
- **订阅取消函数保留** — OnStop 正确清理
- **sqlc 生成代码未被手动修改** — 29 个文件全部干净
- **cmd 之间无交叉依赖** — mcp-orch/mcp-lsp/mcp-ida 隔离良好
- **洋葱架构核心层** — contract/dto 依赖极度干净
