# Manus-like Agent 功能实现提示词文档

> 适用场景：将现有项目改造成具备“自主任务规划、工具调用、异步执行、过程可视化、结果验证、产物生成”的 AI Agent 系统。
> 目标使用对象：Claude Code。
> 生成日期：2026-06-11。

---

## 1. 文档用途

本文档用于帮助你向 Claude Code 清晰表达项目意图：

- 不是复制 Manus 的品牌、界面或专有实现。
- 目标是实现类似 Manus 体验的“自主 AI Agent”能力。
- 用户输入复杂目标后，系统能够：
  - 理解目标
  - 拆解任务
  - 生成计划
  - 调用工具执行
  - 展示实时进度
  - 保存日志和中间产物
  - 验证执行结果
  - 在高风险操作前请求用户确认
  - 输出最终结果或可下载产物

---

## 2. 一句话版需求

适合放在 Claude Code 会话开头。

```text
我要在当前项目中实现一个对标 Manus 的自主 AI Agent 功能：用户输入目标后，系统能自动规划、调用工具、执行多步骤任务、展示实时进度、生成最终产物，并具备异步执行、权限控制、任务日志、人工确认和结果验证能力。请先阅读代码库，再给出架构方案和分阶段实现计划，确认后逐步编码。
```

---

## 3. 完整规划版 Claude Code 提示词

适合首次让 Claude Code 理解项目目标、阅读代码库并输出完整实现方案。

