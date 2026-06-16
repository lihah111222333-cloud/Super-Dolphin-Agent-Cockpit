# P23.10: DAG 模板 + UI 编辑能力

> 创建时间：2026-04-25 | 状态：**未开动（后段子任务，依赖 P3 + P6）**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 7
> 用户 2026-04-25 提出：「DAG 要有 UI，用户可以保持模版，然后编辑模版或者编辑 dag 任务。」

## 目标

为 DAG 提供「模板 + UI 编辑」能力：用户可保存 DAG 模板（可参数化的可复用骨架）、基于模板实例化任务、独立编辑模板或已实例化的 DAG 任务。降低用户操作门槛，让自动迭代场景从「agent 写 DAG」演化为「用户视化设计模板 + agent 触发实例化」。**后段子任务**。

## UX 设计原则（authoritative）

用户 2026-04-25 提出 UI 必须满足：「一眼可以看懂怎么设置，可视化、配置化，可由 agent 创建 DAG 用户手动微调，用户使用模板功能创建 DAG，可以添加模板。」翻译为以下硬约束：

- **一眼看懂**：默认视图必须能让非工程用户在 30 秒内理解 DAG 结构与当前进度——拓扑图 + 节点状态色码 + 关键字段悬停
- **可视化**：DAG 拓扑必须有图形化展示（v1 mermaid，v2 视需求换 d3）；状态推进通过颜色 + icon 即时反馈
- **配置化**：节点字段（launch / verify / depends_on / output_schema 等）走表单编辑，**不**要求用户写 JSON / YAML
- **agent 创建 + 用户微调**：agent 通过 `task_create_dag` 创建后，用户能在 UI 上无缝看到并编辑（依赖 P3 显式 start：agent 创建后默认 `trigger=manual`，用户编辑确认后再 start）
- **模板可由用户添加**：用户既能消费现有模板，也能保存自己设计的 DAG 为新模板（包括从已有 DAG 一键 "save as template"）

## 三个用户故事（authoritative）

### 故事 1：agent 创建 → 用户微调
1. 主 agent 调 `task_create_dag(trigger="manual", auto_start=false, ...)` 创建 DAG（P4 路径）
2. UI「任务列表」实时出现新 DAG，状态 `draft`
3. 用户点开 → 看到拓扑 + 各节点 launch spec → 在 UI 上调整某节点 prompt 或 verify 配置（P10 `dag/edit_node`，CAS 仅 pending）
4. 用户点 "Start" 按钮 → UI 调 `dag/start(dag_key)`（P3 路径）→ DAG 开始自驱推进

### 故事 2：用户从模板创建
1. 用户在「模板库」浏览 → 选中模板 → 点 "Use this template"
2. UI 弹出参数表单（基于模板的 `parameters` 字段渲染）→ 用户填值
3. UI 调 `dag/instantiate(template_key, params)` → 服务端拷贝模板 snapshot 进 task → 返回 dag_key
4. UI 跳转任务列表新建 DAG，与故事 1 第 3 步合流（用户可继续微调或直接 Start）

### 故事 3：用户添加模板
1. 用户在「任务列表」中选一个已经设计好（哪怕未启动）的 DAG → 点 "Save as template"
2. UI 弹出表单：`title / description / 哪些字段参数化（标 `{{...}}` 占位）/ scope (private/tenant/public)`
3. UI 调 `dag/template/create(...)` → 服务端落 `dag_templates` 表 + 第一份 revision
4. 模板立即出现在「模板库」，其它人（按 scope）可见可 fork


## 现状校准（事实层）

