# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

> [!IMPORTANT]
> **🤖 100% AI 编写与守护**
> 本仓库的所有代码与文档——包括全部 Go 后端逻辑、React 前端界面、AST/SSA 编译器级守卫规则以及本说明文档——**完全由 AI 智能体自主编写和重构**，人类仅负责提供宏观的架构方向与设计决策。这是“自防御仓库模式（Self-Guarding Repo Pattern）”最真实的活体实证。

**AI 原生的软件治理与多智能体开发控制平面。**

Super Dolphin Agent 面向以 AI 智能体为主要维护力量的软件项目。它在一个桌面控制平面中整合多智能体会话、工具执行、MCP 编排、多语言 LSP、定时任务、记忆、Provider 原生技能、实时事件流与机器强制的工程边界。

英文 [README.md](README.md) 是规范真源。若翻译与英文在产品含义、命令、路径、环境变量、规则 ID 或许可证身份上存在差异，以英文为准。

## 它具体是做什么的？

Super Dolphin Agent 不是聊天机器人、Cursor 提示词模板或 API 包装器。它是一个**本地桌面运行态环境与编译器级软件治理防火墙**，旨在让 AI 智能体自主开发和维护软件，同时确保代码库不会沦为屎山。

它通过将系统拆分为以下三个协同运行的支柱，彻底解决了**“AI 编码黑盒熵增”**的问题：

1. **本地控制中心（桌面应用）**：基于 Wails 的桌面应用（`cmd/agent-terminal`），提供可视化控制台。它能启动和观察多智能体会话、实时查看工具调用链、用自然语言调度定时任务、管理本地 SQLite 向量记忆，并实时以事件流形式输出 AI 工作区日志。
2. **代码智能引擎（LSP 与 MCP Sidecars 旁路进程）**：
   - **LSP 旁路进程 (`cmd/mcp-lsp`)**：通用多语言 Language Server Protocol 服务，负责为 AI 索引整个代码仓，为其提供精准、结构化的“跳转定义”、“查找引用”和“类型层级分析”等编译器级代码感知能力，代替粗暴的文本搜索。
   - **编排旁路进程 (`cmd/mcp-orch`)**：协调 Model Context Protocol (MCP) 并管理工具执行 DAG（有向无环图），确保 AI 必须通过结构化、受控的接口读写文件与运行指令，而不是滥用裸 bash 脚本。
3. **免疫系统（AST/SSA 单元测试守卫）**：直接嵌入在项目的 Go 单元测试集（`internal/archtest`）中。它在测试运行时，会将 AI 变更的代码编译为 Go 编译器底层的**静态单赋值（SSA）中间表示**进行数据流分析。这能物理卡死 AI 的各种代码坏味道（如静默吞错、在 OnStart 里造成死锁、或者跨越洋葱架构边界调用底层的 Data/Store），从源头上拦截不合格的 Git commit。

### 生产级参考实现（学习防腐治理）

Super Dolphin Agent 是一套生产级的多智能体编排系统。虽然专为实际生产负载设计，但它同时可以作为一个高标准的参考仓库，供开发者学习：

1. **如何解决氛围编程的痛点（免受 AI 结构熵增）**：它提供了一套完整的、经生产验证的“免疫软件工程”蓝图。它向开发者展示了如何在标准的 Go 单元测试中编写 AST 语法分析、构建 SSA 静态数据流分析，以及如何设计自动收紧的质量棘轮门禁，以此保护项目不受 AI 编码熵增的侵蚀。
2. **生产级的多智能体架构设计**：项目本身的源码就是一套干净、基于依赖注入（`fx` 框架）、契约优先（`internal/contract`）的多 Agent 桌面控制平面的工业级实现。它为以下技术方向提供了清晰的参考模式：
   - 如何安全地启动、关闭和恢复并发运行的 Agent 协程（Worker Goroutines）。
   - 如何拉起 Stdio MCP 旁路子进程，并将裸 JSON-RPC 协议转换为类型安全的 Go 结构体。
   - 如何在本地 JSONL 数据库中持久化与管理会话历史。
   - 如何设计和运行基于 SQLite 的本地向量记忆搜索。

<!-- sd:why -->
## 为纯 AI 维护而设计

“AI 维护”不是让 AI 在没有审查的情况下任意改代码，也不要求 AI 一次理解整个仓库。它意味着 AI 是主要实现力量，而仓库本身提供完成可靠修改所需的导航、约束与证据。人类继续负责产品目标、高影响决策、凭据与发布。

维护闭环围绕“有限上下文”设计：

1. 通过生成式代码地图和文件级 AI 项目地图定位变更面。
2. 通过能力契约和明确的架构约定理解公开行为。
3. 通过 LSP 的定义、引用、调用层级和 diagnostics 修改小范围代码。
4. 通过 AST、SSA、依赖边界、复杂度预算和 fail-fast 规则限制改动。
5. 通过聚焦测试、生成物检查和变更感知门禁证明结果后，才允许提交或推送。

