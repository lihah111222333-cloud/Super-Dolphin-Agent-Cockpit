# Super-Dolphin Manus-like Agent 系统设计规格

> 生成日期：2026-06-11
> 依据：`docs/ai01-docs/manus_like_agent_claude_code_prompt.md` Phase 1 要求
> 状态：待确认（本文档为设计方案，未开始编码）

---

## 1. 当前项目理解

### 1.1 技术栈与架构

| 层 | 现状 |
|---|---|
| 后端 | Go 1.25.7，fx 装配 + rungroup + sqlc + jrpc2 + stateless 状态机，Fail-Fast 纪律 |
| 进程拓扑 | `cmd/agent-terminal`（Wails 桌面主进程 + HTTP/WS server）+ `cmd/mcp-orch`（编排 peer）+ `cmd/mcp-lsp`（代码智能 peer）+ `cmd/mcp-ida`（空壳） |
| 前端 | `frontend-app/`（React 19 + Vite 8 + Zustand + React Query，自实现路由，8 页面） |
| 数据库 | Postgres（本地 54320），sqlc 生成 store 层 |
| LLM | Provider 三层抽象（Driver/Session/TurnHandle），Claude CLI 与 Codex 双实现，`DreamExecutor` 提供无 session 单 prompt 调用 |
| 通信 | Wails WebSocket 桥承载 jrpc2 RPC + push 事件（非 REST） |

### 1.2 已有能力盘点（对照提示词文档第 8 节 MVP 清单）

| MVP 模块 | 现状 | 完成度 | 关键位置 |
|---|---|---|---|
| **Task** | DAG 模板 + Run 执行实例双层模型，OCC 版本控制，terminate/delete/list/get 齐全 | ★★★★☆ | `cmd/mcp-orch/store/taskdag/contract.go:561-675` |
| **Planner** | 无独立服务；靠 seed 的 `dag_designer` prompt template 引导主 agent 用 `task_create_dag` 工具产出 DAG | ★★☆☆☆ | migrations 0084/0085/0108/0109 |
| **Executor** | WakeupDispatcher（10s tick + lease + claim）+ WakeupReclaimer + NodeExecutor 抽象（agent/automation），失败分类重试 + 级联失败 + 幂等 | ★★★★★ | `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`、`nodeexec/` |
| **ToolRegistry** | toolbridge 三路路由（host-direct / orch peer / lsp peer），schema 声明 + enum 校验 + CapabilityGate | ★★★★☆ | `internal/platform/toolbridge/handler.go:83-112`、`cmd/mcp-orch/tools/factory.go:68-135` |
| **Tools** | 文件读写（shared_file_*）、代码智能（lsp 7 工具）、agent 编排（launch/stop/message）、workspace 隔离、TTS/视频；**缺**：web_search、browser_action、data_analysis、code_execution 沙箱 | ★★★☆☆ | `cmd/mcp-orch/tools/` |
| **Verifier** | 节点状态机有 `awaiting_verify` 中间态，但无自动验证服务；turn 有 30min stall 检测 | ★★☆☆☆ | `sql/queries/task_dag_node_runtime.sql` |
| **Artifact** | shared_files（磁盘 source + DB 索引 + reports/ 前缀 + retention GC），**无独立 artifact 实体** | ★★★☆☆ | `internal/platform/sharedfilefs/disk.go`、`sharedfilecleanup/gc.go` |
| **Permission** | ApprovalManager（pending 去重 + UI 回调 + fail-closed + 断线重放）+ hooks 三阶段拦截 + `hook_pending_reviews` 持久化 + approval policy 六枚举 | ★★★★★ | `internal/platform/rpc/approval.go`、`internal/platform/hooks/` |
| **Memory** | 文件型记忆（user/feedback/project/reference）+ private/team 双 scope + 检索注入 + 相似整合 + auto-dream | ★★★★★ | `internal/module/memory/` |
| **Observability** | trace_id/span_id 全链路（JSONL sink + rotation + retention），RPC payload 自动注入 traceparent，前端 ingest | ★★★★★ | `internal/platform/observability/`、`frontend-app/src/shared/api/wailsBridge.js:695` |
| **Frontend 工作台** | Chat 三栏（含 RuntimePanel diff/token/活动）+ WorkflowPage（DAG 列表/节点编辑/运行历史/拓扑）；**缺**：统一任务视图、计划清单实时勾选、产物画廊 | ★★★☆☆ | `frontend-app/src/pages/chat/ChatPage.jsx:5963`、`workflows/WorkflowPage.jsx` |

