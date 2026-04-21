# P1b: Cron 定时任务

## 目标
为 Agent 赋予时间维度的主动性。允许 Agent 或用户安排周期性任务（例如每日 9:00 巡检日志，每小时爬取数据等）。

## 现状校准

- 当前仓库的持久化基线是 Postgres + `migrations/` + `sql/queries/` + `internal/store/*`，不是 SQLite。
- 面向 agent 的能力入口统一通过 `internal/module/*/rpc.go` 输出 `rpc.HandlerMapResult`，不是 `internal/mcpserver/common/*`。
- 第一版 Cron 执行建议复用现有 `thread.Service.Start` 与 `turn.Service`，不要直接在 platform 层偷偷拉 raw provider session。
- 当前 approval 流程依赖 live RPC/UI callback；后台定时任务不能假定存在可交互前端，因此 v1 必须明确走 non-interactive 执行策略。

## 推荐架构

- **任务存储层**：Postgres 持久化任务定义、执行状态、下一次触发时间。
- **调度循环**：`fx.Lifecycle` 启动的后台 Goroutine 每分钟 Tick，claim due job 后触发执行。
- **执行路径**：先复用 `thread`/`turn` 现有闭环创建后台 thread，再由 `turn` 发起 prompt；后续如需 truly headless fast-path，再评估 `DreamExecutor`。
- **并发保护**：利用 Postgres `FOR UPDATE SKIP LOCKED` + `claimed_at/claimed_by` 做 claim，而不是单机内存锁。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| DDL + 查询 | `migrations/00X_cron_jobs.sql`、`sql/queries/cron_job.sql` [NEW] | 定义 `cron_jobs` 表与 claim/update/list 查询 |
| 存储库 | `internal/store/cron/{contract.go,module.go,store.go}` [NEW] | 提供 CRUD、`ClaimDueJobs()`、`ReleaseClaim()`、`MarkFinished()` |
| 业务服务 | `internal/module/cron/service.go` [NEW] | 负责任务创建/更新/删除/列举与参数校验 |
| 调度器 | `internal/module/cron/scheduler.go` [NEW] | Ticker 循环 + claim + 执行 + 回写状态 |
| RPC 接口 | `internal/module/cron/rpc.go` [NEW] | 暴露 slash 风格方法，例如 `cronjob/create`、`cronjob/list`、`cronjob/delete` |
| 生命周期接线 | `internal/app/modules.go`、`internal/store/module.go`、`sqlc.yaml` | 注册 cron 模块与 cron store，并把新 schema/query 接进 sqlc 生成面 |

## DDL 设计

```sql
CREATE TABLE IF NOT EXISTS cron_jobs (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    prompt           TEXT NOT NULL,
    schedule_type    TEXT NOT NULL DEFAULT 'cron',
    schedule_expr    TEXT NOT NULL,
    timezone         TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT 'codex',
    model            TEXT NOT NULL DEFAULT '',
    cwd              TEXT NOT NULL DEFAULT '',
    config           JSONB NOT NULL DEFAULT '{}'::jsonb,
    skills           JSONB NOT NULL DEFAULT '[]'::jsonb,
    notify_channel   TEXT NOT NULL DEFAULT '',
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    next_run_at      TIMESTAMPTZ NOT NULL,
    last_scheduled_at TIMESTAMPTZ,
    last_run_at      TIMESTAMPTZ,
    claimed_at       TIMESTAMPTZ,
    claimed_by       TEXT NOT NULL DEFAULT '',
    thread_id        TEXT NOT NULL DEFAULT '',
    agent_id         TEXT NOT NULL DEFAULT '',
    active_turn_id   TEXT NOT NULL DEFAULT '',
    last_turn_id     TEXT NOT NULL DEFAULT '',
    failure_count    INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 0,
    next_retry_at    TIMESTAMPTZ,
    last_status      TEXT NOT NULL DEFAULT '',
    last_error_at    TIMESTAMPTZ,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cron_jobs_due
    ON cron_jobs(next_run_at)
    WHERE enabled = TRUE;
```

> 迁移文件放在 `migrations/` 目录，命名如 `00X_cron_jobs.sql`。新增 migration 后还要同步更新根 `sqlc.yaml` 的 `schema:` 列表，并重新运行 `sqlc generate`；本仓不是自动扫描整个 `migrations/` 目录。

## RPC Schema 建议

```json
{
  "name": "cronjob/create",
  "description": "创建定时任务",
  "inputSchema": {
    "type": "object",
    "properties": {
      "name":     { "type": "string", "description": "任务名称" },
      "prompt":   { "type": "string", "description": "执行的 prompt 内容" },
      "schedule_expr": { "type": "string", "description": "Cron 表达式" },
      "timezone": { "type": "string", "description": "调度时区" },
      "provider": { "type": "string", "description": "provider，默认 codex" },
      "model":    { "type": "string", "description": "模型名" },
      "cwd":      { "type": "string", "description": "执行目录" },
      "skills":   { "type": "array", "items": { "type": "string" }, "description": "绑定的 skill 名列表" },
      "notify_channel": { "type": "string", "description": "通知通道别名，交由 P2 解析" },
      "enabled":  { "type": "boolean", "description": "是否启用" }
    },
    "required": ["name", "prompt", "schedule_expr"]
  }
}
```