- 当前**无** DAG 模板概念：`task_dags` 表只承载实例（`migrations/0004_ack_dag.sql:33-67`、`internal/sidecar/orch/store/taskdag/contract.go:160-181`）
- DAG 创建是 upsert：`internal/sidecar/orch/orchestration/dag.go:109-131`；每次创建相当于"新实例"，无"基于模板实例化"路径
- 无 DAG 编辑专用 RPC：当前只能 `task_create_dag` 整体 upsert（覆盖）+ `task_update_node` 改 node status/result（`internal/sidecar/orch/tools/task_tools.go:84-91`）；不能只改 node 的 prompt / depends_on / verify spec
- node UpsertNode 会覆盖整 node：`internal/sidecar/orch/sql/queries/task_dag_node_write.sql:1-12`
- UI 框架基础：Wails 桌面应用 + WS（`internal/ui/wails/http_server.go:15,39-46`），但真实前端主要在 `cmd/agent-terminal/frontend/vue-app/*`；Wails 只处理 bridge/transport。当前前端 DAG 列表使用通用 `DataPage`，但 `DataPage` 未 emit `select`，`DagDetailModal` 仍是占位；P10 owner 必须先接通 `dashboard/dagDetail`。
- 拓扑可视化：`mermaid` 依赖已存在，但当前**无** DAG 专用 renderer / graphviz / d3 组件占位

## 推荐架构

### 模板（template）vs 任务（instance）

- **模板**：DAG 骨架 + 参数化字段（如 `{{repo_path}}` / `{{branch}}` / `{{model}}`）
- **任务**：基于模板实例化时**拷贝模板 snapshot 进任务**——后续模板编辑**不**影响已实例化任务（解耦）
- 任务可独立编辑（仅未推进节点）

### RPC 设计

- `dag/template/create | get | list | update | delete`：模板 CRUD
- `dag/instantiate(template_key, params)`：基于模板实例化 DAG（拷贝 snapshot 进 task）
- `dag/edit_node(dag_key, node_key, fields)`：只改单 node 的 prompt / depends_on / verify spec / launch spec；CAS fence：只在 node `status=pending` 时允许；`status>=running` 拒绝（`ErrNodeAlreadyRunning`）
- `dag/edit_dag(dag_key, fields)`：改 DAG 级 schedule / metadata；只允许 `draft/not_started` 或全部节点仍 `pending` 且 `started_at IS NULL`，已启动 DAG 只能 fork 新 revision，不得原地改执行语义

### UI 形态

- **模板库 tab**：列出 owner / tenant 可见的所有模板，支持搜索 / fork / 创建
- **任务列表 tab**：列出 owner / tenant 可见的所有 DAG 实例，状态一目了然
- **模板编辑器**：拓扑可视化（推荐 mermaid 渐进式 / 后期可换 d3）+ 节点表单编辑（launch spec / verify spec / depends_on）
- **任务编辑器**：复用模板编辑器组件；推进过的节点只读（灰色）+ tooltip 显示当前 status

### 编辑权限矩阵

| 对象 | 状态 | 可编辑字段 |
|---|---|---|
| 模板 | 任意 | 全部（影响后续实例化）|
| 任务（DAG）整体 | 无 running 节点 | schedule / metadata |
| 任务（DAG）整体 | 有 running 节点 | 拒绝（`ErrDAGRunning`）|
| 任务节点 | `pending` | launch / depends_on / verify spec |
| 任务节点 | `running` 或终态 | 只读 |

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| DDL | `0070_dag_templates.sql` [NEW]（编号校准） | `dag_templates` 表 + `task_dags.template_key/template_version/schema_hash` 列 + `dag_template_revisions`（版本历史） |
| 模板 store | `internal/sidecar/orch/store/dagtemplate/*.go` [NEW] | CRUD + 版本管理 |
| 模板 service | `internal/sidecar/orch/orchestration/template_service.go` [NEW] | 模板 → DAG 实例化（拷贝 snapshot）+ 参数渲染 |
| 模板 RPC registrar | `internal/sidecar/orch/orchestration/rpc_template.go` [NEW] | `registerDAGTemplateRPC` 注册 `dag/template/*` + `dag/instantiate` + `dag/edit_node` + `dag/edit_dag`；`rpc.go` 只调用 registrar |
| 编辑权限 fence | `internal/sidecar/orch/sql/queries/task_dag_node_runtime.sql`（扩展） | CAS：`UPDATE ... WHERE status='pending'` 拒绝跳态写入 |
| UI bridge/transport | `internal/ui/wails/http_server.go`、`internal/ui/wails/bridge.go`（扩展） | 只承载 Wails WS/bridge 调用与 P6 identity，不放主要页面实现 |
| UI 模板库 tab | `cmd/agent-terminal/frontend/vue-app/*`（新增/扩展 DAG template components/store/routes） | 列表 / 详情 / 搜索 / fork；通过 Wails bridge 调 RPC |
| UI 任务列表 tab | `cmd/agent-terminal/frontend/vue-app/*`（DAG instances components/store/routes） | 列表 / 状态 / 关联模板；接 `dashboard/dagDetail` |
| UI 编辑器 | `cmd/agent-terminal/frontend/vue-app/*`（DAG editor + mermaid renderer） | 拓扑可视化 + 节点表单 + 编辑权限矩阵 |
| 鉴权 | 复用 P6 `WithCallerIdentity` middleware | 模板 / 任务的 owner / tenant 鉴权 |

