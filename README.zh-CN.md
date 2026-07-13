# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**面向 AI 编写软件的自守护仓库。** AI 智能体负责实现改动；由仓库拥有的地图、契约、测试与门禁决定这些改动是否足够安全，可以留下。

> [!IMPORTANT]
> **维护者声明：原创代码与项目自有文档 100% 由 AI 编写，由人类指导，由仓库守护。** 产品代码、测试代码和项目自有文档均由 AI 智能体编写或重构。人类负责产品意图、架构裁决、凭据和发布。AI 作者身份不代表绝对正确：每项被接受的改动仍必须通过仓库拥有的证据与门禁。上游法律和社区文本保留其原始署名。

Super Dolphin Agent 是一个 **AI 原生的软件治理与多智能体开发控制平面**。它把本地桌面运行时、MCP 编排、多语言 LSP 导航、Provider 集成、持久化工作流和机器强制执行的工程边界整合为一个可工作的参考实现。

英文 [README.md](README.md) 是规范概览。各译本保持相同的产品范围、命令、路径、环境变量、仓库身份和许可证。详细事实见[架构说明](docs/open-source/ARCHITECTURE.md)、[治理实证](docs/open-source/GOVERNANCE.md)以及生成的[代码地图](docs/doc/codemap/README.md)。

<!-- sd:why -->
## 为什么需要 Super Dolphin

大多数 Agent 框架优化的是任务执行。Super Dolphin 还治理一项已完成任务可以对长期演进的软件系统做出什么改动。

它的维护闭环分为六个阶段：

1. **定位**：通过生成的代码地图和能力契约找到目标区域。
2. **理解**：通过 LSP 获取定义、引用、调用层级和诊断。
3. **修改**：只改动边界明确且所有权清晰的狭窄范围。
4. **约束**：用 AST/SSA 规则、依赖边界、复杂度预算和 fail-fast 契约约束 diff。
5. **证明**：用聚焦测试、生成物检查和变更感知门禁证明结果。

6. **学习**：从已经证实的修复中提取根因、归纳不变量，并把反复出现的模式提升为回归证据或可执行守卫。

### 给氛围编程加上护栏

AI 生成代码的速度可以达到人工编写的十倍甚至百倍，因此瓶颈会从“产出代码”转移到“测试与可信交付”。如果同类缺陷仍可能潜伏在其他位置，或再次出现在 AI 生成的代码中，那么只修复一个实例不算完成。

Super Dolphin 会定期汇总已经被测试或真实使用证明的 Bug 修复证据，将其沉淀为可复用的工程知识；稳定的模式再被提升为仓库自有的测试、fixture、AST/SSA 规则、依赖策略或其他可执行门禁。一旦 AI 再次生成曾经出现过的坏味道，门禁会拒绝这项改动并强制 AI 修复，直到满足交付规格。

Skill 和提示词可以指导生成，代码守卫则决定什么能够被接受，后者具有更强的强制约束力。候选守卫仍必须具备可复现证据、可泛化的不变量和确定性的验收检查——这是证据驱动的基线收紧，不是不受约束的自我修改。当前仓库已经实现自动记忆整合和大规模代码守卫基础设施；把每一次修复全自动、端到端地转化为新守卫，仍是持续建设的工程方向，而不是已经覆盖所有缺陷的宣传口号。

这正是 AI 原生氛围编程的演进方向：人类定义意图、架构和验收边界，AI 只能在规格内生成代码；仓库从缺陷中学习并持续收紧工程基线，让系统越来越健壮、代码越来越清晰，而不是依赖人工反复发现和处理同一类 Bug。

### 有界上下文维护

本仓库的设计目标，是让常规改动不必把整个代码库装入单个模型上下文。生成式导航、窄契约和确定性失败信号帮助智能体找到相关范围，并快速修复违规。

这不保证所有改动都是局部的。横跨多个模块的工作仍需更广泛的引用与影响面分析，所有被接受的改动也仍须具备相应的测试和评审证据。

### 开发历程：为什么会有 Super Dolphin

Super Dolphin 是一条连续工程演进路线的第三个主要阶段：

1. **第一阶段**是 Python 命令行多 Agent 工具，用来验证模型能否拆分任务、通过工具协作，并完成真实工程工作。
2. **`go-agent-v2` 是这个工程的直接前身。** 它从内部任务分发工具逐步发展为能够实际工作的工程系统，整合了自动化量化交易工作流、多智能体桌面控制、Provider 集成和持久化执行。它在真实工作中证明了产品方向的价值，并不是一个准备丢弃的原型。
3. **Super Dolphin / V3 于 2026 年 3 月 19 日启动**，代表新的架构阶段。它继承前身已经验证的能力和运行经验，同时重建长期 AI 驱动开发所需的工程基础。

