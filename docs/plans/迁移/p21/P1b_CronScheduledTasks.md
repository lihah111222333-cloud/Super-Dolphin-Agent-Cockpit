# P1b: Cron 定时任务

## 目标

为系统补齐时间维度的主动性。v1 先支持用户 / 宿主管理周期性任务；若未来需要模型直接安排，再单独补 agent-visible tool 方案。

## 现状校准

- 当前仓库的持久化基线是 Postgres + `migrations/` + `sql/queries/` + `internal/store/*`，不是 SQLite。
- 面向宿主 / UI 的能力入口统一通过 `internal/module/*/rpc.go` 输出 `rpc.HandlerMapResult`，不是 `internal/mcpserver/common/*`。若未来要给模型直接调用，需另做 agent-visible tool，不在本期默认范围。
- 第一版 Cron 执行建议复用现有 `thread.Service.Start` 与 `turn.Service`，不要直接在 platform 层偷偷拉 raw provider session。
- 当前 approval 流程依赖 live RPC / UI callback；后台定时任务不能假定存在可交互前端，因此 v1 必须明确走 non-interactive 执行策略。

## 推荐架构

- **`fx.Module` 层**：只 `Provide` scheduler / service / store 等对象；DB pool 生命周期继续由 `internal/platform/db` 管。Scheduler 自身无需 `OnStop` drain。
- **`BusModule` 层**：通过 `fx.Invoke(RegisterSubscribers)` 把 Cron 相关订阅器注入 `bus.subscribers`，只订 job submit / turn terminal / completion reconciliation 之类事件。
- **`RunnerModule` 层**：**拆成两个独立 `Runner` actor**，同时进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） 并由 `run.Group` 统一调度；禁止在 actor 的 `Run()` 内再起匿名 goroutine（违反 `docs/契约/rungroup-convention.md:123-131` 反模式）：
  - `cronTickActor`：长跑 tick loop，claim due job + 入队提交 turn；受 ctx cancel 退出。
  - `cronLeaseActor`：独立 actor，周期续租本节点所有已 claim 的 job；受 ctx cancel 退出。
  - 两 actor 共享只读的 `claim registry`（由 scheduler service 提供），不互相起 goroutine。
- **shutdown 流**：`ctx cancel → run.Group 全退 → bus 停派发 subscribers → fx 释放资源`；Cron v1 不在 `fx` 生命周期里手写 cancel worker。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| DDL + 查询 | `migrations/0045_cron_jobs.sql`、`sql/queries/cron_job.sql` [NEW] | 目标新增 `public.cron_jobs` / `public.cron_job_runs` 与 claim / renew / finish / list 查询；`ClaimDueJobs()` 的 SQL 形状必须显式包含 `FOR UPDATE SKIP LOCKED`。**编号以仓内实际下一可用为准**（当前已占到 `0044_drop_router_priority.sql`） |
| 存储库 | `internal/store/cron/{contract.go,module.go,store.go}` [NEW] | 提供 CRUD、`ClaimDueJobs()`、`RenewLease()`、`ExtendClaim()`、`ReleaseClaim()`、`MarkFinished()` |
| Run 存储 | `internal/store/cron/run_store.go` 或同包扩展 [NEW] | 持久化单次触发 run，记录 idempotency key、submitted turn、终态 |
| 业务服务 | `internal/module/cron/{contract.go,service.go}` [NEW] | 负责任务创建 / 更新 / 删除 / 列举与参数校验；定义 `ErrMissingCWD`、`ErrProviderNotSupported` 等硬错误 |
| 调度器对象 | `internal/module/cron/scheduler.go` [NEW] | 封装 claim / submit / renew / reconcile 逻辑，但不自己长跑 |
| Turn / Provider 契约扩展 | `internal/dto/provider/turn.go`、`internal/module/turn/{contract.go,service.go}`、`internal/contract/provider.go` | 为 `StartTurn` 补 `dedupe_key` 输入，并新增 `LookupByDedupeKey()` / `Observe()` 恢复面 |
| Runner / Subscriber 接线 | `internal/module/cron/module.go` [NEW] | `fx.Invoke` 注入 `bus.subscribers`；`cronTickActor` 与 `cronLeaseActor` 两个独立 actor 进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） |
| Host RPC 接口 | `internal/module/cron/rpc.go` [NEW] | 暴露宿主侧 slash 风格方法，如 `cronjob/create`、`cronjob/list`、`cronjob/delete` |
| 模块接线 | `internal/app/modules.go`、`internal/store/module.go`、根 `sqlc.yaml` | 注册 cron 模块与 cron store，并把 schema / queries 接进 **root** sqlc 生成面 |

