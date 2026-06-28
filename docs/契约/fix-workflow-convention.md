# Bug Fix 工作流规范

> 本规范用于团队内所有缺陷修复、回归修复、评审返修和工具契约修正。目标不是制造流程负担，而是保证每个 fix 都能被复现、被验证、被审查，并且不会用隐式兜底掩盖真实问题。

## 1. 全仓通用性评估结论

本规范由 2026-06-01 LSP 多轮 fix/评分流程复盘抽象而来，但不属于 LSP 专用规范。它适用于整个 `super-agent-v3` 仓库的 Go 后端、MCP sidecar、Provider/runtime、前端、SQL/store、打包脚本、文档、codemap、skills 和 release 验证。

通用性评估结论：当前规范的主干 `Repro -> Root Cause -> RED -> Fix -> GREEN -> Guard -> Residual Retest -> Report` 对全仓成立；需要避免把 LSP 的工具名、样本语言、输出字段形态写进主规则。LSP 只作为案例来源，具体规则必须表达为“契约边界、外部进程、评分口径、二进制产物、环境限制”等全仓通用概念。

全仓 fix 流程必须覆盖以下共性：

| 项 | 做得好的地方 | 需要固化的改进 |
|:---|:---|:---|
| 复现记录 | 能记录环境、调用参数、实际结果、预期结果和验收标准 | 每个问题固定一个最小复现 ID，计划、测试、报告都引用同一个 ID |
| 优先级 | 能区分 P0/P1/P2/P3，并说明影响 | 优先级绑定合入/发布门槛，例如 P0 未修不得合入，release scope 内 P1 未修不得发布 |
| 对照验证 | 能记录“已确认修复项”和“残留问题” | 残留复测作为 fix 完成前必选步骤，不作为事后补充 |
| 实施计划 | 能拆分任务、文件清单和验证命令 | 每个任务明确对应 bug-locking 测试或可执行验收命令 |
| 契约意识 | 能关注字段、状态、错误分类、生命周期和文档一致性 | 契约破坏类 bug 统一纳入 fail-fast 和同提交回归测试要求 |
| 多轮评估 | 能让多个 agent/reviewer 从不同角度评估结果 | 评估必须绑定样本矩阵、扣分原因和口径修正记录，不能只留下最终结论 |
| 外部边界 | 能通过真实进程、CLI、MCP、HTTP、DB、打包产物等边界验证 | 必须明确被测对象来源，禁止用测试内替代物冒充指定产物 |
| 中断续跑 | 能在会话重启后继续推进 | 必须恢复工作树、阶段、红测状态、生产改动和下一步，不能把 partial 写成 fixed |

从 LSP 复盘抽象出的全仓通用规则：

| 案例发现 | 全仓规则 |
|:---|:---|
| 超宽范围搜索曾打断工具进程 | 工具/CLI/服务评估必须先冻结样本集、数据规模和边界；压力测试单独记录，不混入常规评分 |
| 语义化 payload 曾被误判成“不统一” | 评审必须区分“通用控制 envelope”和“领域语义 payload”，不要为了表面统一损失语义 |
| Java/Rust/Python 场景受 classpath、workspace、依赖安装影响 | 多环境能力评分必须记录样本来源和环境完整性，先区分 environment / unsupported / implementation defect |
| 二进制 e2e 曾混淆“源码临时构建”和“指定产物调用” | e2e 必须写明被测产物来源；用户指定产物时，缺失应 fail-fast，不得静默自建替代物 |
| 多轮评分分数变化来自口径校准 | 多轮评估必须保留 baseline、fix 后、口径校准后的分数和原因 |
| 红测建立后修复轮被中断 | `red_locked`、`fixing`、`partial`、`fixed` 必须分开报告 |
| 多个红测合并在一个文件 | 允许合并文件，但报告必须逐条列出每个 bug/test 的 RED/GREEN 状态 |

## 2. 总原则

1. **先复现，后修复**：没有稳定复现或明确的不可复现说明，不进入代码修改。
2. **先锁 bug，后改代码**：fix 类提交必须包含同提交回归测试、fixture、golden、快照或可执行验收脚本。
3. **先改根因，后清症状**：不得只改输出、日志或文档来掩盖底层契约错误。
4. **Fail-Fast，不兜底**：异常、配置缺失、契约失配、数据缺失必须显式报错并阻断，禁止静默降级、默认配置和吞错继续。
5. **完成定义包含复测**：只跑单测不代表完成；必须复测原始复现步骤，并确认没有新增残留问题。
6. **范围可解释**：每个 fix 都要说明改动范围、未覆盖项和为什么没有扩大修复面。