这套机制不依赖“人类或 AI 必须把整个代码库记在脑中”这一脆弱前提。

### 项目由来：直面 AI 代码腐化

Super Dolphin Agent 于 2026 年 3 月 19 日从 `go-agent-v2` 的全新迁移开始。V2 是我们此前开发的商业闭源原型项目，结合了自动化量化交易与多智能体桌面控制。该原型已经证明了产品价值：智能体会话、工具、Provider、事件、恢复和桌面交互都工作良好。然而，它完全是由 AI 智能体仅在 *软约束*（Markdown 指南和系统提示词）下编写完成的。

在没有机器强制执行的“仓库免疫守卫”情况下，每次局部的功能添加都在侵蚀全局架构的清晰度。久而久之，V2 累加了 80 多个手写 RPC 方法，参数绑定、校验、能力判断、日志、错误映射和注册路径分散在不同文件。生命周期逻辑散落在多个 manager 文件中，并混入异步恢复副作用和隐式状态机。中心事件处理器一度膨胀到 557 行；手工对象的初始化装配超过 200 行。代码虽然仍在运行，但要回答“系统的权威行为到底在哪里”已经变得越来越难。

这就是我们定义的 **AI 代码腐化（AI Code Rot 或 AI 结构熵）**：这不是指责开发者，也不等于系统立即不能运行，它意味着系统局部功能仍然正常，但其全局契约、依赖边界与代码质量已在退化。高速的 AI 迭代会显著放大这种失败模式，因为每个局部看似合理的补丁都可能暗中增加一条隐式路径，最终迫使整个项目陷入 AI 因上下文上限和代码混乱而无法安全推进的泥潭。

最初的 V3 决策拒绝在约 8.3 万行的旧系统上原地换引擎。旧系统被保留为行为证据，各项能力则按函数粒度迁入显式契约。Super Dolphin 是将这些教训转化为公共开源范式后的产物，将仓库本身打造为了对抗 AI 熵增的免疫系统：

| V2 的结构熵 | Super Dolphin 的回应 |
|---|---|
| 手写 RPC 与分散的横切逻辑 | typed request、统一契约面、显式 middleware 与错误语义 |
| 生命周期迁移和副作用散落 | 声明式状态迁移、类型安全事件与明确 owner 的 lifecycle runner |
| 手工 `New()` / `Close()` 对象图 | `fx` 组合根与显式启动/关闭所有权 |
| 业务模块耦合存储和外部适配器 | 洋葱边界、Module-owned Port 与防腐 adapter |
| 混合抽象层级的巨型函数 | 组合方法与 `80 / 4 / 10` 函数、嵌套、复杂度预算 |
| 依赖评审者记住约定 | AST/SSA 守卫、地图、清单、hooks 与可复现证据 |

因此，V2 不是需要隐藏的历史，而是 Super Dolphin 治理体系持续对抗的失效模型。

### 工程防腐：阻断 AI Code Rot

AI 能快速生产代码，也能快速放大架构漂移。Super Dolphin 把这种漂移视为 **AI Code Rot（AI 代码腐化）**，并尽量在引入位置附近把它转换成机器可见的错误。

| 防腐层 | 阻断的问题 | 仓库中的证据 |
|---|---|---|
| 导航真源 | 改错子系统、依赖过期心智模型 | `docs/doc/codemap`、project map、capability manifest |
| 架构边界 | Module 直接依赖 Store、Provider、UI 或 Command 实现 | 类型化边界注册表与 AST import 评估 |
| 语义守卫 | 吞错、静默兜底、不安全生命周期与过宽编排接口 | AST 守卫与 priority SSA 分析 |
| 复杂度预算 | 一个函数混入业务、基础设施、协议和持久化细节 | 默认函数有效行数 `<= 80`、嵌套 `<= 4`、圈复杂度 `<= 10` |
| 债务棘轮 | 新改动继续恶化旧债，或通过重建基线“洗白” | 生产/测试冻结分区拒绝新增违规，并在代码改善时自动收缩 |
| 可复现门禁 | 没有地图、测试、生成物与精确证据却宣称完成 | pre-commit、pre-push 与变更感知 AI maintenance gates |

80 行不是适用于所有项目的教条，而是针对本项目编排型负载的边界：流程函数应表达同一抽象层级，通过组合方法调用窄接口，而不是把协议、数据库和业务细节写成几百行流水账。更深的规则是：**策略必须可见，细节必须封装，例外必须显式且可测量。**

### 为什么它不是又一个 Agent Framework