> `[...] [NEW]` 表示目标新增路径，当前仓库尚不存在；它们是实施落点，不是“现状已存在文件”的事实锚点。

## DDL 设计

```sql
CREATE TABLE IF NOT EXISTS public.cron_jobs (
    id                  TEXT        PRIMARY KEY,
    name                TEXT        NOT NULL,
    prompt              TEXT        NOT NULL,
    schedule_type       TEXT        NOT NULL DEFAULT 'cron',
    schedule_expr       TEXT        NOT NULL,
    timezone            TEXT        NOT NULL DEFAULT '',
    provider            TEXT        NOT NULL DEFAULT 'codex',
    model               TEXT        NOT NULL DEFAULT '',
    cwd                 TEXT        NOT NULL,
    config              JSONB       NOT NULL,
    skills              JSONB       NOT NULL DEFAULT '[]'::jsonb,
    notify_channel      TEXT        NOT NULL DEFAULT '',
    enabled             BOOLEAN     NOT NULL DEFAULT TRUE,
    next_run_at         TIMESTAMPTZ NOT NULL,
    last_scheduled_at   TIMESTAMPTZ,
    last_run_at         TIMESTAMPTZ,
    claimed_at          TIMESTAMPTZ,
    claimed_by          TEXT        NOT NULL DEFAULT '',
    lease_expires_at    TIMESTAMPTZ,
    claim_token         TEXT        NOT NULL DEFAULT '',
    thread_id           TEXT        NOT NULL DEFAULT '',
    agent_id            TEXT        NOT NULL DEFAULT '',
    active_turn_id      TEXT        NOT NULL DEFAULT '',
    last_turn_id        TEXT        NOT NULL DEFAULT '',
    failure_count       INTEGER     NOT NULL DEFAULT 0,
    max_attempts        INTEGER     NOT NULL DEFAULT 0,
    next_retry_at       TIMESTAMPTZ,
    last_status         TEXT        NOT NULL DEFAULT '',
    last_error_at       TIMESTAMPTZ,
    last_error          TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.cron_job_runs (
    id                  TEXT        PRIMARY KEY,
    job_id              TEXT        NOT NULL REFERENCES public.cron_jobs(id),
    scheduled_at        TIMESTAMPTZ NOT NULL,
    idempotency_key     TEXT        NOT NULL,
    dedupe_key          TEXT        NOT NULL DEFAULT '',
    thread_id           TEXT        NOT NULL DEFAULT '',
    agent_id            TEXT        NOT NULL DEFAULT '',
    turn_id             TEXT        NOT NULL DEFAULT '',
    submitted_at        TIMESTAMPTZ,
    status              TEXT        NOT NULL DEFAULT 'pending',
    error               TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cron_jobs_due
    ON public.cron_jobs (COALESCE(next_retry_at, next_run_at))
    WHERE enabled = TRUE;

CREATE UNIQUE INDEX IF NOT EXISTS uq_cron_job_runs_idempotency
    ON public.cron_job_runs (job_id, idempotency_key);

CREATE UNIQUE INDEX IF NOT EXISTS uq_cron_job_runs_dedupe_key
    ON public.cron_job_runs (dedupe_key)
    WHERE dedupe_key <> '';
```