## 3. 标准流程

### 3.1 Intake：缺陷接收

接收 bug 时必须记录：

| 字段 | 要求 |
|:---|:---|
| Bug ID | 推荐格式：`YYYY-MM-DD-area-short-name` |
| 来源 | 用户反馈、评审发现、CI、线上日志、dogfood、工具评测等 |
| 优先级 | P0/P1/P2/P3，按本规范第 4 节定义 |
| 影响面 | 用户路径、发布路径、数据正确性、安全性、开发工具链、文档契约 |
| 当前行为 | 可观察的实际结果，必须包含命令、输入或截图路径 |
| 预期行为 | 可判断的目标状态，不写“应该正常”这类空泛描述 |
| 合入门槛 | 修到什么程度才能合入、发布或解除阻塞 |

如果缺陷来自评审返修，必须保留原始 finding 的文件和行号。

### 3.2 Repro：最小复现

复现文档必须让另一个工程师在同仓库或指定对照仓库中独立跑通。

最小要求：

| 项 | 要求 |
|:---|:---|
| 环境 | 仓库路径、分支/commit、语言版本、关键环境变量 |
| 步骤 | 命令、工具调用、输入文件或 UI 操作 |
| 实际结果 | 原始输出摘要，必要时保留日志或截图 |
| 预期结果 | 字段、状态码、错误分类、文件内容或 UI 状态 |
| 对照组 | 至少一个“缩小范围后正常”或“邻近工具正常”的对照，避免误判 |
| 验收标准 | 修复后必须满足的可执行断言 |

不可复现时不能直接修。必须先补充“不可复现原因”和下一步证据需求，例如缺日志、缺版本、依赖外部服务不可达。

### 3.3 Triage：优先级与门槛

| 优先级 | 定义 | 门槛 |
|:---|:---|:---|
| P0 | 数据损坏、secret 泄露、供应链风险、核心路径不可用、会误导系统继续错误执行 | 未修不得合入 |
| P1 | 发布阻塞、主流程失败、契约破坏、fail-fast 破坏、测试/工具链结果不可信 | 当前交付范围内未修不得发布 |
| P2 | 边界行为错误、诊断不准、默认体验明显变差、可维护性风险 | 发布前应修；否则必须有 owner 和 follow-up |
| P3 | 文档、命名、非阻塞清理 | 可延后，但不得混入高优先级 fix 提交造成噪声 |

优先级不是工作量排序。高优先级小修先做；低优先级大重构不能阻塞 P0/P1 收口。

### 3.4 Root Cause：根因定位

根因分析至少回答四个问题：

1. 哪个契约被破坏：输入、输出、状态机、持久化、生命周期、权限、文档还是工具用法。
2. 破坏发生在哪个边界：入口参数、领域逻辑、store、provider、MCP handler、前端状态或打包脚本。
3. 为什么现有测试没有抓到：缺场景、断言太弱、只测 happy path、mock 不真实，还是 guard 缺口。
4. 最小修复面是什么：应修的 owner 文件、同包测试、需要同步的文档或 ADR。

禁止用“加默认值”“忽略异常”“返回空列表”“fallback 到旧字段”等方式替代根因修复，除非该 fallback 是已经被产品契约明确接受的兼容行为，并且有测试锁定边界。

### 3.5 RED：先证明测试能抓住 bug

每个 fix 至少满足一种 RED 证据：

| 类型 | 适用场景 | 要求 |
|:---|:---|:---|
| 新增失败测试 | 当前代码仍有 bug | 先运行新增测试，确认失败信息命中目标缺陷 |
| Mutation RED | 当前树已经被部分修过，无法自然复现旧 bug | 临时反向改动重引入 bug，测试必须失败；mutation 必须恢复 |
| 可执行复现 RED | 难以写单测的 CLI、打包、UI、外部工具问题 | 复现命令必须失败，并记录可审计输出 |
| Golden/Snapshot RED | 输出契约、格式、UI 快照 | 旧输出与目标快照差异必须明确 |

没有 RED 证据的 fix 必须在报告中说明原因，并补一个等价的行为锁定手段。

### 3.6 Fix：最小代码改动

修复阶段遵循：

