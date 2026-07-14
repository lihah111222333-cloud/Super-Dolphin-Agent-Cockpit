---
name: "后端"
description: "在 super-agent-v3 中编写、审查或重构 Go 后端代码时使用。"
trigger_words: ["Go后端", "golang", "go", "backend", "fx", "sqlc", "jrpc2", "rungroup", "stateless", "mcp", "V3架构"]
---

# Go 后端开发规范 (V3 契约合规版)

> **代码审查是交付的一部分：** 按本技能编写或修改的后端代码会依据 [`代码审查维度`](../代码审查维度/SKILL.md) 中与变更面适用的维度接受审查。实现时必须预留可复查的源码、测试、LSP diagnostics、门禁与 Git 状态证据；“能编译”或单次测试通过不代表审查通过。

## 子文件按需加载

详细内容拆分在同目录子文件中。读完本文件后，根据任务只加载最相关的子文件；若任务跨越多个后端边界，再按证据追加读取。

| 加载场景 | 内容摘要 | 子文件 |
|---------|---------|-------|
| 命名变量/函数/包、格式化代码、写注释 | 命名 (MixedCaps/包名/接口名)、格式化 (gofmt/行宽120)、导入分组 (3组)、文档注释规范、Happy Path 编码 | `./naming_formatting.md` |
| 快速查阅 Go 惯用写法、原则确认 | Effective Go 官方规则精选: 格式化/命名/错误处理/接口设计/并发/文档注释/控制结构/零值可用 | `./effective_go_rules.md` |
| V3 目录结构、分层设计、fx 模块化 | 目录结构 (`cmd/mcp-*`, `internal/module/*`, `internal/platform/*`)、依赖注入 (`fx.Module`, `fx.Provide`)、边界隔离 | `./project_structure.md` |
| 重构文件、消除重复、代码审查 | 文件组织红线、DRY、工厂模式、接口隔离 (实现私有化，接口公开)、Options 模式 | `./code_organization.md` |
| 错误处理、错误映射、日志上下文 | jrpc2 标准错误映射 (`jrpc2.Errorf`)、应用级业务 code、日志预留字段常量、上下文透传 | `./error_handling.md` |
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
| **DI 容器** | 运行时装配 MUST 统一由 `go.uber.org/fx` 完成，禁止使用包级全局变量。 |
| **持久化** | 数据库操作 MUST 使用 `sqlc` 生成 `Querier` 接口，严禁引入 ORM 或手写增删改查。 |
| **RPC 通信** | RPC 通信 MUST 基于 `github.com/creachadair/jrpc2` 实现 JSON-RPC 2.0，禁止直接使用 Gin 暴露 HTTP 接口。 |
| **生命周期** | 长跑任务 MUST 注入 `group:"runners"` 并由 `internal/platform/runner.RunGroup` 托管；禁止在构造器、handler 或 singleton 初始化中启动未受控 goroutine。 |
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
| 单文件下限 | \<50 行文件不自动判错；只有当它只是无边界价值的碎片化包装时才合并，测试、契约、窄端口和清晰 owner 边界可以保留。 |
| 隐藏实现 | 模块只暴露 Interface 和 `fx.Module`，具体的结构体实现应当保持包私有。 |
| 接口定义 | 接口在使用方定义，而非实现方。 |

---

## 提交与推送门禁（证据主导）

门禁结论必须由**本次实际提交或推送对象的可复查证据**支持，不能只根据计划、历史结果、代理口述、空日志、单个 `PASS` / `DONE` 或工作区曾经全绿作出判断。命令退出码为 0 只是必要条件；还必须确认检查对象、输出中的实际 gate、Git 对象和远端状态彼此一致。

### 提交门禁

| 判定项 | 必须保留的证据 | 判定规则 |
|------|----------------|---------|
| Hook 已启用 | `git config --get core.hooksPath` 指向 `.githooks`；首次接入使用 `make install-hooks` | 未启用 hook 时，不得把普通 `git commit` 描述为已经过仓库提交门禁。 |
| pre-commit | 本次 `git commit` 输出中出现实际执行的 `pre-commit` gate 及最终 `pre-commit OK`，且命令退出码为 0 | 以 staged index 为检查对象；必须处理生成物刷新、全量 guard，以及变更语言对应的格式化、vet、测试或前端检查。 |
| commit-msg | 本次提交输出或复跑证据显示中文提交信息 guard 与 fix-test guard 通过 | `fix` / `hotfix` / `bugfix` / `修复` 类提交必须在同一提交包含锁定缺陷的测试、fixture、golden 或 snapshot。 |
| 提交对象 | `git rev-parse HEAD`、`git show --stat --oneline --decorate HEAD`，必要时核对 `git diff HEAD^ HEAD -- <scope>` | 必须证明门禁通过后生成的 commit 就是声称已提交的变更；只有工作区 diff 或暂存状态不算已提交。 |