> 迁移文件风格与 `migrations/0035_agent_feedback_events.sql` 对齐：使用 `CREATE ... IF NOT EXISTS`、`public.<table>` 前缀、字段对齐和统一索引命名。编号实施时按 `ls migrations/` 的下一个可用编号命名；当前口径为 `0045_cron_jobs.sql`。
>
> Cron v1 **core-only**：只改根 `sqlc.yaml`，生成命令固定为仓库根的 `sqlc generate`，产物进入 `internal/store/sqlc/*`；**不改** `internal/sidecar/orch/sqlc.yaml`。未来若 `mcp-orch` 直接消费 cron 查询，再单独补那套生成面。
>
> `cwd` 为必填字段；service 层缺值直接 `ErrMissingCWD`。当 `provider=codex` 时，identity config keys 也必须齐全并在 service 层 fail fast。
>
> `ClaimDueJobs()` 的 SQL 必须沿用仓内现有 claim 先例的形状：对 due rows 先 `SELECT ... FOR UPDATE SKIP LOCKED`（或等价 `UPDATE ... FROM (...) RETURNING` + `SKIP LOCKED`），由 DB 层完成并发 fencing；禁止无锁扫描后回到应用层做“谁抢到算谁”。

## Host RPC Params 建议

```json
{
  "method": "cronjob/create",
  "params": {
    "name": "daily-report",
    "prompt": "check logs",
    "schedule_expr": "0 9 * * *",
    "timezone": "Asia/Seoul",
    "provider": "codex",
    "model": "gpt-5.4",
    "cwd": "/repo",
    "config": {
      "codexHome": "/Users/demo/.codex-providers/glm",
      "codexInstanceKey": "glm",
      "codexModelProvider": "glm-compat"
    },
    "skills": ["log-inspector"],
    "notify_channel": "slack.default",
    "enabled": true
  }
}
```

> `provider` 当前仅接受 `"codex"`（见后文白名单）；`"claude"` 在 v1 直接返 `ErrProviderNotSupported`。
> 当 `provider="codex"` 时，`config.codexHome / codexInstanceKey / codexModelProvider` 三字段必填，任一缺失返 `ErrCodexHomeRequired` 等 sentinel error。
> `notify_channel` 不走 `NOTIFY_DEFAULT_CHANNEL` 兜底；未配置时直接丢通知（见 P2 alias 规则）。

## 并发安全与错误处理

- Store API 必须包含 `RenewLease` / `ExtendClaim`，并基于原 `claim_token` 做条件更新。
- `MarkFinished` / `ReleaseClaim` 也必须基于原 `claim_token` 或等价 fence 做条件更新，避免旧 worker 在被抢占后继续覆盖终态。
- **lease TTL = 30 分钟，heartbeat = 5 分钟续一次**，即 `cronLeaseActor` 每 5 分钟对所有已 claim 的 run 一次性 `RenewLease`，将 `lease_expires_at` 延后 30 分钟。事实上的结果是：任何长过 30 min 未续租的 claim 都会自动交出所有权。
- **活 turn 自动续租**：`cronLeaseActor` 必须同时订阅 `turndto.TurnProgress` / `ItemCompleted` 这类 progress 类事件：收到 progress 时对应 `active_turn_id` 所属 run 立刻 `RenewLease` 一次。这样长过 30 min 的 turn 只要还在真实产出，就不会因为默认 TTL 过期丢 claim；不活的 turn 按老规矩被抢占。
- **超 30 min 的 turn 执行**：需在任务提交时显式调用 `ExtendClaim(dur)` 将 TTL 参数化抬升；`cronLeaseActor` 仅负责默认续租，不负责替超长 turn 自动决定更大的 TTL。
- **`claim_token` 生成应用层**：由 `internal/module/cron/scheduler.go` 用 UUID v4 生成后以 SQL 参数传入，不依赖数据库 `gen_random_uuid()` / `pgcrypto` 扩展；migration 中仍 **禁止** `CREATE EXTENSION pgcrypto` 以保证 core 部署免扩展。
- **并发 claim 的 SQL fencing**：`ClaimDueJobs()` 必须把“挑出 due job + 写入 `claimed_by/claimed_at/lease_expires_at/claim_token`”包在同一 SQL 事务内，并使用 `FOR UPDATE SKIP LOCKED` 防止双节点重复 claim。
- **retry 预算**：`max_attempts=0` 表示“不做额外 retry，直接等下一次 schedule”（**语义**上等同 `retry_budget=0`；字段名保留是为了对齐现有 store 命名约定，service 层 DTO 建议以 `retry_budget` 别名暴露以免歧义）；`max_attempts>0` 时才启用 retry。回退策略固定为指数退避 + full jitter（base=30s，cap=15m）；当 `failure_count >= max_attempts` 时，本次 run 记为终态 `failed` 并释放 claim，不自动 disable job。
- **retry 必须被下一次 schedule 上界截断**：计算出的 `next_retry_at` 若 `>= next_run_at`，直接放弃本次 retry，状态推进到 `failed` 并等下次 schedule。避免 daily job 失败 30 s 后重试污染下一天 context。service 层把这个上界钉死在 `ClaimDueJobs` 的 `COALESCE(next_retry_at, next_run_at)` 选择面上。

