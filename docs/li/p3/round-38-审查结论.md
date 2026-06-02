# 第 38 轮审查结论

## 审查范围

- `cmd/mcp-orch/store/sqlc/db.go`（DBTX interface、Queries、WithTx）
- `cmd/mcp-orch/store/sqlc/task_ack.sql.go`（ListTaskAcks、UpsertTaskAck）
- `cmd/mcp-orch/store/sqlc/workspace_run.sql.go`（GetWorkspaceRun、ListWorkspaceRuns、TransitionWorkspaceRunStatus、UpdateWorkspaceRunStatus、UpsertWorkspaceRun、UpsertWorkspaceRunFile、ListWorkspaceRunFiles）
- `cmd/mcp-orch/store/commandcard/contract.go`（Reader、Store、CommandCard、CommandCardVersion、ListFilter）
- `cmd/mcp-orch/store/sharedfile/contract.go`（Reader、Store、SharedFile、UpsertParams、ListFilter）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `task_ack.sql.go:14-26` ListTaskAcks SQL | 弱契约 | `LIMIT $5` 由 caller 控制，无 SQL 层硬上限 | caller 传 limit=1000000 时 DB 单 query 返巨大结果集，阻塞协程并耗尽内存 | SQL 加 `LIMIT LEAST($5, 1000)` 硬上限 |
| `workspace_run.sql.go:117-124` ListWorkspaceRuns | 弱契约 | 同上无硬上限 | 同上 | 同上 |
| `workspace_run.sql.go:70-77` ListWorkspaceRunFiles | 弱契约 | 同上无硬上限；filter 全空时返所有 file 行 | 全表扫 + 巨大返回，是 OOM 直接路径 | 同上 + 必填至少一个过滤条件 |
| `workspace_run.sql.go:165-178` TransitionWorkspaceRunStatus | 静默 | `WHERE run_key = $4 AND status = $5` 命中 0 行时返 `pgx.ErrNoRows` —— 但调用方可能未区分「run 不存在」vs「状态机阻拦」 | 状态机阻拦应是有意义的业务错（如 active→merged 阻拦），与 NotFound 混在一起 | SQL 拆为 GetRunStatus + Transition 两步；或改用 RETURNING + 应用层判定 |
| `workspace_run.sql.go:165-176, 214-225` 状态机 | 弱契约 | finished_at 在 status='merged'/'aborted'/'failed' 时设 NOW，'active' 时清 NULL —— 列在 SQL 字符串内的硬编码状态 | 新增终态需同步改 4 处 SQL（Transition + Update 两个相同 case 表达式） | 提取常量，或通过 store 层包装 |
| `task_ack.sql.go:81-99` UpsertTaskAck | 静默 | `progress` 字段用 `GREATEST(0, LEAST($8, 100))` 硬截断到 [0,100] | 调用方传 -1 或 200 被静默截断，无法发现 bug | 应在 store 层校验 + 返回 error |
| `db.go:14-18` DBTX interface | 弱契约 | 三个 method 接受 `...interface{}` 可变参数 | 类型擦除，错误参数（如 nil pointer）在 SQL 执行期才暴露 | 是 sqlc 生成代码模式，无法改动；但建议在 store 层加 wrapper 校验 |
| `commandcard/contract.go:13-16` ListFilter.Limit | 弱契约 | int32 类型，0 / 负值语义无文档 | caller 传 0 时 SQL 走 `LIMIT 0` 返空集，看似无数据但实际有数据；负值行为 undefined | 加文档；或在 store 层强制 Limit > 0 |
| `sharedfile/contract.go:13-16` | 同上 | 同上 | 同上 | 同上 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `task_ack.sql.go:36-78` ListTaskAcks rows.Next loop | 每次 Scan 是同步 IO；大结果集（数千行）累积阻塞 | 加 result-row count 监控；> 1000 行打 Warn |
| `workspace_run.sql.go:85-114` ListWorkspaceRunFiles | ILIKE 查询 + 大 limit 时 DB 慢；当前文件无 ILIKE，但 list_task_acks 有（line 21-23） | DB 端 EXPLAIN 监控；ILIKE 在大表上 seq scan，应有 trigram 索引 |
| `task_ack.sql.go:21-23` ILIKE 三列 OR | 单 query 在 ack_key/title/description 三列上各做一次 ILIKE | 同上：trigram 索引 + 监控 |
| `workspace_run.sql.go:165-212` TransitionWorkspaceRunStatus | 同步事务路径：UPDATE + RETURNING + Scan | DB 锁等待监控；0 rows 时是否打 Info 日志（区分 not-found 与 status-conflict） |
| `db.go:28-32` WithTx | 事务上下文嵌套；如果 transaction-scoped query 慢，整个事务 timeout | 事务持续时间监控；> 1s 告警 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `task_ack.sql.go:85` | progress 静默截断 [0,100] |
| `task_ack.sql.go:17-23` | 4 个 filter 任一空字符串视为「不过滤」（约定） |
| `workspace_run.sql.go:73-74, 120-121` | 同上 filter 空字符串视为不过滤 |
| `workspace_run.sql.go:169, 218` | metadata `COALESCE(NULL, '{}'::jsonb)` 静默替 NULL 为空对象 |
| `workspace_run.sql.go:171-175, 220-224` | finished_at 在状态切换时按 status 决定 set/clear（业务规则编码到 SQL） |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `db.go:14-18` DBTX | `...interface{}` 全类型擦除 |
| `task_ack.sql.go:28-34` Params | Column1-4 命名无业务含义（status/priority/assignedTo/keyword） |
| `workspace_run.sql.go:79-83, 126-130, 180-185` | 同上 Column1/2/3 无业务命名 |
| `task_ack.sql.go:102-115` UpsertParams | Column8/11/12 同上 |
| `workspace_run.sql.go:165-176` 状态转换 | 终态列表硬编码在 SQL CASE 表达式 |
| `commandcard/contract.go:13-16, sharedfile/contract.go:13-16` | ListFilter.Limit 0 / 负值语义 |