```text
你现在是一名资深 AI Agent 架构师和全栈工程师。请在当前项目中实现一个“对标 Manus 体验”的自主任务执行 Agent 系统。

<目标>
我不是要复制 Manus 的品牌、UI 或专有实现，而是要在我的项目中实现类似的核心能力：
用户输入一个复杂目标后，系统能够理解目标、拆解任务、生成计划、调用工具执行、持续反馈进度、产出最终结果，并在高风险操作前请求用户确认。
</目标>

<请先做的事>
1. 先完整阅读当前代码库，识别技术栈、目录结构、前端框架、后端框架、数据库、鉴权方式、任务队列、已有 AI/LLM 调用逻辑。
2. 不要立刻写代码。先输出一份实现方案，说明：
   - 当前项目已有能力
   - 缺失能力
   - 推荐架构
   - 需要新增或修改的模块
   - 数据库表设计
   - API 设计
   - 前端页面/组件设计
   - 后台任务执行机制
   - 安全边界
   - 测试方案
3. 方案确认后，再按模块逐步实现。
</请先做的事>

<核心功能范围>
请实现一个 Agent Mode，至少包含以下能力：

1. 任务入口
   - 用户可以输入一个自然语言目标，例如：
     “帮我调研某个行业并生成报告”
     “根据上传文件生成一个网站”
     “分析 CSV 数据并生成图表”
     “帮我规划并执行一个多步骤开发任务”
   - 系统创建一个 Task，并进入 Agent 执行流程。

2. 任务规划 Planner
   - 将用户目标拆解成结构化步骤。
   - 每一步包含：
     - step_id
     - title
     - description
     - tool_needed
     - expected_output
     - status: pending/running/success/failed/skipped
   - 计划必须可展示给用户。
   - 用户可以确认、编辑、重新生成计划。

3. 工具调用 Tool Registry
   请设计一个可扩展的工具注册系统，后续可以方便新增工具。
   MVP 阶段至少支持：
   - file_read：读取项目文件或用户上传文件
   - file_write：生成报告、JSON、Markdown、HTML 等文件
   - code_execution：在安全沙箱中执行 Python/Node 脚本
   - web_search：联网搜索，要求带来源记录
   - browser_action：使用 Playwright 或类似方案执行网页访问、截图、信息提取
   - data_analysis：处理 CSV/Excel/JSON 数据并生成统计摘要
   - artifact_generation：生成 Markdown、HTML、PPT 草稿、图表、报告等产物

4. 执行器 Executor
   - 根据 Planner 生成的步骤逐项执行。
   - 每一步执行前检查工具权限。
   - 每一步执行后保存：
     - 输入
     - 工具调用参数
     - 工具返回结果
     - 日志
     - 中间产物
     - 错误信息
   - 失败时支持重试、跳过、重新规划。

5. 观察与验证 Verifier
   - 每个步骤执行完成后，检查结果是否满足 expected_output。
   - 如果不满足，自动生成修复建议或重新执行。
   - 最终结果生成前，做一次整体自检：
     - 是否完成用户目标
     - 是否有遗漏步骤
     - 是否有明显错误
     - 是否需要用户进一步确认

6. 异步任务系统
   - Agent 任务可能运行较久，不能阻塞普通 HTTP 请求。
   - 如果当前项目已有队列系统，请复用。
   - 如果没有，请设计轻量方案，例如：
     - BullMQ / Redis
     - Celery
     - Background Worker
     - cron/polling fallback
   - 前端需要能查看任务状态：
     - queued
     - planning
     - waiting_user_approval
     - running
     - verifying
     - completed
     - failed
     - cancelled

7. 前端交互
   请实现一个类似“Agent 工作台”的页面或组件：
   - 输入任务目标
   - 展示 Agent 生成的计划
   - 展示每一步执行状态
   - 展示实时日志或流式进度
   - 展示中间产物
   - 支持暂停、继续、取消
   - 高风险操作前展示确认弹窗
   - 最终展示结果和可下载产物

8. 记忆与上下文
   - 设计 Memory 层，但 MVP 不要过度复杂。
   - 至少支持：
     - task memory：当前任务上下文
     - user preference：用户偏好，例如输出语言、报告格式、代码风格
     - project memory：当前项目约定，例如技术栈、目录规范、API 风格
   - 记忆需要可查看、可删除。

9. 权限与安全
   必须实现安全边界，不能让 Agent 无限制执行危险操作。
   至少包含：
   - 工具权限白名单
   - 文件系统访问范围限制
   - shell/code execution 沙箱限制
   - 禁止读取 .env、密钥、token，除非用户显式授权
   - 对外部网站提交表单、付款、发送邮件、删除数据、修改生产环境等操作必须人工确认
   - 工具调用参数和结果要审计记录
   - 对 prompt injection 做基础防护：网页内容、文件内容、用户内容都不能自动覆盖系统安全规则

10. 可观测性
   - 每个任务都要有 task_id。
   - 每个步骤都要有 step_id。
   - 每次工具调用都要有 tool_call_id。
   - 日志要能追踪完整执行链路。
   - 错误要能定位到具体步骤和工具。

</核心功能范围>

<推荐架构>
请优先使用以下分层架构，除非当前项目技术栈明显不适合：

- AgentController：接收用户目标，创建任务
- PlannerService：生成任务计划
- ExecutorService：执行计划步骤
- ToolRegistry：注册和调用工具
- ToolAdapters：具体工具实现，如浏览器、文件、代码执行、搜索、数据分析
- VerifierService：验证每一步和最终结果
- MemoryService：保存任务上下文和用户偏好
- ArtifactService：管理生成文件和下载链接
- PermissionService：处理工具权限和人工确认
- TaskWorker：后台异步执行任务
- TaskStore/Database：持久化任务、步骤、日志、工具调用、产物

请根据当前项目语言和框架落地，不要强行引入不必要的新技术。
</推荐架构>

<数据库设计>
请根据项目现有数据库方案设计表结构。至少需要支持以下实体：

1. agent_tasks
   - id
   - user_id
   - title
   - original_prompt
   - status
   - created_at
   - updated_at
   - completed_at
   - error_message

2. agent_steps
   - id
   - task_id
   - order_index
   - title
   - description
   - tool_needed
   - expected_output
   - status
   - started_at
   - completed_at
   - error_message

3. agent_tool_calls
   - id
   - task_id
   - step_id
   - tool_name
   - input_json
   - output_json
   - status
   - started_at
   - completed_at
   - error_message

4. agent_artifacts
   - id
   - task_id
   - step_id
   - artifact_type
   - name
   - path_or_url
   - metadata_json
   - created_at

5. agent_memories
   - id
   - user_id
   - scope
   - key
   - value
   - created_at
   - updated_at

6. agent_approvals
   - id
   - task_id
   - step_id
   - risk_level
   - action_summary
   - status
   - approved_by
   - created_at
   - resolved_at

如果当前项目使用 Prisma/Drizzle/TypeORM/SQLAlchemy/Django ORM，请用项目现有 ORM 实现。
</数据库设计>

<API 设计>
请实现或设计以下 API，命名可按项目风格调整：

- POST /api/agent/tasks
  创建 Agent 任务

- GET /api/agent/tasks/:id
  获取任务详情、步骤、状态、产物

- POST /api/agent/tasks/:id/plan
  生成或重新生成计划

- POST /api/agent/tasks/:id/run
  开始执行任务

- POST /api/agent/tasks/:id/pause
  暂停任务

- POST /api/agent/tasks/:id/resume
  继续任务

- POST /api/agent/tasks/:id/cancel
  取消任务

- POST /api/agent/approvals/:id/approve
  批准高风险操作

- POST /api/agent/approvals/:id/reject
  拒绝高风险操作

- GET /api/agent/tasks/:id/events
  获取实时事件，优先 SSE；如果项目不适合 SSE，可用 WebSocket 或轮询

</API 设计>

<LLM 调用要求>
请抽象 LLM Provider，不要把模型供应商写死。
至少设计：
- generatePlan()
- executeReasoning()
- summarizeToolResult()
- verifyStep()
- generateFinalAnswer()

要求：
- prompt 模板集中管理
- 支持替换 OpenAI/Anthropic/本地模型
- LLM 输出必须尽量使用 JSON schema 校验
- JSON 解析失败时要自动修复或重试
- 所有 LLM 调用要记录 request metadata 和 response metadata，但不要记录敏感密钥

</LLM 调用要求>

<实现策略>
请按以下阶段推进：

Phase 1：代码库调研与设计文档
- 阅读项目结构
- 输出 docs/agent-manus-like-spec.md
- 输出 docs/agent-implementation-plan.md
- 不写业务代码，除非需要创建文档

Phase 2：数据模型与基础服务
- 新增数据库模型/迁移
- 实现 TaskStore
- 实现 AgentController 基础 API
- 添加基础测试

Phase 3：Planner
- 实现 PlannerService
- 使用 JSON schema 生成结构化计划
- 前端展示计划
- 支持用户确认计划

Phase 4：Tool Registry
- 实现 ToolRegistry
- 实现 file_read/file_write/code_execution/data_analysis 的最小可用版本
- 给每个工具添加权限声明和参数校验

Phase 5：Executor + Worker
- 实现异步执行
- 支持步骤状态更新
- 支持日志记录
- 支持失败重试

Phase 6：Verifier + Artifacts
- 实现步骤验证
- 实现最终结果汇总
- 实现产物管理

Phase 7：前端 Agent 工作台
- 任务输入
- 计划展示
- 步骤进度
- 实时日志
- 产物下载
- 暂停/继续/取消/批准

Phase 8：安全加固与测试
- 权限系统
- 高风险操作确认
- prompt injection 基础防护
- 单元测试、集成测试、端到端测试
- 文档更新

</实现策略>

<代码质量要求>
1. 遵循当前项目已有代码风格。
2. 不要大规模重写无关代码。
3. 不要引入重复框架。
4. 新增模块要有清晰边界。
5. 所有新增 API 要有错误处理。
6. 所有工具调用要有输入校验。
7. 所有任务状态变化要可追踪。
8. 关键逻辑要写测试。
9. 实现后运行项目现有测试、lint、typecheck、build。
10. 如果测试失败，先定位根因，不要通过删除测试或绕过校验来掩盖问题。

</代码质量要求>

<验收标准>
完成后，至少要能演示以下任务：

Demo 1：研究报告任务
用户输入：“调研 AI Agent 产品的发展趋势，生成一份 Markdown 报告。”
系统应：
- 创建任务
- 生成计划
- 执行搜索/整理
- 生成报告 artifact
- 展示步骤日志和最终报告

Demo 2：数据分析任务
用户上传 CSV 或使用示例 CSV，输入：“分析这个数据并生成摘要和图表。”
系统应：
- 读取数据
- 生成统计摘要
- 生成图表或 HTML/Markdown 结果
- 保存 artifact

Demo 3：代码辅助任务
用户输入：“检查当前项目中某个模块的问题并提出修复方案。”
系统应：
- 读取相关代码
- 生成分析计划
- 输出修复建议或 PR 风格改动
- 运行测试或 typecheck 验证

</验收标准>

<输出要求>
请先输出：
1. 你对当前项目的理解
2. 你建议的 Agent 架构
3. 文件改动清单
4. 数据库设计
5. API 设计
6. 分阶段实现计划
7. 风险点和需要我确认的问题

在我确认前，不要开始大规模编码。
</输出要求>
```

