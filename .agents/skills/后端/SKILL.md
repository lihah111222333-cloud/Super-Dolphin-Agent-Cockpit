---
name: "后端"
description: "完整的 Go 后端开发指南，涵盖 Effective Go 最佳实践、super-agent-v3 架构契约（fx, SQLite/sqlc, jrpc2, rungroup, stateless）。在编写、审查或重构 Go 代码时使用此技能。"
trigger_words: ["Go后端", "golang", "go", "backend", "fx", "sqlc", "jrpc2", "rungroup", "stateless", "mcp", "V3架构"]
---

# Go 后端开发规范 (V3 契约合规版)

## 子文件按需加载

详细内容拆分在同目录子文件中。读完本文件后，根据任务用 `view_file` 加载**仅需的那 1 个子文件**。

| 加载场景 | 内容摘要 | 子文件 |
|---------|---------|-------|
| 命名变量/函数/包、格式化代码、写注释 | 命名 (MixedCaps/包名/接口名)、格式化 (gofmt/行宽120)、导入分组 (3组)、文档注释规范、Happy Path 编码 | `./naming_formatting.md` |
| 快速查阅 Go 惯用写法、原则确认 | Effective Go 官方规则精选: 格式化/命名/错误处理/接口设计/并发/文档注释/控制结构/零值可用 | `./effective_go_rules.md` |
| V3 目录结构、分层设计、fx 模块化 | 目录结构 (`cmd/mcp-*`, `internal/module/*`, `internal/platform/*`)、依赖注入 (`fx.Module`, `fx.Provide`)、边界隔离 | `./project_structure.md` |
| 重构文件、消除重复、代码审查 | 文件组织红线、DRY、工厂模式、接口隔离 (实现私有化，接口公开)、Options 模式 | `./code_organization.md` |
| 错误处理、错误映射、日志上下文 | jrpc2 标准错误映射 (`jrpc2.Errorf`)、应用级业务 code、日志预留字段常量、上下文透传 | `./error_handling.md` |
| 后台任务托管、并发模型、生命周期管理 | `oklog/run` 契约 (`execute/interrupt` 模型)、Runner 接口、禁用独立 goroutine 的规则 | `./concurrency_basics.md` |
| 写测试、排查 bug、依赖注入测试 | 表驱动测试、测试辅助函数、`fx` Graph 依赖方向测试坑、状态机全矩阵测试 (State Matrix Test) | `./testing_pitfalls.md` |

---

## 核心强制规则 (始终生效，无需加载子文件)

### 格式化与命名

| 规则 | 要求 | 示例 |
|------|------|------|
| 格式化 | MUST `gofmt`，推荐 `goimports` 自动排序导入 | `goimports -w .` |
| 导出命名 | MixedCaps (大写开头) | `func NewService()`, `type Agent struct` |
| 未导出命名 | mixedCaps (小写开头) | `func parseConfig()`, `var runtimeCache` |
| 包名 | 小写单词，无下划线，简短 | `user`, `rpc`, `config` |
| 接口命名 | 单方法接口: 动词+er 后缀 | `Reader`, `Writer`, `Closer` |

### 架构基础 (V3 契约)

| 规则 | 要求 |
|------|------|
| **DI 容器** | 运行时装配 MUST 统一由 `go.uber.org/fx` 完成，禁止使用包级全局变量。 |
| **持久化** | 数据库操作 MUST 使用 `sqlc` 生成 `Querier` 接口，严禁引入 ORM 或手写增删改查。 |
| **RPC 通信** | RPC 通信 MUST 基于 `github.com/creachadair/jrpc2` 实现 JSON-RPC 2.0，禁止直接使用 Gin 暴露 HTTP 接口。 |
| **生命周期** | 长跑任务和后台 goroutine MUST 由 `github.com/oklog/run` (RunGroup) 托管，禁止 `go func(){}` 满天飞。 |
| **状态机** | 复杂实体生命周期 MUST 使用 `qmuntal/stateless` 进行全矩阵映射，禁止零散的 `switch/case` 和二次副作用推导。 |
| **事件总线** | 进程内事件解耦 MUST 使用 `kelindar/event`，必须使用强类型结构体传递，禁止使用 `map[string]any`。 |

### 错误处理与日志

| 规则 | 要求 |
|------|------|
| ALWAYS 检查 | 每个返回 error 的调用 MUST 检查，NEVER `val, _ := fn()` |
| jrpc2 错误映射 | 返回给客户端的错误 MUST 遵循 JSON-RPC 标准，使用 `jrpc2.Errorf(Code, msg)` 并映射应用 Code。 |
| 跨层边界 | 内部跨层调用应适当包装错误，保留原始原因。 |
| 结构化日志 | MUST 从 Context 获取日志实例 (`logger.FromContext(ctx)`)，并使用预留的 `FieldXxx` 常量。 |

### 代码组织与接口

| 规则 | 要求 |
|------|------|
| 禁止 chains | NEVER `_chains.go` 文件，0 容忍。 |
| 单文件下限 | \<50 行的文件 MUST 合并到父文件。 |
| 隐藏实现 | 模块只暴露 Interface 和 `fx.Module`，具体的结构体实现应当保持包私有。 |
| 接口定义 | 接口在使用方定义，而非实现方。 |

---

## 项目文档索引

*本技能配套的具体落地规范，请查阅当前工作区内的以下契约文档：*

**仓库边界：** 本仓库没有独立后端子模块；不要使用其他仓库的后端子目录命令或旧守卫入口。store 层默认 SQLite，路径由 `SUPER_DOLPHIN_SQLITE_PATH` / `SUPER_DOLPHIN_HOME` 决定；不要把其他数据库或 ORM 作为默认实现。

| 文档 | 路径 |
|------|------|
| 模块化与 fx DI 契约 | `docs/契约/modularity-convention.md`, `docs/契约/fx-convention.md` |
| sqlc 数据库契约 | `docs/契约/sqlc-convention.md` |
| 生命周期与 RunGroup 契约 | `docs/契约/rungroup-convention.md` |
| RPC 与 MCP 契约 | `docs/契约/jrpc2-convention.md`, `docs/契约/mcp-service-convention.md` |
| 状态机与事件总线契约 | `docs/契约/statemachine-event-convention.md` |
