# 第 27 轮审查结论（补漏）

## 审查范围

> 本轮为补漏轮——衔接第 26 轮（embeddedpg 包）与第 28 轮（notify/transport 层）。覆盖 `internal/platform/db` 包，这是 mcp-orch 的数据库访问基础设施层。

- `internal/platform/db/errors.go`（StoreError、WrapStoreError、IsNotFound、IsConflict、IsTimeout、IsUniqueViolation、classifyStoreError）
- `internal/platform/db/tx.go`（WithTx、runWithTx、OpenReadOnlyRows、openDirectRows、rollbackTx、txCleanupContext）
- `internal/platform/db/pool.go`（Pool 类型别名）
- `internal/platform/db/module.go`（NewPool、ensureDatabaseExists、autoMigrate、VerifyMinSchemaVersion、applyBaselineIfMissing、applyPendingMigrations、executeMigration、splitMigrationBody、registerLifecycle、embeddedPostgresResource）
- `internal/platform/runner/contract.go`（Runner、NoopRunner、Worker、AsRunner、closeOnce）
- `internal/platform/runner/group.go`（RunGroup、runOne、startSignalWatcher、preferRunGroupError）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `module.go:38` NewPool | 弱契约 | `poolCfg.MaxConns = 100` 硬编码 | 100 个连接对小型 embedded postgres 是过度（embedded 默认 max_connections=100，全用完应用 + 其他客户端会拒连）；对大型生产又可能不够 | 改为 cfg-driven，从 platformconfig 读取；零值时合理默认（如 25） |
| `module.go:42-45` NewPool | 静默 | `pgxpool.NewWithConfig` 是 lazy 的，第一次 Ping 才报连接错 | 应用启动时 NewPool 成功，但实际 DB 不可达——错误延迟到 OnStart Ping（line 444）才暴露 | 已被 line 444 ping 兜住，可接受；但应在 module 注释明确这一点 |
| `module.go:65-99` ensureDatabaseExists | 兜底 | 连不上 default `postgres` db 时直接 return error；连上后 EXISTS check + CREATE | 连不上时返原始错误，但这个错误对运维不直观（没说"没法连 postgres meta DB"） | 包装错误为 "ensure database exists: cannot connect to postgres meta DB: %w" |
| `module.go:80-83` ensureDatabaseExists | 静默 | `defer conn.Close(context.Background())` 用 Background ctx | 应用关闭时 close 不响应 cancel | 用 cleanup context（与 tx.go:107-114 一致） |
| `module.go:50-56` requireDatabaseURL | 弱契约 | 仅校验 trim 后非空；未校验 URL 格式 | 拼写错误的 URL（如 `postgres//host`）会在 ParseConfig（line 33）才报错；错误信息混乱 | 加 `url.Parse` 预校验 + scheme="postgres"/"postgresql" |
| `tx.go:50` runWithTx | 静默 | `_ = tx.Rollback(ctx)` 错误吞掉 | rollback 错误（如 connection lost）被吞——caller 认为 rollback 已完成，可能错误地复用相关连接 | 至少 Warn 日志包 fn 错误；或 errors.Join |
| `tx.go:42-48` runWithTx panic | 兜底 | panic 时用独立 cleanupCtx 做 rollback；rollback 错误吞掉 | rollback 失败时 panic 仍重抛，但连接可能处于半事务状态 | rollback 错误时记 Error 日志（带 stack） |
| `tx.go:56-82` OpenReadOnlyRows | 静默 | 类型断言 `queryer.(readOnlyTxBeginner)` 失败时静默走 `openDirectRows`（无 ReadOnly 隔离）| caller 期望 ReadOnly 事务保护，但拿到的是普通 query；可能写到只读 replica 失败或在 master 上加锁 | 类型断言失败时返 error，让 caller 知道未取得 ReadOnly 模式 |
| `tx.go:101-104` openDirectRows finish | 弱契约 | finish closure 接收 `success bool` 但只调用 `rows.Close()`，success 参数未用 | 与 tx 版的 finish（line 73-81）签名一致是好的，但参数实际无意义；调用方误以为可控制 | 加注释说明「direct 模式下 success 无意义，仅满足接口」 |
| `module.go:296-311` getAppliedMigrations | 静默 | rows.Scan 失败时 line 306 `if err := ... ; err == nil` 仅在成功时记录——失败的扫描行被静默丢弃 | 部分 migration 标记被漏读，applyPendingMigrations 误以为未应用 → 重复执行 | Scan 失败应 return err |
| `module.go:330-334` applyPendingMigrations | 排序 | `sort.Strings(toApply)` 字典序排序文件名 | 假设文件名 `001_xxx.sql, 002_xxx.sql, ..., 100_xxx.sql, ..., 1000_xxx.sql`：1000 < 999 < 99 < 100；但文件名都是固定 4 位前缀（如 `0103_xxx.sql`），所以字典序≈数字序——只要前缀位数固定就 OK | 加注释说明依赖固定 4 位前缀；或显式按数字 prefix 解析后排序 |
| `module.go:344-353` shouldApplyMigration | 静默 | `strings.HasPrefix(n, "000") \|\| strings.HasPrefix(n, "001")` 跳过 baseline | 排除逻辑硬编码字符串；未来加 `0010_xxx.sql` 会被误跳过（因 HasPrefix "001"） | 改为完整文件名匹配 `n == "001_baseline.sql"` |
| `module.go:373` executeMigration | 静默 | `_, _ = fmt.Sscanf(f, "%d_", &version)` 错误丢弃 | 文件名不带数字前缀（如手工命名）时 version=0；插入 schema_migrations 后无序排错难 | 解析失败应 return error |
| `group.go:97-105` preferRunGroupError | 复杂 | 「current==nil 或 current==context.Canceled」时用 next；否则保留 current | 「保留首个非 cancel 错误」逻辑正确但隐式；与 errors.Join 哲学冲突 | 加注释说明优先级：first non-cancel error 取胜 |
| `contract.go:60-63` closeOnce | 静默 | `defer func() { _ = recover() }()` 吞 panic | close on closed channel 会 panic；recover 是 idempotent close 的合理实现，但吞所有 panic（包括 nil deref）过宽 | 用 sync.Once 替代，或 recover 后只对 close-on-closed 类 panic 静默 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `module.go:42-45` NewPool | 启动 lazy，但 OnStart 的 ensureDatabaseExists+autoMigrate+Ping 都是同步阻塞 | 在 `registerLifecycle.OnStart` 各阶段加 `time.Now()` 计时 + Info 日志（"step=ensureDatabase, duration=..."） |
| `module.go:380-387` execMigrationBody | 单条 migration 串行；大型迁移（如全表 ALTER）阻塞启动数分钟 | 已是预期行为；至少加 per-segment duration 日志，便于运维定位卡住的语句 |
| `module.go:101-110` autoMigrate | ensureSchemaMigrationsTable + applyBaselineIfMissing + applyPendingMigrations 串行 | 每阶段 duration；超 30s 打 Warn |
| `tx.go:26-32` WithTx | 单次事务持续时间不可见 | runWithTx 入口/出口加 duration；> 5s 打 Warn |
| `group.go:32-70` RunGroup | N 个 runner 并行；单个 runner 卡住 cancel 后其他 runner 也要 drain | 已通过 `for remaining := len(runners); remaining > 0` 保证 drain；但 drain 时间不可见——加 shutdown duration 日志 |
| `group.go:43-46` safego.Go | runner panic 被 runOne 接住转 error；但 goroutine 创建/销毁开销在 N 大时累积 | runner 数量 > 50 时考虑 goroutine pool（当前应当不会到这个量级） |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `tx.go:50` | `_ = tx.Rollback(ctx)` 错误吞掉 |
| `tx.go:45` | panic 路径 rollback 错误吞掉 |
| `tx.go:57-59` | OpenReadOnlyRows 类型断言失败静默退化为非事务模式 |
| `module.go:306-308` | getAppliedMigrations Scan 失败静默跳过该行 |
| `module.go:373` | executeMigration version 解析失败静默 version=0 |
| `contract.go:60-63` | closeOnce recover 吞所有 panic |
| `group.go:91` | startSignalWatcher 收到信号后 errCh 阻塞会丢信号（buffer=1，但只发一次所以 OK） |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `module.go:38` MaxConns | 硬编码 100，无文档说明 |
| `tx.go:56-82` OpenReadOnlyRows | 类型断言决定是否使用事务，对 caller 不透明 |
| `tx.go:101-104` openDirectRows finish | success 参数无意义 |
| `errors.go:48-62` WrapStoreError | 已是 StoreError 时直接返回（不重新分类）；caller 不知 wrap 是否生效 |
| `module.go:330-334` 文件名排序 | 依赖固定位数前缀 |
| `module.go:198-225` requiredBaselineTables | 25 个表名硬编码；新增 baseline 表需同步改 |
| `contract.go:35-37` AsRunnerGroup | 与 AsRunner 完全等价，存在意义不明 |

