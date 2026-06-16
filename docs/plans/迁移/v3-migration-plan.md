# V3 迁移方案

> 日期：2026-03-19
> 迁移源：`github.com/multi-agent/go-agent-v2`
> 迁移目标：`/Volumes/bot/super-agent-v3`
> 目标文件：`docs/plans/迁移/v3-migration-plan.md`
> 决策前提：6 个框架已批准；Provider 双端收敛已批准；MCP 工具路径统一已批准；新增约束为 MCP Server 必须拆成 3 个独立预编译工具家族二进制

---

## 1. 迁移总览

### 1.1 结论

V3 不应在 V2 仓库上原地重构，而应采用迁移模式：

1. V2 仓库继续稳定运行，作为行为参考系和回归基线。
2. V3 仓库从 0 长出新的包边界、生命周期和框架骨架。
3. 每一批迁移都以“把 V2 某一块逻辑变成 V3 的显式模块”为目标，而不是在 V2 上做薄封装。
4. 最终切换发生在 V3 已通过完整契约验证之后，而不是“重构到一半上线”。

这不是“重写一遍试试看”。
这是“用旧系统做规格书，用新系统做实现”。

### 1.2 为什么不是原地重构

| 维度 | 原地重构 | 从零迁移 | 结论 |
|---|---|---|---|
| 包边界重建 | 需要在 God Object 上边改边拆 | 可直接按新边界落包 | 从零迁移胜出 |
| 生命周期重构 | `initStores()`、`sync.WaitGroup`、`KillAll()` 需要边运行边换 | 可从第一天就是 `fx + run.Group` | 从零迁移胜出 |
| RPC 重构 | 151（含 23 noop）个方法需维持旧注册链和新注册链并存 | 新仓库直接 `jrpc2 handler.Map` | 从零迁移胜出 |
| Store 重构 | 20 个手写 Store 与 sqlc 生成代码交错存在 | 新仓库直接 `sqlc + repo adapter` | 从零迁移胜出 |
| Provider 收敛 | 旧仓库里要同时维护 Codex/Claude 双链路兼容 | 新仓库可直接以统一 Provider 为唯一模型 | 从零迁移胜出 |
| 状态机显式化 | 旧逻辑散在 4 个文件且带副作用 | 新仓库可先建迁移表再实现动作 | 从零迁移胜出 |
| 风险隔离 | 改一处可能影响线上路径 | V2 完整保留，V3 独立试错 | 从零迁移胜出 |
| 进度可见性 | 代码在原地拆散，阶段成果不清晰 | 每个批次都对应一个可运行模块 | 从零迁移胜出 |

核心原因不是“V2 差”，而是 V2 的痛点都集中在“基础骨架层”：

- `go-agent-v2/internal/apiserver`：17,199 行生产代码。
- `go-agent-v2/legacy-agentsdk`：13,979 行生产代码。
- `go-agent-v2/pkg/toolsdk`：16,891 行生产代码。
- 仅这三块合计：48,069 行。
- `go-agent-v2/internal/apiserver` + `go-agent-v2/internal/runner` + `go-agent-v2/internal/store` + `go-agent-v2/internal/bus` 合计：24,396 行。
- 当前实际 JSON-RPC 方法数：151（含 23 noop）。
- `withRequiredThreadID(...)` 当前实际调用点：16 处。
- `context.WithTimeout(...)` 在主要目录中的显式调用点：51 处。

这说明 V2 的主要成本不是“单个业务函数难写”，而是“基础设施和横切逻辑已经渗入所有业务路径”。

### 1.3 迁移模式的核心思路

迁移模式不是复制粘贴，而是四条主线并行：

1. **V2 继续跑**
   V2 只接收 bugfix、数据修复和回归补洞，不再做架构级改造。
2. **V3 从 0 长**
   V3 先搭骨架，再按依赖顺序把逻辑迁过去。
3. **旧代码当规格书**
   V2 的 golden、contract、matrix、behavior 测试只保留“行为约束”，不保留文件形状约束。
4. **按批次切，不按 package 名义切**
   每一批都以一个“可闭环的运行面”结束，例如 Store 闭环、状态机闭环、Provider 闭环。

### 1.4 迁移模式的工作流

```text
V2 主线：继续承载现有行为
  ├── 仅接受 bugfix / 数据修复 / 行为补测试
  └── 作为 V3 行为对照源

V3 主线：从 0 开始显式长出
  ├── P0 建骨架：fx / run.Group / sqlc / 目录树
  ├── P1 建数据层：20 个 Store → sqlc + repo
  ├── P2 建事件层：typed event bus
  ├── P3 建状态机：runner 显式迁移表
  ├── P4 建 Provider：Codex / Claude 收敛
  ├── P5 建 RPC：151（含 23 noop）方法统一注册
  ├── P6 建入口：Wails + jrpc2 + run.Group
  ├── P7 建辅助业务模块：workspace / skills / dashboard / uistate / lspgui
  ├── P8 抽取 MCP 编排工具：`cmd/mcp-orch` 独立服务
  ├── P9 抽取 MCP LSP 工具：`cmd/mcp-lsp` 独立服务
  └── P10 丰满 Two-Zone 工厂：shared / naming / arch guard 收口

验证主线：始终先于切换
  ├── 行为规格迁移
  ├── golden / contract / matrix 回归
  ├── shadow replay / canary
  └── 最终切换
```

### 1.5 从零迁移而不是“新旧混跑一个二进制”的理由

V3 不采用“一个二进制内同时跑 V2/V3 两套骨架”的理由：

- `fx` 需要一套明确的对象图；V2 的手动接线会把图重新打散。
- `run.Group` 需要统一 actor 生命周期；V2 的散落 `SafeGo` 会绕过它。
- `sqlc` 需要让服务层只看 repo 接口；V2 的手写 Store 会重新把 SQL 细节暴露上去。
- `stateless` 需要唯一状态源；V2 的 `proc.State` / `effectiveState` 双状态表示会继续污染模型。
- `jrpc2` 需要统一注册表；V2 的 `go-agent-v2/internal/apiserver/methods*.go` / `dashrpc` / inline route 会形成第二套注册链。

所以 V3 的正确策略不是“在 V2 旁边挂一层 adapter”，而是“在 V3 里把 adapter 作为短期迁移工具，最后消失”。

### 1.6 目标代码量预估

V2 官方口径是 87,900 行核心产品代码。
V3 的目标是通过框架替代手写骨架、Provider 双端收敛、MCP 三家族拆分、typed event 消除路由样板，将手写核心代码压缩到 **30,000 - 40,000 行**（减少 54% - 66%）。

| 口径 | V2 | V3 目标 | 说明 |
|---|---:|---:|---|
| 手写核心代码 | 87,900 | 30,000 - 40,000 | 预计减少 54% - 66% |
| sqlc 生成代码 | 0 | 8,000 - 10,000 | 只在 `internal/store/sqlc/` |
| 仓库总代码量 | 87,900 | 38,000 - 50,000 | 包含少量生成代码 |
| `go-agent-v2/internal/apiserver` 等价层 | 17,199 | 3,000 - 4,500 | `jrpc2` handler.Map + 中间件消灭手写注册/上下文包装/nil-guard |
| `go-agent-v2/internal/store` 等价层 | 3,112 | 800 - 1,200 手写 + 生成代码 | 手写 repo 极薄，SQL 全量生成化 |
| `go-agent-v2/internal/runner` 等价层 | 3,333 | 1,200 - 1,800 | `stateless` 声明式迁移表 + `run.Group` actor 替代手写状态管理 |
| `go-agent-v2/legacy-agentsdk + go-agent-v2/internal/apiserver/codexadapter` 等价层 | 17,500+ | 4,000 - 5,500 | 双端统一消灭平行路径 + MCP manifest 替代 DynamicTools 链路 |
| `go-agent-v2/pkg/toolsdk` 等价层 | 16,891 | 8,000 - 10,000 | 三家族拆分后消除跨族样板、registry/schema/dispatch 统一 |

### 1.7 V3 如何实现 3-4W 行目标

V2 的 87,900 行中，真实业务复杂度和可消除的骨架样板各占多大比例，决定了压缩空间。
逐层拆解：

**可深度压缩的骨架层（预计消除 35,000 - 45,000 行）：**

- `go-agent-v2/internal/apiserver` 17,199 行 → `jrpc2` handler.Map + 中间件替代手写注册链、nil-guard 汇总、上下文包装、God Object server，预计压缩到 3,000 - 4,500 行。
- `go-agent-v2/legacy-agentsdk` + `codexadapter` 17,500+ 行 → Provider 双端收敛消灭平行实现路径，预计压缩到 4,000 - 5,500 行。
- `go-agent-v2/pkg/toolsdk` 16,891 行 → 三家族拆分后消除跨族样板、registry/schema/dispatch 统一，预计压缩到 8,000 - 10,000 行。
- `go-agent-v2/internal/store` 3,112 行 → `sqlc` 全量生成，手写 repo 极薄化，预计压缩到 800 - 1,200 行。
- `go-agent-v2/internal/runner` 3,333 行 → `stateless` 声明式状态机替代手写状态管理，预计压缩到 1,200 - 1,800 行。
- `go-agent-v2/internal/bus` 散落事件路由 → `kelindar/event` typed event 消灭 `map[string]any` payload 和字符串 topic 样板。

**保留但精简的真实复杂度（预计 13,000 - 18,000 行）：**

- LSP 工具核心逻辑（协议、搜索、编辑、replace-range）：真实复杂度，但消除 dispatch/display 样板后可压缩。
- IDA 工具域：独立复杂域，保留核心但去除跨族冗余。
- Wails/UI/Dashboard：视图层保留，但 `fx` 装配替代手写接线后显著减薄。
- Workspace/Orchestration/DAG：业务编排保留，状态机框架减少手写状态管理。

**关键压缩手段总结：**

1. 框架替代骨架 — `fx`/`jrpc2`/`stateless`/`sqlc`/`run.Group`/`kelindar/event` 六框架各砍一层手写样板。
2. 双端收敛 — Provider 统一消灭 Claude/Codex 平行代码路径。
3. 三族拆分 — MCP family 编译隔离消灭跨族运行时注册和 schema 冗余。
4. typed event — 消灭事件路由中的字符串 topic + `map[string]any` + 手写 dispatcher。
5. 极薄 repo — `sqlc` 承担 SQL，手写层只保留领域语义聚合。

### 1.8 总工期预估

| 配置 | 工期 | 人天 | 说明 |
|---|---:|---:|---|
| 1 人主导 | 17 - 19 周 | 82 - 95 人天 | 风险最低，但反馈周期长 |
| 2 人并行 | 10 - 12 周 | 82 - 95 人天 | 推荐方案 |
| 3 人并行 | 8 - 10 周 | 90 - 110 人天 | 只有在接口冻结后才划算 |

推荐配置：

- 1 名主设计/主集成人员。
- 1 名并行工程师负责 Store / Tool / Provider 子面。
- V2 仓库 feature freeze 到“仅 bugfix + 契约补测试”。

### 1.9 里程碑级时间预算

| 阶段 | 周期 | 交付物 |
|---|---:|---|
| W1-W2 | 2 周 | P0 骨架、P1 schema/sqlc 首批落地 |
| W3-W4 | 2 周 | P1 完整 repo、P2 typed bus |
| W5-W6 | 2 周 | P3 runner 状态机 |
| W7-W8 | 2 周 | P4 unified provider |
| W9-W10 | 2 周 | P5 jrpc2 RPC 主面 |
| W11 | 1 周 | P6 入口与 Wails 集成 |
| W12 | 1 周 | P7 辅助业务模块（workspace / skills / dashboard / uistate / lspgui） |
| W13 | 1 周 | P8 `cmd/mcp-orch` 独立服务抽取 |
| W14-W15 | 2 周 | P9 `cmd/mcp-lsp` 独立服务抽取 |
| W16 | 1 周 | P10 Two-Zone/shared 丰满化 |

---

## 2. V3 包结构设计

### 2.1 设计原则

V3 的包结构必须满足 7 条硬约束：

1. `fx` 只存在于装配层和入口层。
2. `run.Group` 只负责编排长跑 actor，不承担依赖注入。
3. `sqlc` 生成代码只在数据库基础设施层出现。
4. Provider transport 和业务服务彻底分离。
5. RPC handler 不直接操作 SQL，不直接操作 provider transport。
6. 所有跨 goroutine 的状态变更都走显式状态机或 typed event。
7. MCP Server 不再是一个混编二进制，而是 3 个独立预编译家族二进制。

### 2.2 顶层目录树