### Crash-window idempotency state machine

- `cron_job_runs.status` 固定为：`pending → submitting → submitted → running → finished / failed / observe_lost`。`observe_lost` 是**第三类终态**：用于 `submitted/running` 恢复时 `turn.Service.Observe(turn_id)` 返回 `not_found` / `permission_denied` 的情况——此时既不能重入 `StartTurn`，也无法确认 turn 真实结局，记为 `observe_lost` + 告警，不自动重试。`failure_count` 不为此分支累加。
- 每次触发按 **三步原子协议** 推进，避免 provider 已接单但 `turn_id` 未落库的窗口：
  1. `pending → submitting`：先写 `cron_job_runs` 记录 + `idempotency_key` + `dedupe_key = sha256(job_id||scheduled_at||idempotency_key)`；`dedupe_key` 必须持久化到 run row，作为 `StartTurn` 的 provider-side dedupe input 与 crash recovery 查询键。
  2. `submitting` 态下调 `turn.Service.StartTurn` **并传入 `dedupe_key`**；provider 一旦返回 `turn_id`，先回填 `turn_id/submitted_at`，再 **CAS** `submitting → submitted`。若崩在“已拿到 turn_id 但状态还没推进”之间，允许出现 `submitting + turn_id!=空` 的半提交窗口。
  3. `submitted` 态立即注册 `turn.Service.Observe(turn_id)`；Observe 挂上后由 scheduler **CAS** `submitted → running`，终态再由 observe / turn terminal 统一推进 `running → finished/failed`。
- **崩溃恢复规则**：抢占者按当前状态分支恢复，绝对禁重新 `StartTurn` 同一 run：
  - `submitting` 且 `turn_id` **为空**：未知 provider 是否已接单 → 调 `turn.Service.LookupByDedupeKey(dedupe_key)`，命中则回填 `turn_id` 并推进到 `submitted`；未命中视为 `failed` 并计入 `failure_count`。
  - `submitting` 且 `turn_id` **非空**：说明 provider 已接单、DB 还没完成状态推进 → 直接按 `turn_id` 调 `turn.Service.Observe(turn_id)`，随后把状态补推进到 `submitted/running`；**禁止**再次调 `StartTurn`。
  - `submitted` 或 `running` 且 `turn_id` **非空**：都只允许走 `turn.Service.Observe(turn_id)` 恢复观察链，不得重交 provider。
  - `Observe` 返回 `ErrTurnNotFound` / `ErrTurnPermissionDenied` / 明确不可恢复错误：把 run 推进到 `observe_lost` 终态并释放 claim，记告警指标 `cron_observe_lost_total`；**禁止**回退到 `StartTurn` 或计入 `failure_count` 触发 retry。
  - `finished/failed`：终态，只做幂等核对不重跑。