## 修复优先级

### P0（必须本周修）
1. **`tx.go:50` rollback 错误吞掉**——这是事务正确性的根本问题。fn 失败 + rollback 失败时，连接处于半事务状态，下次复用会导致后续 query 看到部分 commit 数据。改为 `errors.Join(fn err, rollback err)` 或至少 Warn 日志带 stack。
2. **`tx.go:57-59` OpenReadOnlyRows 类型断言静默退化**——caller 显式选择 ReadOnly 模式来防止意外写入或获取 read-only replica。类型断言失败静默走非事务模式违背 caller 意图。改为返回 error。
3. **`module.go:306-308` getAppliedMigrations Scan 错误吞掉**——直接威胁 migration 幂等性。已应用的 migration 被误判为未应用 → 重复执行 → CREATE TABLE 等 DDL 必然失败 → autoMigrate 全失败。

### P1（本月）
4. `module.go:38` MaxConns 改为 cfg-driven
5. `module.go:373` executeMigration 文件名解析失败应 return error
6. `module.go:344-353` shouldApplyMigration 改为完整文件名匹配
7. `tx.go:42-48` runWithTx panic 路径 rollback 错误加 Error 日志
8. `contract.go:60-63` closeOnce 改为 sync.Once

### P2（下个 sprint）
9. 各阶段加 duration 日志（ensureDatabaseExists / autoMigrate / each migration / WithTx）
10. `errors.go:48-62` WrapStoreError 文档化「已 wrapped 时不重新分类」语义
11. `module.go:198-225` requiredBaselineTables 改为从 SQL schema 元数据派生