### HEAD drift note（2026-04-25）

不要再把 UI 页面落点写到 Wails 内部 dag 页面目录。当前 HEAD 的真实前端主要在 `cmd/agent-terminal/frontend/vue-app/*`；`internal/ui/wails/*` 负责 Wails bridge、HTTP/WS transport、native binding。P10 派单时按 10–15 文件拆分：前端 components/store/routes 在 vue-app，bridge/auth/transport 在 Wails，service/RPC/store 在 `cmd/mcp-orch`。

### schema / RPC 拆分依赖

P10 UI 表单字段不得要求 P7/P8/P11/P12/P13 并行修改 `internal/sidecar/orch/tools/task_tools.go`。字段注册表读取 `internal/sidecar/orch/tools/dag_schema_registry.go` 的 per-feature providers；未合入 provider 的字段在 UI feature gate 隐藏但保留 extension slot。模板 RPC 通过 `registerDAGTemplateRPC` 落在 `rpc_template.go`，避免与 P3/P6/P11 同时改 `rpc.go`。

## DDL / SQL

**`0070_dag_templates.sql`** 草案（编号开 PR 时校准）：

```sql
CREATE TABLE IF NOT EXISTS public.dag_templates (
    template_key    TEXT        PRIMARY KEY,
    title           TEXT        NOT NULL,
    description     TEXT        NOT NULL DEFAULT '',
    schema_json     JSONB       NOT NULL,  -- DAG 模板 + 参数化标记
    parameters      JSONB       NOT NULL DEFAULT '[]'::jsonb,  -- [{name, type, default, required}]
    schema_hash     TEXT        NOT NULL DEFAULT '',
    schema_version  INTEGER     NOT NULL DEFAULT 1,
    owner_id        TEXT        NOT NULL DEFAULT '',
    tenant_id       TEXT        NOT NULL DEFAULT '',
    scope           TEXT        NOT NULL DEFAULT 'private',
    version         INTEGER     NOT NULL DEFAULT 1,
    enabled         BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.dag_template_revisions (
    template_key    TEXT        NOT NULL REFERENCES public.dag_templates(template_key) ON DELETE CASCADE,
    version         INTEGER     NOT NULL,
    schema_json     JSONB       NOT NULL,
    parameters      JSONB       NOT NULL,
    schema_hash     TEXT        NOT NULL DEFAULT '',
    schema_version  INTEGER     NOT NULL DEFAULT 1,
    edited_by       TEXT        NOT NULL DEFAULT '',
    edited_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (template_key, version)
);

ALTER TABLE public.task_dags ADD COLUMN template_key TEXT NOT NULL DEFAULT '';
ALTER TABLE public.task_dags ADD COLUMN template_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.task_dags ADD COLUMN template_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE public.task_dags ADD COLUMN template_schema_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE public.task_dags ADD COLUMN template_schema_version INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_dag_templates_owner_tenant
    ON public.dag_templates (owner_id, tenant_id, scope);

-- 活表 task_dags 上的索引必须使用 CREATE INDEX CONCURRENTLY，放入单独 no-transaction migration
```