---

## 4. 强硬执行版 Claude Code 提示词

当你已经确定要 Claude Code 直接开始实现 MVP 时，使用此版本。

```text
请直接在当前项目中实现一个“Manus-like Agent MVP”。

要求：
1. 不复制 Manus 品牌，只实现类似的自主任务执行能力。
2. 先快速扫描代码库，识别技术栈和现有架构。
3. 然后直接实现 MVP，不要停留在纯文档阶段。
4. MVP 必须包含：
   - Agent 任务创建
   - Planner 生成结构化步骤
   - Task/Step/ToolCall/Artifact 持久化
   - ToolRegistry
   - 至少 file_read、file_write、code_execution、data_analysis 四个工具
   - Executor 顺序执行步骤
   - Verifier 基础验证
   - 前端 Agent 工作台
   - 任务状态展示
   - 产物保存和查看
   - 基础权限控制
5. 使用当前项目已有技术栈和代码规范。
6. 不要重写无关模块。
7. 每完成一个阶段就运行测试、typecheck、lint 或 build。
8. 如果当前项目没有测试，请为 Agent 核心服务补充最小测试。
9. 所有 LLM 输出必须使用 JSON schema 或等价机制校验。
10. 实现完成后输出：
   - 改动文件列表
   - 如何启动
   - 如何测试
   - Demo 用例
   - 已实现能力
   - 未实现能力
   - 下一步建议

请现在开始。
```

