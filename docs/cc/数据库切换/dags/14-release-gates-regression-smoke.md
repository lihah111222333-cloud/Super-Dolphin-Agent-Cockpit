# Task 14: 发布 Gate、回归、性能冒烟与备份恢复

## Agent Prompt

你负责实现 SQLite 切换的发布 gate 测试与文档。不要改核心 store 逻辑，除非测试揭示缺口并且修复范围极小。目标是把 G1-G14 变成可运行验证，结果随 PR 落盘。

## Scope

依赖：Task 13。

解锁：Task 15。

## 修改点

- Create:
  - `docs/cc/数据库切换/sqlite-backup-restore.md`
  - `docs/cc/数据库切换/sqlite-release-gate-report.md`
  - `internal/platform/db/sqlite/smoke_test.go`
  - `internal/platform/db/sqlite/backup_restore_smoke_test.go`
  - `internal/platform/db/sqlite/query_plan_test.go` or equivalent `sqlite_query_plan_test`
  - `internal/platform/db/sqlite/mixed_write_pressure_test.go` or equivalent process-level harness
  - `internal/store/sqlite_regression_test.go` or per-store fixture tests.
  - `cmd/mcp-orch/sqlite_smoke_test.go`
  - `.github/workflows/sqlite-release-gates.yml` or required jobs in `.github/workflows/ci.yml`
- Add fixture builders:
  - medium fixture: 1,000 threads, 10,000 system logs, 1,000 prompts/versions, 500 cron jobs/runs, 500 DAG runs, 10,000 wakeups/events.
  - large fixture: 100,000 system logs, 50,000 session insights, 20,000 cron runs, 20,000 DAG wakeups/events.
  - medium fixture runs in normal tests; large fixture is behind explicit `sqlite_stress` tag or explicit `-run` command, and the report records command output.
  - fixture distribution must cover at least 100 `thread_id`, 20 `agent_id`, multiple statuses, 30 days of timestamps, and JSON payload sizes around 1KB, 16KB, and 64KB. observed approval/token, pending/dispatching/sent/failed wakeup, and running/terminal DAG run samples must be non-zero.
- Document:
  - WAL backup must include `.db/.wal/.shm` or run checkpoint before copying.
  - Backup while the app may be running must use SQLite Online Backup API, or quiesce writes/hold a backup lock, run `wal_checkpoint(TRUNCATE)`, then copy a consistent snapshot.
  - restore smoke reads/writes thread, prompt, cron, DAG.
  - restore smoke must run `PRAGMA integrity_check`, `PRAGMA foreign_key_check`, and schema version gate before read/write smoke.
  - report format: commit SHA, OS/arch, gate id G1-G14, command, cwd, start/end time, exit code, raw log artifact path, result PASS/FAIL, and blocker owner for non-P0 follow-ups. P0 gates may not use SKIPPED.
  - SQLite release gate workflow must run G1-G14 commands, include an OS matrix for `ubuntu-latest`, `windows-latest`, and `macos-latest` for packaging smoke, upload raw command logs and `sqlite-release-gate-report.md` as artifacts, and fail on any P0 gate failure or missing report entry.

## Gate 覆盖

- G1 SQLite runtime startup: clean install creates SQLite and PRAGMAs pass.
- G2 no PG runtime dependency: no `DATABASE_URL` needed; PG env ignored.
- G3 schema baseline: all tables/constraints/indexes present; schema version gate enforced.
- G4/G5 store parity: main store and mcp-orch store focused regression tests pass.
- G6/G7/G8/G9/G10 concurrency and lock gates pass.
- G11 mixed write pressure gate:
  - 启动两个 OS 进程，共用同一 SQLite 文件。
  - 进程 A 模拟主应用写入/查询：`system_logs`、`agent_status`、thread/binding、prompt/session_insights、cron_jobs/cron_job_runs。
  - 进程 B 模拟 `mcp-orch` 写入/查询：DAG start、node transition、wakeup claim、worker lease、DAG run event append。
  - 持续 5 分钟或 CI 可配置等价时长；失败条件：不可恢复 `SQLITE_BUSY` / `SQLITE_LOCKED`、retry 耗尽无错误上下文、重复 claim/dispatch/run、event 丢失或覆盖。
  - 报告记录 retry 次数、最大等待、失败数、WAL 文件大小、checkpoint 前后大小。
- G12 packaging removes PG.
- G13 old PG data ignored and documented.
- G14 regression/performance/backup restore documented and smoke tested.

## 不允许改

- 不要 encode fake success into report before commands run.
- 不要 lower gate from P0 to P1.
- 不要 claim performance is acceptable without fixture command output.

## 验收方案

```bash
make sqlc-verify
make guard
./scripts/test_with_guard.sh ./internal/platform/db ./internal/store/... ./internal/module/... -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1
make build-plain
```

性能与查询计划 gate：

- 对 dashboard/thread/log/prompt/cron/DAG/wakeup 列表查询跑 `EXPLAIN QUERY PLAN`；大表 fixture 下出现无界 SCAN 大表且没有 indexed LIMIT path 时失败。
- 新增 dashboard N+1 regression test，在 DB executor/test hook 上统计每个 dashboard list RPC 的 SQL 次数。thread/log/prompt/cron/DAG/wakeup 列表查询的 SQL 次数不得随 page size 线性增长；需要 per-row detail 时必须批量查询。
- Dashboard/list 查询必须采用 metadata-only projection；`raw`、`extra`、`events`、`prompt_payload`、`result`、`config` 等大字段只能在 detail-by-id 查询读取。
- 大量写入 + 长读事务场景下记录 `.wal` 增长；长读关闭后执行 `wal_checkpoint(TRUNCATE)`，验证 checkpoint 可完成且 `.wal` 可回收。
- 在 `SetMaxOpenConns(1)` 下记录 dashboard list latency、write latency、retry count、max wait；数据证明瓶颈时登记 P2 follow-up，不在 gate 任务里引入 read pool/write queue。

OS packaging smoke:

- P0 OS packaging smoke must pass on all supported release platforms before RC: `windows-latest`, `macos-latest`, `ubuntu-latest`.
- Each platform must build or verify an unsigned local package, start with a clean `SUPER_DOLPHIN_HOME`, start with PG env still set, start with an old PG data dir present, create SQLite, verify PRAGMAs, and confirm no bundled PostgreSQL resource is required or launched.
- Skipping any platform smoke is a P0 failure, not an owner-tracked follow-up.

CI/report artifact:

- Save raw command outputs and summarized results in `docs/cc/数据库切换/sqlite-release-gate-report.md`.
- Upload raw logs and report as CI artifacts.
