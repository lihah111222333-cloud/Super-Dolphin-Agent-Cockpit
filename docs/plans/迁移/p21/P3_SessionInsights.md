# P3: Session Insights 遥测

## 目标

在 turn / session 结束后自动收集聚合指标（tool 调用频率、终态、token snapshot、`skills_selected` 等），为自学习闭环、运维分析和后续排序提供数据支撑；首期交付保持 API-only。

## 现状校准

- 当前 typed event bus 已经暴露 `turndto.Turn*`、`tooldto.ToolCall*`、`uidto.UITokensUpdated` 等事件，但不同事件头部并不等价完整，不能假定每条事件都天然带齐 `thread_id/agent_id/turn_id`。
- 当前并没有一个现成 typed event 能直接给出模型实际用了哪些 skill；v1 最多只能先存 `skills_selected`，来源是 `PrepareTurn` resolver 的选择集。
- 后端已经有 `dashboard/logs` RPC，但 `internal/module/dashboard/ui_page.go:22-30` 的 `DashboardPage` 目前只聚合 `Agents/DAGs/TaskTraces/Skills/CommandCards/Prompts/Memory`；前端也没有 logs / insights 页面，因此 P3 首期必须按 API-only 收口。
- 当前仓库持久化统一走 Postgres / sqlc；P3 v1 同样是 **core-only**，不引入 SQLite 旁路，也不修改 `internal/sidecar/orch/sqlc.yaml`。
- `ToolApprovalRequested` 生产事件当前只在 codex path 命中；`internal/provider/claudecli` 下没有对应 translator，因此 Claude 线程上的 `approval_requests` 目前只能记成 **`0 + approval_requests_observed=FALSE`**，不能读作“真实零次审批”。
- signed skill 验签**延后到 P22**；P3 只消费 observation / insights 数据，不在本期追加 skill verifier 维度。

### Canonical Turn Observation Contract

Canonical Turn Observation Contract：共享 observation 层统一产出 local turn id ↔ provider turn id、`call_id -> turn_id`、`skills_selected`、token snapshot、terminal precedence 与 raw/typed 去重事实；P0b 是 owner，P3 只消费这层输出。

- 必须维护 `local turn id ↔ provider turn id` 映射表，供 turn 终态、tool 事件与 provider raw event 对齐。
- 必须维护 `call_id -> turn_id` 映射；`internal/dto/tool/event.go:46-55` 的 `ToolDiffUpdated` 只有 `ThreadID/AgentID/CallID`，**没有 `turn_id`**，归因不能跳过这张表。
- `skills_selected` 只表示 resolver 在 `PrepareTurn` 选中并准备注入的 skill 集合，**不等于模型实际使用**。
- token snapshot 要做归一：保留旧的非零 token 计数，不被 zero-event 覆盖；Claude path 的 `UITokensUpdated` 经 `internal/provider/unified/ui_tokens.go:58-75` 固定 `Projection="thread"`，且可能不带 `turn_id`，不能直接当 per-turn 权威值。
- terminal precedence 必须固定：`interrupted/aborted` 一旦成立，不能被 late `completed` 覆盖；`internal/dto/turn/event.go:11-21` 的 `TurnCompleted.Success` 是非指针 `bool`，缺字段时有默认 true 陷阱。
- `dto.BusRawProviderEvent` 与 typed event 必须在 observation 层统一去重，只允许按 `call_id`、raw event id 或等价 key 合并一次；collector / trajectory 不得 raw + typed 双算。
- observation 层为 P0b 前置交付；P3 作为 consumer 依赖这层事实，不再自建第二套 turn 归因逻辑。

## 架构设计