**`0070_dag_template_index_no_tx.sql`**（no-transaction，禁止 `BEGIN/COMMIT`）：

```sql
CREATE INDEX CONCURRENTLY idx_task_dag_template
    ON public.task_dags (template_key, template_version)
    WHERE template_key <> '';
```

## 依赖

- P3 已合入（`owner_id / tenant_id / scope` 字段已就位）
- P6 已合入（外部 RPC AuthN/AuthZ；UI 调用方需鉴权）
- P0–P2 / P7–P13 schema freeze（模板 schema 必须能表达 P7–P13 引入字段；若 P11–P13 未合入，字段必须以 feature gate 隐藏但保留 schema extension slot）

## 风险

- **模板演进**：模板修改后已实例化任务**不**应受影响（实例化时拷贝 snapshot；模板版本号递增 + revision history 表）
- **编辑已运行 DAG**：必须区分"已推进节点"（只读）vs"未推进节点"（可编辑）；CAS fence 拒绝跳态写入
- **schema 维度膨胀**：模板 schema 必须支持 P7–P13 全部新字段（`last_activity_at` 不需要进 schema，但 `verify.*` / growth / swarm / output_schema 必须支持或 feature-gated）；模板兼容性测试要覆盖
- **UI 选型**：mermaid 渐进式低风险但拓扑能力弱（不支持自由布局）；d3 是重组件但能力强；推荐 v1 mermaid → v2 视需求换 d3，由 owner 选型
- **多人编辑冲突**：v1 不做实时协作；同一模板被多人同时编辑时按 last-write-wins + revision history 提供回滚；UI 上加 "正在编辑" 提示但不强锁
- **参数注入安全**：`{{...}}` 渲染必须 escape 用户输入；不允许参数值里带 prompt injection 进 verifier / launcher
- **fork 链路爆炸**：模板 fork 可能形成 N 个变体；v1 最小只记录 `forked_from_template_key/version/revision_id` 供审计，不做 lineage 图谱；若缺这些字段，UI 必须明确显示“不支持审计 lineage”

## 必测项

- 模板 CRUD（含权限：owner/tenant/scope）
- 基于模板实例化 DAG（参数渲染 + snapshot 拷贝）
- 编辑未推进 DAG（pending node CAS 通过）
- 编辑已推进 DAG 节点（running / 终态）→ 拒绝 `ErrNodeAlreadyRunning`
- 编辑已启动 DAG 整体 → 拒绝 `ErrDAGRunning`
- 模板版本递增 + revision history 回滚
- 模板修改不影响已实例化任务
- 参数注入 escape（`{{}}` 渲染不被 injection）
- UI 拓扑可视化基础渲染（mermaid v1）
- UI 编辑权限矩阵（已推进节点只读）
- 鉴权：跨 tenant 不可见 / 跨 owner 不可编辑

## 输入材料