**结论：项目已具备约 70% 的 Manus-like 基座。这不是"从零造 Agent 系统"，而是"补三个缺口 + 一次前端信息架构重组"。**

### 1.3 缺失能力（按重要性排序）

1. **PlannerService**：目前"目标 → 计划"靠 prompt template 引导主 agent 调工具，无结构化 schema 校验、无"计划展示 → 用户确认/编辑 → 再执行"的产品闭环。
2. **统一 Task 聚合视图**：用户视角的"任务"目前散落在 thread（对话）、DAG（工作流）、cron job（定时）三处，无统一状态机（`queued/planning/waiting_user_approval/running/verifying/completed/failed/cancelled`）和统一列表。
3. **VerifierService**：`awaiting_verify` 状态位已存在但无消费者；缺 expected_output 比对与最终自检。
4. **Artifact 实体**：shared_files 是文件通道不是产物模型，缺 artifact_type/metadata/与 step 关联/下载导出语义。
5. **暂停/恢复原语**：DAG 只有 terminate（取消），无 pause/resume。
6. **三个工具适配器**：web_search（带来源记录）、data_analysis（CSV/Excel 统计）、browser_action（Playwright）。code_execution 部分存在（provider CLI 自带 Bash，但宿主侧无独立沙箱执行器）。
7. **前端 Agent 工作台**：计划清单（步骤勾选）、任务状态列表、产物画廊三个组件。

---

## 2. 推荐架构

### 2.1 总原则

**复用优先**。提示词文档的概念架构与本项目实体映射如下，不引入任何新框架、新队列、新存储：

| 概念架构（提示词文档） | 本项目落地 | 新增/复用 |
|---|---|---|
| AgentController | `internal/module/agenttask`（新模块）：接收目标、创建 AgentTask、聚合状态 | **新增** |
| PlannerService | `internal/module/agenttask/planner`：用 `DreamExecutor`（现成的无 session 单 prompt LLM 原语）+ JSON schema 校验生成计划，产出物直接落为 DAG | **新增（薄层）** |
| ExecutorService | 复用 `cmd/mcp-orch` 的 WakeupDispatcher + NodeExecutor | **复用** |
| ToolRegistry | 复用 toolbridge + `cmd/mcp-orch/tools/factory.go` 注册机制 | **复用** |
| ToolAdapters | 新增 `web_search` / `data_analysis` / `browser_action` 三个 MCP 工具到 mcp-orch | **新增** |
| VerifierService | `internal/module/agenttask/verifier`：消费 `awaiting_verify` 状态，用 DreamExecutor 做 expected_output 比对 | **新增（薄层）** |
| MemoryService | 复用 `internal/module/memory`（已超出需求） | **复用** |
| ArtifactService | `internal/module/artifact`：在 shared_files 之上加 artifact 元数据表 | **新增（薄层）** |
| PermissionService | 复用 ApprovalManager + hooks 三阶段 | **复用** |
| TaskWorker | 复用 rungroup runner 模式（dispatcher 已是 runner） | **复用** |
| TaskStore | 复用 sqlc + 新增 3 张表（见 §3） | **复用+扩展** |

### 2.2 核心设计决策

**决策 1：AgentTask 是聚合根，不是新执行引擎。**
`agent_tasks` 表只是"用户视角的任务"聚合层，每个 AgentTask 关联一个 DAG（dag_key）+ 可选 thread_id。执行仍 100% 走现有 wakeup/dispatcher 链路。AgentTask 状态由 DAG run 状态 + 审批 pending 状态投影而来（监听现有 `task/node/statusChanged`、`approval/request` 总线事件折叠）。

**决策 2：Planner 产出物就是 DAG。**
不发明新的 plan 结构。Planner 用 DreamExecutor 单 prompt 调用产出 JSON（schema 校验 + 解析失败重试 ≤2 次，仍失败则 Fail-Fast 报错，不静默降级），转换为 `task_create_dag` 等价的节点集合写入。step 的 `expected_output` 存入 node `config.expected_output`，`tool_needed` 存入 `config.exec`。用户"确认/编辑/重新生成计划"复用现有 `task_dag_apply_ops`（OCC typed ops）。