V3 出现的原因不是前身不能工作。恰恰相反，前身能够工作并持续增加功能；但 AI 生成局部改动的速度，已经超过仅靠约定和人工评审的架构所能安全吸收的速度。测试可以证明一条局部路径正确，但系统整体的所有权、生命周期、依赖方向和可读性仍会继续退化。根据维护者的发布前记录，这种压力最终表现为：

- 80 多个 RPC 方法累积出多套并行的绑定、校验、能力判断和日志路径；
- 生命周期所有权分散到多个 manager 和异步副作用中；
- 一个中心事件处理器增长到 557 行；
- 手工应用装配超过 200 行。

因此，V3 不是一次普通的功能升级。它把原本存在于评审者记忆和提示词中的架构知识，迁移到仓库自有的契约、代码地图、类型化边界、回归证据和可执行门禁中。它要解决的失效模式就是 **AI 代码腐化（AI code rot）**：局部改动仍然工作，但全局契约、所有权边界和可读性持续退化。

前身的私有开发历程属于维护者提供的背景，而不是公开证据。因此，公开仓库展示的是从这些经验中形成的架构回应、代码守卫、回归 fixture 和可复现命令。

| 前身暴露的工程压力 | Super Dolphin 的回应 |
|---|---|
| 并行的手写 RPC 路径 | 类型化请求、单一契约面、显式 middleware 与错误语义 |
| 分散的生命周期副作用 | 声明式状态迁移、类型化事件和所有权明确的 lifecycle runner |
| 手工对象图 | `fx` 组合以及明确的启动、关闭所有权 |
| 业务代码耦合 adapter | 洋葱边界、模块自有 port 和防腐 adapter |
| 混合抽象层级的巨型函数 | 本仓库专用的 `80 / 4 / 10` 函数长度、嵌套与复杂度预算 |
| 把评审者记忆当作规则 | AST/SSA 守卫、生成地图、manifest、hook 和可复现证据 |

`80 / 4 / 10` 预算不是普适的代码风格规则，而是针对这个编排密集型仓库设置并持续收紧的约束：默认有效函数长度 `<= 80`、嵌套 `<= 4`、圈复杂度 `<= 10`。

### 仓库强制执行什么

| 层级 | 防范的问题 | 仓库证据 |
|---|---|---|
| 导航真源 | 修改错误的子系统或依赖过时的项目认知 | `docs/doc/codemap`、project map、capability manifest |
| 架构边界 | 领域代码越过边界访问 Store、Provider、UI 或 Command 实现 | 类型化后端边界注册表与 AST import 求值 |
| 语义守卫 | 忽略错误、静默 fallback、不安全的生命周期路径和宽服务传播 | AST 守卫与 priority SSA 分析 |
| 复杂度棘轮 | 新代码增加已知结构债务 | 函数、嵌套、复杂度以及 production/test freeze 分区 |
| 验收证据 | 把智能体的“完成”状态当作证明 | 聚焦测试、生成状态检查、Git hook 和变更感知门禁 |

### 有历史来源的案例

维护者记录了五起发布前事件，如今均有公开回归证据：LSP 使用了错误 worktree 的 scope、Provider identity 缺失、持久 Agent 缺少运行时真相、异步 UI 失败被静默吞掉，以及架构守卫被 type alias 绕过。

请阅读[治理实证](docs/open-source/GOVERNANCE.md)中对历史事件与公开证据边界的说明，并运行其中保留的全部证明。

### 为什么它不是又一个 Agent 框架

| 常见 Agent 框架 | Super Dolphin Agent |
|---|---|
| 优化任务执行 | 治理任务如何改变真实软件系统 |
| 给智能体更多工具和上下文 | 给智能体有界上下文和允许的依赖方向 |
| 把一次运行结束视为成功 | 要求测试、诊断、生成状态检查和 Git 证据 |
| 主要依赖提示词纪律 | 在代码、测试、hook 和生成 manifest 中强制不变量 |
| 用重试或默认值掩盖状态缺失 | 配置、身份、所有权或依赖缺失时 fail-fast |

<!-- sd:architecture -->
## 架构

```text
frontend-app/             React/Vite desktop UI
        |
cmd/agent-terminal/       Wails host and RPC boundary
        |
internal/app/             composition and anti-corruption adapters
        |
internal/contract/        stable ports and DTOs
        |
internal/module/          business capabilities
   |             |
internal/store/   internal/provider/
SQLite/sqlc       Codex and provider runtime integration

cmd/mcp-lsp/              generic multi-language LSP peer
cmd/mcp-orch/             orchestration, DAG, cron, and agent tools
```