1. 只改与根因直接相关的生产代码、测试和文档。
2. 同一个提交中包含 bug-locking 测试或验收脚本。
3. 不把无关格式化、重命名、搬目录混入 fix。
4. 不修改 guard 阈值来让任务通过。
5. 不直接编辑生成物，除非确认生成器不可用并在报告中说明。
6. 发现更大的架构问题时，先修当前 bug 的最小闭环，再开 follow-up。
7. 写红测和修 bug 必须分清状态：新增红测通过编译且按预期失败，只能说明“bug 已锁定”；只有生产修复后同一红测转绿，才能说明“bug fixed”。
8. 多个红测可以放在同一个文件，但每个红测必须有独立名字、独立期望和独立 GREEN 证据。
9. fix 轮被中断、只完成定位或只形成方案时，最终状态必须是 `partial`、`blocked` 或 `interrupted`，不得把上一轮红测报告复用成修复报告。

### 3.7 GREEN：验证修复

验证必须覆盖三层：

| 层级 | 要求 |
|:---|:---|
| 定点测试 | 新增或修改的回归测试必须通过 |
| 受影响包 | Go 改动运行 `./scripts/test_with_guard.sh <affected packages> -count=1`；前端、SQL、codemap 按仓库命令策略运行 |
| 原始复现 | 重新执行 repro 文档里的最小复现，确认实际结果等于预期结果 |

如果跳过某项验证，必须写明原因，例如 docs-only、平台不可用、依赖服务不可达、外部二进制缺失。

### 3.8 Residual Retest：残留问题复测

fix 完成前必须做一次残留复测：

| 复测项 | 要求 |
|:---|:---|
| 原 bug | 不再复现 |
| 邻近场景 | 同类输入、边界值、截断、错误分类、空结果等不退化 |
| 旧兼容 | 如果保留兼容字段或旧入口，必须证明行为受控 |
| 新残留 | 发现新问题时建 follow-up，并标明是否阻塞当前合入 |

残留复测不是“顺手测一下”。它是判断能否声明 fixed 的必要证据。

### 3.9 Report：修复报告

最终报告必须包含：

| 字段 | 要求 |
|:---|:---|
| 结论 | fixed、partially fixed、blocked 或 not reproducible |
| 改动文件 | 生产代码、测试、文档分别列出 |
| RED 证据 | 命令和失败摘要 |
| GREEN 证据 | 命令和通过摘要 |
| 原始复现复测 | 原 repro 是否通过 |
| guard/baseline | guard 是否通过；如 `internal/archtest/baseline.json` 有 diff，必须解释 |
| 未覆盖项 | 未跑的命令、未覆盖平台、外部依赖限制 |
| Follow-up | 残留问题、owner、优先级、是否阻塞 |

### 3.10 Multi-Agent Review：多轮 agent/reviewer 评估

当 fix 目标包含工具可用性、Agent 体验、输出契约、提示词、多语言支持、UI 体验、打包发布、性能稳定性或安全边界时，允许使用多轮 agent/reviewer 评估。评估只能作为辅助证据，不能替代 RED/GREEN 测试。

评估必须满足：

| 项 | 要求 |
|:---|:---|
| 样本矩阵 | 固定仓库、文件、输入、坐标、环境变量、数据规模、用户路径或外部依赖版本 |
| 能力矩阵 | 明确覆盖哪些 API/tool/action/UI flow/CLI command/package/script/release artifact |
| 评分维度 | 至少包含认知负荷、功能完整度、可用性；按场景可加安全性、性能、可观测性、发布可靠性 |
| 证据绑定 | 每个扣分点必须绑定真实调用、输出字段、状态码、错误码、日志、截图、trace 或测试输出，不接受“感觉不统一” |
| 口径修正 | 如果用户或 reviewer 指出扣分口径错误，必须记录修正前后分数和原因 |
| 轮次收敛 | 至少区分 baseline 评分、fix 后评分、口径校准后评分；最终分必须说明采用哪一轮口径 |
| 残留映射 | 每个低分项必须映射到 P0/P1/P2/P3 残留问题或明确标记为 accepted semantics |

如需要量化，建议使用 10 分制：

| 分数 | 含义 |
|:---|:---|
| 9.5-10 | 主流程稳定，契约清晰，残留多为环境/平台天然差异或低频边界 |
| 9.0-9.4 | 可作为日常主力路径/工具使用，仍有可解释的边界问题或轻微认知成本 |
| 8.0-8.9 | 核心可用，但存在明显噪声、错误分类、边界提示、环境覆盖或能力缺口 |
| 7.0-7.9 | 可辅助使用，但 agent 需要频繁绕路、复查或降级到其他工具 |
| < 7.0 | 不适合作为默认工具，必须先修稳定性或契约问题 |