**决策 3：状态机映射，不新造状态。**
AgentTask 八状态由现有状态投影：

```
queued                 ← agent_tasks 行已建，DAG 未建
planning               ← Planner DreamExecutor 进行中
waiting_user_approval  ← 计划已生成待确认 / ApprovalManager 有 pending / hook_pending_reviews 有待审
running                ← DAG run status=running
verifying              ← 任一节点 awaiting_verify / 最终自检中
completed              ← run status=succeeded 且终检通过
failed                 ← run status=failed / planning 失败
cancelled              ← run status=cancelled / 用户取消
```

**决策 4：暂停 = wakeup 闸门，不动节点状态机。**
新增 `agent_tasks.paused` 标志；WakeupDispatcher claim 时 join 检查所属 task 的 paused 位（一个 WHERE 条件），暂停期间 wakeup 留在 pending。resume 清标志即可。正在 running 的节点不打断（与 Manus 行为一致：暂停的是"后续调度"）。

**决策 5：Verifier 是 runner，不是同步钩子。**
新增 `verifierActor`（rungroup runner，10s tick）：扫 `awaiting_verify` 节点 → 取 node result + `config.expected_output` → DreamExecutor 判定 → 通过则 `CompleteTaskDagNode`，不通过则按 retry policy 走 `FailNodeAndCancelDownstream` 或生成修复建议写回 result。无 expected_output 的节点直接放行（保持现有 DAG 行为不变）。最终自检：run finalize 后对整体目标做一次 DreamExecutor 检查，结果写入 agent_tasks。

**决策 6：Artifact 表引用 shared_files，不另存内容。**
`agent_artifacts` 只存元数据（type/name/关联 task+step/metadata），`path` 指向 `.agnet/shared/` 下既有路径，复用现有磁盘 source、100KB 阈值、retention GC（artifact 引用路径加入 GC 的 pinned 保护）。

### 2.3 模块边界图

```
frontend-app（React）
  └─ jrpc2 RPC（wailsBridge）
       ├─ agent-task/* RPC ──→ internal/module/agenttask（新）
       │                          ├─ planner/   （DreamExecutor + schema 校验）
       │                          ├─ verifier/  （verifierActor runner）
       │                          └─ projector/ （bus 事件 → AgentTask 状态投影）
       ├─ artifact/* RPC  ──→ internal/module/artifact（新，薄层）
       └─ 现有 RPC（thread/turn/approval/dashboard/dag…）不动
cmd/mcp-orch
  ├─ 现有：task_* 工具、WakeupDispatcher、NodeExecutor   （不动，dispatcher 加 paused 闸门）
  └─ 新增工具：web_search / data_analysis / browser_action
internal/store
  └─ 新增 sqlc：agent_tasks / agent_artifacts / agent_task_events
```

---

## 3. 数据库设计

提示词文档要求 6 张表，对照后**只需新增 3 张**，其余复用：

| 提示词要求 | 处置 |
|---|---|
| agent_tasks | **新增**（见下） |
| agent_steps | **复用** `task_dag_nodes`（已含 order 等价物 depends_on、title、status、config 内嵌 tool_needed/expected_output、started/completed 时间戳由 events 承担） |
| agent_tool_calls | **复用** observability JSONL trace（已含 tool call 级 trace_id/span_id/call_id/duration/status，含输入输出审计）；不重复落 PG |
| agent_artifacts | **新增**（见下） |
| agent_memories | **复用** `internal/module/memory` 文件型存储（scope/key/value 语义已覆盖且更强） |
| agent_approvals | **复用** `hook_pending_reviews`（migration 0025，已含 risk 语义 topic/decision/default_action/deadline/idempotency） |

### 3.1 新增表（migration 草案）

