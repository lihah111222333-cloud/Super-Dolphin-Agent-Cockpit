# Task 01: SQLite open fail-fast 与诊断一致性

## Agent Prompt

你负责修复 MR !52 的 SQLite P1/P3 问题：已有 SQLite DB 文件只读时必须在 `NewDB()`/`sqlite.Open()` 打开期 fail-fast；当 `NewDB()` 直调路径的父级是普通文件时，错误必须定位到父路径而不是 leaf DB 文件。不要修改配置 env 优先级，不要修改 migration schema，不要自动 chmod 用户已有 DB 文件。

## Scope

依赖：无。

可并行：可与 `02-codex-identity-resume-request-canonicalization.md` 并行。

建议 task worktree：`.worktrees/mr52-bugfix-task-01-sqlite-open`

## 源码追溯

- `internal/platform/db/module.go` 的 `NewDB()` 直接调用 `sqlite.Open()`。
- `internal/platform/db/sqlite/open.go` 的 `prepareFilesystem()` 当前先检查 leaf，再检查 parent。
- `internal/platform/db/sqlite/open.go` 的 `ensureDatabaseFile()` 对已有 DB 直接返回。
- `internal/platform/securefs/owner_only_unix.go` 不拒绝 owner read-only 文件。
- `internal/platform/config/config.go` 的 `validateSQLiteParent()` 已覆盖启动期 env 父路径诊断，本任务只补底层直调一致性。

## 修改点

- Modify: `internal/platform/db/sqlite/open.go`
  - 在 `prepareFilesystem()` 中先计算并检查 parent，再检查 leaf DB 文件。
  - 对已有非目录 DB 文件新增写权限探测 helper，例如：

```go
func probeExistingDatabaseWritable(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("SQLite database file is not writable: %s: %s", redactPath(path), securefs.SafeErrorForPath(err, path))
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close SQLite database file %s: %s", redactPath(path), securefs.SafeErrorForPath(err, path))
	}
	return nil
}
```

  - `validateExistingDatabasePath(clean)` 对已有文件先保留 owner-only 检查，再调用写探测。
  - 缺失 parent 仍由 `ensureParentDirectory(parent)` 创建，并保持 `0700`/owner-only 逻辑。

- Modify: `internal/platform/db/module_config_test.go`
  - 新增 `TestNewDBRejectsParentFileWithParentRedaction`。
  - 加强 `TestNewDBRejectsReadOnlyDatabaseFileWithRedaction`，确保错误不泄露完整 path，并包含 DB leaf 的 redacted basename。
  - 保留已有 directory path、unwritable parent、PRAGMA 测试。

## 不允许改

- 不要修改 `internal/platform/config/config.go` 的 env 优先级。
- 不要修改 SQLite migration 文件。
- 不要自动 chmod 已有 DB 文件。
- 不要改 `securefs.RedactPath` 的脱敏规则。

## 性能要求

- 每次 `sqlite.Open()` 对已有 DB 最多新增一次 `os.OpenFile(path, os.O_RDWR, 0)` 和 close。
- 不允许在 migration 循环、PRAGMA verify 或业务 SQL 前后重复做文件写探测。

## 风险要求

- 父路径为文件时，`NewDB()` 必须报 parent redacted path。
- DB path 自身为目录时，仍必须报 DB leaf redacted path。
- 父目录不存在时，仍必须创建父目录并继续创建 DB。
- existing DB 权限错误必须 fail-fast，不允许继续打开半可用 DB。

## 验收方案

1. 每个 Go 文件修改后运行单文件 guard：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/platform/db/sqlite/open.go
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/platform/db/module_config_test.go
```

2. focused tests：

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./internal/platform/db ./internal/platform/config -run 'NewDBRejects(ReadOnly|ParentFile|Unwritable|Directory)|ResolveSQLitePathRejectsParentFile|SQLitePath' -count=1
```

3. 手工复核错误契约：

- read-only DB error 不包含完整 DB path。
- parent-file `NewDB()` error 包含 `<redacted:private-parent>` 或测试中实际 parent basename 的 redacted 形式。
- parent-file `NewDB()` error 不包含完整 leaf DB path。
- config 层 `SUPER_DOLPHIN_SQLITE_PATH` 父路径测试继续通过。

## Review Checklist

- 生产就绪性：启动期阻断半可用 DB，不静默 fallback。
- 性能：写探测只发生在打开期一次。
- 风险：检查顺序没有破坏缺失父目录创建和 DB path 为目录的错误。
- 可维护性：路径检查仍集中在 sqlite open 包内。
- 测试充分性：P1/P3 都有回归测试。