- `UNIQUE(job_id, idempotency_key)` 挡 DB 层重复入库；dedupe_key 传给 provider 挡跨进程 `StartTurn` 重交（需 provider driver 支持幂等推进，codex driver v1 有幂等语义 → claude driver 未对齐→ v1 Cron 白名单仅 `provider=codex`）。
- `provider` 字段语义冻结：只能为 `codex|claude`（与当前 `internal/provider/codexapp` / `internal/provider/claudecli` concrete module 边界以及 `agent_provider_binding.provider` immutable 语义一致）；**禁止** `codex:qwen` / `codex-glm` 这种混合值；实例选择只走 `config.codexHome / codexInstanceKey / codexModelProvider` 等 identity keys。

## 执行策略建议

- v1 推荐一 job 绑定一条可复用后台 thread：稳定绑定字段是 `thread_id/agent_id`；运行态另用 `active_turn_id`、`last_turn_id` 追踪本次执行。
- 调度 runner 只负责 claim due job、构造 `PrepareTurn` / `StartTurn` 请求并入队观察；真正的长跑与 lease / observe 恢复都通过 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） 托管，不放进 `fx` 生命周期。
- `fx.Invoke(RegisterSubscribers)` 负责把 turn terminal / completion reconciliation 订阅器注入 `bus.subscribers`；回调只做状态推进、入队与幂等更新，不做同步慢查询。
- `approvalPolicy=never` 只适合作为后台 job 不进入人工审批的一部分约束；它不是统一 auto-approve 语义，当前真实自动批准范围仅 `Kind=request_user_input`。v1 必须白名单 provider / sandbox / tool 组合。

## 必测项

- fake clock / injectable ticker：验证 lease 续约（5 min heartbeat 延 30 min lease）、backoff（base=30s、cap=15m、full jitter）、`max_attempts=0`=不重试、timezone、重启恢复。
- DB fencing：验证并发 claim、旧 worker 丢租后写终态、已提交 run 被抢占者重复提交。
- **submitting-window crash 恢复**：至少覆盖 `submitting + turn_id=空`、`submitting + turn_id!=空`、`submitted/running + turn_id!=空` 三分支；assert 都不会重新调 `StartTurn`，而是只走 `LookupByDedupeKey` / `Observe` 路径。
- `claim_token` 应用层生成：验证 migration 无 `CREATE EXTENSION pgcrypto`，运行时 `claim_token` 均在 Go 侧用 UUID 生成。
- non-interactive policy：验证不会把 `approvalPolicy=never` 误解为全部自动通过，且仅 `request_user_input` 命中现有 auto-approve 特例。
- wiring：验证 `bus.subscribers` 与 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`） 都被正确装配（包含 `cronTickActor` 和 `cronLeaseActor` 两个独立 actor），且 shutdown 只依赖 `ctx cancel`。
- provider 白名单：验证 v1 `cronjob/create` 仅接受 `provider=codex`；claude 路径未对齐 dedupe_key 语义应拒收并返 `internal/module/cron/contract.go` 定义的 `ErrProviderNotSupported`。
- `observe_lost` 终态：构造 `submitted/running + turn_id` 但 `Observe` 返回 `ErrTurnNotFound` 的 fixture，断言 run 被推进到 `observe_lost`、claim 被释放、告警指标 `cron_observe_lost_total` 递增且**不**调 `StartTurn`。
- 活 turn 自动续租：注入伪 `TurnProgress`，断言对应 run 的 `lease_expires_at` 被刷新；不发 progress 的 run 按默认 5 min heartbeat 续租。
- retry 上界：构造 `next_retry_at >= next_run_at` 的 fixture，断言 retry 被放弃、状态推 `failed`、等下次 schedule。