## 修复优先级

### P0（必须本周修）
1. **`task_ack.sql.go:14-26` 等三个 List 查询无硬 LIMIT 上限**——`ListTaskAcks` / `ListWorkspaceRuns` / `ListWorkspaceRunFiles` 都让 caller 控制 limit。caller 误传巨大值（或恶意 caller）会让单 query 返几十万行，DB 协程阻塞 + 应用 OOM。SQL 层加 `LIMIT LEAST($N, 1000)` 硬上限是 1 行 SQL 修改。
2. **`workspace_run.sql.go:165-178` TransitionWorkspaceRunStatus 不区分错误原因**——「run_key 不存在」vs「当前状态不匹配」是不同的业务错，前者是数据 bug 应 alert，后者是状态机正常拒绝可能可重试。当前都返 `pgx.ErrNoRows`。改为：先 SELECT 拿当前状态，应用层判定后再 UPDATE。

### P1（本月）
3. `task_ack.sql.go:85` progress 截断改 store 层校验
4. `commandcard/contract.go:13-16, sharedfile/contract.go` ListFilter.Limit 文档化语义
5. `workspace_run.sql.go` 状态机硬编码提取为常量

### P2（下个 sprint）
6. `db.go:14-18` 在 store 层包 typed wrapper
7. 所有 sqlc 生成的 `Column1/2/3` 改用 sqlc.yaml 的 column_name 配置生成有意义名称

## 边界条件

1. **`task_ack.sql.go:85` GREATEST/LEAST 截断设计意图**：DB 层防御性截断 progress 到 [0,100] 是「不让无效数据进库」的合理 fail-soft。但与 fail-fast 张力：调用方传 -1 时无感知，下次又传 -1，bug 永远不暴露。**理想方案**：store 层校验（fail-fast）+ DB 层截断（防御性 final layer）。当前只有 DB 层，缺 store 层。
2. **sqlc 生成的 Column1/2/3 命名**：这是 sqlc.yaml 配置不全的副作用。sqlc 默认对未命名表达式参数（如 `($1::text = '' OR ...)` 中的 `$1`）生成 `Column1` 风格。可在 sqlc.yaml 加 `query_parameter_name` 配置或 SQL 中用 `@status_filter::text` 命名占位符。**P2 因为代码生成层修改，影响所有调用方**。
3. **`db.go` DBTX interface 设计是 sqlc 标准模式**：使 Queries 既能用普通 connection 也能用 transaction（pgx.Tx 实现 DBTX）。`...interface{}` 类型擦除是无法避免的——pgx 底层就是 driver-level varargs。store 层必须在更高层用 typed wrapper（factory.go 中的 `queryOne`/`queryMany`/`queryValue` 是好的尝试，但仅包装错误，未包装参数类型）。
4. **`workspace_run.sql.go` 状态机的 SQL-level 编码**：line 171-175 的 CASE 表达式硬编码了 `'merged'`/`'aborted'`/`'failed'`/`'active'` 四个状态的 finished_at 行为。这是把业务规则塞进 SQL 的反模式——业务规则应在 Go 层（store/orchestration）维护，SQL 只做 CRUD。维护成本高（新增终态需改 SQL+迁移+测试），且无法共享给其他 sql 文件（如果有 wakeup 状态转换也用类似规则）。
5. **`commandcard/contract.go` 和 `sharedfile/contract.go` 整体设计良好**：interface 拆分清晰（Reader 子集 + Store 全集），`UpsertParams` 是 typed struct（不是 sqlc 自动生成的 Params 直接暴露），mapping function 在 store.go 内部隔离。这是项目内 store 层 contract 的良好实践，建议作为模板推广到 taskdag 等其他 store。
6. **`task_ack.sql.go:14-23` ILIKE 三列 OR 的查询性能**：搜索同时在 ack_key/title/description 三列上做 ILIKE %pattern%。在大表（>10K 行）上是 seq scan + 三次 lower compare。生产应有 PostgreSQL trigram 索引（pg_trgm extension）+ GIN 索引才能高效。**审查无法确认是否已建索引**——建议下轮覆盖 migrations/ 目录验证。

---

**本轮总结**：发现 2 个 P0 问题：①三个 List 查询无 SQL 层硬 LIMIT 上限是 OOM 直接路径；②TransitionWorkspaceRunStatus 不区分 not-found vs status-conflict。`commandcard/contract.go` 和 `sharedfile/contract.go` 是 store contract 良好实践模板。sqlc 生成的 Column1/2/3 命名是配置不全副作用。状态机硬编码到 SQL 是反模式，业务规则应在 Go 层维护。

**累计进度**：38 轮完成。cron `fd4b4728` 继续推进。
