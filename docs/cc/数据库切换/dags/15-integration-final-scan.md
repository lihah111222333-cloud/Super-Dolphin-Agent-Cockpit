# Task 15: 集成扫描与最终验证

## Agent Prompt

你负责把所有 SQLite 切换分支集成到 `codex/sqlite-switch-integration` 后做最终扫描。不要新增功能；只修复集成冲突、生成物漂移、遗漏的 PG runtime 依赖、测试报告缺口。任何 P0 gate 失败都必须阻断合并。

## Scope

依赖：Task 14。

这是最终集成任务。

## 修改点

- Review:
  - `git diff --stat`
  - all modified `go.mod`, `go.sum`, `sqlc.yaml`, generated sqlc files.
  - `internal/archtest/baseline.json` if changed.
  - `docs/doc/codemap/02-mcp-orch.md`, `docs/doc/codemap/08-platform.md`, `docs/doc/codemap/10-store.md`, and codemap index if changed by generated docs.
- Fix only:
  - merge conflicts.
  - sqlc generated drift.
  - PG dependency leftovers in product runtime.
  - missing report links or stale docs.

## 必跑扫描

```bash
rg -n "pgx|pgxpool|pgconn|pgtype|embeddedpg|DATABASE_URL|POSTGRES_CONNECTION_STRING|FOR UPDATE SKIP LOCKED|pg_advisory|pg_try_advisory|jsonb|::jsonb|ILIKE|NOW\\(" internal cmd sql migrations README.md run-new-ui-desktop.sh run-new-ui-desktop-hot.sh
```

Packaging/runtime scan:

```bash
rg -n "embedded_postgres_resource_path|SUPER_DOLPHIN_POSTGRES_|postgres runtime|packaged postgres|bundled PostgreSQL|pg_ctl|initdb" README.md run-new-ui-desktop.sh run-new-ui-desktop-hot.sh .env.packaging.example scripts internal cmd
```

SQLite danger scan:

```bash
rg -n "ATTACH|DETACH|VACUUM|REINDEX|PRAGMA|writable_schema|load_extension" internal cmd sql migrations README.md scripts
```

分类规则：

- Allowed:
  - historical docs under `docs/**` that explicitly describe old PG behavior.
  - credential redaction patterns that include `DATABASE_URL`.
  - tests asserting PG env is ignored.
  - archived PG migration files only if they are no longer product SQLite schema inputs.
- Blocked:
  - product runtime parses PG env as DB source.
  - startup or packaging launches embedded PG.
  - sqlc query files for SQLite include PG-only syntax.
  - generated store code imports pgx.
  - `embedded_postgres_resource_path`, `SUPER_DOLPHIN_POSTGRES_*`, packaged postgres copy/verify, or PostgreSQL smoke checks remain in product packaging paths.
  - SQLite danger statements appear in product query paths, dbquery execution, generated SQL, or provider/tool-facing code.

SQLite danger scan classification:

- Allowed only for platform SQLite open PRAGMAs, documented backup checkpoint flow, and tests asserting dbquery rejection.
- Blocked in product query paths, dbquery execution, generated SQL, and provider/tool-facing code.

## 验收方案

```bash
make sqlc-verify
make guard
./scripts/test_with_guard.sh ./internal/platform/db ./internal/store/... ./internal/module/... -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1
make build-plain
make codemap-check
```

如果 frontend packaging or docs changed:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

## 最终报告要求

- 列出所有 P0 gate 状态。
- 列出 PG residual scan 的每类命中和处理结论。
- 列出 Windows/macOS/Linux packaging smoke 的 artifact 链接和通过状态；任一平台未运行或失败即阻断合并/RC。
- 确认旧 PG 数据不迁移、不读取、不阻断 SQLite 启动。
- 确认 `schema_migrations` 低版本 fixture fail-fast。
- 确认 `DATABASE_URL` / `POSTGRES_CONNECTION_STRING` 仍在 credential redaction 中，但不再作为 DB config source。
- 确认 `SUPER_DOLPHIN_SQLITE_PATH` 与 resolved SQLite path 不泄漏到 provider/tool env 或普通日志。

## 不允许改

- 不要用 `git add .`。
- 不要更新 `internal/archtest/baseline.json` 但不解释 diff。
- 不要在测试失败时写“ready to merge”。