提交门禁失败、被跳过或证据缺失时，只能报告“未通过/未证实”。禁止使用 `--no-verify` 常态绕过；若用户明确授权紧急绕过，必须披露跳过了哪些 gate、补跑结果和仍缺失的证据，不得声称正常门禁已通过。

### 推送门禁

| 判定项 | 必须保留的证据 | 判定规则 |
|------|----------------|---------|
| 推送对象 | push 前记录 `git rev-parse HEAD`、当前分支与目标 remote/ref | `.githooks/pre-push` 只允许推送当前 `HEAD`；检查对象不一致即阻断。 |
| pre-push | 本次 `git push` 输出中出现实际执行的提交信息、fix-test、AI maintenance 和变更影响面 gate，最终 `pre-push OK`，且 push 退出码为 0 | Go 变更按 push range 跑受影响包；前端变更跑 lint、非 e2e test、build；相关 sqlc、capability contract、技能镜像检查必须按 hook 条件执行。 |
| 生成物门禁 | codemap / project-map check 的命令与退出码 | pre-commit 和 pre-push 都必须 fail-fast；任一 drift 或生成命令失败均为 blocker，不得降级成 warning。 |
| 远端落点 | push 成功输出，并用 `git ls-remote <remote> <ref>` 或等价远端查询确认目标 ref SHA 等于推送前记录的 `HEAD` | `pre-push OK` 只证明推送前检查通过，不证明远端已接收；远端 SHA 未对齐时不得声称“已推送”。 |

### 结论口径

- “可提交”：与变更面匹配的验证已通过，但尚未生成 commit。
- “已提交”：提交门禁通过，且已核对 commit SHA 与提交内容；同时报告未提交/未跟踪的同范围改动。
- “可推送”：提交对象和 pre-push 所需检查具备通过证据，但尚未确认远端更新。
- “已推送”：`git push` 成功，且远端目标 ref SHA 与本地目标 commit 一致。
- 任一强制 gate 失败均为 blocker；未覆盖的 deferred E2E / CI 检查和脏工作区必须单独披露，不能被汇总成全绿。

提交/推送 hook 的当前行为以 `.githooks/pre-commit`、`.githooks/commit-msg`、`.githooks/pre-push` 和 `.githooks/README.md` 为事实来源；技能不得复制一份与 hook 漂移的静态命令清单。

---

## 项目文档索引

*本技能配套的具体落地规范，请查阅当前工作区内的以下契约文档：*

**仓库边界：** 本仓库没有独立后端子模块；不要使用其他仓库的后端子目录命令或旧守卫入口。store 层默认 SQLite，路径由 `SUPER_DOLPHIN_SQLITE_PATH` / `SUPER_DOLPHIN_HOME` 决定；不要把其他数据库或 ORM 作为默认实现。

| 文档 | 路径 |
|------|------|
| 契约与骨架索引 | `docs/契约/README.md`, `docs/架构/README.md` |
| 模块化与 fx DI 契约 | `docs/契约/modularity-convention.md`, `docs/契约/fx-convention.md` |
| sqlc 数据库契约 | `docs/契约/sqlc-convention.md` |
| 生命周期与 RunGroup 契约 | `docs/契约/rungroup-convention.md` |
| RPC 与 MCP 契约 | `docs/契约/jrpc2-convention.md`, `docs/契约/mcp-service-convention.md` |
| 状态机与事件总线契约 | `docs/契约/statemachine-event-convention.md` |
| 架构骨架 | `docs/架构/skeleton-fx.md`, `docs/架构/skeleton-rungroup.md`, `docs/架构/skeleton-jrpc2.md`, `docs/架构/skeleton-event.md`, `docs/架构/skeleton-stateless.md` |