- README §"P10 DAG 模板 + UI 编辑能力"
- [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 7
- p21 P0 自学习模板（参考 modal / scope 形态）
- 用户原话：「DAG 要有 UI，用户可以保持模版，然后编辑模版或者编辑 dag 任务」
## 待办

- **错误 catalog（a7）**：把 `ErrCallerIdentityRequired` / `ErrNodeAlreadyRunning` / `verdict_lost` / schema validation error 映射成人话说明 + 下一步修复入口；不得只展示内部枚举。
- **模板 preview / diff / lineage（a7+a5）**：模板实例化前展示参数预览、版本 diff、`schema_hash`；v1 可不做 fork lineage 图，但必须保存 fork 来源字段或写明不支持审计链。
- **WS 实时事件（a7）**：任务列表 / 详情页订阅 DAG / verify / repair / budget 事件，agent 创建后实时出现，Start CTA 只在 manual/draft 状态显示。
- **金融预设可发现性（a7+a10）**：模板库提供“金融预设” badge，展示 `unanimous + additionalProperties=false + audit_log=true + parse_mode=strict + schema_draft=2020-12 + hash-chain` 的合规说明。
- **cost preview UI（a10 必修）**：模板页 / 实例化页 / Start 前显示预计 nodes、swarm members、repair rounds、arbiter calls、token、base/max/p95 $/DAG、$/月、provider capacity verdict（足够/不足/需审批/阻断）；超过 tenant/subscription 预算阈值必须 approval 或 hard block，二次确认不能替代授权。
- **当前 UI 接通项（a7 必修）**：`DataPage` 增 `select` 事件，DAG 列表卡片可点击/键盘进入详情；`DagDetailModal` 必须调用 `dashboard/dagDetail` 填充真实 nodes，并显示 Start CTA / 只读锁 / 状态 legend。
- **成本/权限边界**：Start CTA 依赖 P3 `dag/start` 和 P9 `dag/cost_preview`；P10 不得在缺 P6 caller identity / tenant filter 时开放跨 tenant 模板列表。

## UI 状态字典与页面契约（需求补全仲裁）

### 状态 legend（v1）

| 状态 | 文案 | 可编辑 | 下一步 |
|---|---|---|---|
| `draft`（派生态，不是主 status） | 未启动，可配置 | DAG/node 可编辑 | Start / Save as template |
| `pending` | 等待依赖 | node 可编辑 | 等待或调整依赖 |
| `running` | 执行中 | 只读 | 查看 activity |
| `awaiting_verify/verifying` | 校验中（source=`verify_phase`） | 只读 | 查看 verifier |
| `repairing` | 修复中（source=`verify_phase/output_validation_phase`） | 只读 | 查看反馈 |
| `validating_output` | JSON 输出校验中（source=`output_validation_phase`） | 只读 | 查看校验详情 |
| `validation_repairing` | JSON 输出修复中（source=`output_validation_phase`） | 只读 | 查看 schema 错误 |
| `idle_watch` | 活性观察中（source=`activity_state`） | 只读 | 查看 activity |
| `relaunching` | 自动重拉中（source=`activity_state`） | 只读 | 查看 relaunch 原因 |
| `backpressured` | 背压中（source=`growth_capped`/P9 signals） | 只读 | 查看容量/预算 |
| `done` | 完成 | 只读 | 查看结果 |
| `failed` | 失败 | 只读 | 查看错误/重试入口 |
| `observe_lost` | 观察丢失 | 只读 | 运维恢复/人工处理 |
| `verdict_lost` | 裁决不可得 | 只读 | 重试 arbiter / 人工处理 |

`display_state = status + verify_phase + output_validation_phase + activity_state + growth_capped`；legend 展示的是 UI display state，不得污染主 `task_dag_nodes.status`。`draft = trigger=manual AND started_at IS NULL AND no running nodes`，仅 UI 派生态，不得写入 `task_dag_nodes.status`。

### 表单字段注册表

P10 owner 必须按分组注册字段：launch、depends_on、verify(P8)、activity/idle(P7)、scale/cost(P9)、growth_budget/convergence(P11)、swarm(P12)、output_schema/output_validation(P13)。字段注册表必须是可验收 schema：`field_key/source_schema_path/default/validator/help/advanced/sensitive/editable_when/error_catalog_key/feature_gate`。用户不得直接编辑 raw JSON/YAML 作为唯一入口。

### cost preview 保密边界

UI 只显示预算估算、base/max/p95、是否足够、风险档位、审批状态；禁止展示 bucket 余量、内部窗口、provider 原始 RPM/TPM/concurrency 或 per-tenant quota 算法。Start/Edit CTA 的禁用原因必须映射成错误 catalog 人话说明。

### 大规模 UI

DAG detail 默认 summary + cursor node page；result/audit/validation 默认不返回；拓扑超过 1000 nodes 进入 cluster/virtualized mode，节点详情懒加载，禁止一次渲染 10000-node Mermaid。模板 Use/Save 必须有 params preview、schema diff/hash、fork 来源或明确“不支持审计 lineage”。
