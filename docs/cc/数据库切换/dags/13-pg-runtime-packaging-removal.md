# Task 13: PG Runtime、配置、打包与文档移除

## Agent Prompt

你负责在 SQLite store 和 mcp-orch 改造完成后移除产品运行时 PostgreSQL 依赖。目标是发布包、启动脚本、manifest、provider env passthrough、README 都不再要求 bundled PostgreSQL、`DATABASE_URL` 或 `POSTGRES_CONNECTION_STRING`。旧 PG 数据不会迁移，只写清楚忽略和可选清理说明。

## Scope

依赖：Task 04 到 Task 12。

解锁：Task 14。

## 修改点

- Modify:
  - `README.md`
  - `run-new-ui-desktop.sh`
  - `run-new-ui-desktop-hot.sh`
  - `.env.packaging.example`
  - `scripts/package_linux.sh`
  - `scripts/package_macos.sh`
  - `scripts/package_windows.ps1`
  - `scripts/package_*`
  - `scripts/verify_packaged_app_*`
  - `scripts/*release*`
  - `internal/app/new_ui_scripts_test.go`
  - `internal/app/new_ui_postgres_scripts_test.go` rename or replace with SQLite tests.
  - `internal/app/*postgres*test.go`
  - `internal/contract/manifest.go`
  - `internal/platform/runtimeenv/**`
  - `internal/provider/unified/manifest_test.go`
  - `cmd/super-dolphin-updater/install.go`
  - `cmd/super-dolphin-updater/install_test.go`
  - `cmd/super-dolphin-release-manifest/**`
- Create/Modify docs:
  - `docs/cc/数据库切换/sqlite-data-reset.md`
  - `docs/doc/codemap/02-mcp-orch.md`
  - `docs/doc/codemap/08-platform.md`
  - `docs/doc/codemap/10-store.md`
- Remove or archive from product runtime:
  - `internal/platform/embeddedpg/**` startup wiring and packaged smoke dependency.
  - bundled PostgreSQL manifest/resource path requirements.
- Add docs:
  - SQLite default path.
  - `SUPER_DOLPHIN_SQLITE_PATH`.
  - old PG local data is ignored and not migrated.
  - optional manual cleanup instructions.
  - SQLite data reset flow: stop app and sidecars, optionally back up first, then remove `.db`, `.db-wal`, `.db-shm` together or checkpoint then remove DB. Warn reset discards local development data and is not PG -> SQLite migration.
  - codemap updates must use the generator if the codemap is generated; run `make codemap-check`.

## 语义要求

- Product startup must not call `embeddedpg.Start`.
- Product startup must not parse PG DSN for DB configuration.
- Provider/sidecar env passthrough must not include `DATABASE_URL` as DB dependency.
- Keep `DATABASE_URL` and `POSTGRES_CONNECTION_STRING` in credential redaction patterns even after removing them as DB config sources.
- Add redaction tests proving config dumps/logs redact `DATABASE_URL`, `POSTGRES_CONNECTION_STRING`, `SUPER_DOLPHIN_SQLITE_PATH`, and resolved SQLite DB paths.
- Provider/tool environment must not receive DB secrets or resolved SQLite paths. Only trusted `cmd/mcp-orch` may receive the SQLite path through an explicit internal config channel.
- Tests must prove:
  - PG env remains set but SQLite path wins.
  - old PG data dir exists but startup does not touch it.
  - mcp-orch starts without `DATABASE_URL`.

## 不允许改

- 不要 delete historical docs that explain old PG work unless they are generated build artifacts.
- 不要 add migration utility.
- 不要 leave README prerequisites saying PostgreSQL is required.

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/contract ./internal/provider/unified ./cmd/super-dolphin-updater -count=1
make build-plain
```

静态扫描：

```bash
rg -n "embeddedpg\\.Start|pgxpool\\.New|DATABASE_URL|POSTGRES_CONNECTION_STRING|bundled PostgreSQL|PostgreSQL \\(for store layer|embedded_postgres_resource_path|SUPER_DOLPHIN_POSTGRES_|postgres runtime|packaged postgres" README.md run-new-ui-desktop.sh run-new-ui-desktop-hot.sh .env.packaging.example scripts internal cmd
```

预期：产品 runtime 路径无 PG 启动/配置依赖；命中只能是废弃说明、历史文档、测试 fixture、credential redaction 之类非 DB 配置入口。
