# Task 01: SQLite 配置与平台运行时

## Agent Prompt

你在 Super-Dolphin 仓库中执行 PostgreSQL 到 SQLite 切换的基础任务。只处理配置解析、SQLite 打开/PRAGMA、migration runner 生命周期入口，不迁移业务 store。必须阅读 `docs/cc/数据库切换/postgres-to-sqlite-switch-review-2026-06-11.md` 的第 4、7、8、10、12 节。实现时保持 fail-fast：PRAGMA 不满足、SQLite path 为空、schema gate 不达标时直接返回错误，不允许静默降级到 PG 或内存库。

## Scope

依赖：无。

可并行：可与 `02-sqlite-schema-baseline.md` 并行；必须在 `03-sqlc-db-boundary.md` 前完成。

## 修改点

- Modify: `go.mod`, `go.sum`
  - 默认使用 `modernc.org/sqlite`，保持 CGo-free 桌面打包路径。
  - 不得切到 `mattn/go-sqlite3`，除非另有单独打包决策文档说明 CGO/交叉编译成本。
- Modify: `internal/platform/config/config.go`
  - 新增 `SQLitePath string` 或等价字段。
  - 默认路径从 `SUPER_DOLPHIN_HOME` 派生，例如 `<home>/super-dolphin.db`；packaged runtime 必须用各平台可写用户数据目录：Windows `%APPDATA%` 或 packaged app data dir，macOS Application Support，Linux XDG data home fallback。
  - 支持 `SUPER_DOLPHIN_SQLITE_PATH` 显式覆盖。
  - 空显式 path、不可写父目录、指向目录的 DB path 必须 fail-fast，并在错误里只输出 redacted path。
  - 产品运行时忽略 `DATABASE_URL` 和 `POSTGRES_CONNECTION_STRING`，不再调用 `embeddedpg.ResolveFromEnvironment`。
  - 不再通过 `exportDatabaseURLIfMissing` 回写 `DATABASE_URL`。
- Modify: `internal/platform/config/config_test.go`
  - 覆盖默认 SQLite path、显式 path、PG env 残留不生效、子进程 env 不含 DB `DATABASE_URL` 回写。
  - 覆盖 Windows/macOS/Linux packaged path resolver，验证不会写入应用安装目录。
- Modify/Create: `internal/platform/db/module.go`
  - 把 `NewPool` 替换为 SQLite open 函数，例如 `NewDB(cfg *config.Config) (*sql.DB, error)`。
  - 打开后设置并验证：
    - `PRAGMA foreign_keys = ON`
    - `PRAGMA journal_mode = WAL`
    - `PRAGMA busy_timeout = 5000`
    - `PRAGMA synchronous = FULL`
    - `PRAGMA wal_autocheckpoint` 显式设置并验证；如采用 SQLite 默认值，必须在代码注释和测试名里说明原因。
  - 设置 `SetMaxOpenConns(1)`。
  - 创建父目录时使用 user-only 权限：POSIX `0700`；DB、WAL、SHM 文件使用 POSIX `0600`。Windows 下在支持时设置 current-user-only ACL。已有目录或 DB 文件 group/world-writable 时 fail-fast。
  - 生命周期 OnStart 执行 SQLite migration runner 和 `VerifyMinSchemaVersion`。
- Modify/Create: `internal/platform/db/checkpoint.go`
  - 新增 `Checkpoint(ctx, mode)` 或等价 helper，支持 `PASSIVE` 与 `TRUNCATE`，供备份、维护和 WAL 回收测试使用。
- Create: `internal/platform/db/sqlite/`
  - 放 SQLite open、PRAGMA 验证、migration runner 的实现文件和测试 fixture。
- Modify: `internal/platform/db/module_config_test.go`
  - 删除空 `DATABASE_URL` 失败预期，替换为空 SQLite path 或无效目录 fail-fast。
- Modify: `internal/platform/db/schema_version_test.go`
  - 保留 `MinRequiredSchemaVersion = 103` gate 测试，改为 SQLite query row 也能复用。

## 不允许改

- 不要修改业务 store SQL。
- 不要删除 `internal/platform/embeddedpg/**`，PG runtime 删除由 Task 13 负责。
- 不要把 SQLite path 默认到临时目录或内存库。

## 验收方案

1. 每个 Go 文件修改后运行：

```bash
./scripts/test_with_guard.sh internal/platform/config/config.go
./scripts/test_with_guard.sh internal/platform/db/module.go
```

2. focused tests：

```bash
./scripts/test_with_guard.sh ./internal/platform/config ./internal/platform/db -count=1
```

必须覆盖：

- default path 与 `SUPER_DOLPHIN_SQLITE_PATH` 的权限、可写性、redacted error。
- clean startup 后 DB/WAL/SHM 文件权限。
- packaged path resolver 在 Windows/macOS/Linux 的期望目录。
- `wal_autocheckpoint` 与 `Checkpoint(ctx, "TRUNCATE")` 行为。

3. 静态检查：

```bash
rg -n "ResolveFromEnvironment|exportDatabaseURLIfMissing|DATABASE_URL|POSTGRES_CONNECTION_STRING" internal/platform/config internal/platform/db
```

预期：`internal/platform/config` 不再把 PG env 当作 DB 配置源；命中只能是废弃说明或测试断言“忽略 PG env”。