---

## 5. 建议放入项目根目录的 `CLAUDE.md`

建议在项目根目录创建 `CLAUDE.md`，让 Claude Code 在后续开发过程中持续理解项目目标和约束。

```md
# CLAUDE.md

## Project Goal

This project is building a Manus-like autonomous AI Agent system.

The goal is not to clone Manus branding or proprietary implementation. The goal is to implement a general-purpose task execution agent that can:

- understand a user's high-level goal
- break it into steps
- execute steps using tools
- show progress
- verify results
- generate artifacts
- request user approval before risky actions

## Agent Architecture

Use this conceptual architecture unless the existing project strongly suggests another approach:

- AgentController
- PlannerService
- ExecutorService
- ToolRegistry
- ToolAdapters
- VerifierService
- MemoryService
- ArtifactService
- PermissionService
- TaskWorker
- TaskStore

## Engineering Rules

- Explore the codebase before implementation.
- Prefer existing project patterns.
- Do not rewrite unrelated code.
- Add tests for core Agent logic.
- Run lint, typecheck, tests, and build after implementation.
- Do not access secrets such as .env files unless explicitly required and approved.
- All external side effects must go through PermissionService.
- All tool calls must be logged.
- LLM outputs should be schema-validated where possible.

## Security Rules

The Agent must not perform high-risk actions without user approval, including:

- sending emails
- submitting forms
- making purchases
- deleting files or database records
- changing production configuration
- accessing private credentials
- calling external APIs that mutate state

## MVP Acceptance Criteria

The MVP is acceptable when the user can:

1. Create an Agent task from a natural language goal.
2. See a generated plan.
3. Run the plan.
4. Watch step progress.
5. See logs and intermediate outputs.
6. Receive a final artifact.
7. Cancel or pause the task.
8. Approve or reject risky actions.
```