## 并发安全与错误处理

### Claim 伪码
```go
// ClaimDueJobs 使用 Postgres 行级锁 claim due jobs。
func (s *Store) ClaimDueJobs(ctx context.Context, workerID string, limit int) ([]Job, error) {
    // WITH due AS (
    //   SELECT id
    //   FROM cron_jobs
    //   WHERE enabled
    //     AND COALESCE(next_retry_at, next_run_at) <= NOW()
    //     AND (claimed_at IS NULL OR claimed_at < NOW() - INTERVAL '30 minutes')
    //   ORDER BY next_run_at
    //   FOR UPDATE SKIP LOCKED
    //   LIMIT $1
    // )
    // UPDATE cron_jobs
    // SET claimed_at = NOW(), claimed_by = $2
    // WHERE id IN (SELECT id FROM due)
    // RETURNING *;
}
```

## 执行策略建议

- v1 更推荐“一 job 绑定一条可复用后台 thread”：首次 `thread.Start` 创建稳定的 `thread_id/agent_id`，后续优先 `thread.Resume` 或通过 `contract.SessionResolver` 恢复同一 thread 对应 session。
- 稳定绑定字段应是 `thread_id/agent_id`；运行态另用 `active_turn_id`、`last_turn_id` 等字段，避免把“线程绑定”与“一次执行中的活动 turn”混在一起。
- 然后通过 `turn.Service.PrepareTurn` + `StartTurn` 发起 prompt，确保技能注入、prompt assembly、provider 路由都沿用现有闭环；`StartTurn` 之后必须等待 `handle.Done()` / `handle.Err()` 再更新 job 最终状态，不能把“提交 turn 成功”误记为“job 成功”。
- direct service path 不会自动触发 pending-launch 的 `SpawnIfNeeded`。Cron v1 要么避免 `DeferSpawn`，要么在首轮前显式 spawn，不能假设 `turn.Service` 会帮忙懒启动。
- v1 建议显式固定 `approvalPolicy=never`，并仅选择已知支持非交互运行的 provider/model；不要把后台 job 设计成等待人工审批。
- 即便 `approvalPolicy=never`，后台任务也不等于“所有审批自动通过”。当前无前端时整体仍偏 fail-closed，应只允许已知可无交互运行的 provider/sandbox/tool 组合。
- 若后续需要无 thread 的超轻执行模式，再把 `DreamExecutor` 作为可选优化路径，而不是 P1b 首版前置依赖。

## 安全与集成约束

- `notify_channel` 只存逻辑别名，例如 `slack.default`、`dingtalk.ops`；Webhook URL / secret 应由 P2 从运行时配置解析，不应直接写入 `cron_jobs`。
- 新增 store 时，除 `migrations/` 和 `sql/queries/` 外，还要补 `internal/store/module.go`、根 `sqlc.yaml` 与对应生成文件；本仓不会在运行时自动生成 store 代码。
- 若 job 更新后变更了 `provider`、`cwd`、关键 `config` 或其他会影响底层 session 身份的字段，不应继续复用旧 `thread_id`；应显式停旧 thread 并重建。当前 `agent_provider_binding` 对 `provider/provider_thread_id` 有 immutable 约束，不能把“改配置”偷做成原 thread 原地改绑。
- 当前 `thread.List`/dashboard 不会自动隐藏后台 thread。若不希望 Cron 线程直接混入 UI，还需要另行设计 `agent_type=cron`、隐藏字段或 dashboard/thread list 过滤策略。
- P2 未落地前，`notify_channel` 只能做存储与展示，不能承诺真正发 HTTP。

### 错误处理策略
| 场景 | 处理方式 |
|---|---|
| Job 执行成功 | 清空 claim 字段，更新 `last_run_at` / `next_run_at` / `last_status` / `last_turn_id`，必要时经 P2 发通知 |
| Job 执行失败 | 清空 claim 字段，记录 `last_error` / `last_error_at` / `failure_count`，并推进 `next_retry_at` 或禁用任务，避免下一次 tick 立即重试 |
| Job 超时 (>30min) | 下次 claim 时把过期 `claimed_at` 当作 stale lease 抢占 |
| Scheduler 崩溃重启 | 依赖 DB claim/lease 自动恢复，不需要启动时暴力清表 |

**Hermes 源码对照点**:
- `tools/cronjob_tools.py:221-384` — 暴露给 Agent 的 `cronjob` 工具
- `tools/cronjob_tools.py:388-465` — `CRONJOB_SCHEMA`
- `cron/jobs.py` — 底层 Job 解析与存储逻辑