关键依赖规则是所有权向内：模块定义自身需要的 port，adapter 实现这些 port；Platform 和 Provider 包不得向上导入业务模块。后端边界注册表是生成架构规则地图的单一真源。

组件职责、数据流、真源和已知范围见[架构说明](docs/open-source/ARCHITECTURE.md)。文件级导航请使用生成的[代码地图](docs/doc/codemap/README.md)。

### 当前范围

- 桌面应用及其针对本仓库的治理闭环已经在这里实现。
- `make guard` 及相关检查治理的是本仓库；它们不被宣传为适用于任意仓库的通用扫描器。
- 已检入的公共源码策略和校验基础组件属于发布就绪基础。完整的源码导出 CLI、密封 receipt 工作流、公共 CI 门禁和独立守卫发行物尚未作为已发布能力提供。
- 文档中的规范 GitHub URL 是公开发布目标。只有仓库所有者完成发布检查清单后，clone、Issue 与私密报告链接才可用。
- 当前桌面 Provider 流程需要 Codex。只有明确针对 Claude Provider 集成的工作才使用 Claude。

<!-- sd:quick-start -->
## 快速开始

### 前置条件

- Go 1.25.7
- Node.js 20+ 与 npm
- 已安装并完成认证的 OpenAI Codex CLI（`codex`）
- `gopls`
- `typescript-language-server` 与 TypeScript 5.9.3

下面的 clone 命令指向规范公共仓库，将在正式发布后可用。在此之前，现有维护者应继续使用当前已获授权的 checkout。

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks

go install golang.org/x/tools/gopls@latest
npm install -g typescript-language-server typescript@5.9.3
( cd frontend-app && npm ci )
```

运行当前桌面开发流程：

```bash
# macOS
./run-new-ui-desktop.sh

# Windows PowerShell
.\run-new-ui-desktop.ps1
```

SQLite 会自动创建在 `SUPER_DOLPHIN_HOME/super-dolphin.db`。设置 `SUPER_DOLPHIN_SQLITE_PATH` 可以使用其他本地文件。PostgreSQL 环境变量不是产品数据库的配置入口。

构建与测试：

```bash
make build-plain
make test
make frontend-app-build && go test ./... -count=1
( cd frontend-app && npm run lint && npm test && npm run build )
```

使用 linked Git worktree 的贡献者必须在编辑前构建并验证 worktree 本地的 LSP peer。确切命令见[贡献指南](CONTRIBUTING.md#worktree-and-lsp-readiness)。

<!-- sd:governance-demo -->
## 可复现的治理证明

查看一个明确变更文件会选择哪些门禁，但不实际执行：

```bash
./scripts/ai_maintenance_gates.sh --print-plan --changed-file README.md
```

运行本仓库的核心治理检查：

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

这些命令验证架构规则、守卫行为、生成式导航、project map 漂移和 capability manifest。它们只适用于本仓库，并会在真源过期时失败，而不会静默刷新。只有在所属真源被有意修改时，才使用显式的 `*-refresh` 目标。

## 代码质量

| 指标 | 当前真源 |
|---|---|
| 架构测试 | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 329 runnable `Test*` functions across 127 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| 架构规则 | [生成的后端边界地图](docs/doc/codemap/13-archtest-boundaries.md) |
| 测试覆盖率 | 从当前测试运行重新计算；不声明静态百分比 |
| CI | [GitHub Actions](.github/workflows/ci.yml) |

<!-- sd:security -->
## 安全

不要提交凭据、Provider home、本地数据库、日志、用户 Memory 或机器特定配置。身份、所有权、配置或依赖缺失时必须 fail-closed，不能静默降级。

请通过[安全策略](SECURITY.md)中的私密流程报告漏洞。不要在公开 Issue 中提交漏洞利用细节、密钥、trace payload 或用户数据。

<!-- sd:community -->
## 社区与贡献

欢迎范围明确的 Issue 和 Pull Request。请从以下文档开始：

- [贡献指南](CONTRIBUTING.md)
- [支持](SUPPORT.md)
- [行为准则](CODE_OF_CONDUCT.md)
- [路线图](docs/open-source/ROADMAP.md)
- [变更日志](CHANGELOG.md)
- [发布检查清单](docs/open-source/RELEASE_CHECKLIST.md)

欢迎 AI 辅助的贡献，但贡献者仍须对提交的 diff、测试、安全、许可证和证据负责。生成式回答或一次通过的 Agent 运行不能代替仓库门禁。

## 许可证

本项目采用 [Apache License 2.0](LICENSE)。项目及第三方署名说明见 [NOTICE](NOTICE)。