- **`fx.Module` 层**：只 `Provide` collector、store、flush queue、serializer 等对象；不在 constructor / lifecycle 里跑采集 loop。
- **`BusModule` 层**：`fx.Invoke(RegisterSubscribers)` 把 collector subscriber 注进 `bus.subscribers`；回调内只做状态 merge / enqueue，**禁同步 DB 写**。
- **`RunnerModule` 层**：flush worker 实现 `Runner.Run(ctx)` 并进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`）；批量写库、重试和 coalesce 都在这里完成。
- **shutdown 流**：`ctx cancel → run.Group 全退 → bus 停派发 subscribers → fx 释放资源`；collector 不在 `fx` 生命周期里手写 drain。v1 采用 **bounded drain 5s**：flush queue 超时未落库的样本直接丢弃并记指标，不承诺 durable WAL/补偿队列。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 共享 observation 层 | `internal/module/turn/observation.go` [NEW] | 由 P0b 交付，统一产出 turn / tool / token / terminal / dedupe 事实 |
| 指标收集模块 | `internal/module/insight/{module.go,collector.go,service.go}` [NEW] | 只消费 observation 层输出，不再自建第二套 turn 归因规则 |
| 存储层 | `internal/store/insight/{contract.go,module.go,store.go}` [NEW] | 基于 sqlc 读写 `public.session_insights` |
| DDL + 查询 | `migrations/0046_session_insights.sql`、`sql/queries/session_insight.sql` [NEW] | 建表、聚合查询、按时间窗筛选；**编号以仓内实际下一可用为准**（当前已占到 `0044_drop_router_priority.sql`，Cron 预占 `0045`） |
| API 层 | `internal/module/dashboard/rpc.go`、必要的 DTO/serializer [PATCH] | 首期仅新增 `dashboard/insights/list` 这类 host/API 读接口；**不另建独立 insight RPC namespace**，`ui/dashboard/get?page=insights` 与前端导航延后到未来 UI 轮次 |
| 模块接线 | `internal/store/module.go`、`internal/app/modules.go`、根 `sqlc.yaml` | 把新 store / module 接进 DI 与 **root** sqlc 生成面 |

> `[...] [NEW]` 表示目标新增路径，当前仓库尚不存在；它们是实施落点，不是“现状已存在文件”的事实锚点。

## DDL 设计

```sql
CREATE TABLE IF NOT EXISTS public.session_insights (
    id                          BIGSERIAL PRIMARY KEY,
    thread_id                   TEXT        NOT NULL DEFAULT '',
    agent_id                    TEXT        NOT NULL DEFAULT '',
    session_id                  TEXT        NOT NULL DEFAULT '',
    provider                    TEXT        NOT NULL DEFAULT '',
    local_turn_id               TEXT        NOT NULL DEFAULT '',
    provider_turn_id            TEXT        NOT NULL DEFAULT '',
    started_at                  TIMESTAMPTZ,
    completed_at                TIMESTAMPTZ,
    duration_ms                 INTEGER     NOT NULL DEFAULT 0,
    success                     BOOLEAN,
    status                      TEXT        NOT NULL DEFAULT 'unknown',
    stop_reason                 TEXT        NOT NULL DEFAULT '',
    tool_calls                  INTEGER     NOT NULL DEFAULT 0,
    tool_calls_observed         BOOLEAN     NOT NULL DEFAULT TRUE,
    tool_failures               INTEGER     NOT NULL DEFAULT 0,
    tool_failures_observed      BOOLEAN     NOT NULL DEFAULT TRUE,
    approval_requests           INTEGER     NOT NULL DEFAULT 0,
    approval_requests_observed  BOOLEAN     NOT NULL DEFAULT FALSE,
    token_input                 INTEGER     NOT NULL DEFAULT 0,
    token_output                INTEGER     NOT NULL DEFAULT 0,
    token_total                 INTEGER     NOT NULL DEFAULT 0,
    token_snapshot_observed     BOOLEAN     NOT NULL DEFAULT FALSE,
    context_window_tokens       INTEGER     NOT NULL DEFAULT 0,
    ui_projection               TEXT        NOT NULL DEFAULT '',
    skills_selected             JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_session_insights_thread_created
    ON public.session_insights (thread_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_session_insights_created
    ON public.session_insights (created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS uq_session_insights_local_turn
    ON public.session_insights (thread_id, local_turn_id)
    WHERE thread_id <> '' AND local_turn_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS uq_session_insights_provider_turn
    ON public.session_insights (provider, agent_id, provider_turn_id)
    WHERE provider <> '' AND agent_id <> '' AND provider_turn_id <> '';
```

> **`*_observed` flag 区分 unknown vs 0**：`approval_requests_observed=FALSE` 表示当前 provider 未对齐统一事件面（如 Claude path 不产生 `ToolApprovalRequested`），数值 0 不能被读作业务上的“零次审批”；`tool_calls_observed` / `tool_failures_observed` 默认为 TRUE（两个 provider 都对齐了事件面）；`token_snapshot_observed=FALSE` 用于只收到 zero-event / context-window-only 时明示未检索到真实 token snapshot。

> `success BOOLEAN` 必须保持 nullable，并一路延续到 sqlc / store contract。根 `sqlc.yaml` 已启用 `emit_pointers_for_null_types: true`，因此 collector 域模型字段也必须保持 `Success *bool`，不能在中间层偷改成 `bool`。
>
> P3 v1 **core-only**：只改根 `sqlc.yaml`，生成命令固定为仓库根的 `sqlc generate`，产物进入 `internal/store/sqlc/*`；**不改** `internal/sidecar/orch/sqlc.yaml`。migration 文件名实施时按实际下一可用编号命名；当前口径为 `0046_session_insights.sql`。

## 收集来源建议

- collector 只消费 observation 层输出，不直接混算 raw + typed；`tool_calls`、`tool_failures`、`approval_requests`、token snapshot 的去重与归因都由 observation 层统一完成。
- `TurnStarted` / terminal 事件：确定起止时间、终态、stop reason。
- `ToolCallBegin` / `ToolCallEnd`：统计工具调用次数与失败数。
- `ToolApprovalRequested`：统计审批请求次数；P3 v1 的 `approval_requests` **只统计这一个统一事件面**，`request_user_input` / elicitation 暂不并入该指标；Claude path 生产为 0 时必须用 `approval_requests_observed=FALSE` 表示“未观测”，而不是“业务上零次审批”。
- `UITokensUpdated`：记录 token snapshot，但必须保留已有非零 token 计数，不让 zero-event 覆盖。
- `PrepareTurn` instrumentation：提供 `skills_selected`；它只是 resolver 的 prepare-turn 选择集，不代表模型真实使用。

## 关键实现约束

- collector 必须消费共享 observation 层，不要把统计逻辑塞进 `turnTracker` 或再自建一套 turn / call 归因 map。
- **TerminalKind → Status 映射约束**：`observation.TerminalKind` 用空串 `""` 表示未观测，`insight.Status` 的 SQL 默认是字符串 `"unknown"`。collector 写入前必须显式映射 `"" → "unknown"`，其余 5 个终态字符串一致无需翻译。
- `UITokensUpdated` 的 zero-event / context-window-only 事件**禁止覆盖**已有非零 token 计数。
- Claude path 的 `UITokensUpdated` 经 `internal/provider/unified/ui_tokens.go:58-75` 固定 `Projection="thread"`，且可能不带 `turn_id`；它不能直接归到单个 turn。
- `ToolApprovalRequested` 生产仅在 codex path 命中（`internal/provider/codexapp/event_map.go:248-255`）；`internal/provider/claudecli/` 下 0 生产命中。Claude path 的 `approval_requests` 必须 **写 `approval_requests_observed=FALSE`**，值 0 为“未观测”而非“真实零次”；下游聚合查询需显式 `WHERE approval_requests_observed` 才能用于横向比较。当 Claude driver 未来补齐 approval 事件时，直接升级为 `observed=TRUE`，不改列名语义。
- `TurnCompleted.Success` 是非指针 bool，collector 不能仅凭它决定成功 / 失败；unknown 语义必须保留到 `Success *bool`。
- `success` / terminal status 的 precedence 要固定：`interrupted/aborted` 不能被 late `completed` 覆盖。
- **写入模型强制 UPSERT by `(thread_id, local_turn_id)`**：flush worker 不得以 `provider_turn_id` 为主键 insert。具体语义：
  - 新 turn：`INSERT ... ON CONFLICT (thread_id, local_turn_id) DO UPDATE ...`；
  - 已有行收到 `provider_turn_id` / token snapshot：走 UPDATE 合并，不得另开新行；
  - `uq_session_insights_provider_turn` 存在只是次级约束，命中冲突必须回退到按 `local_turn_id` 查找既有行并 UPDATE。
- **terminal precedence 必须在 SQL 层 guard**，不是只靠 collector 的内存状态（进程崩溃重建时会覆写）。UPSERT 的 DO UPDATE 分支使用 `CASE` 保护：
  - `status = CASE WHEN session_insights.status IN ('interrupted','aborted') THEN session_insights.status ELSE EXCLUDED.status END`；
  - `success` 类似：已经是 false / interrupted precedence 的行不允许被 late `completed` 覆盖；
  - token 非零计数：`token_total = GREATEST(session_insights.token_total, EXCLUDED.token_total)`，避免被 zero-event 回退。
- **observed flag 的消费方强制过滤**：`sql/queries/session_insight.sql` 必须同时提供“带 `WHERE *_observed` 过滤的聚合变体”（如 `ListObservedApprovalRequests`），避免调用方忘记过滤而把 Claude 的未观测 0 与 codex 的真实 0 混到一起。
- `session_insights` 只存聚合指标；原始调试仍看 raw provider event、task trace、system log。
- 后续若要把 insights 用于 skill ranking / recommendation，应改 `internal/module/turn/skills.go` 或新 scorer；不要把排序逻辑塞回纯文件写入的 `skill/service.go`。
- signed skill 验签延后到 P22；P3 不把 skill 已出现于 `skills_selected` 误写成已验签且已被模型执行。

## 首期收口

- **首期 API-only**；后端可提供读接口与聚合查询，但不默认承诺前端页面。
- 若未来需要 UI，需单列前端改动：扩 `DashboardPage`、新增 `ui/dashboard/get?page=insights`、补前端导航 / 页面与加载链；这不属于 P21 首期默认交付，当前 API 层只要求 `dashboard/insights/list` 之类 host/API read。
- dashboard 已有 logs RPC 只说明后端已有 `dashboard/logs` 能力，不代表前端已有 logs / insights 页面。

## 必测项

- local / provider turn id 映射：同一逻辑 turn 不应落成两条 insight。
- terminal precedence：`interrupted/aborted` 不能被后到 `completed` 覆盖。
- token snapshot：只收到 zero-token / context-window-only 事件时，不应把已有 token 计数清零。
- Claude path：`approval_requests` 必须写 `approval_requests_observed=FALSE`且值为 0，且 thread-level token snapshot 不能误归到 per-turn；`token_snapshot_observed` 不被 zero-event 进位。
- API-only 收口：若有人要求 UI，必须额外列出 `DashboardPage + ui/dashboard/get?page=insights + 前端导航 / 页面` 改动，而不是文档里默认暗含。
- partial unique index：构造 `thread_id+local_turn_id` 两者皆空 / `provider+agent_id+provider_turn_id` 任一空 的 fixture，验证 `uq_session_insights_*` 不因空串行务执错。
- `*_observed` flag：Claude path assert `approval_requests_observed=FALSE`；codex path assert `approval_requests_observed=TRUE`；context-window-only `UITokensUpdated` assert `token_snapshot_observed` 保持原值不回退。
- UPSERT 模型：先只带 `local_turn_id` 写入，再后到一条带 `provider_turn_id` 的事件，断言最终只有一条行且 provider_turn_id 被合并进同一行；不得产生第二行。
- SQL-level terminal precedence：先写 `interrupted`，再写 `completed(success=true)`，断言 UPSERT 结果仍为 `interrupted`、`success` 未被翻转。
- `observed` 过滤查询：`ListObservedApprovalRequests` 必须排除 Claude path 的 `approval_requests_observed=FALSE` 行；断言该查询结果的 avg / count 不被未观测 0 污染。