| 常见 Agent Framework | Super Dolphin Agent |
|---|---|
| 优化任务执行 | 治理任务如何改变真实软件系统 |
| 给 AI 更多工具和上下文 | 给 AI 有限上下文、能力契约和允许的依赖方向 |
| 把任务运行结束视为成功 | 要求测试、diagnostics、生成状态和 Git 证据 |
| 主要依赖提示词纪律 | 在代码、测试、hooks 和生成清单中执行不变量 |
| 用默认值或重试掩盖故障 | 配置、状态或依赖异常时立即 fail-fast |

```text
目标
  -> 代码地图 + 能力契约
  -> 通过 LSP/MCP 的小范围 AI 修改
  -> AST/SSA/架构守卫
  -> 聚焦测试 + 生成物检查
  -> 可审查证据
  -> 接受提交
```

<!-- sd:architecture -->
## 架构概览

```text
cmd/                 桌面入口、MCP 编排与多语言 LSP sidecar
frontend-app/        当前 React/Vite 桌面前端
internal/contract/   跨模块接口与 DTO
internal/module/     Turn、Prompt、Cron、Memory、Skill 等业务逻辑
internal/platform/   DB、RPC、配置与运行时安全
internal/provider/   Codex、Claude CLI 等 AI Provider 适配器
internal/store/      基于 sqlc 的持久化适配与手写包装
pkg/                 可复用公共库
```

核心业务层只依赖内层契约；Store 充当领域与 SQL 实现之间的防腐层；Provider、MCP、UI 位于外层；`cmd/*` 和组合根负责显式装配。详见[代码地图](docs/doc/codemap/README.md)与[洋葱架构契约](docs/%E5%A5%91%E7%BA%A6/onion-architecture-convention.md)。

## 核心能力

- 多智能体会话、恢复、分叉、调度和实时事件流。
- MCP 编排 sidecar 与通用多语言 LSP peer。
- Cron、Memory、Prompt、Thread 与 Provider 原生技能管理。
- Codex 和 Claude CLI Provider 适配，边界由统一契约保护。
- SQLite 持久化、Wails 桌面宿主与 React/Vite 前端。
- 代码地图、项目地图、能力契约、Archtest 与 AI 维护门禁。

<!-- sd:quick-start -->
## 快速开始

### 前置条件

- Go 1.25.7
- Node.js 20+
- 已安装并登录 OpenAI Codex CLI（当前新 UI 桌面流程必需）
- `gopls`，以及 JS/TS 导航所需的 `typescript-language-server` 与 `typescript@5.9.3`
- Claude Code CLI 仅在明确使用 Claude Provider 时需要

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks
( cd frontend-app && npm install )
./run-new-ui-desktop.sh
```

Windows PowerShell：

```powershell
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks
cd frontend-app; npm install; cd ..
.\run-new-ui-desktop.ps1
```

SQLite 默认位于 `SUPER_DOLPHIN_HOME/super-dolphin.db`；可通过 `SUPER_DOLPHIN_SQLITE_PATH` 指定其他本地文件。运行时规范技能位于 `<workspace>/.agents/skills/` 与 `~/.super-dolphin/skills/personal/{user,agent,imported}/`。

<!-- sd:governance-demo -->
## 可复现的治理证明

先查看某次变更会选择哪些门禁：

```bash
./scripts/ai_maintenance_gates.sh --print-plan --base HEAD
```

直接运行核心防腐检查：

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

这些命令验证架构规则、AST/SSA 守卫、代码导航生成物、project map 漂移和 capability contract。检查模式只读并在真源过期时失败；只有明确更新真源时才使用对应的 `*-refresh` 目标。

完整验证：

```bash
make test
( cd frontend-app && npm run lint && npm test && npm run build )
```

<!-- sd:security -->
## 安全

- 不要提交凭据、Provider home、本地数据库、日志、用户 Memory 或机器配置。
- 缺失配置与异常依赖遵循 [fail-fast 契约](docs/%E5%A5%91%E7%BA%A6/fail-fast-convention.md)，静默兜底属于缺陷。
- 公共源码导出器只读取已提交 Git 对象，并通过默认拒绝策略排除内部计划、归档、运行证据、本地工作区与未跟踪文件。
- 敏感漏洞应私下报告给仓库所有者，不要在公开 Issue 中附带利用细节、密钥或用户数据。

<!-- sd:community -->
## 社区与贡献

欢迎 Issue 和范围明确的 Pull Request。请保持改动小而可验证，遵守模块边界，为修复提供同提交回归测试，并运行与变更面匹配的门禁。架构决策应落成契约和可执行守卫，而不只存在于提示词中。

- [代码地图](docs/doc/codemap/README.md)
- [架构契约](docs/%E5%A5%91%E7%BA%A6/README.md)
- [项目 Agent 指令](AGENTS.md)
- [Apache License 2.0](LICENSE)

## 许可证

本项目采用 [Apache License 2.0](LICENSE)，版权声明见 [NOTICE](NOTICE)。
