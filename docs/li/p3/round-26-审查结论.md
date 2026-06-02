# 第 26 轮审查结论

## 审查范围

- `internal/platform/embeddedpg/config.go`（ResolveConfig、ResolveFromEnvironment、resolveBinDir、resolveShareDir、resolvePort、appDataRoot、databaseURLFor）
- `internal/platform/embeddedpg/runtime.go`（Start/Stop、prepareStartRuntime、ensureStartedDataDir、requiredBinaries、ensureRuntimeDirs、validatePrivateDir、postgres binary 校验）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `config.go:37-48` `ResolveFromEnvironment` | 静默 | `os.UserHomeDir()` 和 `os.Executable()` 错误用 `_` 忽略 | UserHomeDir 失败时 home="" → `appDataRoot` 报 "user home is empty"；但 Executable 失败时 exe="" → `resolveBinDir` 走 fallback 到相对路径 `third_party/postgres/...` | 至少 Warn |
| `config.go:51-52` firstNonEmpty | 兜底 | input.GOOS/GOARCH 为空时 fallback 到 runtime.GOOS/GOARCH | 合理 | OK |
| `config.go:54-56` `databaseURL` | 兜底 | env 中已有 DATABASE_URL/POSTGRES_CONNECTION_STRING 时跳过 embedded postgres | 合理（外部 DB 优先） | OK |
| `config.go:60-65` resolveErr 拼接 | 兜底 | `appDataRoot` 和 `resolvePort` 的错误用 `joinResolveErrors` 拼成字符串放到 `cfg.ResolveError` | 错误被降级成字符串，调用方需要 string compare 判断错误类型 | 改为 `[]error` 或 `errors.Join` |
| `config.go:120-138` `appDataRoot` | 兜底 | userHome 为空时 fallback 到 `./.super-dolphin`（相对路径）+ resolveError | 相对路径会让 postgres 数据写到 cwd 下；这是危险的 fallback | 删除相对路径 fallback，直接返回 error 让调用方决定 |
| `config.go:141-161` `resolveBinDir` | 兜底 | 多层 fallback：env → packaged → projectRoot → executable → 相对路径 `third_party/postgres/...` | 最后的相对路径 fallback 会让 binary 查找走 cwd | 与 appDataRoot 同根问题 |
| `config.go:163-165` `resolveOwner` | 弱契约 | 仅 `desktop` 角色为 owner | 合理 | OK |
| `config.go:204-214` `resolvePort` | 兜底 | 解析失败/超出范围返回 `(0, "must be integer between 1 and 65535")`；空字符串返回默认端口 | 合理的 fail-fast | OK |
| `runtime.go:27-56` `Start` | 兜底 | `prepareStartRuntime` 返回 enabled=false 时直接 return（不报错） | 合理（embedded postgres 未启用） | OK |
| `runtime.go:39-44` running 但非 owned | 弱契约 | `running && !ownsPostgresDataDir(cfg.DataDir)` 时报 error 拒绝复用 | 合理的安全校验（防止误启动他人的 postgres） | OK（正面案例） |
| `runtime.go:58-76` `prepareStartRuntime` | 兜底 | `cfg.Enabled \|\| cfg.Owner == false` 时返回 enabled=false | 合理 | OK |
| `runtime.go:62-64` ResolveError | 兜底 | `cfg.ResolveError != ""` 时报错 | 合理的 fail-fast | OK |
| `runtime.go:126-143` `requiredBinaries` | 弱契约 | binDir 为空时报详细 error；binary 缺失时报详细 error | 合理（正面案例：error message 包含修复指引） | OK |
| `runtime.go:152-166` `ensureRuntimeDirs` | 弱契约 | 创建目录时用 0o700 权限；后续 `validatePrivateDir` 校验权限 | 合理的安全校验 | OK |
| `runtime.go:184-197` `validatePrivateDir` | 弱契约 | 检查目录权限是否包含 group/other 位 | 合理的安全校验（postgres data 必须 0700） | OK（正面案例） |
| `runtime.go:93-117` `Stop` | 兜底 | pg_ctl 缺失时报错；data dir 未初始化时直接 return；running=false 时 forget owned + return | 合理 | OK |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `config.go:38-39` | `os.UserHomeDir()` / `os.Executable()` 错误用 `_` 忽略 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `config.go:38-39` | UserHomeDir/Executable 失败静默 |
| `config.go:60-65` | resolveErr 字符串拼接 |
| `config.go:120-138` | appDataRoot 相对路径 fallback |
| `config.go:141-161` | resolveBinDir 相对路径 fallback |

## 修复优先级

### P0（必须本周修）
1. **`config.go:120-138` `appDataRoot` 相对路径 fallback**——userHome 为空时 fallback 到 `./.super-dolphin` 会让 postgres 数据写到 cwd 下，这是危险的（cwd 可能是临时目录、root 目录等）。改为 hard error。
2. **`config.go:141-161` `resolveBinDir` 相对路径 fallback**——同上，相对路径让 binary 查找走 cwd，会执行不可信的 postgres binary。

### P1（本月）
3. `config.go:37-48` ResolveFromEnvironment 中 UserHomeDir/Executable 失败至少 Warn
4. `config.go:60-65` resolveErr 改用 `[]error` 或 `errors.Join`，避免字符串拼接

### P2（下个 sprint）
5. 无额外 P2

## 边界条件

1. **`appDataRoot` 相对路径 fallback 的影响**：当前 `userHome == ""` 时返回 `./.super-dolphin` + resolveError。`prepareStartRuntime:62-64` 会因为 `cfg.ResolveError` 非空而拒绝启动——所以 fallback 路径实际不会被使用。但这是 fail-soft 风格而非 fail-fast。改为 hard error（直接 return error 而非返回相对路径）会让调用栈更清晰。
2. **`resolveBinDir` 相对路径 fallback**：同样，当前 `cfg.BinDir` 是相对路径时，`requiredBinaries` 的 `os.Stat(path)` 会基于 cwd 解析。如果 cwd 是 `/tmp` 或 `/`，stat 会失败，返回 "binary missing" 错误——所以也不会真的执行不可信 binary。但为了清晰应改为 hard error。
3. **`Start` 的 owned-check 是正面案例**：`running && !ownsPostgresDataDir(cfg.DataDir)` 拒绝复用——防止 super-dolphin 误启动到一个已经被其他工具/进程使用的 postgres。这是正确的 fail-fast 安全设计。
4. **`validatePrivateDir` 是正面案例**：强制 data dir 权限为 0700（无 group/other 位），符合 PostgreSQL 安全要求。错误消息明确，包含实际权限值。
5. **`requiredBinaries` 错误消息正面案例**：错误消息包含具体修复指引（"package postgres, initdb, pg_ctl, and pg_config under %s or set SUPER_DOLPHIN_POSTGRES_BIN_DIR"），是好的 fail-fast 实践。
6. **embeddedpg 包整体代码质量较高**：安全校验完善（owned-check、private dir 权限、binary 完整性）、错误消息清晰、fail-fast 风格强。主要问题是少数路径解析的相对路径 fallback。

---

**本轮总结**：embeddedpg 包代码质量较高，大量正面案例（owned-check、validatePrivateDir、requiredBinaries 错误消息）。2 个 P0 都是相对路径 fallback——虽然实际不会执行不可信代码（被下游 stat 失败拦住），但应改为 hard error 让调用栈更清晰。

**累计进度**：26 轮完成。cron `da34430c` 继续推进。