```text
cmd/
├── agent-terminal/
│   └── main.go
├── mcp-lsp/
│   ├── main.go
│   ├── fx.go
│   ├── tools/
│   ├── adapter/
│   └── mcpserver/
├── mcp-orch/
│   ├── main.go
│   ├── fx.go
│   ├── tools/
│   ├── adapter/
│   └── mcpserver/
└── mcp-ida/
    ├── main.go
    └── fx.go

internal/
├── app/                 ← fx 聚合 + desktop/bootstrap
│   ├── app.go
│   ├── modules.go
│   ├── runner.go
│   └── thread_orchestration_adapter.go
├── contract/            ← 当前纯接口合同
│   ├── approval.go
│   ├── provider.go
│   └── session_resolver.go
├── dto/                 ← 当前已落地的数据结构
│   ├── agent/
│   │   ├── event.go
│   │   ├── guard.go
│   │   └── state.go
│   ├── provider/
│   │   ├── capability.go
│   │   ├── event.go
│   │   ├── manifest.go
│   │   ├── message.go
│   │   ├── session.go
│   │   ├── thread.go
│   │   ├── thread_config.go
│   │   └── turn.go
│   ├── shared/
│   │   ├── errors.go
│   │   ├── event.go
│   │   ├── ids.go
│   │   └── input.go
│   ├── task/
│   │   └── event.go
│   ├── tool/
│   │   └── event.go
│   ├── turn/
│   │   ├── event.go
│   │   └── model.go
│   ├── ui/
│   │   └── event.go
│   └── workspace/
│       └── event.go
├── platform/            ← 基础设施层（原 infra/）
│   ├── config/
│   │   ├── module.go
│   │   ├── config.go
│   │   ├── provider.go
│   │   └── timeouts.go
│   ├── db/              ← pgxpool + sqlc + migrations
│   │   ├── module.go
│   │   ├── pool.go
│   │   ├── migrate.go
│   │   ├── tx.go
│   │   ├── query_guard.go
│   │   ├── sqlc.yaml
│   │   └── queries/
│   ├── bus/             ← kelindar/event
│   │   ├── module.go
│   │   ├── bus.go
│   │   ├── typed.go
│   │   └── subscription.go
│   ├── rpc/             ← jrpc2 server
│   │   ├── module.go
│   │   ├── server.go
│   │   ├── codec.go
│   │   ├── errors.go
│   │   ├── request_context.go
│   │   └── transport_ws.go
│   ├── runner/          ← oklog/run group
│   │   ├── module.go
│   │   ├── group.go
│   │   └── lifecycle.go
│   ├── statemachine/    ← stateless factory
│   │   ├── module.go
│   │   └── factory.go
│   └── shared/          ← Rule of Two 门槛的跨模块共享工具
│       ├── retry.go
│       ├── validation.go
│       └── idgen.go
├── store/               ← 数据访问层
│   ├── sqlc/            ← sqlc 生成代码（只读）
│   │   ├── models.go
│   │   ├── querier.go
│   │   └── *.sql.go
│   ├── thread/
│   │   ├── module.go
│   │   ├── contract.go
│   │   └── store.go
│   ├── task/
│   │   ├── module.go
│   │   ├── contract.go
│   │   └── store.go
│   ├── workspace/
│   │   ├── module.go
│   │   ├── contract.go
│   │   └── store.go
│   ├── sharedfile/
│   │   ├── module.go
│   │   ├── contract.go
│   │   └── store.go
│   ├── prompt/
│   │   ├── module.go
│   │   ├── contract.go
│   │   └── store.go
│   ├── binding/
│   │   ├── module.go
│   │   ├── contract.go
│   │   └── store.go
│   └── ...
├── provider/            ← Provider 收敛层（保持不变）
│   ├── unified/
│   │   ├── client.go
│   │   ├── session.go
│   │   ├── turn.go
│   │   ├── event_map.go
│   │   ├── mcp_manifest.go
│   │   ├── capabilities.go
│   │   └── thread_config.go
│   ├── claudecli/
│   │   ├── driver.go
│   │   ├── transport.go
│   │   ├── event_map.go
│   │   └── history.go
│   └── codexapp/
│       ├── driver.go
│       ├── transport.go
│       ├── event_map.go
│       ├── recovery.go
│       └── history.go
├── module/              ← 业务域（每个自带 fx.Module）
│   ├── thread/          ← module.go + contract.go + service.go
│   │   ├── module.go
│   │   ├── contract.go
│   │   ├── service.go
│   │   ├── rpc.go
│   │   └── events.go
│   ├── turn/
│   │   ├── module.go
│   │   ├── contract.go
│   │   ├── service.go
│   │   └── review.go
│   ├── skill/
│   │   ├── module.go
│   │   ├── contract.go
│   │   ├── service.go
│   │   └── loader.go
│   ├── orchestration/
│   │   ├── module.go
│   │   ├── contract.go
│   │   ├── service.go
│   │   ├── recover.go
│   │   └── runner_actor.go
│   ├── workspace/
│   │   ├── module.go
│   │   ├── contract.go
│   │   ├── service.go
│   │   └── rpc.go
│   ├── uistate/
│   │   ├── module.go
│   │   ├── contract.go
│   │   ├── runtime.go
│   │   └── projection.go
│   ├── lspgui/
│   │   ├── module.go
│   │   ├── contract.go
│   │   ├── service.go
│   │   └── rpc.go
│   └── dashboard/
│       ├── module.go
│       ├── contract.go
│       ├── service.go
│       └── rpc.go
├── mcpserver/           ← MCP 公共层（只保留共享 stdio/manifest）
│   ├── common/
│   │   ├── server.go
│   │   ├── stdio.go
│   │   └── manifest.go
├── ui/                  ← UI 视图层（保持不变）
│   ├── runtime/
│   │   ├── manager.go
│   │   ├── projection.go
│   │   ├── timeline.go
│   │   ├── tokens.go
│   │   └── workspace.go
│   └── dashboard/
│       ├── server.go
│       ├── sse.go
│       ├── code_open.go
│       └── state_groups.go
└── archtest/            ← 架构守护测试
    ├── dependency_direction_test.go
    └── fx_graph_test.go
```

### 2.3 每个包的职责定义

| 包 | 职责 |
|---|---|
| `cmd/agent-terminal` | Wails 桌面入口，只做装配与启动 |
| `cmd/mcp-lsp` | MCP LSP 独立服务，包含 `tools/`、`adapter/`、本地 server/runtime；schema/handler 只在本二进制中定义，并复用 V3 `internal/platform/{config,db}`、`internal/contract`、`internal/dto` |
| `cmd/mcp-orch` | MCP 编排独立服务，包含 `orchestration/`、`store/sqlc/`、`store/*`、`tools/`、本地 server/runtime；schema/handler 只在本二进制中定义，orchestration runtime、本地 store 层与本地 sqlc 层都在本二进制内持有 |
| `cmd/mcp-ida` | 预编译 IDA 家族二进制，隔离重依赖和特定平台能力 |
| `internal/app` | `fx` 模块聚合、对象图与 bootstrap 装配 |
| `internal/contract/*` | 纯接口、事件、常量；原 `port` 内联到这里，不承载实现 |
| `internal/dto/*` | 纯数据结构、状态、ID、错误载荷，不含框架依赖 |
| `internal/platform/config` | 统一配置与 timeout 常量 |
| `internal/platform/db` | pgx pool、事务、migrations、sqlc 配置与 query bootstrap |
| `internal/platform/bus` | `kelindar/event` 包装、typed event 订阅模型 |
| `internal/platform/runner` | `oklog/run` 宿主与 Runner 聚合 |
| `internal/platform/rpc` | `jrpc2` server、method registry、codec、transport、request context、approval 状态机、server→client push bridge |
| `internal/platform/statemachine` | `stateless` factory、状态机构造与护栏 |
| `internal/platform/shared` | 跨 2+ module 复用的纯工具；受 Rule of Two 和行数预算双重约束 |
| `internal/store/sqlc` | `sqlc` 生成代码，只读，不承载业务语义 |
| `internal/store/*` | 数据访问层；包装 `sqlc` 生成层、聚合领域仓储与事务语义 |
| `internal/provider/unified` | V3 唯一 Provider 语义层；两端收敛点 |
| `internal/provider/claudecli` | Claude CLI transport 与事件翻译 |
| `internal/provider/codexapp` | Codex app-server transport 与事件翻译 |
| `internal/module/thread` | 线程生命周期、读取、归档、配置、线程 RPC |
| `internal/module/turn` | turn prepare/runtime/tracker/review/interruption |
| `internal/module/skill` | skill 解析、匹配、装载、命令卡管理；不承载 MCP tool surface |
| `internal/sidecar/orch/orchestration` | P8 前的迁移源；P8 后删除，职责迁到 `internal/sidecar/orch/orchestration` |
| `internal/module/workspace` | 宿主内 workspace 领域服务、merge 与 UI-facing RPC；MCP workspace 能力在 `cmd/mcp-orch` 直接走 store |
| `internal/module/uistate` | UI projection、状态拼装、runtime 视图适配 |
| `internal/module/lspgui` | GUI/LSP 兼容 facade 与宿主侧 RPC；真实 LSP 执行留在 `cmd/mcp-lsp` |
| `internal/module/dashboard` | dashboard 领域服务与相关 RPC |
| `internal/module/core` | 已废弃，initialize 并入 `internal/platform/rpc/initialize.go`；approval/respond 并入 `internal/module/turn/rpc.go`；log/* 并入 `internal/platform/bus` sink |
| `internal/module/debug` | 已废弃，debug/compat RPC 并入 `internal/platform/rpc/debug.go` |
| `internal/mcpserver/common` | 历史共享 stdio/manifest/协议公共层；不再是 P8 `cmd/mcp-orch` 的目标态依赖 |
| `internal/ui/runtime` | UI 事件投影、timeline、workspace diff、token 视图 |
| `internal/ui/dashboard` | dashboard server、SSE、代码打开与视图状态 |
| `internal/archtest` | 依赖方向检查、`fx.ValidateApp`、架构守护测试 |

### 2.A Two-Zone DRY 在 V3 中的继承

#### 来源说明

`Two-Zone DRY` 最初来自 V2 原地重构方案 `docs/plans/2026-03-18-two-zone-dry-v2-executable.md`。
虽然 V2 最终转向“迁移模式”后，该方案不再作为 V2 的执行计划继续推进，但其中关于“跨包公共 DRY”和“包内领域 DRY”分区治理的核心思想，在 V3 中继续保留，只是承载位置从手写 `factory` 转成了框架和平台层。

#### Zone A 在 V3 的演化

V2 的 Zone A 目标，是把跨包复用、无业务领域知识、且不 import 业务包的原语收敛到独立的 factory 共享层。
V3 中，这一层不再以独立共享包形式存在，而是分别被框架和 `internal/platform/` 层吸收：

| V2 Zone A | V3 替代 | 说明 |
|---|---|---|
| legacy handler stub | `jrpc2` + `internal/platform/rpc/` | `handler.New` / `handler.Map` 直接替代 typed handler 工厂 |
| legacy state-machine stub | `stateless` + `internal/platform/statemachine/` | 声明式状态机直接替代手写 FSM 迁移引擎 |
| legacy schema stub | `cmd/mcp-*/tools/*` + 各自本地 manifest/runtime | tool schema 与 manifest 收敛到独立服务本地包 |
| legacy retry stub | `internal/platform/shared/retry.go` | 仍然保留为跨模块共享 helper |

因此，V3 的 Zone A 等价物不是新的中央工厂包，而是 `internal/platform/` 层中的框架承接点。
其中只有满足 Rule of Two 的纯 helper，才进入 `internal/platform/shared/`。

#### Zone B 在 V3 的演化

V2 的 Zone B 用 `internal/*/factory_*.go` 在原 package 内收敛领域内重复。
V3 中，这个模式演变为 `internal/module/*/` 的自治模块模式：每个 module 自带 `module.go`、`contract.go`、`service.go`，必要时补 `rpc.go`、`events.go`、`loader.go` 等专用文件。

```go
package thread

import "go.uber.org/fx"

var Module = fx.Module("thread",
	fx.Provide(NewService),
	fx.Invoke(RegisterRPC),
)
```

由于 `fx.Module`、`jrpc2`、`stateless` 等框架已经提供统一骨架，模块内 DRY 一般不再需要 `factory_` 前缀文件。
但如果某个 module 内部确实存在重复模式，仍然应该优先留在该 module 内，通过 `helpers.go` 或 `patterns.go` 解决，而不是过早上提到共享层。

#### Rule of Two 在 V3 的继承

Rule of Two 在 V3 中保持不变，仍然是跨模块抽象唯一允许的晋升条件：

- 抽象先留在 module 内。
- 至少被 2 个 module 稳定复用，才允许提升到 `internal/platform/shared/`。
- `internal/platform/shared/` 不得 import 任何 `internal/module/*`。
- `internal/platform/shared/` 单文件 ≤500 行，目录总行数 ≤2000 行。

是否“稳定复用”，仍以垂直切片验证和行为等价性为准，而不是凭主观预判提前抽象。

#### 防巨石包规则在 V3 的继承

`internal/platform/shared/` 是 V3 唯一允许的“跨模块共享工具”聚集点，因此同时受 Rule of Two 门槛和行数预算约束：

- 该目录只接受纯工具，不接受 module 级业务语义。
- 该目录同时受复用门槛和预算门槛双重限制。
- 一旦超出预算，必须拆分为更具体的 `internal/platform/xxx/` 子包，而不是继续向 `shared/` 堆叠。

#### V2 → V3 Two-Zone 完整映射表

| V2 Two-Zone 项 | V3 等价物 | 说明 |
|---|---|---|
| Zone A legacy handler stub | `jrpc2` + `internal/platform/rpc/` + `internal/module/*/rpc.go` | 框架提供 handler 装配，模块保有业务 handler |
| Zone A legacy state-machine stub | `stateless` + `internal/platform/statemachine/` | 平台层负责状态机构造，模块层负责规则与动作 |
| Zone A legacy schema stub | `cmd/mcp-*/tools/*` + 各自本地 manifest/runtime | schema 收敛到独立 MCP 服务本地包 |
| Zone A legacy retry stub | `internal/platform/shared/retry.go` | 保留为跨模块共享纯 helper |
| Zone B `internal/apiserver/factory_handler.go` | `internal/platform/rpc/` + `internal/module/*/rpc.go` | 中间件留在平台层，领域 handler 留在 module |
| Zone B `internal/store/factory_repo_*.go` | `internal/store/*/store.go` | `sqlc` 生成查询，手写 repo wrapper 变薄 |
| Zone B `internal/runner/factory_event_rules.go` | `internal/sidecar/orch/orchestration/` + `internal/platform/statemachine/` | 声明式规则在编排模块，状态机宿主在平台层 |
| Zone B `internal/*/factory_*.go` | `internal/module/*/{module.go, contract.go, service.go}` + optional `helpers.go` / `patterns.go` | 模块自治替代原 `factory_*.go` 文件群 |

### 2.4 包依赖方向图

```text
cmd/agent-terminal
  ↓
internal/app
  ↓
ui/*  provider/*  module/*
  ↓      ↓          ↓
store/* contract/* platform/*
  ↓      ↓          ↓
store/sqlc        dto/*
  ↓
stdlib

cmd/mcp-*
  ↓
cmd/mcp-*/{tools,local runtime,local store,local sqlc}
  ↓
