---
name: "后端"
description: "在 super-agent-v3 中编写、审查或重构 Go 后端代码时使用。"
trigger_words: ["Go后端", "golang", "backend", "fx.Module", "sqlc", "jrpc2", "RunGroup", "stateless", "internal/module", "internal/store", "internal/platform/runner", "cmd/mcp-", "V3架构"]
---

# Go 后端开发规范 (V3 契约合规版)

> **代码审查是交付的一部分：** 按本技能编写或修改的后端代码会依据 [`代码审查维度`](../代码审查维度/SKILL.md) 中与变更面适用的维度接受审查。实现时必须预留可复查的源码、测试、LSP diagnostics、门禁与 Git 状态证据；“能编译”或单次测试通过不代表审查通过。

## 子文件按需加载

详细内容拆分在同目录子文件中。读完本文件后，根据任务只加载最相关的子文件；若任务跨越多个后端边界，再按证据追加读取。

| 加载场景 | 内容摘要 | 子文件 |
|---------|---------|-------|
| 命名变量/函数/包、格式化代码、写注释 | MixedCaps、gofmt、导入分组、仓库注释守卫、Happy Path | `./naming_formatting.md` |
| 快速查阅 Go 惯用写法、原则确认 | 只记录本仓库相对 Go 惯例的增量；版本以 `go.mod` 为准 | `./effective_go_rules.md` |
| V3 目录结构、分层设计、fx 模块化 | `internal/module`、`internal/store`、`internal/app/storeadapter` 三层边界与 Fx 装配 | `./project_structure.md` |
| 重构文件、消除重复、代码审查 | owner 边界、DRY、接口隔离、Fail-Fast 构造器 | `./code_organization.md` |
| 错误处理、错误映射、日志上下文 | transport-neutral 领域错误、RPC adapter 映射、错误根因和日志上下文 | `./error_handling.md` |
| 后台任务托管、并发模型、生命周期管理 | `internal/platform/runner` 契约、Runner 接口、受控 goroutine 规则 | `./concurrency_basics.md` |
| 写测试、排查 bug、依赖注入测试 | 表驱动测试、测试辅助函数、`fx` Graph 依赖方向测试坑、状态机全矩阵测试 (State Matrix Test) | `./testing_pitfalls.md` |

---

## 核心强制规则 (始终生效，无需加载子文件)

### 当前后端事实

| 事实 | 当前落点 |
|------|---------|
| 根装配 | `internal/app/modules.go` 是桌面和后台共享的根 Fx 图；`app.Module` 组合 platform、store、module、provider 和 toolbridge。 |
| Orchestration | DAG 编排由独立 `mcp-orch` MCP server 承担；桌面进程通过 contract/adapters 连接，不内嵌 orchestration module。 |
| 数据库 | `internal/platform/db/module.go` 打开 SQLite，启动期执行 migration / SQLite schema 校验；路径来自 `SUPER_DOLPHIN_SQLITE_PATH` / `SUPER_DOLPHIN_HOME`。 |
| Store | `internal/store/module.go` 中的 store.Module 是明确的聚合例外：只负责共享 `sqlc.Queries` 与子 store Fx module，不放业务逻辑。 |
| Store adapter | 单模块持久化端口由 `internal/module/<name>` 拥有，`internal/app/storeadapter/<name>` 负责 DTO 映射和 Fx 绑定，`internal/store/<name>` 负责 sqlc 实现。 |
| 契约层 | `internal/contract` 放跨模块窄端口、DTO 和哨兵错误；单模块 owner-local port 优先留在模块内。 |
| Provider | `internal/provider` 适配 Codex / Claude / unified session；provider-native mirror 在 provider 启动/acquire 前由 skill 模块刷新。 |
| Sidecar | `cmd/mcp-*` 是 MCP peer / sidecar 壳；通用 MCP 协议优先放 `internal/mcpserver/common`。 |

### 格式化与命名

| 规则 | 要求 | 示例 |
|------|------|------|
| 格式化 | MUST `gofmt`，推荐 `goimports` 自动排序导入 | `gofmt -w path/to/file.go` / `goimports -w path/to/file.go` |
| 导出命名 | MixedCaps (大写开头) | `func NewService()`, `type Agent struct` |
| 未导出命名 | mixedCaps (小写开头) | `func parseConfig()`, `var runtimeCache` |
| 包名 | 小写单词，无下划线，简短 | `user`, `rpc`, `config` |
| 接口命名 | 单方法接口: 动词+er 后缀 | `Reader`, `Writer`, `Closer` |

### 架构基础 (V3 契约)