## 边界条件

1. **`module.go:159-174` VerifyMinSchemaVersion 是 fail-fast 正面案例**：autoMigrate 跑完后强制校验 schema_migrations.MAX(version) >= MinRequiredSchemaVersion (=103)。这是项目内为数不多的「启动期 sanity gate」实践，错误信息双语（中英），明确给出当前/期望版本。**强烈推荐作为 fail-fast 模板推广**。
2. **`tx.go:37-54` runWithTx 的 panic-safe 设计**：line 41-48 用 cleanup ctx 做 rollback 然后重抛 panic，让 caller 看到原始 stack。注释明确说明「Rollback 在 panic 路径用 cleanupCtx 是因为 ctx 可能已 cancel」。这是细致的边界处理，**正面案例**。但 `_ = tx.Rollback(cleanupCtx)` 仍吞错（line 45），与 line 50 同问题。
3. **`module.go:380-416` migration split sentinel 设计**：通过 `-- SPLIT --` 注释行分段执行，是为了支持 `CREATE INDEX CONCURRENTLY`（不能在事务内）与 `ALTER TABLE`（需要原子性）共存。这是对 PostgreSQL 限制的优雅解法。注释（line 358-361）解释清楚，**正面案例**。
4. **`group.go:32-70` RunGroup 的 first-error 策略**：`preferRunGroupError` 让首个 non-cancel error 优先于 context.Canceled。这是合理的——cancellation 通常是后续派生的，原始错误才是 root cause。但隐式约定在 line 97-105 实现，需读懂才知道。建议加包级注释说明此策略。
5. **`module.go:418-455` registerLifecycle 的 5 步启动顺序**：embeddedPostgres.Open → ensureDatabaseExists → autoMigrate → VerifyMinSchemaVersion → pool.Ping。任一步失败都用 `failAfterEmbeddedOpen` 关闭 embedded，避免泄漏。这是良好的资源生命周期管理。但注意 line 437 `autoMigrate(ctx, pool, cfg.ProjectRoot)`：如果 cfg.ProjectRoot 为空，下游 `filepath.Join("", "migrations")` 会用 cwd——与第 35 轮 memory/config.go 的同类问题。建议在 NewPool 或 OnStart 校验 ProjectRoot 非空。
6. **`errors.go:48-62` WrapStoreError 的 idempotency**：line 52-55 检测到已是 StoreError 时直接返回不重新分类。这是合理的——避免多层 store wrapper 嵌套时把 ErrNotFound 重新分类为 ErrConflict。但对调用方语义不透明：caller 看 `WrapStoreError(err, "list", "users")` 但 entity/operation 可能是上一层的——不会被本层覆盖。建议改为：检测到已 wrapped 时只更新 entity/operation 路径但保留 Kind。
7. **`group.go:43-46` safego.Go 的失败路径未审**：本审查未读 `safego.Go` 实现。如果 safego.Go 内部有 panic recovery（很可能有，从命名看），那 runOne line 73-77 的 recover 是冗余的——这种情况下 runOne recover 永远不触发。下轮建议覆盖 internal/util/safego。

---

**本轮总结**（补漏轮）：发现 3 个 P0 问题：①`tx.go:50` rollback 错误吞掉影响事务正确性；②`tx.go:57-59` OpenReadOnlyRows 静默退化违背 caller 意图；③`module.go:306-308` getAppliedMigrations Scan 错误吞掉威胁 migration 幂等性。`VerifyMinSchemaVersion` 是 fail-fast 正面案例模板。`runWithTx` 的 panic-safe + cleanup ctx 是细致的边界处理。`splitMigrationBody` 是对 PG 限制的优雅解法。

**累计进度**：27 轮（补漏）+ 28-39 = 共 13 轮新增审查完成。cron `fd4b4728` 继续推进。