internal/contract/* + internal/dto/* + internal/platform/{config,db}
  ↓
stdlib
```

### 2.5 import 规则

1. `internal/contract` 和 `internal/dto` 不能 import `fx`、`jrpc2`、`pgx`、`wails`。
2. `internal/module/*` 不能在业务实现里 import `fx`；`fx.Module` 只允许出现在 `module.go`。
3. `internal/provider/claudecli` 和 `internal/provider/codexapp` 不能直接 import `internal/store/*`。
4. `internal/platform/*` 不能 import `internal/module/*`。
5. `internal/store/*` 只能依赖 `internal/platform/db`、`internal/store/sqlc`、`internal/contract`、`internal/dto`。
6. `internal/module/*` 不得承载 MCP tool schema、dispatcher、stdio runtime；这些实现只能位于 `cmd/mcp-*/*`。
7. `cmd/mcp-*` 允许 import `internal/contract/*`、`internal/dto/*`、`internal/platform/{config,db}` 与各自本地包；不得 import `internal/module/*`；P8 的 `cmd/mcp-orch` 还必须禁止 import `internal/store/*` 与 `internal/store/sqlc/*`。
8. `cmd/mcp-* -> internal/*` 只能单向复用；`internal/*` 禁止反向 import `cmd/mcp-*`。
9. `cmd/mcp-lsp` 不能 import `cmd/mcp-orch`、`cmd/mcp-ida` 或其他 `cmd/*`。
10. `cmd/mcp-orch` 不能 import `cmd/mcp-lsp`、`cmd/mcp-ida` 或其他 `cmd/*`。
11. `cmd/mcp-ida` 不能 import `cmd/mcp-lsp`、`cmd/mcp-orch` 或其他 `cmd/*`。
12. 三个 MCP family 只允许共享 `internal/platform/{config,db}`、`internal/contract/*`、`internal/dto/*` 等纯共享层。
13. P8/P9 落地前必须把上述 MCP 新依赖方向规则写进 archtest；不能等到 P10 再补守卫。
14. 只有 `internal/app`、`internal/platform/*/module.go`、`internal/store/*/module.go`、`internal/module/*/module.go` 和 `cmd/*` 可以 import `fx` 模块清单。
15. 所有 timeout 常量统一定义在 `internal/platform/config/timeouts.go`。

### 2.6 三阶段生命周期在代码中的落点

```go
package app

import (
	"context"

	"github.com/oklog/run"
	"go.uber.org/fx"
)

type Runner interface {
	Run(ctx context.Context) error
}

type In struct {
	fx.In
	Lifecycle fx.Lifecycle
	Runners   []Runner `group:"runners"`
}

func BindRuntime(in In) {
	var g run.Group
	for _, r := range in.Runners {
		r := r
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(
			func() error { return r.Run(ctx) },
			func(error) { cancel() },
		)
	}

	// signal actor
	// ...

	in.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() { _ = g.Run() }()
			return nil
		},
		OnStop: func(context.Context) error {
			return nil
		},
	})
}
```

三阶段语义是：

1. `fx Build + Start`：只负责构造资源、绑定 hook、初始化连接。
2. `run.Group Run`：只负责跑 actor，任何一个退出就触发全局收敛。
3. `fx Stop`：逆序释放资源，关连接、关文件、关进程。

### 2.7 RPC 注册方式

```go
package handlers

import (
	"context"

	"github.com/creachadair/jrpc2/handler"
)

type ThreadService interface {
	List(context.Context, ThreadListRequest) (ThreadListResponse, error)
}

func ThreadMap(svc ThreadService) handler.Map {
	return handler.Map{
		"thread/list": Wrap(ThreadScope(), Validate(), svc.List),
	}
}

func Registry(parts ...handler.Map) handler.Map {
	out := handler.Map{}
	for _, part := range parts {
		for name, h := range part {
			out[name] = h
		}
	}
	return out
}
```

V2 里的 `withRequiredThreadID` 16 处重复，在 V3 中统一变成 `ThreadScope()` 中间件。

### 2.8 与 V2 的对照表

| V2 区域 | V3 归宿 | 变化 |
|---|---|---|
| `go-agent-v2/internal/apiserver` | `internal/platform/rpc`, `internal/module/*`, `internal/provider/*`, `internal/ui/*`, `internal/app` | 拆掉 God Object，RPC/模块/provider/UI 分层 |
| `go-agent-v2/internal/runner` | `internal/sidecar/orch/orchestration` + `internal/platform/statemachine` + `internal/platform/runner` | 状态机显式化，actor 宿主与业务编排分离 |
| `go-agent-v2/internal/store` | `internal/platform/db`, `internal/store/sqlc`, `internal/store/*` | SQL 生成化，仓储回到独立 Store 层 |
| `go-agent-v2/internal/bus` | `internal/platform/bus` | 事件改为 typed event |
| `go-agent-v2/internal/uistate` | `internal/ui/runtime`, `internal/module/uistate` | UI 投影与业务状态拼装分离 |
| `go-agent-v2/internal/service` | `internal/module/*` | 保留职责，但按业务域重组 |
| `go-agent-v2/internal/mcp` | `cmd/mcp-*/local runtime/server` | 协议层与具体服务一并迁到独立二进制本地包 |
| `go-agent-v2/legacy-agentsdk` | `internal/provider/*` + `internal/module/turn|thread|skill` + `internal/contract` + `internal/dto` | provider-specific 与 provider-neutral 逻辑拆开 |
| `go-agent-v2/pkg/toolsdk` | `cmd/mcp-lsp/*` + `cmd/mcp-orch/*` + `cmd/mcp-ida/*` | 按独立服务重编译，不进入 V3 核心模块层 |
| `go-agent-v2/cmd/mcp-server` | `cmd/mcp-lsp`, `cmd/mcp-orch`, `cmd/mcp-ida` | 从混编改为三二进制 |

### 2.9 现有 V3 文档的继承关系

本方案直接承接并落地以下既有文档：

- [`2026-03-19-framework-selection.md`](../2026-03-19-framework-selection.md)
- [`2026-03-19-provider-convergence.md`](../2026-03-19-provider-convergence.md)
- [`fx-convention.md`](../../契约/fx-convention.md)
- [`jrpc2-convention.md`](../../契约/jrpc2-convention.md)
- [`stateless-convention.md`](../../契约/stateless-convention.md)
- [`rungroup-convention.md`](../../契约/rungroup-convention.md)
- [`sqlc-convention.md`](../../契约/sqlc-convention.md)
- [`event-convention.md`](../../契约/event-convention.md)

本文件的作用不是重复这些契约，而是把它们组织成一个可执行迁移计划。

---

## 3. Provider 统一方案

### 3.1 统一目标

V3 中不存在“Codex adapter 路线”和“Claude adapter 路线”两套业务层。
V3 只存在：

1. **统一 Provider 语义层**
   `internal/provider/unified`
2. **两套 transport driver**
   `internal/provider/claudecli`
   `internal/provider/codexapp`

换言之：

- 业务服务只看统一接口。
- 线程配置、turn prepare、skills、tool 路由、timeline 都在统一层。
- driver 只负责“怎么连”“怎么发”“怎么收”“怎么翻译事件”。

### 3.2 统一后分层

| 层 | 责任 | 是否 provider-specific |
|---|---|---|
| `service/turn` | 组装 prompt、技能、输入、输出 schema | 否 |
| `provider/unified` | 会话语义、capability fallback、MCP manifest、统一事件 | 否 |
| `provider/claudecli` | CLI spawn、CLI transport、Claude 事件解析 | 是 |
| `provider/codexapp` | app-server transport、JSON-RPC 事件解析、connection dead 恢复 | 是 |

### 3.3 统一的 Provider 接口设计

```go
package provider

import (
	"context"
	"encoding/json"
)

type Driver interface {
	Name() string
	StartSession(ctx context.Context, req StartSessionRequest) (Session, error)
	ResumeSession(ctx context.Context, req ResumeSessionRequest) (Session, error)
}

type Session interface {
	ThreadID() string
	Capabilities() CapabilitySet
	StartTurn(ctx context.Context, req TurnRequest) (TurnHandle, error)
	Interrupt(ctx context.Context, req InterruptRequest) error
	ListThreads(ctx context.Context) ([]ThreadRef, error)
	ForkThread(ctx context.Context, req ForkRequest) (ForkResult, error)
	Close(ctx context.Context) error
}

type TurnRequest struct {
	ThreadID     string
	Inputs       []InputItem
	Skills       []SkillRef
	OutputSchema json.RawMessage
	Overrides    TurnOverrides
	MCP          MCPManifest
}

type TurnOverrides struct {
	Model  string
	Effort string
}
```

这个接口的关键点：

- 没有 `SubmitWithSkillsAndOverrides` 这种 provider-specific 名字。
- 技能、schema、override 都进入统一 `TurnRequest`。
- driver 能力差异通过 `CapabilitySet` 表达，而不是通过 type assertion 链表达。

### 3.4 统一 MCP 工具路径

V3 的工具接入方式不再是：

- Claude：生成临时 MCP 配置。
- Codex：把 `DynamicTools` 注入 `ThreadStart`。

V3 的唯一方式是：

1. 构建统一 MCP manifest。
2. manifest 指向预编译好的 MCP 家族二进制。
3. Provider 连接这些二进制，不再直接接收工具 schema 列表。

### 3.5 MCP Server 三家族拆分

新增硬约束：

1. `go-agent-mcp-lsp`
   包含 LSP 工具 + RUN 工具。
2. `go-agent-mcp-orch`
   包含 orchestration + DAG + workspace 相关工具。
3. `go-agent-mcp-ida`
   包含 IDA 工具。

这三个二进制独立编译、独立发布、独立被 provider 挂载。
不再存在一个“全量混编 mcp-server”。

### 3.6 为什么必须拆成三个独立二进制

| 维度 | 混编单二进制 | 三家族独立二进制 | 结论 |
|---|---|---|---|
| 冷启动 | 所有依赖都装入一个进程 | 按需挂载 | 三家族更优 |
| 故障域 | 任一工具族崩溃影响全部工具 | 故障局限在单家族 | 三家族更优 |
| 打包 | LSP / orchestration / IDA 重依赖混装 | 可以独立裁剪 | 三家族更优 |
| 权限 | 所有线程都默认看到全部工具 | 按线程能力挂载 | 三家族更优 |
| 预编译 | 一个大目标 | 三个明确产物 | 三家族更优 |
| Provider manifest | 一个庞大 manifest | 多 server manifest，可按需组合 | 三家族更优 |

#### 2026-03-22 架构决策记录：MCP 工具独立服务

- 决策：P8 的 orchestration 家族不再拆成“宿主 runtime + MCP bridge”，而是把 `internal/sidecar/orch/orchestration/*`、相关 store 包和依赖的 sqlc 查询/生成代码一起迁到 `cmd/mcp-orch/*`；P9 的 LSP 工具继续收敛到 `cmd/mcp-lsp/*`。
- 原因 1：MCP 天然以独立进程 + stdio JSON-RPC 运行，生命周期与桌面宿主解耦。
- 原因 2：独立二进制不等于复制一套框架；`cmd/mcp-orch` 仍只共享 V3 的 `internal/platform/{config,db}`、`internal/contract`、`internal/dto`。
- 原因 3：agent runtime、report、runtime snapshot、DAG、资源类 store 与 sqlc 查询本来就在同一对象图里，迁到 `cmd/mcp-orch` 内部后不再需要宿主桥接或内部 store 回跳。
- 原因 4：Provider 只需要挂载 manifest 和二进制健康状态，不需要感知工具内部实现。
- 结果 1：`cmd/mcp-orch` 的 stdio runtime / manifest / registry 也本地化，shared layer 收缩到 config/db/contract/dto。
- 结果 2：具体 MCP 工具定义（schema + handler）只存在于对应 `cmd/mcp-*`。
- 结果 3：P8 完成后 `internal/sidecar/orch/orchestration/*` 删除，`internal/sidecar/orch/orchestration/*` 成为编排真相源；`internal/sidecar/orch/store/*` 与 `internal/sidecar/orch/store/sqlc/*` 成为本地数据层。

### 3.7 家族与工具面的映射

| 家族二进制 | 工具范围 | 备注 |
|---|---|---|
| `cmd/mcp-lsp` | `lsp_*`, `code_run`, `code_run_test` | RUN 工具与 LSP 场景耦合最强，放同一二进制；独立进程但共享 V3 `platform/store/contract` |
| `cmd/mcp-orch` | `orchestration_*`, `task_*`, `workspace_*`, `command_*`, `prompt_*`, `shared_file_*` | 独立进程；内含本地 `orchestration/*` runtime、本地 `store/*` 与本地 `store/sqlc/*`，只共享 `platform/{config,db}`、`contract/*`、`dto/*` |
| `cmd/mcp-ida` | `ida_*` | 隔离平台和重依赖 |

### 3.8 统一 MCP manifest 生成

```go
package unified

type ToolFamily string

const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"
)

type MCPBinary struct {
	Name    string
	Command []string
}

func BuildManifest(enabled []ToolFamily) MCPManifest {
	var bins []MCPBinary
	for _, fam := range enabled {
		switch fam {
		case FamilyLSP:
			bins = append(bins, MCPBinary{Name: "go-agent-mcp-lsp", Command: []string{"go-agent-mcp-lsp"}})
		case FamilyOrch:
			bins = append(bins, MCPBinary{Name: "go-agent-mcp-orch", Command: []string{"go-agent-mcp-orch"}})
		case FamilyIDA:
			bins = append(bins, MCPBinary{Name: "go-agent-mcp-ida", Command: []string{"go-agent-mcp-ida"}})
		}
	}
	return MCPManifest{Binaries: bins}
}
```

统一层的规则：

- 默认线程挂载 `LSP + Orchestration`。
- 只有启用 IDA 能力的线程才挂载 `IDA`。
- driver 不知道工具是怎么注册出来的，只拿到 manifest。

### 3.9 Codex/Claude 收敛表

| 能力 | V2 Claude | V2 Codex | V3 统一表现 |
|---|---|---|---|
| 工具接入 | MCP config | DynamicTools 注入 | 统一 MCP manifest |
| 技能提交 | `SubmitWithSkills` | `SubmitWithSkillsAndOverrides` | 统一 `TurnRequest.Skills + Overrides` |
| 模型覆盖 | launch/resume 级别 | turn 级别支持更强 | 统一 override，driver 自主降级 |
| thread listing | 支持 | 支持 | optional capability |
| fork | 支持度有限 | 原生支持 | optional capability |
| realtime | 提供方式不同 | 提供方式不同 | RPC 层统一 capability gating |
| 事件 | NDJSON/CLI | JSON-RPC/app-server | 统一映射成 `core/agent/event.go` |

### 3.10 `SubmitWithSkillsAndOverrides` 的迁移处理

V3 不保留这个 API 名字。
它被拆成统一的 3 步：

1. `service/turn/prepare.go`
   解析输入、技能选择、prompt 合并、schema。
2. `provider/unified/turn.go`
   形成 `TurnRequest{Skills, Overrides, MCP}`。
3. driver capability 选择
   - 支持 turn override：直接透传。
   - 不支持 turn override，但支持 idle 时重配：先改 thread config 再发 turn。
   - 完全不支持：返回 typed capability error。

### 3.11 Codex 特有能力的处理

| Codex 特有能力 | V3 处理 |
|---|---|
| `SubmitWithSkillsAndOverrides` | 收敛到统一 `TurnRequest` |
| app-server connection-dead 自动恢复 | 留在 `provider/codexapp/recovery.go`，但只暴露统一错误和统一事件 |
| DynamicTools deny list | 删除；三家族二进制已经在编译时分割工具面 |
| rollout 历史读取 | 下沉到 `provider/codexapp/history.go`，对上只暴露 thread history 接口 |

### 3.12 Claude 特有能力的处理

| Claude 特有能力 | V3 处理 |
|---|---|
| CLI 参数组装 | 留在 `provider/claudecli/transport.go` |
| MCP config 生成 | 提升为 `provider/unified/mcp_manifest.go` 公共能力 |
| 文件提示注入 | 迁到 `service/turn/prompt.go`，不再留在 Claude driver |
| history backend | 留在 `provider/claudecli/history.go`，通过统一接口暴露 |

### 3.13 统一事件归一化

| 原始来源 | V3 映射 |
|---|---|
| Claude stream event | `core/agent/event.go` 的 typed event |
| Codex JSON-RPC notification | `core/agent/event.go` 的 typed event |
| MCP tool begin/end | `core/agent/event.go` + `tool` 侧 typed event |
| request_user_input | `service/turn/review.go` 可消费事件 |
| connection_dead | `runtime/agent/recovery.go` 可消费事件 |

### 3.14 Provider 层验收目标

1. `go-agent-v2/internal/apiserver/codexadapter` 在 V3 中不存在等价目录。
2. V3 没有任何地方再向 provider 直接传 `[]DynamicTool`。
3. `provider == "codex"` 这类硬编码分支只允许存在于 driver 注册表和 capability 表。
4. 所有 provider 线程都通过同一套 manifest 发现 MCP family 二进制。

---

## 4. 迁移批次（按依赖顺序）

### 4.0 总表

| 批次 | 依赖 | 主要目标 | 框架 | 预估人天 |
|---|---|---|---|---:|
| P0 | 无 | 骨架、`go.mod`、`fx`、`run.Group`、`sqlc.yaml`、三 MCP binary skeleton | `fx` `run` `sqlc` | 5 |
| P1 | P0 | 20 个 Store 迁移到 sqlc + repo | `sqlc` | 12 |
| P2 | P1 | typed event bus 替换手写 bus | `kelindar/event` | 4 |
| P3 | P1 P2 | runner 状态机迁移到 `stateless` | `stateless` `run` | 8 |
| P4 | P0 P2 P3 | Provider 双端统一 + MCP manifest | `fx` `run` | 12 |
| P5 | P1 P2 P3 P4 | 151（含 23 noop）RPC 方法 + server 基础设施迁移到 `jrpc2` | `jrpc2` `fx` | 16 |
| P6 | P0 P4 P5 | Wails/入口层整合 | `fx` `run` | 7 |
| P7 | P0 P1 P2 P4 P5 | 辅助业务模块：workspace、skills、dashboard、uistate、lspgui | `fx` `event` `run` | 8 |
| P8 | P0 P1 P4 P5 P7 | 整体迁移 `internal/sidecar/orch/orchestration/*`、相关 `internal/store/*` 与依赖的 `internal/store/sqlc/*` / `sql/queries/*.sql` 到 `cmd/mcp-orch/*`；19 个可交付工具 + 1 个延后（`task_start_node`） | `fx` `stdio` `sqlc` | 4 |
| P9 | P0 P4 P5 | MCP LSP 工具独立服务（`cmd/mcp-lsp`，可完全独立运行），并共享 V3 framework code | `fx` `stdio` | 12 |
| P10 | P7 P8 P9 | Two-Zone/shared 丰满化与架构收口 | `fx` `archtest` | 6 |

### 4.1 P0：基础设施

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/go.mod`, `go-agent-v2/migrations/*.sql`, `go-agent-v2/internal/database/*`, `go-agent-v2/internal/apiserver/server_bootstrap.go`, `go-agent-v2/cmd/agent-terminal/main_setup.go`, `go-agent-v2/cmd/mcp-server/main.go` |
| 迁移目标 | `internal/app/*`, `internal/platform/config/*`, `internal/platform/db/*`, `internal/store/sqlc/*`, `cmd/mcp-lsp/*`, `cmd/mcp-orch/*`, `cmd/mcp-ida/*`, `cmd/agent-terminal/*` |
| 使用框架 | `go.uber.org/fx`, `github.com/oklog/run`, `sqlc` |
| 预期代码量变化 | 手写代码 +1,500 ~ +2,000；暂不计 sqlc 生成 |
| 验证方式 | `fx.ValidateApp`、启动/停止 smoke、`sqlc generate`、migrations dry-run |
| 预估天数 | 5 |

P0 的交付物不是业务功能，而是“V3 有了正确骨架”。

P0 必做事项：

1. 在 V3 `go.mod` 中加入并锁定 6 个框架版本。
2. 建立 `fx.Module` 清单，而不是手写 `NewServer(...)` 大装配函数。
3. 建立 `run.Group` 的 value-group runner 宿主。
4. 建立 `internal/platform/db/sqlc.yaml`、`sql/queries/` 和 `internal/store/sqlc/` 目录。
5. 把现有迁移 SQL 整理成 V3 的基线 schema。
6. 建立 3 个 MCP family 命令：
   `cmd/mcp-lsp`
   `cmd/mcp-orch`
   `cmd/mcp-ida`
7. 把 stdio loop、manifest、registry 固定在各个 `cmd/mcp-*` 入口层；`cmd/mcp-orch` 不再依赖共享 MCP common 层。

P0 的关键设计约束：

- `go-agent-v2/cmd/mcp-server` 不再是 V3 终态。
- `cmd/mcp-lsp` 只编译 LSP + RUN。
- `cmd/mcp-orch` 编译 orchestration + DAG + workspace + command + prompt + shared_file。
- `cmd/mcp-ida` 只编译 IDA。
- `fx` 不负责长跑 actor 编排。
- `run.Group` 不负责构造依赖。

P0 的 Done 标准：

- `make build` 能产出 4 个主产物：`agent-terminal`, `mcp-lsp`, `mcp-orch`, `mcp-ida`。
- `internal/app` 无手写对象大拼装。
- `cmd/*` 无 `sync.WaitGroup` 启停编排。
- V3 仓库能空启动并优雅退出。

### 4.2 P1：Store 层（sqlc 迁移 20 个 Store）

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/internal/store/*.go`, `go-agent-v2/migrations/*.sql` |
| 迁移目标 | `internal/platform/db/*`, `internal/store/sqlc/*`, `internal/store/*`, `internal/contract/*`, `internal/dto/*` |
| 使用框架 | `sqlc` |
| 预期代码量变化 | 手写 Store 约 -1,000 ~ -1,400 行；生成代码 +8,000 ~ +10,000 行 |
| 验证方式 | repo integration tests、schema drift tests、behavior contract tests |
| 预估天数 | 12 |

P1 的拆分建议：

1. **日志与偏好**
   `system_log`
   `audit_log`
   `ai_log`
   `bus_log`
   `ui_preference`
   `shared_file`
2. **线程与运行时**
   `agent_provider_binding`
   `agent_thread`
   `agent_status`
   `cwd_lock`
3. **协作与编排**
   `task_ack`
   `task_dag`
   `task_dag_wakeup`
   `task_trace`
   `workspace_run`
   `topology_approval`
4. **模板与交互**
   `prompt_template`
   `command_card`
   `interaction`
   `db_query`

P1 的核心规则：

1. `module/*` 只依赖 `store/*` 暴露的接口，不直接穿透到底层 SQL。
2. `sqlc` 生成包不被 `module/*` 直接 import。
3. V2 的 legacy `agent_codex_binding` 被吸收进 provider binding 兼容查询，不再保持独立业务概念。
4. 所有 SQL 安全策略统一到 `internal/platform/db/query_guard.go`。

P1 的验证：

- 每个 repo 至少一组 happy path integration test。
- 每个关键 repo 至少一组 error path / zero row semantic test。
- `store_behavioral_extended_guard_test` 里的行为规格迁移为 V3 repo 行为测试。
- `store_schema_guard_test` 迁移为 sqlc schema drift test。

P1 的 Done 标准：

- V3 中不存在手写 SQL builder 式 Store。
- V3 中所有写路径都通过 `internal/store/*` 或 `sqlc.Querier`。
- `internal/store/*` 在 V3 是正式的数据访问层，只保留薄仓储封装，不承载业务编排。

### 4.3 P2：事件总线（kelindar/event）

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/internal/bus/*`, `go-agent-v2/internal/apiserver/server_event_handler.go`, `go-agent-v2/internal/uistate/event_*` 的总线耦合部分 |
| 迁移目标 | `internal/platform/bus/*`, `internal/module/uistate/projection.go`, `internal/contract/agent/event.go` |
| 使用框架 | `github.com/kelindar/event` |
| 预期代码量变化 | `go-agent-v2/internal/bus` 手写代码 -250 ~ -400 行 |
| 验证方式 | publish/subscribe contract、typed payload compile-time checks、event routing tests |
| 预估天数 | 4 |

P2 不只是把一个库换成另一个库。
P2 的真正目标是：

1. 删除 `map[string]any` / `json.RawMessage` 型业务载荷在总线内部的传播。
2. 把事件分类从“字符串 topic + 手写 payload”变成“typed event + 投影器”。
3. 把 bus log 持久化从总线主流程移到订阅 sink。

P2 的事件建议分层：

- `AgentLifecycleEvent`
- `TurnEvent`
- `ToolEvent`
- `TaskEvent`
- `WorkspaceEvent`
- `UIProjectionEvent`

P2 的 Done 标准：

- 总线内部没有业务 `map[string]any` payload。
- topic 常量不再是公开主模型。
- `go-agent-v2/internal/apiserver/server_event_handler.go` 这种 500+ 行变异管道被拆成多个投影订阅器。

### 4.4 P3：状态机（stateless 迁移 AgentManager）

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/internal/runner/manager*.go`, `go-agent-v2/internal/runner/provider_registry.go`, `go-agent-v2/internal/apiserver/codexadapter/*` 中与 tracked turn / deferred submit / recover 相关逻辑 |
| 迁移目标 | `internal/sidecar/orch/orchestration/*`, `internal/module/turn/service.go`, `internal/platform/statemachine/*` |
| 使用框架 | `github.com/qmuntal/stateless`, `github.com/oklog/run` |
| 预期代码量变化 | runner 层净减 500 ~ 1,000 行 |
| 验证方式 | full transition matrix、recover matrix、queued firing tests、race tests |
| 预估天数 | 8 |

P3 解决的不是“代码长”，而是“状态含义不再隐式”。

V3 状态建议：

- `Provisioning`
- `Idle`
- `TurnQueued`
- `TurnStarting`
- `TurnRunning`
- `AwaitingUserInput`
- `Recovering`
- `Stopping`
- `Stopped`
- `Failed`

V3 触发器建议：

- `LaunchSucceeded`
- `LaunchFailed`
- `TurnEnqueued`
- `TurnAccepted`
- `TurnCompleted`
- `TurnAborted`
- `UserInputRequested`
- `UserInputResolved`
- `RecoverRequested`
- `StopRequested`
- `ProcessExited`

P3 的关键原则：

1. UI 可见状态只从状态机 + queue 派生，不再有 `effectiveState` 第二表示。
2. 状态切换表和副作用分离：
   迁移规则在 `transitions.go`
   副作用在 `actions.go`
3. 所有恢复入口统一经过 `recovery.go`。
4. 所有 queued submission 统一经过 `submission_queue.go`。

P3 的状态机示意：

```go
sm.Configure(StateIdle).
	Permit(TriggerTurnEnqueued, StateTurnQueued)

sm.Configure(StateTurnQueued).
	Permit(TriggerTurnAccepted, StateTurnStarting).
	Permit(TriggerStopRequested, StateStopping)

sm.Configure(StateTurnStarting).
	Permit(TriggerTurnRunning, StateTurnRunning).
	Permit(TriggerRecoverRequested, StateRecovering)
```

P3 的 Done 标准：

- V3 没有 `effectiveState` 这类并列可变状态字段。
- 所有合法状态迁移都能导出一张 matrix。
- recover 行为只在一个状态机入口上建模。

### 4.5 P4：Provider 统一（收敛 Claude/Codex）

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/legacy-agentsdk/claude/*`, `go-agent-v2/legacy-agentsdk/codex/*`, `go-agent-v2/legacy-agentsdk/agentcore/*`, `go-agent-v2/internal/apiserver/codexadapter/*`, `go-agent-v2/internal/runner/provider_registry.go` |
| 迁移目标 | `internal/provider/unified/*`, `internal/provider/claudecli/*`, `internal/provider/codexapp/*`, `internal/sidecar/orch/orchestration/*` |
| 使用框架 | `fx`, `run.Group` |
| 预期代码量变化 | provider 相关手写代码净减 4,000 ~ 6,000 行 |
| 验证方式 | driver contract suite、dual-provider parity tests、MCP manifest tests |
| 预估天数 | 12 |

P4 的工作流：

1. 建统一 driver 接口。
2. 先把 Claude 和 Codex transport 分别塞到 driver。
3. 把技能、prompt、override、history、timeline 提升到 unified/service 层。
4. 删除 `DynamicTools` 直传。
5. 改用三家族 MCP manifest。

P4 特别注意：

- `cmd/mcp-lsp` 与 `cmd/mcp-orch` 是默认挂载。
- `cmd/mcp-ida` 只按线程能力挂载。
- driver 不允许自己私下拼 tool schema。

P4 的 Done 标准：

- `SubmitWithSkillsAndOverrides` 不再出现在 V3 公共接口中。
- `DynamicTools []...` 不再穿过 runtime/service/provider 业务主链路。
- 两个 provider 走同一套 turn prepare、skill resolve、prompt compose、MCP manifest。
- **review 功能（`ReviewStart` / `/review` 命令）推迟到 P5 RPC 层实现。** P4 不包含 `module/turn/review.go`。原因：`contract.Session` 无 review 专用接口，review 本质是 slash-command，与 RPC handler 层更紧密，P5 统一中间件后补齐。

### 4.6 P5：RPC 层（jrpc2 迁移 151（含 23 noop）方法 + Server 基础设施）

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/internal/apiserver/methods*.go`, `go-agent-v2/internal/apiserver/server.go`, `go-agent-v2/internal/apiserver/server_conn_ws.go`, `go-agent-v2/internal/apiserver/server_payload.go`, `go-agent-v2/internal/apiserver/server_approval.go`, `go-agent-v2/internal/apiserver/dashboard_bindings.go`, `go-agent-v2/internal/apiserver/workspace_methods.go`, `go-agent-v2/internal/apiserver/notifications.go`, `go-agent-v2/internal/apiserver/orchestration_report.go` |
| 迁移目标 | `internal/platform/rpc/{initialize.go,transport_ws.go,codec.go,approval.go,push.go,debug.go}`, `internal/platform/bus/*`, `internal/module/*/rpc.go` |
| 使用框架 | `github.com/creachadair/jrpc2`, `fx` |
| 预期代码量变化 | V2 源 6,707 行 → V3 目标 4,500-6,500 行 |
| 验证方式 | registry completeness、schema contract、golden response tests、JSON-RPC protocol tests、push/approval integration tests |
| 预估天数 | 16 |

P5 不是“把函数搬到别的文件”。
P5 要完成的是：

1. 151（含 23 noop）方法统一进入一个 `handler.Map`。
2. 方法面、WebSocket transport、payload codec、server→client push 通道、approval 状态机一并迁移。
3. `handler.Check().AllowArray(false).SetStrict(true).Wrap()` 覆盖所有公开方法；thread scope、capability guard、approval、logging 全部中间件化。
4. `Server` God Object 不再充当“所有状态 + 所有依赖 + 所有 transport”的容器。

P5 的 handler 分组归宿：

| 旧分组 | 修正后归宿 |
|---|---|
| ~~`initialize.go`~~ | `internal/platform/rpc/initialize.go` |
| ~~`thread.go`~~ | `internal/module/thread/rpc.go` |
| ~~`turn.go`~~ | `internal/module/turn/rpc.go` |
| ~~`config.go`~~ | 合并到 `internal/module/thread/rpc.go`（`thread/config/*`） |
| ~~`skills.go`~~ | `internal/module/skill/rpc.go` |
| ~~`command.go`~~ | `internal/module/skill/rpc.go` |
| ~~`workspace.go`~~ | `internal/module/workspace/rpc.go` |
| ~~`ui.go`~~ | `internal/module/uistate/rpc.go` |
| ~~`dashboard.go`~~ | `internal/module/dashboard/rpc.go` |
| ~~`orchestration.go`~~ | `internal/sidecar/orch/tools/orchestration_tools.go` |
| ~~`log.go`~~ | `internal/platform/bus` sink |
| ~~`debug.go`~~ | `internal/platform/rpc/debug.go` |

P5 的规则：

1. 每个 RPC 注册面只依赖模块 facade，不依赖具体 store 或 transport。
2. `threadId` 必填不在 handler 里手工校验，而在 middleware 统一处理。
3. 错误统一映射成 `*jrpc2.Error`。
4. response shape 由 contract test 锁定，不由 AST guard 锁定。
5. handler.Map 片段通过 `fx` value group 聚合，不允许第二条手写注册链。

P5 的 Done 标准：

- 151（含 23 noop）方法都能在统一注册表中被枚举。
- 所有公开方法都使用 `handler.Check().SetStrict(true)`。
- server→client push 通道可用。
- approval 状态机迁移完成。
- fx 图闭环：所有模块 `handler.Map` 自动注册。
- V3 没有第二套手写注册链；模块 RPC 统一由 `internal/platform/rpc` 聚合。
- `go-agent-v2/internal/apiserver/server_context.go` 一类 nil-guard 汇总文件在 V3 不存在等价物。

### 4.7 P6：入口层（cmd/, Wails 集成）

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/cmd/agent-terminal/*`, `go-agent-v2/cmd/server/*`, `go-agent-v2/cmd/app-server/*`, `go-agent-v2/internal/dashboard/*` |
| 迁移目标 | `cmd/agent-terminal/*`, `internal/ui/runtime/*`, `internal/ui/dashboard/*`, `internal/app/*` |
| 使用框架 | `fx`, `run.Group`, `Wails v3` |
| 预期代码量变化 | 入口层手写代码净减 800 ~ 1,200 行 |
| 验证方式 | desktop boot smoke、signal shutdown、WS bridge smoke、dashboard SSE smoke |
| 预估天数 | 7 |

P6 的重点是把入口变成“薄装配层”：

- `cmd/agent-terminal` 只负责 Wails app 与 fx app。
- `go-agent-v2/cmd/server` 可被吸收进 `cmd/agent-terminal` 或废弃。
- dashboard server 下沉到 `internal/ui/dashboard`。

P6 与 P4 的耦合点：

- Provider 启动参数里必须带 MCP family manifest。
- Wails UI 需要能够观察这三个 family 的健康状态，但不直接拥有它们。

P6 的 Done 标准：

- Desktop app 能完整启动 V3 后端。
- UI 能正常发起 `thread/start`, `turn/start`, `workspace/run/create`, `agent.launch`。
- 启停链不依赖 `sync.WaitGroup` 人工收尾。

### 4.8 P7：辅助业务模块（skills、workspace、dashboard、uistate、lspgui）

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/internal/service/*`, `go-agent-v2/internal/dashboard/*`, `go-agent-v2/internal/uistate/*`, `go-agent-v2/internal/apiserver/methods_ui_lsp_gui.go` |
| 迁移目标 | `internal/module/{skill,workspace,dashboard,uistate,lspgui}/*`, `internal/ui/*` |
| 使用框架 | `fx`, `event`, `run.Group` |
| 预期代码量变化 | 手写代码持平或略减，重点是宿主内领域模块化 |
| 验证方式 | module contract、workspace integration、dashboard/uistate projection、GUI facade smoke |
| 预估天数 | 8 |

P7 是把宿主内必须长期存在的辅助业务模块迁到 V3 核心层。

P7 不承载 MCP 工具实现；MCP 编排/LSP 工具分别在 P8/P9 中作为独立服务落地。

P7 必须分成 5 个小闭环：

1. **P7a Skill / Command Card**
   `internal/module/skill`
2. **P7b Workspace Host Service**
   `internal/module/workspace`
3. **P7c Dashboard**
   `internal/module/dashboard`
4. **P7d UI State**
   `internal/module/uistate`
5. **P7e LSP GUI facade**
   `internal/module/lspgui`

P7 的关键规则：

- `internal/module/*` 只承载宿主业务与 RPC facade，不承载 MCP tool schema/dispatcher/runtime。
- `internal/module/lspgui` 只保留 GUI/LSP 兼容面；真实 file/search/inspect/xref/edit 等逻辑在 P9。
- `workspace` 的宿主服务与 `workspace_*` MCP 工具分离；后者在 P8。

P7 的 Done 标准：

- `internal/module/{skill,workspace,dashboard,uistate,lspgui}` 都具备最小闭环。
- 宿主 UI/RPC 可以访问这些模块。
- `internal/module/*` 中没有 MCP tool 定义。

### 4.9 P8：从 V3 现有实现抽取 MCP 编排工具到独立服务

这里同样遵循核心层职责原则：agent-terminal 只保留 Agent 管理、工具管理（MCP manifest 构建与注入）、Hooks，以及暴露 `ctl/*` RPC 接口；核心层不启动 MCP 进程。P8 需要把编排相关能力整体收敛到独立 `cmd/mcp-orch` 服务。

| 项目 | 内容 |
|---|---|
| 迁移源 | `internal/sidecar/orch/orchestration/*`, `internal/store/{taskdag,workspace,prompt,commandcard,sharedfile}`、必要时 `internal/store/binding`，以及对应 `internal/store/sqlc/*` / `sql/queries/*.sql` |
| 迁移目标 | `cmd/mcp-orch/{orchestration,store/sqlc,store/*,tools}/*` |
| 使用框架 | `fx`, `stdio JSON-RPC`, `sqlc` |
| 预期代码量变化 | 从 V3 现有 orchestration 模块整体迁移 + 本地 store/sqlc 层复制迁移 + 11 个 tool adapter；19 个可交付工具 + 1 个延后项 `task_start_node`；V3 核心层不新增 MCP tool surface |
| 验证方式 | `go build ./cmd/mcp-orch`、`tools/list` schema contract、删除 `internal/sidecar/orch/orchestration` 后的编译 smoke、以及 `cmd/mcp-orch` 对 `internal/store/*` / `internal/store/sqlc/*` 零 import 检查 |
| 预估天数 | 4 |

P8 的目标不是把 MCP 工具塞回 `internal/module/*`，而是把编排工具面、相关 store 层和依赖 sqlc 层一起迁到独立 `cmd/mcp-orch` 服务，同时只共享 V3 的 config/db/contract/dto 基础设施。

P8 的关键规则：

- tool handler、registry、runtime、resource adapter 都留在 `cmd/mcp-orch/*`。
- `internal/sidecar/orch/orchestration/*` 是迁移后的本地编排组件；P8 完成后不再依赖 `internal/sidecar/orch/orchestration/*`。
- `internal/sidecar/orch/store/*` 与 `internal/sidecar/orch/store/sqlc/*` 是迁移后的本地数据层；P8 完成后 `cmd/mcp-orch` 不再依赖 `internal/store/*` 或 `internal/store/sqlc/*`。
- `workspace_*`、`command_*`、`prompt_*`、`shared_file_*` 直接调用本地 store；`task_*` 通过迁移后的本地 DAG 逻辑 + 本地 `taskdag.Store` 执行。
- 这一步是“迁移”而不是“复制 V2”：MCP 二进制必须站在本地 runtime/store/sqlc + 共享 config/db/contract/dto 之上，不能长期保留一份平行的 V2 内核。
- P8 可交付范围固定为 19 个工具：`orchestration_*` 5 个、`task_*` 3 个（不含 `task_start_node`）、`workspace_*` 5 个、`command_*` 2 个、`prompt_*` 2 个、`shared_file_*` 2 个。
- `task_start_node` 当前无 V3 等价 service/controller，只能作为延后项，不能混入 P8 manifest。
- 候选 host store 包必须先做 xref 审计；当前基线下 `taskdag`、`binding`、`workspace`、`prompt`、`commandcard`、`sharedfile` 都要 copy+keep。
- provider 侧只消费 manifest 和二进制，不再直接感知 `DynamicTools` 或宿主内部 tool registry。

P8 的 Done 标准：

- `cmd/mcp-orch` 可独立启动并通过 stdio 暴露 19 个可交付工具：`orchestration_*`、`task_create_dag`、`task_get_dag`、`task_update_node`、`workspace_*`、`command_list|get`、`prompt_list|get`、`shared_file_read|write`。
- `internal/sidecar/orch/orchestration/*` 已整体删除；`internal/sidecar/orch/orchestration/*` 成为编排真相源。
- `internal/sidecar/orch/store/*` 与 `internal/sidecar/orch/store/sqlc/*` 已本地化；`cmd/mcp-orch` 对共享基础设施的依赖只剩 `internal/platform/{config,db}`、`internal/contract/*`、`internal/dto/*`。
- `task_start_node` 明确延后且不进入 P8 对外可见 manifest。
- `prompts/list|write|delete` 的宿主 UI surface 保持可用，P8 不得为迁 MCP 把 prompt 宿主入口搬空。
- P8 合入前，archtest 必须新增并通过 `cmd/mcp-orch` 禁止 import `internal/module/*`、`internal/store/*`、`internal/store/sqlc/*`，以及 `cmd/mcp-*` 禁止交叉 import、`internal/*` 禁止反向 import `cmd/mcp-*` 三条规则。
- 宿主退出时不需要持有 `cmd/mcp-orch` 内部对象图。

### 4.10 P9：MCP LSP 工具独立服务，可完全独立运行

| 项目 | 内容 |
|---|---|
| 迁移源 | `go-agent-v2/pkg/toolsdk/lsp/*`, `go-agent-v2/pkg/toolsdk/tools/{lsp_tools,lsp_tools_ide,code_run}.go`, `go-agent-v2/internal/mcp/*` |
| 迁移目标 | `cmd/mcp-lsp/*` |
| 使用框架 | `fx`, `stdio JSON-RPC` |
| 预期代码量变化 | 高复杂度保留在独立服务；宿主只保留 GUI facade 与 manifest 挂载 |
| 验证方式 | `go build ./cmd/mcp-lsp`、LSP contract suite、gopls bootstrap smoke、独立运行 smoke |
| 预估天数 | 12 |

P9 要求 `cmd/mcp-lsp` 作为完整 MCP LSP 服务独立存在，不依赖 `agent-terminal` 宿主进程对象图即可运行，但仍共享 V3 主框架代码。

P9 的关键规则：

- `lsp_file`、`lsp_inspect`、`lsp_xref`、`lsp_grep`、`lsp_structure`、`lsp_edit`、`lsp_completion`、`code_run`、`code_run_test` 全部实现在 `cmd/mcp-lsp/*`。
- `cmd/mcp-lsp` 的 schema/handler 只在本二进制中定义，但底层能力优先通过 import 复用 V3 `internal/platform`、`internal/store`、`internal/contract`，而不是复制 V2 代码。
- gopls 进程池、workspace root 管理、protocol/runtime bootstrap 由 `cmd/mcp-lsp` 自管。
- `internal/module/lspgui` 只保留 GUI/RPC 适配，不得承载真实 LSP 工具逻辑。
- provider 与前端只通过 manifest 和 RPC facade 使用 LSP 能力，不直接依赖工具实现包。

P9 的 Done 标准：

- `cmd/mcp-lsp` 在没有桌面宿主的情况下也能通过 stdio 提供完整工具面。
- LSP family 不链接 orchestration/IDA tool 运行时。
- `cmd/mcp-lsp` 对公共能力的复用来源明确落在 V3 `internal/platform`、`internal/store`、`internal/contract`。
- 宿主侧只保留 GUI facade、事件桥接与 manifest 挂载。

### 4.11 P10：Two-Zone/shared 丰满化

P10 处理 P7-P9 完成后的 shared 提升、命名漂移修正与 module.go 纯化。
MCP 新依赖方向守卫不属于 P10 延后项，而是 P8/P9 的前置条件。
执行细节以 `docs/plans/迁移/p10-execution-plan.md` 为准。

---

## 5. 测试迁移策略

### 5.1 哪些 V2 测试是“行为规格书”

以下测试不应按“旧文件形状”理解，而应按“行为规格”理解：

| V2 测试组 | 规格性质 | V3 处理 |
|---|---|---|
| `go-agent-v2/internal/guards/golden/rpc_response_*.go` | RPC 输出形状规格 | 迁移到 V3 `rpc` golden tests |
| `go-agent-v2/internal/guards/rpc_golden_test.go` | 端到端方法组合行为 | 迁移为 V3 contract replay |
| `go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go` | 151（含 23 noop）方法参数/返回 shape | 迁移为 `jrpc2` 注册表 contract |
| `go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go` | 其余方法 shape | 同上 |
| `go-agent-v2/internal/runner/runner_response_golden_guard_test.go` | runner 输出快照 | 迁移为 runtime snapshot contract |
| `go-agent-v2/internal/runner/state_matrix_guard_test.go` | 状态转移矩阵 | 迁移为 stateless matrix tests |
| `go-agent-v2/internal/store/store_behavioral_extended_guard_test.go` | repo 行为语义 | 迁移为 repo 行为测试 |
| `go-agent-v2/internal/store/store_db_contract_test.go` | DB 契约 | 迁移为 sqlc repo integration |
| `go-agent-v2/internal/mcp/mcp_response_golden_guard_test.go` | MCP 协议输出 | 迁移为 family-level MCP contract |
| `go-agent-v2/internal/mcp/lifecycle_matrix_guard_test.go` | MCP 生命周期矩阵 | 迁移为各 family 本地 lifecycle contract |
| `go-agent-v2/internal/mcp/state_matrix_guard_test.go` | 协议状态机 | 迁移为 stdio runtime contract |
| `go-agent-v2/legacy-agentsdk/service/runtime/*` | turn prepare/runtime 规则 | 迁移为 `module/turn/*` 单测 |
| `go-agent-v2/legacy-agentsdk/service/history/*` | thread history 规则 | 迁移为 `module/thread/service.go` 单测 |
| `go-agent-v2/legacy-agentsdk/service/archive/*` | archive/unarchive 行为 | 迁移为 `module/thread/service.go` 单测 |
| `go-agent-v2/pkg/toolsdk/tools/*schema*` | tool schema 契约 | 迁移为三个 family 的 schema contract |

### 5.2 哪些 V2 guard 测试不应原样迁移

以下 guard 在 V3 中应被替换，不应一比一照搬：

| V2 guard 类型 | 原因 | V3 替代方式 |
|---|---|---|
| function inventory guard | 锁的是文件切分，不是业务行为 | 以 public contract + import boundary 替代 |
| split guard | 锁的是文件归属，不是行为 | 以 package boundary test 替代 |
| code size guard | 只对 V2 结构有效 | 保留少量预算门槛，不移植旧阈值 |
| AST snippet guard | 锁旧实现细节 | 以 matrix/behavior/contract 替代 |
| nil-guard shape test | V3 由 `fx` 构造失败直接替代 | `fx.ValidateApp` + constructor tests |
| hard-coded timeout snippet guard | V3 timeout 集中到 `internal/platform/config/timeouts.go` | 单文件配置测试 |

### 5.3 V2 guard 在 V3 中如何体现

| V2 问题域 | V2 guard 代表 | V3 对应验证 |
|---|---|---|
| 方法注册完整性 | `rpc_registry_guard_test.go` | `handler.Map` completeness test |
| event mapping 完整性 | `server_event_mapping_*` | typed event routing snapshot |
| store schema 对齐 | `store_schema_guard_test.go` | sqlc generate + migration checksum |
| runner 状态一致性 | `effective_state_guard_test.go` | stateless full matrix |
| transport 协议语义 | `transport_protocol_*` | jrpc2 + MCP protocol contract |
| tool schema | `tools/schema_compat_test.go` | family schema snapshot |

### 5.4 V3 测试架构

V3 的测试分为 6 层：

1. **Pure unit**
   `contract`, `dto`, `module/*`, `provider/unified` 的无 IO 逻辑。
2. **Repo integration**
   基于 Postgres + `sqlc` 的真实 repo 测试。
3. **State machine matrix**
   `module/orchestration/*` + `platform/statemachine/*` 的全转移矩阵。
4. **Driver contract**
   Claude 和 Codex driver 共用同一 contract suite。
5. **RPC/MCP contract**
   `jrpc2` 方法契约与 MCP family stdio 契约。
6. **Desktop smoke**
   Wails 启动、线程操作、workspace 流程。

### 5.5 `sqlc Querier` 的利用方式

```go
package repo_test

type fakeQuerier struct {
	dbgen.Querier
}

func TestAgentStatusRepo_Get(t *testing.T) {
	q := fakeQuerier{}
	_ = q
}
```

测试规则：

- `module/*` 单测可 mock `store/*` 暴露的接口。
- repo 单测尽量直接打真实 Postgres，不 mock `sqlc` 生成层。
- 只有 `module/*` 层需要 fake store/repo，store 层不需要 fake SQL。

### 5.6 `fx` 测试工具

V3 统一用 `fxtest` 或 `fx.ValidateApp` 做对象图检查。

建议建立：

- `internal/testutil/fxapp`
- `internal/testutil/providerfake`
- `internal/testutil/db`

### 5.7 `stateless` 全矩阵测试

V3 状态机必须做到：

1. 每个状态的合法 trigger 集有快照。
2. 每个 trigger 的 guard 都有 true/false 两面测试。
3. 每个 entry/exit action 都能被断言是否执行。
4. 恢复、停止、并发 fire 的顺序有专门测试。

### 5.8 MCP family 测试要求

每个 family 都要有 4 类测试：

1. `tools/list` schema snapshot
2. `tools/call` request/response contract
3. stdio framing contract
4. family 独立构建 smoke

### 5.9 最终 CI 门槛

| 阶段 | 必过项 |
|---|---|
| 每个 PR | `go test`, `go vet`, `sqlc generate`, import boundary check |
| 每个批次封板 | repo integration, matrix, RPC contract, MCP contract |
| 切换前 | desktop smoke, provider dual-run parity, workspace/IDA smoke |

---

## 6. 迁移清单（Checklist）

> 说明：
> 1. 本清单覆盖以下核心包的**全部生产代码文件**：`go-agent-v2/internal/apiserver/`, `go-agent-v2/internal/store/`, `go-agent-v2/internal/runner/`, `go-agent-v2/internal/bus/`, `go-agent-v2/legacy-agentsdk/`, `go-agent-v2/pkg/toolsdk/`。
> 2. 测试文件不在本节逐文件列出，它们在第 5 节按规格类型迁移。
> 3. “动作”只使用四种：`迁移` / `重写` / `丢弃` / `合并`。
>
> 2026-03-22 架构决策覆盖说明：
> 本节凡涉及 MCP/LSP/编排/IDA 工具的 V3 归宿，统一以 `cmd/mcp-lsp/*`、`cmd/mcp-orch/*`、`cmd/mcp-ida/*` 为准。
> 旧版写法中的 `internal/tool/*`、`internal/mcpserver/{lsp,orch,ida}`、`internal/module/coderun|ida` 均视为早期草案，不再作为终态目录。

### 6.1 `go-agent-v2/internal/apiserver/`

| V2 文件 | 动作 | V3 归宿 | 说明 |
|---|---|---|---|
| `go-agent-v2/internal/apiserver/codexadapter/adapter.go` | 重写 | `internal/provider/unified/client.go` | 统一 Provider 语义入口 |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_deferred_turn_start.go` | 重写 | `internal/sidecar/orch/orchestration/runner_actor.go` | 延迟 turn/start 进入提交队列 |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_event.go` | 重写 | `internal/provider/unified/event_map.go` | 原始 provider 事件统一映射 |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_helpers.go` | 合并 | `internal/provider/unified/session.go` | session 辅助逻辑 |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_launch_config.go` | 合并 | `internal/provider/unified/thread_config.go` | launch config 归统一层 |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go` | 合并 | `internal/provider/unified/client.go` | provider lifecycle 统一化 |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_memory_stats.go` | 合并 | `internal/platform/rpc/*` | memory stats 不再归 provider |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_plan_heartbeat.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | 计划心跳归 runtime snapshot |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_stall.go` | 重写 | `internal/sidecar/orch/orchestration/recover.go` | stall/recover 进入状态机 |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go` | 重写 | `internal/provider/unified/turn.go` | 统一 turn request |
| `go-agent-v2/internal/apiserver/codexadapter/adapter_thread_listing.go` | 重写 | `internal/module/thread/service.go` | 线程列举归 service |
| `go-agent-v2/internal/apiserver/codexadapter/doc.go` | 丢弃 | - | V2 包文档无迁移价值 |
| `go-agent-v2/internal/apiserver/codexadapter/stream_timeout.go` | 合并 | `internal/platform/config/timeouts.go` | timeout 常量集中 |
| `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go` | 重写 | `internal/module/thread/service.go` | thread config 归 service |
| `go-agent-v2/internal/apiserver/codexadapter/thread_messages.go` | 重写 | `internal/module/thread/service.go` | thread messages 归 history service |
| `go-agent-v2/internal/apiserver/codexadapter/thread_messages_internal.go` | 合并 | `internal/provider/codexapp/history.go` | provider-specific 历史读取 |
| `go-agent-v2/internal/apiserver/codexadapter/thread_recover.go` | 重写 | `internal/sidecar/orch/orchestration/recover.go` | recover 统一入口 |
| `go-agent-v2/internal/apiserver/commonadapter/common.go` | 合并 | `internal/module/turn/service.go` | 通用 prompt/merge 逻辑 |
| `go-agent-v2/internal/apiserver/commonadapter/doc.go` | 丢弃 | - | 文档并入 V3 设计文档 |
| `go-agent-v2/internal/apiserver/commonadapter/skills.go` | 合并 | `internal/module/skill/service.go` | skill 公共逻辑并域化 |
| `go-agent-v2/internal/apiserver/contracts/doc.go` | 丢弃 | - | 契约文档由 `contract` 与 `dto` 承载 |
| `go-agent-v2/internal/apiserver/contracts/types.go` | 重写 | `internal/dto/skill/skill.go` + `internal/contract/skill.go` | 技能/契约类型回归核心模型 |
| `go-agent-v2/internal/apiserver/dagwatcher/advancer.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` | phase1 watcher 推进逻辑 |
| `go-agent-v2/internal/apiserver/dagwatcher/controller.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` | 控制器并入 watcher |
| `go-agent-v2/internal/apiserver/dagwatcher/deps.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | task service 提供依赖面 |
| `go-agent-v2/internal/apiserver/dagwatcher/direct_update_advance.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | 直接推进逻辑并入 watcher |
| `go-agent-v2/internal/apiserver/dagwatcher/dispatcher.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` | wakeup dispatcher 独立 |
| `go-agent-v2/internal/apiserver/dagwatcher/helpers.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | 辅助逻辑并入 watcher |
| `go-agent-v2/internal/apiserver/dagwatcher/runtime_helpers.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | runtime 辅助逻辑并入 watcher |
| `go-agent-v2/internal/apiserver/dagwatcher/status_guard.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | 状态判定并入 watcher |
| `go-agent-v2/internal/apiserver/dashboard_bindings.go` | 重写 | `internal/module/dashboard/rpc.go` + `internal/platform/rpc/*` | dashboard RPC 归 handler |
| `go-agent-v2/internal/apiserver/db_scope_cwd.go` | 合并 | `internal/platform/rpc/request_context.go` | scoped context 统一 |
| `go-agent-v2/internal/apiserver/internal_messages.go` | 重写 | `internal/module/turn/service.go` | 内部消息路由归 turn runtime |
| `go-agent-v2/internal/apiserver/methods.go` | 重写 | `internal/platform/rpc/method.go` | 统一方法注册表 |
| `go-agent-v2/internal/apiserver/methods_command.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/skill/service.go` | command/skills 分拆 |
| `go-agent-v2/internal/apiserver/methods_config.go` | 重写 | `internal/platform/rpc/method.go` | config 统一 handler |
| `go-agent-v2/internal/apiserver/methods_log_relay.go` | 重写 | `internal/platform/rpc/method.go` | log relay 独立 handler |
| `go-agent-v2/internal/apiserver/methods_orchestration.go` | 重写 | `internal/sidecar/orch/tools/orchestration_tools.go` + `internal/sidecar/orch/orchestration/service.go` | orchestration 工具面收口 |
| `go-agent-v2/internal/apiserver/methods_thread.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/thread/rpc.go` | thread handler |
| `go-agent-v2/internal/apiserver/methods_thread_helpers.go` | 合并 | `internal/module/thread/service.go` | thread 辅助逻辑并入 service |
| `go-agent-v2/internal/apiserver/methods_thread_turn.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/thread/rpc.go` + `internal/module/turn/rpc.go` | thread/turn 边界拆开 |
| `go-agent-v2/internal/apiserver/methods_turn.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/turn/rpc.go` | turn handler |
| `go-agent-v2/internal/apiserver/methods_ui_code_open.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/uistate/runtime.go` + `internal/ui/dashboard/code_open.go` | UI 读写代码面拆分 |
| `go-agent-v2/internal/apiserver/methods_ui_lsp_gui.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/uistate/runtime.go` | GUI LSP handler |
| `go-agent-v2/internal/apiserver/methods_ui_projects.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/uistate/runtime.go` | project 侧栏归 UI handler |
| `go-agent-v2/internal/apiserver/methods_ui_sidebar.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/uistate/runtime.go` | sidebar 统一 |
| `go-agent-v2/internal/apiserver/methods_ui_state.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/uistate/runtime.go` | ui state handler |
| `go-agent-v2/internal/apiserver/methods_ui_state_helpers.go` | 合并 | `internal/module/uistate/runtime.go` | ui state 拼装逻辑 |
| `go-agent-v2/internal/apiserver/notifications.go` | 合并 | `internal/platform/rpc/server.go` | 通知发送归 transport 层 |
| `go-agent-v2/internal/apiserver/orchestration_report.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` | orchestration report 独立 |
| `go-agent-v2/internal/apiserver/provider_adapter_registry.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` | provider 注册归 orchestration 模块 |
| `go-agent-v2/internal/apiserver/server.go` | 重写 | `internal/platform/rpc/server.go` | 不再保留 God Server |
| `go-agent-v2/internal/apiserver/server_approval.go` | 重写 | `internal/platform/rpc/*` + `internal/module/turn/service.go` | approval 从 server 抽离 |
| `go-agent-v2/internal/apiserver/server_bootstrap.go` | 重写 | `internal/app/app.go` + `internal/app/lifecycle.go` | 启动骨架重建 |
| `go-agent-v2/internal/apiserver/server_conn.go` | 重写 | `internal/platform/rpc/server.go` | 连接管理归 RPC platform |
| `go-agent-v2/internal/apiserver/server_conn_ws.go` | 重写 | `internal/platform/rpc/transport_ws.go` | WS transport 独立 |
| `go-agent-v2/internal/apiserver/server_context.go` | 丢弃 | - | `fx` 构造消灭 nil-guard 汇总文件 |
| `go-agent-v2/internal/apiserver/server_dynamic_tools.go` | 重写 | `cmd/mcp-orch/adapter/*` + `cmd/mcp-lsp/adapter/*` | 动态工具桥接按 family 下沉到独立二进制 |
| `go-agent-v2/internal/apiserver/server_dynamic_tools_diff.go` | 合并 | `internal/ui/runtime/workspace.go` | diff 更新归 UI runtime |
| `go-agent-v2/internal/apiserver/server_event_handler.go` | 重写 | `internal/module/uistate/projection.go` | 事件处理变为投影器 |
| `go-agent-v2/internal/apiserver/server_ida.go` | 重写 | `cmd/mcp-ida/*` | IDA runtime 与工具面留在独立二进制 |
| `go-agent-v2/internal/apiserver/server_ida_stub.go` | 合并 | `cmd/mcp-ida/*` | stub 逻辑并入同一 IDA family |
| `go-agent-v2/internal/apiserver/server_lifecycle.go` | 重写 | `internal/app/lifecycle.go` | 生命周期归 app |
| `go-agent-v2/internal/apiserver/server_memory_stats.go` | 合并 | `internal/platform/rpc/*` | debug 统一出口 |
| `go-agent-v2/internal/apiserver/server_payload.go` | 合并 | `internal/platform/rpc/codec.go` | payload 编解码归 codec |
| `go-agent-v2/internal/apiserver/server_payload_filechange.go` | 合并 | `internal/ui/runtime/workspace.go` | 文件变化归 UI workspace 视图 |
| `go-agent-v2/internal/apiserver/server_phase1_deps.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | phase1 依赖显式化 |
| `go-agent-v2/internal/apiserver/server_state_groups.go` | 丢弃 | - | 旧状态组结构不保留 |
| `go-agent-v2/internal/apiserver/server_state_throttle.go` | 合并 | `internal/module/uistate/runtime.go` | 节流逻辑归 UI state |
| `go-agent-v2/internal/apiserver/server_thread_patch.go` | 重写 | `internal/module/thread/service.go` | thread patch 归 thread service |
| `go-agent-v2/internal/apiserver/server_transport.go` | 重写 | `internal/platform/rpc/server.go` | transport 主循环归 infra |
| `go-agent-v2/internal/apiserver/server_user_input_responder.go` | 重写 | `internal/module/turn/service.go` | request_user_input 归 review 服务 |
| `go-agent-v2/internal/apiserver/tool_provider_adapters.go` | 重写 | `cmd/mcp-orch/adapter/*` + `cmd/mcp-lsp/adapter/*` + `cmd/mcp-ida/adapter/*` | tool adapter 按 family 收口 |
| `go-agent-v2/internal/apiserver/tool_providers.go` | 重写 | `internal/sidecar/lsp/tools/*` + `internal/sidecar/orch/tools/*` + `cmd/mcp-ida/tools/*` | schema 导出按 family 拆分 |
| `go-agent-v2/internal/apiserver/workspace_methods.go` | 重写 | `internal/platform/rpc/method.go` + `internal/module/workspace/rpc.go` | workspace handler |

### 6.2 `go-agent-v2/internal/store/`

| V2 文件 | 动作 | V3 归宿 | 说明 |
|---|---|---|---|
| `go-agent-v2/internal/store/agent_provider_binding.go` | 迁移 | `internal/platform/db/queries/agent_provider_binding.sql` + `internal/store/binding/store.go` | provider binding 核心 repo |
| `go-agent-v2/internal/store/agent_status.go` | 迁移 | `internal/platform/db/queries/agent_status.sql` + `internal/store/agentstatus/store.go` | agent 状态 repo |
| `go-agent-v2/internal/store/agent_thread.go` | 迁移 | `internal/platform/db/queries/agent_thread.sql` + `internal/store/thread/store.go` | agent thread repo |
| `go-agent-v2/internal/store/agent_thread_binding.go` | 合并 | `internal/platform/db/queries/agent_provider_binding.sql` + `internal/store/binding/store.go` | legacy codex binding 吸收进 provider binding |
| `go-agent-v2/internal/store/ai_log.go` | 迁移 | `internal/platform/db/queries/ai_log.sql` + `internal/store/ailog/store.go` | AI log repo |
| `go-agent-v2/internal/store/audit_log.go` | 迁移 | `internal/platform/db/queries/audit_log.sql` + `internal/store/auditlog/store.go` | 审计日志 repo |
| `go-agent-v2/internal/store/bus_log.go` | 迁移 | `internal/platform/db/queries/bus_log.sql` + `internal/store/buslog/store.go` | bus sink repo |
| `go-agent-v2/internal/store/command_card.go` | 迁移 | `internal/platform/db/queries/command_card.sql` + `internal/store/commandcard/store.go` | command card repo |
| `go-agent-v2/internal/store/cwd_lock.go` | 迁移 | `internal/platform/db/queries/cwd_lock.sql` + `internal/store/cwdlock/store.go` | cwd lock repo |
| `go-agent-v2/internal/store/db_query.go` | 迁移 | `internal/platform/db/queries/db_query.sql` + `internal/store/dbquery/store.go` | db query repo |
| `go-agent-v2/internal/store/doc.go` | 丢弃 | - | 包文档不迁移 |
| `go-agent-v2/internal/store/helpers.go` | 合并 | `internal/platform/db/queries.go` | mapper 与共用 helper |
| `go-agent-v2/internal/store/interaction.go` | 迁移 | `internal/platform/db/queries/interaction.sql` + `internal/store/interaction/store.go` | interaction repo |
| `go-agent-v2/internal/store/models.go` | 重写 | `internal/contract/*` + `internal/dto/*` + `internal/store/sqlc/models.go` | 模型回核心层，DB model 由 sqlc 生成 |
| `go-agent-v2/internal/store/preference_scope.go` | 合并 | `internal/platform/db/tx.go` | context scope 统一 |
| `go-agent-v2/internal/store/prompt_template.go` | 迁移 | `internal/platform/db/queries/prompt_template.sql` + `internal/store/prompt/store.go` | prompt template repo |
| `go-agent-v2/internal/store/shared_file.go` | 迁移 | `internal/platform/db/queries/shared_file.sql` + `internal/store/sharedfile/store.go` | shared file repo |
| `go-agent-v2/internal/store/sql_safety.go` | 重写 | `internal/platform/db/query_guard.go` | SQL 安全门槛集中 |
| `go-agent-v2/internal/store/system_log.go` | 迁移 | `internal/platform/db/queries/system_log.sql` + `internal/store/systemlog/store.go` | system log repo |
| `go-agent-v2/internal/store/task_ack.go` | 迁移 | `internal/platform/db/queries/task_ack.sql` + `internal/store/taskack/store.go` | task ack repo |
| `go-agent-v2/internal/store/task_dag.go` | 迁移 | `internal/platform/db/queries/task_dag.sql` + `internal/store/task/store.go` | DAG 主表 repo |
| `go-agent-v2/internal/store/task_dag_phase1.go` | 迁移 | `internal/platform/db/queries/task_dag_wakeup.sql` + `internal/store/task/store.go` | wakeup/lease 合并到 DAG repo |
| `go-agent-v2/internal/store/task_trace.go` | 迁移 | `internal/platform/db/queries/task_trace.sql` + `internal/store/tasktrace/store.go` | task trace repo |
| `go-agent-v2/internal/store/topology_approval.go` | 迁移 | `internal/platform/db/queries/topology_approval.sql` + `internal/store/topologyapproval/store.go` | topology approval repo |
| `go-agent-v2/internal/store/ui_preference.go` | 迁移 | `internal/platform/db/queries/ui_preference.sql` + `internal/store/uipreference/store.go` | ui preference repo |
| `go-agent-v2/internal/store/workspace_run.go` | 迁移 | `internal/platform/db/queries/workspace_run.sql` + `internal/store/workspace/store.go` | workspace run repo |

### 6.3 `go-agent-v2/internal/runner/`

| V2 文件 | 动作 | V3 归宿 | 说明 |
|---|---|---|---|
| `go-agent-v2/internal/runner/doc.go` | 丢弃 | - | 包文档不迁移 |
| `go-agent-v2/internal/runner/launch_facade.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | facade 被 manager 吸收 |
| `go-agent-v2/internal/runner/manager.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` + `internal/platform/statemachine/factory.go` | manager 分拆 |
| `go-agent-v2/internal/runner/manager_auto_recover.go` | 重写 | `internal/sidecar/orch/orchestration/recover.go` | 自动恢复统一 |
| `go-agent-v2/internal/runner/manager_event.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` + `internal/provider/unified/event_map.go` | 事件派发拆层 |
| `go-agent-v2/internal/runner/manager_launch.go` | 重写 | `internal/sidecar/orch/orchestration/runner_actor.go` | launch 与 supervise 收口 |
| `go-agent-v2/internal/runner/manager_lifecycle.go` | 重写 | `internal/sidecar/orch/orchestration/runner_actor.go` | lifecycle 合并 |
| `go-agent-v2/internal/runner/manager_recover.go` | 重写 | `internal/sidecar/orch/orchestration/recover.go` | recovery 统一文件 |
| `go-agent-v2/internal/runner/manager_submission.go` | 重写 | `internal/sidecar/orch/orchestration/runner_actor.go` | 提交队列独立 |
| `go-agent-v2/internal/runner/manager_wakeup_context.go` | 重写 | `internal/sidecar/orch/orchestration/contract.go` | wakeup 上下文独立 |
| `go-agent-v2/internal/runner/provider_registry.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` | provider registry 新位置 |
| `go-agent-v2/internal/runner/ringbuf.go` | 合并 | `internal/sidecar/orch/orchestration/service.go` | 缓冲与快照合并 |

### 6.4 `go-agent-v2/internal/bus/`

| V2 文件 | 动作 | V3 归宿 | 说明 |
|---|---|---|---|
| `go-agent-v2/internal/bus/bus.go` | 重写 | `internal/platform/bus/bus.go` | 底座替换为 typed bus wrapper |
| `go-agent-v2/internal/bus/doc.go` | 丢弃 | - | 包文档不迁移 |
| `go-agent-v2/internal/bus/orchestration.go` | 重写 | `internal/sidecar/orch/orchestration/service.go` | orchestration 事件归 task service |
| `go-agent-v2/internal/bus/resilient.go` | 重写 | `internal/platform/bus/subscription.go` | fallback/retry 作为订阅策略 |
| `go-agent-v2/internal/bus/router.go` | 重写 | `internal/module/uistate/projection.go` + `internal/platform/bus/typed.go` | 路由从字符串改为 typed 投影 |
| `go-agent-v2/internal/bus/types.go` | 重写 | `internal/contract/agent/event.go` + `internal/platform/bus/typed.go` | 消息类型回核心事件模型 |

### 6.5 `go-agent-v2/legacy-agentsdk/`

| V2 文件 | 动作 | V3 归宿 | 说明 |
|---|---|---|---|
| `go-agent-v2/legacy-agentsdk/agentcore/client.go` | 重写 | `internal/contract/provider.go` | 统一 provider port |
| `go-agent-v2/legacy-agentsdk/agentcore/doc.go` | 丢弃 | - | 包文档不迁移 |
| `go-agent-v2/legacy-agentsdk/agentcore/types.go` | 重写 | `internal/contract/agent/event.go` + `internal/dto/thread/config.go` + `internal/dto/turn/input.go` | 共享类型回归核心层 |
| `go-agent-v2/legacy-agentsdk/claude/capabilities.go` | 合并 | `internal/provider/unified/capabilities.go` | capability matrix 收口 |
| `go-agent-v2/legacy-agentsdk/claude/client.go` | 重写 | `internal/provider/claudecli/driver.go` | Claude driver |
| `go-agent-v2/legacy-agentsdk/claude/client_cli_events.go` | 重写 | `internal/provider/claudecli/event_map.go` | Claude event map |
| `go-agent-v2/legacy-agentsdk/claude/client_cli_transport.go` | 重写 | `internal/provider/claudecli/transport.go` + `internal/provider/unified/mcp_manifest.go` | transport 与 manifest 分离 |
| `go-agent-v2/legacy-agentsdk/claude/doc.go` | 丢弃 | - | 包文档不迁移 |
| `go-agent-v2/legacy-agentsdk/claude/history_backend.go` | 重写 | `internal/provider/claudecli/history.go` | Claude history |
| `go-agent-v2/legacy-agentsdk/claude/session.go` | 合并 | `internal/provider/unified/session.go` | session 抽象上提 |
| `go-agent-v2/legacy-agentsdk/codex/client.go` | 重写 | `internal/provider/codexapp/driver.go` | Codex driver |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver.go` | 重写 | `internal/provider/codexapp/driver.go` | app-server 会话控制 |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go` | 重写 | `internal/provider/codexapp/event_map.go` | Codex event map |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver_filter.go` | 丢弃 | - | DynamicTools deny list 不再存在 |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver_health.go` | 合并 | `internal/provider/codexapp/recovery.go` | health/retry 合并 |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver_helpers.go` | 合并 | `internal/provider/codexapp/transport.go` | transport helper |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver_jsonrpc_id.go` | 合并 | `internal/platform/rpc/codec.go` | JSON-RPC id 处理回基础设施 |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go` | 重写 | `internal/provider/codexapp/transport.go` | app-server protocol |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go` | 合并 | `internal/provider/codexapp/driver.go` | runtime 行为合并 |
| `go-agent-v2/legacy-agentsdk/codex/client_appserver_transport.go` | 重写 | `internal/provider/codexapp/transport.go` | transport 主体 |
| `go-agent-v2/legacy-agentsdk/codex/doc.go` | 丢弃 | - | 包文档不迁移 |
| `go-agent-v2/legacy-agentsdk/codex/events.go` | 合并 | `internal/provider/codexapp/event_map.go` | event 类型并入 driver |
| `go-agent-v2/legacy-agentsdk/codex/interface.go` | 丢弃 | - | alias 型兼容层不保留 |
| `go-agent-v2/legacy-agentsdk/codex/rollout_reader.go` | 重写 | `internal/provider/codexapp/history.go` | rollout reader 私有化 |
| `go-agent-v2/legacy-agentsdk/doc.go` | 丢弃 | - | v2 兼容文档不迁移 |
| `go-agent-v2/legacy-agentsdk/guardtest/function_surface.go` | 丢弃 | - | 文件形状 guard 不迁移 |
| `go-agent-v2/legacy-agentsdk/pathutil/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/pathutil/pathutil.go` | 合并 | `internal/dto/shared/path.go` | path 工具回 shared |
| `go-agent-v2/legacy-agentsdk/service/archive/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_core.go` | 重写 | `internal/module/thread/service.go` | archive 服务统一 |
| `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_io.go` | 合并 | `internal/module/thread/service.go` | IO 细节合并 |
| `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_ops.go` | 合并 | `internal/module/thread/service.go` | archive 操作合并 |
| `go-agent-v2/legacy-agentsdk/service/archive/thread_archive_utils.go` | 合并 | `internal/module/thread/service.go` | util 合并 |
| `go-agent-v2/legacy-agentsdk/service/command/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/command/slash_command_logic.go` | 重写 | `internal/module/thread/service.go` | slash command 语义变为显式 service 方法 |
| `go-agent-v2/legacy-agentsdk/service/common/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/common/turn_common_paths.go` | 合并 | `internal/dto/shared/path.go` | 公共路径逻辑上提 |
| `go-agent-v2/legacy-agentsdk/service/history/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/history/thread_history_core.go` | 重写 | `internal/module/thread/service.go` | thread history 统一 |
| `go-agent-v2/legacy-agentsdk/service/interrupt/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go` | 重写 | `internal/module/turn/service.go` | interrupt 统一 |
| `go-agent-v2/legacy-agentsdk/service/lifecycle/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go` | 重写 | `internal/module/thread/service.go` | thread lifecycle |
| `go-agent-v2/legacy-agentsdk/service/lifecycle/turn_resume_core.go` | 合并 | `internal/module/thread/service.go` | resume 合并 |
| `go-agent-v2/legacy-agentsdk/service/listing/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/listing/thread_listing_core.go` | 重写 | `internal/module/thread/service.go` | listing service |
| `go-agent-v2/legacy-agentsdk/service/messages/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/messages/thread_messages_logic.go` | 重写 | `internal/module/thread/service.go` | messages 归 history |
| `go-agent-v2/legacy-agentsdk/service/prompt/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/prompt/turn_prompt_core.go` | 重写 | `internal/module/turn/service.go` | prompt compose 统一 |
| `go-agent-v2/legacy-agentsdk/service/rollout/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/rollout/thread_messages_hydration_core.go` | 合并 | `internal/module/thread/service.go` | history hydration 合并 |
| `go-agent-v2/legacy-agentsdk/service/rollout/thread_messages_rollout_core.go` | 合并 | `internal/module/thread/service.go` | rollout merge 合并 |
| `go-agent-v2/legacy-agentsdk/service/runtime/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/runtime/stream_timeout.go` | 合并 | `internal/platform/config/timeouts.go` | timeout 集中化 |
| `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go` | 重写 | `internal/module/turn/service.go` | turn prepare |
| `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_adapters.go` | 合并 | `internal/module/turn/service.go` | runtime adapter 不再独立存在 |
| `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go` | 重写 | `internal/module/turn/service.go` | turn runtime |
| `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_operations.go` | 合并 | `internal/module/turn/service.go` | operations 合并 |
| `go-agent-v2/legacy-agentsdk/service/runtime/turn_steer_alignment.go` | 合并 | `internal/module/turn/service.go` | steer 逻辑合并 |
| `go-agent-v2/legacy-agentsdk/service/support/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/support/interrupt_state.go` | 合并 | `internal/module/turn/service.go` | interrupt state 并入 tracker |
| `go-agent-v2/legacy-agentsdk/service/support/prompt_match.go` | 重写 | `internal/module/skill/service.go` | prompt match 归 skill |
| `go-agent-v2/legacy-agentsdk/service/tracker/doc.go` | 丢弃 | - | 文档不迁移 |
| `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_core.go` | 重写 | `internal/module/turn/service.go` | tracker 核心 |
| `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_lifecycle_core.go` | 合并 | `internal/module/turn/service.go` | lifecycle 合并 |
| `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_rules_core.go` | 合并 | `internal/module/turn/service.go` | rules 合并 |
| `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_stall_core.go` | 合并 | `internal/module/turn/service.go` | stall 合并 |
| `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_state_core.go` | 合并 | `internal/module/turn/service.go` | state 合并 |
| `go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_summary_core.go` | 合并 | `internal/module/turn/service.go` | summary 合并 |

### 6.6 `go-agent-v2/pkg/toolsdk/`

> 2026-03-22 路径收敛更新：
> `go-agent-v2/pkg/toolsdk/` 在 V3 的终态不再落到 `internal/tool/*`、`internal/tool/registry/*` 或 family-specific `internal/mcpserver/*`。
> family-specific 代码统一进入 `cmd/mcp-lsp/*`、`cmd/mcp-orch/*`、`cmd/mcp-ida/*`；P8 的 `cmd/mcp-orch` 不再依赖共享 MCP common 层。

| V2 范围 | 动作 | V3 终态归宿 | 说明 |
|---|---|---|---|
| `go-agent-v2/pkg/toolsdk/lsp/*` | 重写/拆分 | `internal/sidecar/lsp/tools/*` + `cmd/mcp-lsp/adapter/*` + `cmd/mcp-lsp/mcpserver/*` | LSP/RUN 的 schema、handler、runtime、protocol 都留在独立二进制 |
| `go-agent-v2/pkg/toolsdk/tools/{lsp_tools.go,lsp_tools_ide.go,lsp_schema_builder.go,code_run.go}` | 重写/合并 | `internal/sidecar/lsp/tools/*` | `lsp_*`、`code_run*` 只在 `cmd/mcp-lsp` 注册 |
| `go-agent-v2/pkg/toolsdk/tools/{orchestration.go,orchestration_report.go,resource*.go,providers.go}` | 重写/合并 | `internal/sidecar/orch/tools/*` + `cmd/mcp-orch/adapter/*` | orchestration/task/workspace/command/prompt/shared_file 工具统一落在 `cmd/mcp-orch` |
| `go-agent-v2/pkg/toolsdk/tooladapter/*` | 按 family 重写 | `cmd/mcp-lsp/adapter/*` + `cmd/mcp-orch/adapter/*` + `cmd/mcp-ida/adapter/*` | adapter 不再落到 `internal/tool/registry/*` |
| `go-agent-v2/pkg/toolsdk/tools/{helpers.go,tool_result_success.go,truncation.go,types_sdk.go}` | 按 family 吸收 | `internal/sidecar/lsp/tools/*` + `internal/sidecar/orch/tools/*` + `cmd/mcp-ida/tools/*` | 结果包络、schema helper、截断规则跟随各 family 二进制 |
| `go-agent-v2/pkg/toolsdk/tools/{ida_tools.go,ida_tools_p3.go,ida_tools_p4.go,ida_tools_p5.go}` | 重写/合并 | `cmd/mcp-ida/tools/*` | IDA 家族独立二进制 |
| `go-agent-v2/pkg/toolsdk/visualide/*` | 合并 | `internal/ui/dashboard/code_open.go` | UI 视图层逻辑不进入 MCP family |

### 6.7 补充模块（package 级）

| V2 模块 | 动作 | V3 归宿 | 说明 |
|---|---|---|---|
| `go-agent-v2/internal/uistate/*` | 重写 | `internal/ui/runtime/*` + `internal/module/uistate/*` | 投影层和状态拼装拆开 |
| `go-agent-v2/internal/service/*` | 重组 | `internal/module/thread|turn|skill|orchestration|workspace|dashboard|uistate|lspgui` + `cmd/mcp-*/tools/*` | 宿主业务域回到 `internal/module/*`，MCP tool surface 落到独立二进制 |
| `go-agent-v2/internal/mcp/*` | 重写 | `cmd/mcp-*/local runtime/*` | 各 family 本地化协议层与服务装配 |
| `go-agent-v2/internal/dashboard/*` | 重写 | `internal/ui/dashboard/*` + `internal/module/dashboard/*` + `internal/platform/rpc/*` | dashboard 变成 UI 视图层 |
| `go-agent-v2/pkg/idamcp/*` | 重组 | `cmd/mcp-ida/*` + 保留必要低层适配 | 优先保持域隔离，不强行深拆 |
| `go-agent-v2/cmd/agent-terminal/*` | 重写 | `cmd/agent-terminal/*` | 保留入口职责但削薄 |
| `go-agent-v2/cmd/mcp-server/*` | 重写 | `cmd/mcp-lsp/*` + `cmd/mcp-orch/*` + `cmd/mcp-ida/*` | 从混编改为三二进制 |
| `go-agent-v2/cmd/server/*` | 合并/丢弃 | `cmd/agent-terminal/*` | 重复入口不再单独保留 |

---

## 7. 风险与对策

| 风险 | 具体表现 | 对策 |
|---|---|---|
| 新旧不一致 | V3 行为与 V2 golden/contract 不一致 | 先迁规格测试，再迁实现；所有批次都必须过 replay/contract |
| 迁移周期长 | P4-P7 容易拖延 | 批次封板；每批完成必须形成可独立验证的运行面 |
| 重写陷阱 | 边写边改需求，V3 失控 | V2 只做 bugfix；新需求先在 V2 写规格，再决定是否进入 V3 |
| 漏迁边界逻辑 | V2 guard 很多，容易漏掉残余逻辑 | 只提炼行为规格，不保留文件形状；加 shadow replay |
| sqlc 漂移 | schema 变更和 query 不同步 | migration checksum + `sqlc generate` 进 CI |
| Provider 收敛失败 | 统一层退化成 if/else 泥球 | capability table + driver contract；provider-specific 逻辑只在 driver 包 |
| MCP family 再次混编 | 为省事重新在 runtime 注册所有工具 | 三二进制独立构建验收；family import boundary check |
| 桌面入口耦合回潮 | Wails 重新把对象图绑回 app struct | `fx` 装配与 UI facade 分离，Wails 只拿 facade |

### 7.1 风险监控指标

每周必须检查：

1. V3 是否出现新的手写大装配函数。
2. `context.WithTimeout` 是否又散落出 `internal/platform/config/timeouts.go` 之外。
3. provider 层是否重新出现 `DynamicTools` 贯穿链路。
4. MCP family 是否出现交叉 import。

### 7.2 明确禁止事项

1. 禁止在 V3 新增第二套 RPC 注册链。
2. 禁止让 `module/*` 直接 import `store/sqlc`。
3. 禁止让 driver 直接 import repo。
4. 禁止恢复单一混编 `go-agent-v2/cmd/mcp-server` 作为终态。

---

## 8. 里程碑与验收标准

### 8.1 批次 Done 标准

| 批次 | Done 标准 |
|---|---|
| P0 | `fx` 对象图成立；4 个目标二进制可构建；空启动/空退出通过 |
| P1 | 20 个 repo 均有 sqlc query + repo adapter；行为测试通过 |
| P2 | typed event bus 接通；旧 bus 不再承担业务主流程 |
| P3 | runner 状态迁移表完整；matrix 测试通过；无双状态表示 |
| P4 | Claude/Codex 同走统一 turn request + MCP manifest；无 `DynamicTools` 直传 |
| P5 | 151（含 23 noop）方法统一注册；push/approval/fx 图闭环；无 God Object server |
| P6 | Wails 可驱动主流程；入口优雅关闭；无手写 WaitGroup 启停 |
| P7 | `skill/workspace/dashboard/uistate/lspgui` 宿主模块闭环；`internal/module/*` 中无 MCP tool 定义 |
| P8 | `cmd/mcp-orch` 暴露 19 个可交付工具；`task_start_node` 明确延后；`orchestration_*` / `task_*` 在本地 runtime 执行，`workspace_*` / `command_*` / `prompt_*` / `shared_file_*` 走本地 store/sqlc；prompt 宿主 UI surface 保持可用；MCP 新依赖方向 archtest 通过 |
| P9 | `cmd/mcp-lsp` 独立运行；LSP/RUN 工具完整；MCP 新依赖方向 archtest 与 stdio contract 通过 |
| P10 | shared 提升、命名漂移修正、module.go 纯化完成；不再承担 MCP 依赖守卫补课 |

### 8.2 最终交付标准

最终交付必须同时满足：

1. **架构完成**
   - `fx = 工厂`
   - `run.Group = 引擎`
   - `stateless = 状态机`
   - `jrpc2 = RPC`
   - `sqlc = Store`
   - `kelindar/event = typed bus`
2. **Provider 收敛完成**
   - 没有 `codexadapter` 等价层
   - 没有 `DynamicTools` 主链路
   - 所有工具都经统一 MCP manifest
3. **MCP 拆分完成**
   - 只有三个家族二进制
   - 不再有全量混编 MCP 终态
4. **契约完成**
   - V2 的关键行为规格全部在 V3 有对应测试
5. **切换完成**
   - Desktop 主流程可用
   - Workspace/DAG 可用
   - Provider 双端可用
   - IDA 场景可按需挂载

### 8.3 最终 grep 级验收项

以下 grep 级规则建议直接写进 CI：

1. `grep -R "DynamicTools" internal/provider internal/module internal/platform internal/app`
   只允许出现在 `mcp_manifest.go` 和 driver 兼容垫片。
2. `grep -R "withRequiredThreadID" .`
   结果必须为 0。
3. `grep -R "provider == \"codex\"" internal`
   只能出现在 driver 注册表或 capability 表。
4. `grep -R "context.WithTimeout" internal`
   只能出现在 `internal/platform/config/timeouts.go` 使用点或明确允许的 transport/retry 层。
5. `go list -deps ./cmd/mcp-lsp`
   不得包含 IDA 包。
6. `go list -deps ./cmd/mcp-ida`
   不得包含 LSP/orchestration 包。

### 8.4 最终切换建议

最终切换分 4 步：

1. V3 shadow replay
2. V3 canary desktop
3. 默认入口切换到 V3
4. V2 进入只读维护期

### 8.5 最终判断标准

如果某个时点 V3 还需要回答以下问题之一，说明不能切：

- “这个方法现在到底走老注册链还是新注册链？”
- “这个工具现在到底在单一 mcp-server 里还是 family binary 里？”
- “这个状态到底看 `proc.State` 还是看另一个派生字段？”
- “这个 SQL 是 repo 发的还是 service 自己拼的？”

只要这些问题还存在，说明迁移尚未完成。

---

## 附录 A：本方案对现有 V3 文档的补充关系

| 现有文档 | 本文新增内容 |
|---|---|
| `2026-03-19-framework-selection.md` | 给出批次、包树、逐文件归宿、验收标准 |
| `2026-03-19-provider-convergence.md` | 新增“三 MCP family 二进制”约束，并把它并入总迁移计划 |
| `2026-03-19-migration-v3.md` | 把原本的迁移模式草案扩展成可执行路线 |

## 附录 B：执行顺序一句话总结

先搭骨架。
再迁数据。
再迁事件。
再迁状态机。
再收敛 Provider。
再迁 RPC。
再接入口。
最后把 `cmd/mcp-orch`、`cmd/mcp-lsp` 和 P10 的 shared/架构收口到终态。