```sql
-- agent_tasks：用户视角任务聚合根
CREATE TABLE agent_tasks (
    id              BIGSERIAL PRIMARY KEY,
    task_key        TEXT NOT NULL UNIQUE,          -- 对外标识
    title           TEXT NOT NULL,
    original_prompt TEXT NOT NULL,                 -- 用户原始目标
    status          TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','planning','waiting_user_approval',
                          'running','verifying','completed','failed','cancelled')),
    paused          BOOLEAN NOT NULL DEFAULT FALSE,
    dag_key         TEXT,                          -- 关联 task_dags（planning 完成后填）
    thread_id       TEXT,                          -- 可选：发起对话线程
    plan_json       JSONB,                         -- Planner 原始产出（含 expected_output，供前端计划清单渲染）
    verify_summary  JSONB,                         -- 最终自检结果
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);
CREATE INDEX idx_agent_tasks_status ON agent_tasks(status) WHERE status NOT IN ('completed','failed','cancelled');

-- agent_artifacts：产物元数据（内容在 shared files 磁盘）
CREATE TABLE agent_artifacts (
    id            BIGSERIAL PRIMARY KEY,
    task_key      TEXT NOT NULL REFERENCES agent_tasks(task_key),
    dag_key       TEXT,
    node_key      TEXT,                            -- 产生该产物的步骤
    artifact_type TEXT NOT NULL,                   -- report|chart|html|markdown|data|other
    name          TEXT NOT NULL,
    path          TEXT NOT NULL,                   -- .agnet/shared/ 相对路径
    metadata_json JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_artifacts_task ON agent_artifacts(task_key);

-- agent_task_events：任务级事件时间线（状态变迁审计，前端进度流数据源）
CREATE TABLE agent_task_events (
    id         BIGSERIAL PRIMARY KEY,
    task_key   TEXT NOT NULL REFERENCES agent_tasks(task_key),
    event_type TEXT NOT NULL,                      -- status_changed|plan_generated|step_started|...
    payload    JSONB,
    trace_id   TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_task_events_task ON agent_task_events(task_key, id);
```

### 3.2 既有表的最小改动

- `task_dag_wakeups` claim 查询：dispatcher 的 `ClaimDueWakeups` SQL 增加 `NOT EXISTS (paused agent_task)` 条件（或 join task_dags→agent_tasks），实现暂停闸门。
- 不改任何既有表结构。改 `sql/queries/**` 后按纪律跑 `make sqlc-verify`。

---

## 4. API 设计

按项目 jrpc2 命名风格（非 REST），与提示词文档 API 一一对应：

| 提示词 REST API | 本项目 jrpc2 method | 说明 |
|---|---|---|
| POST /api/agent/tasks | `agent-task/create` | `{prompt, title?, cwd?}` → 建行（queued）+ 异步触发 planning |
| GET /api/agent/tasks/:id | `agent-task/get` | 聚合返回 task + plan + 节点状态 + artifacts + pending approvals |
| — | `agent-task/list` | 任务列表（状态过滤、分页） |
| POST .../plan | `agent-task/plan/regenerate` | 重跑 Planner（OCC base_version） |
| —（计划编辑） | `agent-task/plan/apply-ops` | 透传 `task_dag_apply_ops` typed ops |
| POST .../run | `agent-task/run` | 确认计划并启动（内部走 task_start_dag 等价路径） |
| POST .../pause | `agent-task/pause` | 置 paused 标志 |
| POST .../resume | `agent-task/resume` | 清 paused 标志 |
| POST .../cancel | `agent-task/cancel` | 内部走 TerminateRun |
| POST /approvals/:id/approve | **复用** `approval/respond` | 已存在（`internal/module/turn/rpc.go:23`） |
| POST /approvals/:id/reject | **复用** `approval/respond` | decision 字段区分 |
| GET .../events | **复用 push 事件** | 项目已是 jrpc2 push（优于 SSE/轮询），新增事件名见下 |
| — | `artifact/list` / `artifact/get` | 产物查询（get 返回元数据 + shared file 读取通道） |

新增 push 事件（注册到 `internal/platform/eventsurface/bind.go`）：

- `agent-task/changed` — 任务状态/进度变更（前端 revision 失效，与现有 `task/dag/changed` 模式一致）
- `agent-task/plan/ready` — 计划生成完毕，待用户确认
- `agent-task/artifact/created` — 新产物

所有 RPC 走现有 `Validate → ThreadScope → CapabilityGate → StrictHandler` middleware 链；payload 自动携带 traceparent（前端 wailsBridge 已实现）。

---

## 5. LLM 调用设计

提示词文档要求的 5 个抽象与落地：