禁止事项：

1. 不得用一个 agent 的主观最终分替代复现文档和测试证据。
2. 不得把领域原生语义 payload 强行压成通用字段，例如 LSP hover、前端组件状态、DB JSON envelope、CLI exit diagnostics。
3. 不得把样本工程缺 classpath、缺 Cargo workspace、缺数据库、依赖未安装、外部服务不可达等环境限制直接算成实现 bug；必须先分类为 environment / unsupported / implementation defect。
4. 不得通过超宽输入、超大数据、异常并发制造崩溃后把结果混入常规评分；压力测试应单独记录。
5. 不得留下临时探针文件；如确需创建，报告必须说明已删除并用 `git status --short` 证明。

### 3.11 Contract Semantics：边界契约判定

判断“契约不统一”前必须先区分两层：

| 层级 | 规则 |
|:---|:---|
| 通用控制 envelope | 成功/失败、错误码、错误分类、hint、分页/截断、重试属性、trace id、状态迁移等控制字段应跨同类场景保持一致 |
| 领域语义 payload | 业务 DTO、UI view-model、LSP hover/signature、DB jsonb payload、provider 原生事件、CLI 原始诊断等可以保留领域原生结构 |

只有通用控制 envelope 不一致、错误分类误导、hint 缺失、统计字段失真、状态迁移非法、已删除参数仍被文档引导、或跨层 contract 与实现不一致时，才按契约 bug 处理。领域语义 payload 的差异如果能降低认知负荷、保留原生信息，并且调用方能直接消费，应记录为 accepted semantics，不作为 fix 目标。

### 3.12 External Boundary E2E：外部边界验收

当任务要求真实调用二进制、CLI、MCP sidecar、HTTP server、DB、打包产物、前端浏览器或外部 provider 时，e2e 测试必须遵守：

| 项 | 要求 |
|:---|:---|
| 被测对象来源 | 明确使用仓库内指定路径、`/tmp` 指定路径、release 构建产物、本地 dev server、测试 DB 或指定 provider stub |
| 缺失行为 | 被测对象不存在或依赖未就绪时 fail-fast，错误信息提示先运行哪条构建/启动/迁移命令 |
| 禁止替代 | 不得在测试中静默构建、启动或替换一个不同对象来冒充指定产物 |
| 调用方式 | 通过真实进程、stdio/JSON-RPC、HTTP、浏览器、DB 连接或等价外部边界调用，不 mock 被测 handler |
| 清理 | 临时 workspace、探针文件、进程、端口、DB、日志和测试产物必须清理；保留的日志路径要写进报告 |

外部边界 e2e 分两类，报告中必须写清楚：

| 类型 | 定义 | 合规用法 |
|:---|:---|:---|
| 源码级 harness | 测试从当前源码临时构建/启动被测对象，再通过真实外部边界调用 | 适合锁定源码行为回归；不得声称覆盖 release/指定产物 |
| 产物级 e2e | 测试只调用已经存在的指定产物、服务或环境 | 适合验证打包产物、手动编译产物、部署形态或用户明确要求的“调用指定产物” |

如果用户明确说“调用指定二进制/产物/服务”“二进制目录在仓库内”“不要自己构建替代物”“验证 packaged app”，默认按产物级 e2e 执行。产物缺失时应 fail-fast，并在错误中给出构建命令；不得自动改成源码级 harness。

### 3.13 Interrupted Fix Handoff：中断与续跑交接

fix 过程被打断或重启会话时，下一轮必须先恢复状态，而不是直接继续写代码。

交接最小清单：

| 项 | 要求 |
|:---|:---|
| 工作树 | 先跑 `git status --short`，标出 owned/unrelated/untracked 文件 |
| 阶段 | 明确当前处于 repro、RED、fixing、GREEN、residual retest 还是 report |
| 红测状态 | 列出每条红测当前是 pass、expected fail、unexpected fail 还是未运行 |
| 生产改动 | 列出已改生产文件；如果没有生产改动，不能说 bug 已修 |
| 临时资源 | 确认临时文件、stash、后台进程、测试二进制是否遗留 |
| 下一步 | 明确只做一件事：继续修红测、补 RED 证据、跑 GREEN，或整理报告 |

中断后的 final/report 必须使用准确状态：