---

## 6. 推荐使用顺序

建议按以下顺序使用本文档内容：

1. 在项目根目录添加 `CLAUDE.md`。
2. 首次使用 Claude Code 时，发送“完整规划版 Claude Code 提示词”。
3. 等 Claude Code 输出设计方案后，检查以下内容：
   - 是否正确识别你的技术栈
   - 是否复用了项目现有架构
   - 是否没有引入过重依赖
   - 是否包含安全边界
   - 是否包含测试与验证方案
4. 确认方案后，让 Claude Code 按 Phase 2、Phase 3、Phase 4 逐步实现。
5. 每个阶段完成后，要求 Claude Code 运行：
   - lint
   - typecheck
   - test
   - build
6. 如果你想快速推进 MVP，可以改用“强硬执行版 Claude Code 提示词”。

---

## 7. 实施阶段总览

| 阶段 | 目标 | 主要产出 |
|---|---|---|
| Phase 1 | 代码库调研与设计 | `docs/agent-manus-like-spec.md`、`docs/agent-implementation-plan.md` |
| Phase 2 | 数据模型与基础服务 | 数据库迁移、TaskStore、Agent 基础 API |
| Phase 3 | Planner | 结构化任务计划、JSON schema 校验、计划确认 |
| Phase 4 | Tool Registry | 工具注册系统、基础工具适配器 |
| Phase 5 | Executor + Worker | 异步执行、步骤状态更新、日志记录 |
| Phase 6 | Verifier + Artifacts | 步骤验证、最终结果汇总、产物管理 |
| Phase 7 | 前端 Agent 工作台 | 输入、计划、进度、日志、产物下载、人工确认 |
| Phase 8 | 安全加固与测试 | 权限控制、prompt injection 防护、单元/集成/E2E 测试 |

---

## 8. MVP 必须具备的核心能力清单

| 模块 | 必须能力 |
|---|---|
| Task | 创建任务、查询状态、取消任务 |
| Planner | 将自然语言目标拆解成结构化步骤 |
| Executor | 按步骤执行、失败处理、状态流转 |
| ToolRegistry | 注册工具、校验参数、记录调用 |
| Tools | 文件读写、代码执行、数据分析、产物生成 |
| Verifier | 验证每一步是否满足预期输出 |
| Artifact | 保存报告、图表、HTML、Markdown 等结果 |
| Permission | 高风险动作前请求用户确认 |
| Memory | 保存任务上下文、用户偏好、项目约定 |
| Observability | task_id、step_id、tool_call_id、日志链路 |
| Frontend | Agent 工作台、计划展示、进度展示、日志展示、产物下载 |

---

## 9. 安全边界清单

Agent 系统必须默认限制以下行为：

- 无授权读取 `.env`、密钥、token、私有证书。
- 无限制访问整个文件系统。
- 无沙箱执行任意 shell 命令。
- 无确认向外部网站提交表单。
- 无确认发送邮件、短信、Webhook。
- 无确认进行购买、付款、下单。
- 无确认删除数据库记录或生产文件。
- 无确认修改生产环境配置。
- 允许网页、文件、用户输入中的内容覆盖系统安全规则。
- 未记录工具调用输入输出。

---

## 10. 参考资料

- Manus 官网：https://manus.im/
- Manus Google Play 页面：https://play.google.com/store/apps/details?hl=ja&id=tech.butterfly.app
- Claude Code Best Practices：https://code.claude.com/docs/en/best-practices
- Claude Prompt Engineering Best Practices：https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/claude-prompting-best-practices