| 要求 | 落地 |
|---|---|
| generatePlan() | `planner.GeneratePlan(ctx, goal, projectContext) (PlanJSON, error)` — DreamExecutor 单 prompt，输出 JSON schema 校验 |
| executeReasoning() | **复用** turn 链路（`contract.Session.StartTurn`）— 节点执行本来就是子 agent turn |
| summarizeToolResult() | **复用** provider 原生（CLI 自带）；节点 result 摘要已有 `get_agent_report` |
| verifyStep() | `verifier.VerifyStep(ctx, nodeResult, expectedOutput) (Verdict, error)` — DreamExecutor |
| generateFinalAnswer() | `verifier.FinalCheck(ctx, task, runSummary) (Summary, error)` — DreamExecutor |

要求落实：

- **Provider 无关**：DreamExecutor 已是 claudecli/codexapp 双实现的统一接口，天然满足。
- **Prompt 模板集中管理**：复用 `prompt_templates` 表 + seed migration 模式（同 dag_designer 先例），新增 `agent_planner`、`agent_verifier` 模板。
- **JSON schema 校验**：Go 侧用 schema 校验 + 解析失败自动重试（≤2 次），最终失败 Fail-Fast 报错（项目禁兜底纪律）。
- **调用审计**：复用 observability trace（记录 metadata，不记录密钥）。

---

## 6. 前端设计（frontend-app）

### 6.1 新增页面：Agent 任务工作台

路由 `/tasks`，加入 NavRail。三区布局（复用 Chat 页既有组件模式）：

```
┌─ 任务列表（左，复用 ThreadRail 模式）─┬─ 任务详情（中+右）──────────────┐
│ ● 运行中  调研报告任务                │ 目标描述 + 状态徽标 + 操作按钮     │
│ ⏸ 待确认  数据分析任务                │ （运行/暂停/继续/取消）            │
│ ✓ 已完成  网站生成任务                ├─ 计划清单（PlanChecklist 新组件）─┤
│ + 新建任务（自然语言输入框）           │ ✓ step1 搜索资料    [日志]        │
│                                      │ ▶ step2 整理大纲    [日志]        │
│                                      │ ○ step3 生成报告               │
│                                      ├─ 审批卡片（复用 Chat 审批组件）──┤
│                                      ├─ 产物区（ArtifactGallery 新组件）┤
│                                      └─ 实时日志流（复用 RuntimePanel）─┘
```

### 6.2 组件复用清单

| 需求 | 处置 |
|---|---|
| 任务输入 | 复用 `ComposerDock` 简化版 |
| 计划展示/确认/编辑 | 新组件 `PlanChecklist`（数据源 plan_json + 节点状态）；编辑透传 apply-ops |
| 步骤实时状态 | 监听 `agent-task/changed` push → React Query invalidate（与现有 revision 模式一致） |
| 实时日志 | 复用 RuntimePanel 活动流 + observability 查询 |
| 审批弹窗 | 复用 Chat 页审批卡片（`messageActions.onApproval` 同款数据流） |
| 产物画廊 | 新组件 `ArtifactGallery`，预览复用 FilesPage 的 Markdown/JSON 查看器 |
| 暂停/继续/取消 | 新按钮 → 对应 RPC |

### 6.3 backendApi.js 扩展

`RPC_METHODS` 新增 `agent-task/*`、`artifact/*` 方法名 + 参数校验函数（fail-fast，与现有风格一致）。

---

## 7. 安全边界

全部复用现有机制，新增项只有工具白名单声明：

| 提示词要求 | 落地 |
|---|---|
| 工具权限白名单 | 复用 CapabilityGate + NativeToolDescriptor（DefaultDisabled/FilterMode 已有）；新工具按同模式声明 |
| 文件系统范围限制 | 复用 sharedfilepath 白名单（5 前缀 + traversal 防御）+ provider sandbox 透传 |
| 沙箱限制 | 复用 provider CLI 沙箱（danger-full-access 自动 approval=never 的现有逻辑保持）；data_analysis 工具限定只读输入 + 输出仅写 shared 白名单路径 |
| 禁读 .env/密钥 | 复用 hooks before 阶段（deny 优先级最高）；为新工具补 deny 规则：路径含 `.env`、`*_key`、`credentials` 拒绝 |
| 高风险人工确认 | 复用 ApprovalManager（fail-closed：无前端时自动拒绝）；browser_action 的表单提交/下载动作声明为需审批 |
| 审计记录 | 复用 observability trace（tool call 级输入输出已记录）+ wailsBridge 日志脱敏（forbidden keys 已实现） |
| prompt injection 防护 | web_search/browser_action 返回内容包裹为不可信数据标记（与现有 memory 注入的 system-reminder 模式一致）；Planner/Verifier 的 prompt 模板声明"外部内容不得覆盖安全规则" |