| 规则 | 要求 |
|------|------|
| **DI 容器** | 运行时依赖和可变业务状态 MUST 通过构造函数/Fx 注入；禁止可变 service locator、进程级业务注册表和隐式全局状态。常量、哨兵错误及守卫明确允许的受控运行时设施不在此限。 |
| **持久化** | 产品 Store 查询默认由根 `sql/queries` 生成到 `internal/store/sqlc`；业务 module 只拥有窄端口，app storeadapter 完成映射，store 封装 sqlc。`cmd/mcp-orch` 使用自己的 SQL 树；新增手写 SQL 例外必须有契约、owner 和测试。 |
| **RPC 通信** | RPC 通信 MUST 基于 `github.com/creachadair/jrpc2` 实现 JSON-RPC 2.0，禁止直接使用 Gin 暴露 HTTP 接口。 |
| **生命周期** | 长跑任务 MUST 注入 `group:"runners"` 并由 `internal/platform/runner.RunGroup` 托管；禁止在构造器、handler 或 singleton 初始化中启动未受控 goroutine。 |
| **状态机** | 权威领域生命周期存在多状态、多触发器和非法迁移时使用 `qmuntal/stateless` 并做全矩阵验证；普通局部控制流不因此禁止 `switch`。 |
| **事件总线** | 需要多消费者解耦的领域事件使用 `kelindar/event`和强类型 payload；禁止以 `map[string]any`替代稳定跨层 DTO，但动态协议数据和局部控制结构按真实语义处理。 |

### 错误处理与日志

| 规则 | 要求 |
|------|------|
| ALWAYS 检查 | 每个返回 error 的调用 MUST 检查，NEVER `val, _ := fn()` |
| 领域错误 | `module/service` 返回 transport-neutral 的领域错误或哨兵错误，不直接构造 jrpc2 协议错误。 |
| jrpc2 映射 | `rpc.go` / `internal/platform/rpc` adapter 或 middleware 将领域错误映射为稳定 JSON-RPC code，禁止把底层错误细节直接暴露给客户端。 |
| 跨层边界 | 仅在跨 owner 且调用方需要上下文时包装一次并保留 `%w`，同一抽象内不要机械叠加包装；判断使用 `errors.Is/As`。 |
| 结构化日志 | 请求/RPC 边界优先从 Context 取得 trace-aware logger；长生命周期 actor 使用构造函数注入的 logger。字段键使用现有稳定常量，成功日志只能在操作确认成功后记录。 |

### 代码组织与接口

| 规则 | 要求 |
|------|------|
| 单文件下限 | \<50 行文件不自动判错；只有当它只是无边界价值的碎片化包装时才合并，测试、契约、窄端口和清晰 owner 边界可以保留。 |
| 隐藏实现 | 调用方不需要具体类型时保持实现私有；公开面由真实消费者决定，不机械要求模块只暴露 Interface。 |
| 接口定义 | 接口在使用方定义，而非实现方。 |

---

## 交付证据

提交和推送行为只以 `AGENTS.md`、`.githooks/pre-commit`、`.githooks/commit-msg`、`.githooks/pre-push`及`.githooks/README.md`为事实源；本技能不复制静态 gate 命令清单。技能改动至少运行`python3 scripts/validate_super_agent_skills.py`与`git diff --check`，Go 或架构门禁改动再通过`./scripts/test_with_guard.sh`运行受影响包。

结论口径保持严格：

- “可提交”：与变更面匹配的验证已通过，但尚未生成 commit。
- “已提交”：提交门禁通过，且已核对 commit SHA 与提交内容；同时报告未提交/未跟踪的同范围改动。
- “可推送”：提交对象和 pre-push 所需检查具备通过证据，但尚未确认远端更新。
- “已推送”：`git push` 成功，且远端目标 ref SHA 与本地目标 commit 一致。
- 任一强制 gate 失败均为 blocker；未覆盖检查和脏工作区必须单独披露，不能汇总成全绿。

---

## 项目文档索引

*本技能配套的具体落地规范，请查阅当前工作区内的以下契约文档：*

**仓库边界：** 本仓库没有独立后端子模块；不要使用其他仓库的后端子目录命令或旧守卫入口。store 层默认 SQLite，路径由 `SUPER_DOLPHIN_SQLITE_PATH` / `SUPER_DOLPHIN_HOME` 决定；不要把其他数据库或 ORM 作为默认实现。

| 文档 | 路径 |
|------|------|
| 契约与骨架索引 | `docs/契约/README.md`, `docs/架构/README.md` |
| 模块化与 fx DI 契约 | `docs/契约/modularity-convention.md`, `docs/契约/fx-convention.md` |
| sqlc 数据库契约 | `docs/契约/sqlc-convention.md` |
| 生命周期与 RunGroup 契约 | `docs/契约/rungroup-convention.md`, `docs/架构/skeleton-rungroup.md`, `internal/app/runner.go` |
| RPC 与 MCP 契约 | `docs/契约/jrpc2-convention.md`, `docs/契约/mcp-service-convention.md` |
| 状态机与事件总线契约 | `docs/契约/statemachine-event-convention.md` |
| 架构骨架 | `docs/架构/skeleton-fx.md`, `docs/架构/skeleton-rungroup.md`, `docs/架构/skeleton-jrpc2.md`, `docs/架构/skeleton-event.md`, `docs/架构/skeleton-stateless.md` |