| 状态 | 使用条件 |
|:---|:---|
| `red_locked` | 已有红测，按预期失败，尚未修生产代码 |
| `fixing` | 已定位根因，生产修复进行中，但未完成 GREEN |
| `partial` | 部分红测转绿，仍有阻塞红测 |
| `blocked` | 缺二进制、缺环境、缺权限或依赖外部状态，无法继续 |
| `fixed` | 所有本轮 blocking 红测和原始复现均已转绿 |

### 3.14 Surface Matrix：按改动面选择验证

全仓 fix 必须按实际改动面选择验证命令。窄修只跑受影响面；跨层契约、共享包、打包或发布路径必须扩大验证。

| 改动面 | 常见 owner | 最低验证 | 需要额外说明 |
|:---|:---|:---|:---|
| Go 业务/平台代码 | `internal/app`、`internal/module`、`internal/platform`、`internal/provider`、`cmd/mcp-*`、`pkg` | `./scripts/test_with_guard.sh <affected packages> -count=1` | 如果改公共 contract、provider bridge、runtime lifecycle，说明为何不跑 `make test`/`make build-plain` |
| 架构守卫/基线 | `internal/archtest`、guard 脚本、`baseline.json` | `./scripts/test_with_guard.sh ./internal/archtest -count=1` 或 `make guard` | baseline diff 必须逐项解释；不得用 freeze 绕过 |
| SQL/store | `internal/store`、`sql/queries`、migrations、sqlc 生成代码 | `make sqlc-verify` 加受影响 Go 包测试 | 说明 schema/migration 兼容性和是否需要 fixture/golden |
| Frontend | `cmd/agent-terminal/frontend` | `node scripts/size-guard.cjs`、`npx vitest run`、`npm run build` | UI/交互改动需浏览器或截图验证；说明未跑浏览器的原因 |
| Wails/desktop/打包 | `cmd/agent-terminal`、`scripts/package_*`、embedded assets、release docs | 相关脚本/guard 测试，加 `make build-plain` 或目标平台 smoke | 平台不可用时列出未覆盖平台和手动验证计划 |
| MCP sidecar/tool contract | `cmd/mcp-lsp`、`cmd/mcp-orch`、`cmd/mcp-ida`、`internal/mcpserver` | sidecar 包测试，加真实 MCP/binary e2e 或 contract test | 明确是源码级 harness 还是产物级 e2e |
| Provider/runtime | `internal/provider`、`internal/platform/rpc`、thread/turn/prompt/memory 串联 | 受影响 provider/module 测试，加原始 turn/session 复现 | 外部 CLI/API 不可用时使用明确 stub，并说明未覆盖真 provider |
| 文档/契约 | `docs/契约`、`docs/decisions`、`docs/adr`、`docs/internal-notes` | `git diff --check` | docs-only 可跳过 Go 测试，但 final 必须说明 |
| codemap | `docs/doc/codemap`、生成脚本 | `make codemap-check` | 如只读使用 codemap 不需跑；编辑 codemap 必须验证 |
| skills/provider mirrors | `.agent/skills`、skill module/provider mirror 相关代码 | 相关 skill/module/provider mirror tests | provider mirrors 是生成物时不要直接编辑，先确认 canonical source |

## 4. 文档落点

| 文档类型 | 推荐路径 | 用途 |
|:---|:---|:---|
| 复现文档 | `docs/li/YYYY-MM-DD-<area>-<bug>-repro.md` | 记录最小复现、实际/预期、验收 |
| 实施计划 | `docs/plans/YYYY-MM-DD-<area>-<fix>.md` | 拆任务、文件范围、测试计划 |
| 评审/返修报告 | `docs/reviews/<topic>-fix-report-YYYY-MM-DD.md` | 汇总 RED/GREEN、guard、未覆盖项 |
| 长期契约 | `docs/契约/*.md` 或 `docs/decisions/ADR-*.md` | 固化跨团队规则和架构决策 |

复现文档和修复报告可以合并，但必须保留本规范要求的字段。

## 5. 模板

### 5.1 Repro 模板

````markdown
# <Area> <Bug> 复现文档

| 项目 | 内容 |
|:---|:---|
| Bug ID | YYYY-MM-DD-area-short-name |
| 日期 | YYYY-MM-DD |
| 仓库/分支 | <path> / <branch or commit> |
| 优先级 | P0/P1/P2/P3 |
| 当前结论 | 可稳定复现 / 间歇复现 / 不可复现 |

## 1. 问题摘要

<一句话说明当前行为为什么错，以及影响什么工作流。>

## 2. 最小复现