---

## 8. 测试方案

按项目纪律（每个 Go 文件改完跑 `./scripts/test_with_guard.sh <file.go>`，包级跑 `./scripts/test_with_guard.sh <pkg> -count=1`）：

| 层 | 测试 |
|---|---|
| store | 新表 sqlc 查询测试 + `make sqlc-verify` |
| planner | JSON schema 校验/重试/失败 Fail-Fast 的表驱动测试（mock DreamExecutor） |
| projector | bus 事件 → AgentTask 状态投影的状态机表驱动测试 |
| verifier | awaiting_verify 消费、通过/不通过/无 expected_output 三分支 |
| 暂停闸门 | dispatcher claim 的 paused 过滤 SQL 测试 |
| RPC | agent-task/* handler 的参数校验 + middleware 链测试 |
| 前端 | 新组件 vitest + `node scripts/size-guard.cjs` + `npm run build` |
| E2E | 三个验收 Demo（研究报告/数据分析/代码辅助）走 Playwright desktop smoke 模式 |
| 架构 | `./scripts/test_with_guard.sh ./internal/archtest -count=1`（新模块边界入守卫） |

---

## 9. 风险点与待确认问题

1. **web_search 数据源**：需要确定搜索 API（自带 key 的 SaaS？复用 provider CLI 的 WebSearch 原生工具？）。**建议**：MVP 先复用 provider 原生 WebSearch（Claude CLI 已有），宿主侧 web_search 工具延后 — 需确认。
2. **browser_action 范围**：Playwright 引入会带较重依赖（Node 子进程）。**建议**：MVP 砍掉 browser_action，Phase 8 后再评估 — 需确认。
3. **AgentTask 与 Chat 线程的关系**：任务可以从 Chat 发起（"帮我做 X"自动建任务）还是只能从任务页发起？**建议** MVP 只从任务页发起，Chat 集成延后 — 需确认。
4. **mcp-orch 与主进程的表归属**：agent_tasks 表由主进程（internal/store）还是 mcp-orch store 管？**建议**：主进程管聚合表（与 cron_jobs 同侧），mcp-orch 只管执行（现有边界不变）— 需架构确认。
5. **DreamExecutor 并发预算**：planning + verifying 都用单 prompt 子进程，多任务并发时的进程数/费用上限需要配额（建议复用 run 的 budget 字段语义）。
6. **暂停闸门的 SQL 改动**触及 `ClaimDueWakeups` 热路径，需保留 `FOR UPDATE SKIP LOCKED` 语义并补并发测试。

---

## 10. 文件改动清单（预估）

| 区域 | 文件 | 动作 |
|---|---|---|
| migrations | `migrations/01xx_agent_tasks.sql` | 新增 3 表 |
| sql | `sql/queries/agent_task*.sql`、`agent_artifact*.sql` | 新增 |
| store | `internal/store/agenttaskstore/`（sqlc 生成 + 接口） | 新增 |
| 模块 | `internal/module/agenttask/{service,planner,verifier,projector,rpc}*.go` | 新增 |
| 模块 | `internal/module/artifact/{service,rpc}*.go` | 新增 |
| 契约 | `internal/contract/agenttask.go` | 新增 |
| 装配 | `internal/app/` fx 装配 + runner 注册 | 修改 |
| 事件 | `internal/dto/agenttask/`、`eventsurface/bind.go` | 新增/修改 |
| orch | `cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql`（paused 闸门） | 修改 |
| orch | `cmd/mcp-orch/tools/data_analysis_tools.go` 等 | 新增 |
| seed | prompt_templates 新增 agent_planner/agent_verifier seed migration | 新增 |
| 前端 | `frontend-app/src/pages/tasks/`（TasksPage + PlanChecklist + ArtifactGallery） | 新增 |
| 前端 | `backendApi.js`（RPC_METHODS）、`App.jsx`（路由+NavRail）、`useClientStore.js`（revision） | 修改 |
| 文档 | codemap 对应卷 + `make codemap-check` | 修改 |
