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
- **`RunnerModule` 层**：长跑 tick loop 实现 `Runner.Run(ctx)` 并进入 `runner.actors`；lease 续租作为 runner 内部 goroutine，随 `ctx.Done()` 退出。
- **shutdown 流**：`ctx cancel → run.Group 全退 → bus 停派发 subscribers → fx 释放资源`；Cron v1 不在 `fx` 生命周期里手写 cancel worker。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| DDL + 查询 | `migrations/0044_cron_jobs.sql`、`sql/queries/cron_job.sql` [NEW] | 定义 `public.cron_jobs` / `public.cron_job_runs` 及 claim / renew / finish / list 查询 |
| 存储库 | `internal/store/cron/{contract.go,module.go,store.go}` [NEW] | 提供 CRUD、`ClaimDueJobs()`、`RenewLease()`、`ReleaseClaim()`、`MarkFinished()` |
| Run 存储 | `internal/store/cron/run_store.go` 或同包扩展 [NEW] | 持久化单次触发 run，记录 idempotency key、submitted turn、终态 |
| 业务服务 | `internal/module/cron/service.go` [NEW] | 负责任务创建 / 更新 / 删除 / 列举与参数校验；空 `cwd` 直接 `ErrMissingCWD` |
| 调度器对象 | `internal/module/cron/scheduler.go` [NEW] | 封装 claim / submit / renew / reconcile 逻辑，但不自己长跑 |
| Runner / Subscriber 接线 | `internal/module/cron/module.go` [NEW] | `fx.Invoke` 注入 `bus.subscribers`；tick loop / flush worker 进入 `runner.actors` |
| Host RPC 接口 | `internal/module/cron/rpc.go` [NEW] | 暴露宿主侧 slash 风格方法，如 `cronjob/create`、`cronjob/list`、`cronjob/delete` |
| 模块接线 | `internal/app/modules.go`、`internal/store/module.go`、根 `sqlc.yaml` | 注册 cron 模块与 cron store，并把 schema / queries 接进 **root** sqlc 生成面 |

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
```

> 迁移文件风格与 `migrations/0035_agent_feedback_events.sql` 对齐：使用 `CREATE ... IF NOT EXISTS`、`public.<table>` 前缀、字段对齐和统一索引命名。
>
> Cron v1 **core-only**：只改根 `sqlc.yaml`，生成命令固定为仓库根的 `sqlc generate`，产物进入 `internal/store/sqlc/*`；**不改** `cmd/mcp-orch/sqlc.yaml`。未来若 `mcp-orch` 直接消费 cron 查询，再单独补那套生成面。
>
> `cwd` 为必填字段；service 层缺值直接 `ErrMissingCWD`。当 `provider=codex` 时，identity config keys 也必须齐全并在 service 层 fail fast。

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

## 并发安全与错误处理

- Store API 必须包含 `RenewLease` / `ExtendClaim`，并基于原 `claim_token` 做条件更新。
- `MarkFinished` / `ReleaseClaim` 也必须基于原 `claim_token` 或等价 fence 做条件更新，避免旧 worker 在被抢占后继续覆盖终态。
- lease 续租作为 runner 内部 goroutine 运行；一旦 `ctx.Done()`，续租也应随之退出。

### Crash-window idempotency state machine

- `cron_job_runs.status` 固定为：`pending → submitted → running → finished / failed`。
- 每次触发都先创建一条 `cron_job_runs` 记录与 `idempotency_key`；`StartTurn` 返回后必须 **CAS** 更新为 `status=submitted`，并同时写入 `turn_id` / `submitted_at`。
- 若 worker 在 `StartTurn` 后崩溃，抢占者读到 `submitted` 且 run 还未 `finished` 时，必须改走 `turn.Service` / turn 查询路径按 `turn_id` 获取最新状态，而不是重新 `StartTurn`。
- `UNIQUE(job_id, idempotency_key)` 只能挡住 DB 层重复入库，**挡不住** provider 已经接单但 worker 尚未来得及落库的窗口，因此 crash recovery 仍要按 `turn_id` 续查。

## 执行策略建议

- v1 推荐一 job 绑定一条可复用后台 thread：稳定绑定字段是 `thread_id/agent_id`；运行态另用 `active_turn_id`、`last_turn_id` 追踪本次执行。
- 调度 runner 只负责 claim due job、构造 `PrepareTurn` / `StartTurn` 请求并入队观察；真正的长跑与 flush 通过 `runner.actors` 托管，不放进 `fx` 生命周期。
- `fx.Invoke(RegisterSubscribers)` 负责把 turn terminal / completion reconciliation 订阅器注入 `bus.subscribers`；回调只做状态推进、入队与幂等更新，不做同步慢查询。
- `approvalPolicy=never` 只适合作为后台 job 不进入人工审批的一部分约束；它不是统一 auto-approve 语义。v1 必须白名单 provider / sandbox / tool 组合。

## 必测项

- fake clock / injectable ticker：验证 lease 续约、backoff、timezone、重启恢复。
- DB fencing：验证并发 claim、旧 worker 丢租后写终态、已提交 run 被抢占者重复提交。
- submitted-turn crash window：验证 worker 在 `StartTurn` 后崩溃时的恢复 / 等待语义。
- non-interactive policy：验证不会把 `approvalPolicy=never` 误解为全部自动通过。
- wiring：验证 `bus.subscribers` 与 `runner.actors` 都被正确装配，且 shutdown 只依赖 `ctx cancel`。