```bash
<command or tool call>
```

## 3. 实际结果

```text
<output summary>
```

## 4. 预期结果

<可断言字段、状态、错误分类或文件内容。>

## 5. 对照组

<缩小范围、邻近工具、旧版本或同类成功案例。>

## 6. 验收标准

| 验收项 | 标准 |
|:---|:---|
| <item> | <assertion> |
````

### 5.2 Fix 报告模板

````markdown
# <Topic> Fix 验证报告

日期：YYYY-MM-DD
工作区：`<path>`
Bug ID：`<id>`

## 结论

fixed / partially fixed / blocked / not reproducible

## 改动文件

- `<file>`：<改动说明>

## RED 证据

```bash
<command>
```

```text
<failure summary>
```

## 红测状态矩阵

| Test/Repro | Bug | RED 状态 | GREEN 状态 | 是否阻塞 |
|:---|:---|:---|:---|:---|
| <test name> | <bug id> | expected fail / not run | pass / not fixed | yes/no |

## GREEN 证据

```bash
<command>
```

```text
<success summary>
```

## 多轮评估

| 轮次 | 样本/范围 | 认知负荷 | 功能 | 可用性 | 场景特有维度 | 综合 | 口径说明 |
|:---|:---|---:|---:|---:|---:|---:|:---|
| baseline | <capabilities/surfaces> |  |  |  |  |  | <known issues> |
| fix 后 | <capabilities/surfaces> |  |  |  |  |  | <changed evidence> |
| 口径校准后 | <capabilities/surfaces> |  |  |  |  |  | <accepted semantics / real residuals> |

## 原始复现复测

<repro command result>

## Guard / Baseline

<guard result and baseline diff status>

## 未覆盖项

- <skipped verification and reason>

## Follow-up

- <residual issue, priority, owner, blocker or non-blocker>
````

## 6. 完成清单

提交或声明完成前逐项确认：

- [ ] `git status --short` 已检查，未混入无关改动。
- [ ] bug 有明确 ID、优先级、影响面和合入门槛。
- [ ] 最小复现已记录，并可由他人执行。
- [ ] 根因定位到具体契约和 owner 文件。
- [ ] 同提交包含回归测试、fixture、golden、snapshot 或可执行验收脚本。
- [ ] RED 证据已记录；若使用 mutation RED，mutation 已恢复。
- [ ] 如果本轮只是新增红测，状态明确写为 `red_locked`，不写 fixed。
- [ ] 修复没有引入隐式默认值、静默降级或吞错继续。
- [ ] 定点测试、受影响包验证和原始复现复测已通过。
- [ ] guard 按改动面运行；如 baseline 变化已说明。
- [ ] 残留问题已复测，并标明是否阻塞当前合入。
- [ ] 如使用多轮 agent/reviewer 评估，评估有固定样本矩阵、扣分证据和口径修正记录。
- [ ] 如涉及边界契约出参、事件或状态，已区分通用控制 envelope 与语义 payload，accepted semantics 不被误报为 bug。
- [ ] 如涉及外部边界 e2e，测试调用的是指定被测对象，缺失时 fail-fast，不在测试内静默自建替代物。
- [ ] 如过程被中断或续跑，报告包含工作树、阶段、红测状态、生产改动和下一步。
- [ ] final/report 明确列出未覆盖项，不把未验证内容说成已验证。

## 7. Review 拒绝条件

出现以下任一情况，reviewer 应要求返工：

1. 只有代码改动，没有 bug-locking 测试或等价验收证据。
2. 复现步骤不可执行，或实际/预期结果不可判断。
3. 通过默认值、空结果、吞错日志、兼容旧字段等方式掩盖异常。
4. P0/P1 问题没有原始复现复测。
5. 修改 guard 阈值、baseline 或生成物来绕过失败，且没有明确授权。
6. 报告声称 fixed，但验证命令未跑或失败。
7. 把无关重构、格式化、迁移历史清理混入 fix，导致 review 无法判断行为变化。
8. 多轮评估只给最终分/结论，没有样本矩阵、调用证据、扣分点和口径修正记录。
9. 把领域原生语义结构误判成输出契约 bug，并推动无收益的统一改造。
10. e2e 声称调用真实产物/服务，实际在测试中临时构建或替换了被测对象。
11. 把“红测已建立”写成“bug 已修复”，或者 fix 轮中断后没有给出准确状态。
12. 多个红测混在一个报告里，但没有逐条说明哪条已转绿、哪条仍阻塞。
