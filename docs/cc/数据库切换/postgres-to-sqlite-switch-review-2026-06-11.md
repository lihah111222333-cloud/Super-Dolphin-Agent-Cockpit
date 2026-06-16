# PostgreSQL 完全移除与 SQLite 本地持久化切换实施规格

检查时间：2026-06-11

代码基线：`d0a8dd160` (`main`, after `origin/main` fast-forward)

目标：将 Super Dolphin 桌面应用与 `cmd/mcp-orch` 的产品运行时持久化从 PostgreSQL / embedded PostgreSQL 切换为本地 SQLite。由于当前仍处于开发阶段，既有本地 PG 数据不作为用户资产，本次切换允许本地数据重置；必须保证 SQLite 新运行时功能完整、并发语义等价、启动与打包体验不再依赖 PostgreSQL，且产品运行时不再读取 PG 相关配置。

修订依据：用户提供的 `three-agent-postgres-to-sqlite-review.md` 与 `sqlite-switch-data-reset-scope-review-2026-06-11.md` 评审文档。本版已按源码追溯真阳性、重复项和上层防护后重写为可执行实施规格。

## 1. 结论

当前项目不能通过“换 driver”完成切换。现有代码把 PostgreSQL 深度嵌入到运行时配置、生命周期、sqlc 生成、手写 store、`mcp-orch` sidecar、迁移系统、并发 claim、advisory lock、JSONB 事件数组和打包脚本中。

要满足“从 PG 完美切换到 SQLite，不能影响原功能”，必须把目标定义为：在下列功能等价矩阵和发布 gate 全部通过后，达到“无已知功能回归、无未覆盖并发语义差异、无产品运行时 PG 依赖残留”的发布状态。旧本地 PG 数据按开发阶段遗留状态处理，不进入本次功能等价范围。绝对意义上的“完美”不能只靠文档保证，但本文件给出了后续实现必须逐项落地和验收的规格。

建议路线：

- 产品运行时硬切到 SQLite，不长期保留双数据库兼容分支。
- 不提供默认 PG -> SQLite 自动迁移工具；旧 PG 数据目录存在时不启动、不读取、不迁移、不阻断 SQLite 启动。
- 产品运行时完全忽略 `DATABASE_URL` / `POSTGRES_CONNECTION_STRING`，不得把 PG DSN 作为 DB 配置源，也不得继续向子进程导出 PG DSN。
- `schema_migrations` 最低版本 gate、cron claim、DAG wakeup claim、DAG scheduled advisory lock、prompt recall topic lock、JSON event append/truncate、多进程写入稳定性、PG runtime/config 完全解耦是 P0 发布阻断项。
- SQLite schema 必须是从当前有效 PG schema 重建的 baseline，不应逐个套用旧 PG migrations。

## 2. 源码证据与评审裁决

### 2.1 当前 PG 依赖面

- `README.md` 仍把 PostgreSQL 和 `DATABASE_URL` 列为 store 前置依赖。
- `internal/platform/config/config.go:31` 通过 `embeddedpg.ResolveFromEnvironment` 解析 embedded PG / 外部 PG，并在 `config.go:206-213` 回写 `DATABASE_URL` 给子进程继承。
- `internal/platform/db/module.go:28-47` 创建 `*pgxpool.Pool`；`module.go:422-444` 在生命周期内启动 embedded PG、建库、跑迁移、校验 schema 版本。
- `internal/platform/db/module.go:136` 定义 `MinRequiredSchemaVersion = 103`；`VerifyMinSchemaVersion` 在 app 与 `cmd/mcp-orch/runtime.go:95` 都会调用。因此“schema 低版本当前完全无防护”不成立，但 SQLite 必须保留等价 gate。
- 根 `sqlc.yaml` 与 `internal/sidecar/orch/sqlc.yaml` 均为 `engine: "postgresql"`，并使用 `sql_package: "pgx/v5"`。
- `internal/store/module.go` 直接提供 `func(pool *pgxpool.Pool) *sqlc.Queries`，主应用 store 当前仍围绕 `pgxpool/sqlc/pgtype` 组织。
- `cmd/mcp-orch/runtime.go:74-101` 独立创建 pgx pool、sqlc queries，并校验 PG schema。
- `internal/platform/embeddedpg/**`、`internal/app/*postgres*test.go`、启动脚本和打包 smoke 都围绕 bundled PostgreSQL。

### 2.2 三 Agent 评审裁决

| 评审项 | 裁决 | 源码追溯 | 处理方式 |
|---|---|---|---|
| 没有 PG -> SQLite 迁移工具会丢历史数据 | 按新产品前提降级：旧 PG 本地数据无用户价值，不作为 P0 | `rg "sd-pg-to-sqlite|PG_MIGRATE|SUPER_DOLPHIN_SQLITE"` 只命中文档/历史规划；产品尚未上线，用户确认允许丢弃旧 PG 本地数据 | 删除正式迁移工具要求；README / release note 明确旧 PG 数据不会自动迁移 |
| 旧 PG 存在但 SQLite 不存在时静默空库 | 按新产品前提改判：不是数据丢失 P0，而是 PG 解耦 P0 | 当前没有 SQLite runtime；现有路径会解析/启动 PG 并 auto-migrate | SQLite runtime 直接创建新库；旧 PG data dir 不读取、不启动、不迁移、不阻断 |
| PG 环境变量残留影响 SQLite 启动 | 真阳性，P0，属于完全解耦风险 | `internal/platform/embeddedpg/config.go:57` 会消费 `DATABASE_URL` / `POSTGRES_CONNECTION_STRING`；`internal/platform/config/config.go:205-213` 会回写 `DATABASE_URL` 给子进程 | 产品运行时完全忽略 PG env；`config.New()` 不再读取或导出这些变量，残留 env 不影响 SQLite 启动 |
| `schema_migrations` 低版本仍允许启动 | 当前 PG 路径不是真阳性；SQLite 必须复制防护 | `VerifyMinSchemaVersion` 已存在并被 app 与 `mcp-orch` 调用 | 不作为全新缺陷；列为 SQLite 等价 gate |
| cron `FOR UPDATE SKIP LOCKED` 替换不严谨会重复 claim | 真阳性，P0，上层 token/dedupe 不是等价防护 | `sql/queries/cron_job.sql:83-96` 与 `internal/module/cron/scheduler.go:247` 明确依赖 `SKIP LOCKED`；`dedupe_key` 包含每次新生成 idempotency key | SQLite 用原子条件 `UPDATE ... RETURNING` + lease，并加并发测试 |
| DAG wakeup claim 替换不严谨会重复 dispatch | 真阳性，P0，后置 holder/status guard 不保护选择阶段 | `internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql:7-28` 在选择阶段使用 `FOR UPDATE SKIP LOCKED` | SQLite 把选择与状态变更合成单个条件更新 |
| PG advisory lock 不能用 Go mutex 替代 | 真阳性，P0，无跨进程上层防护 | `cmd/mcp-orch/dag_cron_runner.go:64-68` 注入 `NewPGAdvisoryLocker`；`task_dag_dag.sql:125-128` 调 `pg_try_advisory_lock` | 新增 `runtime_locks` 表，CAS + holder + lease |
| prompt recall topic lock 不能直接丢弃 | 真阳性，P0，业务可达 | `sql/queries/prompt_template_sections.sql:104` 使用 `pg_advisory_xact_lock`；`internal/module/prompt/service.go:289-292` 与 `internal/module/prompt/intent/commit.go:436` 写 recall section 前会调用 | SQLite 用 `prompt_recall_locks` / 唯一索引 / 事务 CAS 保证同一 cwd/topic 不重复 |
| JSON event append/truncate 语义可能漂移 | 真阳性，P0/P1，历史注释证明有过静默覆盖风险 | `internal/sidecar/orch/sql/queries/task_dag_run.sql:245-277` 用 JSONB 追加并截断最近 50 条 | 建 golden；默认 Go 层读改写，不把复杂 JSON1 SQL 作为首选 |
| 多进程 SQLite 写入稳定性 | 真阳性，P0，现有 PG server 模型没有 SQLite 等价保护 | 桌面主进程和 `mcp-orch` 是独立进程，当前共享 PG server | WAL + busy timeout + 单写连接 + 压测 gate |
| 备份/恢复文档可延期 | 不采纳延期 | WAL 模式下只备份 `.db` 会有恢复风险 | 作为 P1 发布 gate，不进入 RC 后再补 |

### 2.3 本轮生产就绪性复核裁决

| 复核项 | 真实性与可达性 | 上层防护 | 文档处理 |
|---|---|---|---|
| 迁移期间并发写入导致快照不一致或迁移后丢写 | 按新产品前提降级并排除：不做 PG 历史数据迁移时，该风险不再真实可达 | 用户确认旧 PG 本地数据无价值；SQLite 新库从 baseline 创建，不读取 PG | 删除旧 7.x 迁移规格与原 12.3 迁移测试 |
| 升级后无法读取旧 embedded PG data dir | 按新产品前提降级并排除：旧 PG data dir 不再作为输入源 | 产品运行时不启动、不读取旧 PG；旧目录只是遗留文件 | 删除原“首次迁移 PG 读取能力”gate；打包与 config gate 改为验证无 PG runtime/config 消费 |
| 文档 SQL 示例使用 `RETURNING *` | 真阳性，但只影响文档可执行性，不是当前产品运行风险 | `internal/archtest/sqlc_bypass_guard_test.go:15,92-122` 已禁止 `internal/sidecar/orch/sql/queries` 中出现 `SELECT *` / `RETURNING *` | 示例改为枚举列；不升级为运行时 P0 |
| prompt recall topic lock 迁移遗漏 | 真阳性，P0，业务可达：dashboard prompt section 写入与 intent commit 都会在写 recall topic 前走 `LockRecallTopicInCWD` | 无可替代上层防护；后续 duplicate scan 依赖锁避免并发插入穿透 | 8.6、G8 与 12.4 的并发验证需补 recall topic 同 cwd/topic 双写重复=0 |
| 改写完成后缺少回归与冒烟验证 | 真阳性，属于发布就绪风险；当前 SQLite runtime 尚未实现，因此不是现有源码路径上的生产缺陷 | 无上层自动防护；如果没有完整测试，功能缺口、明显卡顿或锁等待可能在发布后才暴露 | 新增 G14 与 12.6，要求实现完成后运行功能回归、启动 smoke、并发压力和基础性能冒烟；删除迁移工具耗时/RSS |
| 回滚与灾难恢复不明确 | 真阳性，但范围改为 SQLite 备份/恢复；PG 回滚语义删除 | 无自动回滚防护；SQLite WAL 模式下备份只拷 `.db` 会有恢复风险 | G14 要求 `.db/.wal/.shm`、checkpoint、恢复流程；不再定义回滚到 PG |
| SQLite baseline 路径不明确 | 真阳性，属于文档执行歧义 | 无需上层防护 | 固定为 `internal/platform/db/sqlite/migrations/001_baseline.sql` |

## 3. 当前持久化边界

### 3.1 主应用 store

以 `internal/store/module.go` 为准，当前主应用注册的持久化子模块包括：

| 分组 | 子 store / 关键表 | 主要职责 |
|---|---|---|
| 线程与绑定 | `thread`, `binding`, `agentstatus`, `turndedupe` / `agent_threads`, `agent_provider_binding`, `agent_status`, `turn_dedupe_registry` | thread 生命周期、provider thread 绑定、运行态恢复、turn 幂等 |
| UI 与运行时状态 | `uipreference`, `cwdlock` / `ui_preferences`, `cwd_instance_locks` | UI 偏好、CWD 互斥 |
| 日志与审计 | `systemlog`, `ailog`, `auditlog`, `buslog`, `tasktrace` / `system_logs`, `audit_events`, `bus_exception_logs`, `task_traces` | dashboard、AI 日志投影、审计与追踪 |
| Prompt 与能力资产 | `prompt`, `routingtest`, `commandcard`, `sharedfile`, `feedback`, `insight` / `prompt_templates`, `prompt_versions`, `prompt_template_sections`, `prompt_routing_tests`, `command_cards`, `shared_files`, `agent_feedback_events`, `session_insights` | prompt CRUD、routing tests、command cards、共享文件索引、反馈与洞察 |
| 协作与审批 | `hookstore`, `interaction`, `topologyapproval` / `hook_pending_reviews`, `agent_interactions`, `topology_approvals` | hooks pending review、agent 间交互、架构审批 |
| Cron | `cron` / `cron_jobs`, `cron_job_runs` | 定时任务调度、claim、run 状态 |
| 运行时查询 | `dbquery` | 受控只读 SQL 查询引擎 |

注意：代码地图 `docs/doc/codemap/10-store.md` 的 store 数量描述已落后于源码；后续实现必须以 `internal/store/module.go`、`sql/queries/**`、`migrations/**` 为准。

### 3.2 `cmd/mcp-orch` store

`cmd/mcp-orch` 有独立 sqlc 配置与 store 包：

| 分组 | 关键表 | 主要职责 |
|---|---|---|
| DAG 模板与节点 | `task_dags`, `task_dag_nodes` | DAG CRUD、节点状态机、OCC、运行中节点绑定 |
| DAG run | `task_dag_runs` | 每次执行实例、events 追加、预算、终态 |
| DAG wakeup | `task_dag_wakeups` | 下游唤醒、claim、sent、retry、fail、reclaim |
| Worker lease | `task_dag_worker_leases` | target agent lease 抢占与续约 |
| Workspace | `workspace_runs`, `workspace_run_files` | workspace run、merge 状态、文件跟踪 |
| Prompt / command / shared file | `prompt_templates`, `prompt_versions`, `command_cards`, `shared_files` | MCP tools 读写资源 |
| Scheduled DAG | `task_dags.trigger/cron_expr/next_run_at` + advisory lock | 定时 DAG 扫描与启动 |

## 4. 目标架构

### 4.1 SQLite runtime

- 新增 SQLite 平台层，保留 `internal/platform/db` 作为上层注入边界，但底层改为 `database/sql` + SQLite driver。
- 默认 driver 建议使用 `modernc.org/sqlite`，理由是 CGo-free，适合 Windows/macOS/Linux 桌面打包。若压测发现性能或兼容问题，再单独评估 `mattn/go-sqlite3`，但后者会引入 CGO 和交叉编译成本。
- SQLite 文件默认放在 `SUPER_DOLPHIN_HOME` 派生目录，例如 `<home>/super-dolphin.db`；新增 `SUPER_DOLPHIN_SQLITE_PATH` 显式覆盖。
- 产品运行时不再把 `DATABASE_URL` 作为 DB 配置入口；`DATABASE_URL` / `POSTGRES_CONNECTION_STRING` 只保留为历史废弃配置名，运行时必须完全忽略。
- 打开连接后必须设置并验证：
  - `PRAGMA foreign_keys = ON`
  - `PRAGMA journal_mode = WAL`
  - `PRAGMA busy_timeout = 5000`
  - `PRAGMA synchronous = FULL`
- `*sql.DB` 初始设 `SetMaxOpenConns(1)`，先建立单进程内串行写安全基线。读写池优化只允许在 P2 中基于压测数据推进。

### 4.2 Schema baseline

- 新增 SQLite schema baseline，固定路径为 `internal/platform/db/sqlite/migrations/001_baseline.sql`，由 `internal/platform/db` 的 SQLite migration runner 负责加载。
- baseline 从当前有效 PG schema 转译，不逐条复用旧 PG migrations，不把 PL/pgSQL、`public.`、`pg_catalog`、PG trigger 迁移到 SQLite。
- `schema_migrations` 保留，字段可简化为 `version INTEGER NOT NULL`, `name TEXT NOT NULL`, `filename TEXT NOT NULL`, `applied_at INTEGER NOT NULL`。启动 gate 必须继续校验 `MAX(version) >= 103` 或项目更新后的最低版本。
- 所有 JSONB 列改为 `TEXT`，加 `CHECK (json_valid(column))`；默认值使用 `'{}'` 或 `'[]'`。
- 时间列统一使用 UTC epoch milliseconds `INTEGER`，store 层负责 `time.Time <-> int64` 转换；所有“now”由 Go 层注入，避免 DB 时区差异。
- 自增主键优先 `INTEGER PRIMARY KEY`；只有确需 SQLite `sqlite_sequence` 行为时才使用 `AUTOINCREMENT`。
- CHECK / UNIQUE / partial index 要按 SQLite 能力重建，不能因为迁移困难而丢约束。特别是 DAG v2、turn dedupe、agent binding、cron run 幂等相关约束必须保留。

## 5. 功能等价矩阵

下表是后续实现验收的主索引。每行都必须有对应代码改动、自动化测试和迁移验证。

| 功能面 | 当前 PG 行为 | SQLite 实施要求 | 验收 |
|---|---|---|---|
| 启动与配置 | `config.New()` 解析 embedded PG / `DATABASE_URL`，`db.Module` 启动 PG 并 auto-migrate | `config.New()` 只解析 SQLite path；产品运行时忽略外部 PG DSN；旧 PG 数据存在也不阻断 SQLite 新库创建 | 新装、PG env 残留、旧 PG data dir 残留三类启动测试 |
| schema gate | `VerifyMinSchemaVersion` 拦截低版本 PG | SQLite runner 保留同等低版本 gate | 低版本 SQLite fixture 启动失败 |
| migrations | PG migrations 按文件 apply，记录 `schema_migrations` | SQLite baseline + 后续 SQLite migrations；不复用 PG migration 文件 | `make sqlc-verify` + migration runner 单测 |
| thread lifecycle | `agent_threads` 保存 thread / runtime / prompt snapshot / recoverable 状态 | 保持字段、状态、JSON snapshot 兼容；时间转 epoch ms | thread start/list/recover/prompt snapshot golden |
| provider binding | `agent_provider_binding` 有 PK、唯一冲突、不可变字段语义 | 保持 `agent_id` 主键、provider thread 唯一、幂等冲突处理；不可变约束用 trigger 或 store 层测试锁定 | binding store 回归测试覆盖冲突、归档、session uuid、cwd |
| turn dedupe | `turn_dedupe_registry` 用时间窗口和 provider id 绑定 | 保持 live entry 查找、terminal 标记、sweep 语义 | 并发 start turn 不重复；terminal 后可重试 |
| agent status | `agent_status` JSON output tail + `NOW()` timestamps | JSON text 有效；updated_at 由 Go 传入 | dashboard agent status 列表一致 |
| UI preference | `ui_preferences.value JSONB` | `value TEXT CHECK json_valid`；key 唯一 | UI 偏好读写 golden |
| CWD lock | PG 条件 upsert + `NOW()-45s` | SQLite 条件 upsert/CAS；heartbeat 和 stale 删除使用 now_ms | 双进程抢锁、心跳、过期抢占 |
| logs/audit/trace | `system_logs`、`audit_events`、`task_traces` 使用 PG 时间与 JSONB | 保持 filter、keyword、limit；AI log 派生正则逻辑改 Go 层或 SQLite 支持函数 | dashboard 日志筛选 golden |
| hook pending review | 手写 PG SQL，pending/resolved/expired/cancelled | 改为 `database/sql` 手写 SQL 或 SQLite sqlc；保留幂等 key 与 deadline | resolver escalate/resolve/recover/cancel 测试 |
| interactions/topology approvals | JSON payload、review 状态更新 | 保持 JSON 与状态机 | interaction review、topology approve/reject 测试 |
| prompt templates | prompt CRUD、versions、sections、runtime metadata、CWD 后置过滤 | 保持 prompt 可见性、sections recall、versions archive；JSON tags/variables 语义一致 | prompt list/get/upsert/delete/version/recall golden |
| routing tests | `prompt_routing_tests` enabled list | 保持 enabled filter 与排序 | routing tests 列表一致 |
| prompt intent drafts | schema 在 `sql/schema/prompt_intent_drafts.sql` | 保持 generated_card/issues JSON、scope、status | draft upsert/list/status 测试 |
| command cards | JSON args schema、runs 聚合 | 保持 list keyword、last_run/run_count | command card list/get/version golden |
| shared files | DB 索引 + 磁盘 source，List 不扫盘 | DB 索引保持；磁盘行为不因 DB 改动变化 | 现有 disk integration 全量迁移 |
| feedback/session insight | feedback events、session insights JSON/观测查询 | 保持 thread/agent/local_turn 查询和 JSON payload | feedback + session insight 查询 golden |
| dbquery | PG 只读查询，`$1..$N` 占位符，PG 黑名单和 pgx rows | 改为 SQLite 只读查询，`?` 或命名参数；更新危险语句/PRAGMA/ATTACH/VACUUM 黑名单；字段名改 `Columns()` | 安全校验单测 + 白名单表查询 |
| cron jobs | `FOR UPDATE SKIP LOCKED` claim，lease renew/release/finish/fail，run dedupe | SQLite 原子 claim + lease；dedupe 不能替代 claim 互斥 | 同进程/跨进程 claim 重复=0、遗漏=0 |
| `mcp-orch` tool registry | `cmd/mcp-orch` 通过 PG store 支撑 task/workspace/prompt/command/shared_file tools | 保持 tools/list、tools/call 输出 shape；store 改 SQLite 不改协议 | `tools/parity_v2_test.go` 及 RPC golden |
| DAG template | `task_dags` + `task_dag_nodes` 支持 OCC、FOR UPDATE、版本 bump、apply ops | 用 SQLite 事务 + 条件更新替代 row lock；OCC 语义不变 | apply ops 并发冲突、环检测、delete gate |
| DAG run | `task_dag_runs` 支持 run 实例、events append/truncate、single running run 约束 | 保持 partial unique / 条件约束；events 用 Go 层 append/truncate | run start/finalize/cancel/event golden |
| DAG wakeup | `FOR UPDATE SKIP LOCKED` claim + sent/retry/fail/reclaim | 单语句条件 update claim；lease 过期可 reclaim | dispatcher 并发重复=0、stale 可恢复 |
| worker lease | PG 条件 upsert/interval | SQLite CAS + now_ms/lease_ms | acquire/renew/release 并发测试 |
| scheduled DAG | PG advisory lock 保证跨进程单 scheduler | `runtime_locks` 表 CAS + holder + lease | 两个 `mcp-orch` 同时跑只启动一次 |
| workspace runs | status CAS、file upsert/list/get | 保持 CAS 和 merge 状态 | workspace create/merge/abort tests |
| packaging | bundled PostgreSQL binary / scripts / manifest | 移除 PG runtime；加入 SQLite 文件说明和旧 PG 废弃说明 | Windows/macOS/Linux smoke |
| backup/restore | 现有 PG 语义 | 文档说明 `.db/.wal/.shm`、checkpoint、恢复流程 | 发布文档 gate |

## 6. PG -> SQLite 映射规则

### 6.1 类型映射

| PostgreSQL | SQLite | Go/store 规则 |
|---|---|---|
| `TEXT`, `VARCHAR` | `TEXT` | 空串与 NULL 保持原 contract |
| `BOOLEAN` | `INTEGER CHECK (col IN (0,1))` | store 映射为 bool |
| `INTEGER`, `BIGINT` | `INTEGER` | 主键用 `INTEGER PRIMARY KEY` |
| `SERIAL`, `BIGSERIAL` | `INTEGER PRIMARY KEY` | seed / fixture 需要固定 id 时显式写入；校验下一次插入不会复用 |
| `TIMESTAMPTZ`, `TIMESTAMP` | `INTEGER` epoch ms | Go 层统一 UTC |
| `JSONB`, `JSON` | `TEXT CHECK json_valid(col)` | `encoding/json.RawMessage` 保持 canonical JSON |
| `INTERVAL` | `INTEGER` milliseconds 或 Go 参数 | 禁止把 PG interval 字符串直接迁移 |
| `TEXT[]`, `ANY($1)` | JSON text 或关联表 | 优先按原查询语义决定，不默认用字符串拼接 |

### 6.2 SQL 习惯映射

| PG 写法 | SQLite 写法 / 策略 | 风险 |
|---|---|---|
| `$1`, `$2` | `?`, `?` 或 sqlc 支持的 named arg | dbquery 和 sqlc 参数校验需重写 |
| `NOW()` | Go 注入 `now_ms` | 时间测试可控 |
| `$1::jsonb`, `$1::timestamptz` | 去 cast，由参数类型和 store 转换保证 | sqlc 生成类型会大变 |
| `ILIKE` | `LOWER(col) LIKE LOWER(?)` 或 Go 层规范化 | Unicode case 语义可能不完全等价，需 golden |
| `regexp_match`, `regexp_replace` | Go 层计算，默认不注册 SQLite UDF | AI log/session 派生查询高风险 |
| `ANY($1::text[])` | 临时表、JSON scan、或 Go 展开固定占位符 | 禁止拼接未转义 SQL |
| `FOR UPDATE` | `BEGIN IMMEDIATE` + 条件 update / OCC | 行锁语义需逐处重建 |
| `FOR UPDATE SKIP LOCKED` | 单语句 `UPDATE ... WHERE ... RETURNING` claim | P0 并发风险 |
| `pg_try_advisory_lock` | `runtime_locks` CAS + lease | P0 跨进程风险 |
| `jsonb_set`, `jsonb_array_elements` | 优先 Go 层读改写；简单取值可用 JSON1 | event append/truncate 必须 golden |
| `pgconn.PgError` code | SQLite driver 错误归一化 | `WrapStoreError` 要重写 conflict/notfound/timeout |
| `pgconn.CommandTag.RowsAffected` | `sql.Result.RowsAffected` | 所有 `execrows` 包装要检查 |
| `pgx.Rows.FieldDescriptions()` | `*sql.Rows.Columns()` | dbquery 结果列名路径要重写 |

### 6.3 查询重写分组

| 查询目录 | 重点文件 | 重写策略 |
|---|---|---|
| `sql/queries` | `cron_job.sql`, `agent_thread.sql`, `prompt_template_sections.sql`, `session_insight.sql`, `ai_log.sql`, `db_query.sql` | 先重写 P0 并发/运行态查询，再处理 dashboard 派生查询 |
| `internal/sidecar/orch/sql/queries` | `task_dag_wakeup_dispatch.sql`, `task_dag_dag.sql`, `task_dag_run.sql`, `task_dag_node_*.sql`, `task_dag_worker_lease.sql`, `workspace_run.sql` | DAG/wakeup/lock/run events 必须先有 golden，再改 SQL |
| 手写 SQL | `internal/store/hookstore`, `internal/store/dbquery`, `internal/sidecar/orch/fxadapter` | 直接改为 `database/sql` 接口，避免继续暴露 pgx 类型 |

## 7. 旧 PG 数据与配置废弃策略

### 7.1 产品运行时行为

本次切换不提供默认 PG -> SQLite 自动迁移。旧 PG 本地数据属于开发阶段遗留状态，不作为用户资产参与功能等价验收。

运行时规则：

- 无 SQLite 文件时，直接创建 SQLite baseline 并启动。
- 旧 embedded PG data dir 存在时，产品运行时不启动 PG、不读取 PG、不迁移 PG、不因旧 PG 存在而阻断 SQLite 启动。
- `DATABASE_URL` / `POSTGRES_CONNECTION_STRING` 存在时，产品运行时完全忽略并继续按 SQLite 启动。
- `config.New()` 不得再从 PG env 推导数据库配置，也不得再把 PG DSN 回写到 `DATABASE_URL` 供子进程继承。
- `cmd/mcp-orch` 与其他 sidecar 必须继承 SQLite 配置，而不是依赖 owner 进程导出的 PG DSN。
- 产品运行时不得保留“PG 不可达则 fallback 到 SQLite”之类隐式分支；SQLite 是唯一运行时 DB。

### 7.2 实施要求

- 移除或隔离 `internal/platform/embeddedpg` 的产品启动路径；如果代码仍保留，只能作为历史兼容/测试辅助存在，不能被 `cmd/agent-terminal` 或 `cmd/mcp-orch` runtime 调用。
- 删除 `internal/platform/config` 中对 `DATABASE_URL` / `POSTGRES_CONNECTION_STRING` 的 DB 入口语义。保留这些字符串只能用于文档、测试 fixture 或明确的废弃说明。
- 更新 provider manifest / env passthrough，避免继续把 `DATABASE_URL` 作为 MCP provider 的必传或默认透传项。
- 打包 manifest、安装脚本、verify smoke 不再要求 `embedded_postgres_resource_path`、Postgres runtime 或 `migrations/` PG 目录。
- 启动脚本不再安装、启动或探测本地 PostgreSQL；开发脚本也应以 SQLite 为默认。
- README / release note 必须说明：切换后旧 PG 本地数据不会自动迁移，PG 相关环境变量不再生效。

### 7.3 验收用例

- 新装：无 PG、无 SQLite，启动后创建 SQLite 并通过 PRAGMA 与 schema gate。
- 旧 PG data dir 存在：无 SQLite 时仍创建 SQLite；不启动 `pg_ctl`、不打开 PG socket、不读取 PG data dir。
- PG env 残留：设置 `DATABASE_URL` / `POSTGRES_CONNECTION_STRING` 后启动，仍使用 SQLite；日志和配置中不得把这些值作为 DB DSN。
- `cmd/mcp-orch` 启动：没有 `DATABASE_URL` 时可正常连接同一 SQLite 文件。
- 静态扫描：除历史文档、废弃说明、测试 fixture 外，产品运行路径不得再出现 `embeddedpg.Start`、`pgxpool.New`、`DATABASE_URL` DB 配置入口或 bundled PG manifest 依赖。

## 8. 并发与锁规格

### 8.1 SQLite 连接策略

- 每个进程默认 `SetMaxOpenConns(1)`，减少进程内写并发。
- 跨进程依赖 WAL + busy timeout + 应用级 CAS/lease；不能假设 SQLite 自动等价 PG server。
- 所有多步写入必须显式事务。需要先抢写锁的流程使用 `BEGIN IMMEDIATE`。
- 不允许吞掉 `database is locked` 后静默降级；可重试的地方必须有有界重试和日志，最终仍 fail-fast。

### 8.2 cron claim

当前 PG query `ClaimDueJobsForUpdate` 依赖 `FOR UPDATE SKIP LOCKED`。SQLite 目标语义：

```sql
UPDATE cron_jobs
SET
  claimed_by = ?,
  claimed_at = ?,
  lease_expires_at = ?,
  claim_token = ?,
  updated_at = ?
WHERE id IN (
  SELECT id
  FROM cron_jobs
  WHERE enabled = 1
    AND (claim_token = '' OR COALESCE(lease_expires_at, 0) <= ?)
    AND COALESCE(next_retry_at, next_run_at) <= ?
  ORDER BY COALESCE(next_retry_at, next_run_at) ASC, id ASC
  LIMIT ?
)
RETURNING id, name, prompt, schedule_type, schedule_expr, timezone, provider,
          model, cwd, config, skills, notify_channel, enabled, next_run_at,
          last_scheduled_at, last_run_at, claimed_at, claimed_by,
          lease_expires_at, claim_token, thread_id, agent_id, active_turn_id,
          last_turn_id, failure_count, max_attempts, next_retry_at,
          last_status, last_error_at, last_error, created_at, updated_at;
```

要求：

- 选择和状态变更必须在同一条写语句或同一 `BEGIN IMMEDIATE` 事务内完成。
- 上层 `claim_token` / `dedupe_key` 只能作为后续 run 幂等保护，不能替代 claim 互斥。
- 并发测试必须覆盖同进程 goroutine 和两个 OS 进程同时 claim。

### 8.3 DAG wakeup claim

当前 `ClaimDueTaskDagWakeups` 在子查询使用 `FOR UPDATE SKIP LOCKED`。SQLite 目标语义：

```sql
UPDATE task_dag_wakeups
SET
  status = 'dispatching',
  claimed_by = ?,
  claimed_at = ?,
  lease_expires_at = ?,
  attempt_count = attempt_count + 1,
  updated_at = ?
WHERE id IN (
  SELECT id
  FROM task_dag_wakeups
  WHERE status = 'pending'
    AND next_retry_at <= ?
    AND (
      trim(dag_key) = ''
      OR trim(node_key) = ''
      OR EXISTS (
        SELECT 1
        FROM task_dag_runs r
        WHERE r.id = task_dag_wakeups.run_id
          AND r.dag_key = task_dag_wakeups.dag_key
          AND r.status = 'running'
      )
    )
  ORDER BY next_retry_at ASC, id ASC
  LIMIT ?
)
RETURNING id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload,
          idempotency_key, status, attempt_count, next_retry_at, claimed_at,
          claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at,
          last_error, created_at, updated_at, run_id;
```

要求：

- `MarkWakeupSent`, `RetryWakeup`, `FailWakeup` 继续校验 `id + claimed_at + claimed_by + lease_expires_at`。
- `ReclaimStaleDispatchingWakeups` 只能回收 lease 过期的 dispatching。
- dispatcher 并发测试重复发送必须为 0。

### 8.4 advisory lock 替代

新增表：

```sql
CREATE TABLE runtime_locks (
  lock_key TEXT PRIMARY KEY,
  holder TEXT NOT NULL,
  lease_expires_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

抢锁：

```sql
INSERT INTO runtime_locks(lock_key, holder, lease_expires_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(lock_key) DO UPDATE SET
  holder = excluded.holder,
  lease_expires_at = excluded.lease_expires_at,
  updated_at = excluded.updated_at
WHERE runtime_locks.lease_expires_at <= ?
   OR runtime_locks.holder = excluded.holder;
```

要求：

- scheduled DAG runner 使用稳定 `lock_key`，例如 `dag_cron/global`.
- holder 必须包含进程 id、启动随机 nonce 和 hostname，避免 pid 复用误释放。
- 释放锁必须匹配 holder；续约必须匹配 holder。
- lock 表协议必须被主进程和 `mcp-orch` 共享，不允许用 Go mutex 替代。

### 8.5 DAG run events

当前 `AppendTaskDagRunEvent` 依赖 PG JSONB array append/truncate。SQLite 目标：

- 默认 Go 层实现：`BEGIN IMMEDIATE` 事务内读取 `events`，反序列化为 `[]json.RawMessage`，追加新 event，保留最后 50 条，序列化写回。
- 如果不用 `BEGIN IMMEDIATE`，写回必须使用从同一连接读出的原始 JSON 文本做 `WHERE run_key = ? AND events = ?` 字节级 CAS；CAS 失败必须重试，不能静默覆盖。
- 建立 golden 覆盖：空数组、50 条、51 条、object/array/string/null payload、node_spawn retry 覆盖历史。

### 8.6 prompt recall topic lock

当前 `LockRecallTopicInCWD` 依赖 `pg_advisory_xact_lock(hashtextextended(cwd || topic))`。它不是死代码：`WriteSection` 和 prompt intent commit 在写入 recall section 前会先锁同一 `cwd/topic`，再扫描可见模板判断重复。SQLite 目标语义：

- 首选 schema 约束：为可见 recall topic 建立可校验的唯一约束或派生表，例如 `prompt_recall_topics(cwd TEXT NOT NULL, topic TEXT NOT NULL, template_id INTEGER NOT NULL, section_key TEXT NOT NULL, PRIMARY KEY(cwd, topic))`，与 section upsert 在同一事务内维护。
- 如果不建派生表，必须用 `BEGIN IMMEDIATE` 包住 lock + duplicate scan + upsert；同一 cwd/topic 的并发写必须串行。
- 不允许用进程内 mutex 替代，因为 prompt 写入可能来自桌面主进程、`mcp-orch` tool 或后续 sidecar。
- 并发测试必须覆盖：同一 cwd/topic 双写只成功一次；不同 cwd 同 topic 可并行；global 与 project scope 的覆盖规则与当前 `promptRecallDuplicateExists` / `promptIntentRecallDuplicateExists` 一致。

## 9. 实施任务

### Phase 0：锁定规格与测试夹具

改动文件：

- 新增 SQLite golden fixtures：`internal/platform/db/sqlite/testdata/**`
- 新增 SQLite runtime fixture：`internal/platform/db/sqlite/runtime_testdata/**` 或同等包
- 新增并发测试 fixture：`internal/store/cron/**`, `internal/sidecar/orch/store/taskdag/**`

任务：

- 为功能等价矩阵建立 golden 样本：JSON、timestamp、ILIKE/search、array、DAG event、cron claim。
- 把当前 PG 行为用测试锁住，确保 SQLite 等价实现能对照。
- 建立固定路径 `internal/platform/db/sqlite/migrations/001_baseline.sql`，并把 runner 单测绑定到该路径。

验证：

- `./scripts/test_with_guard.sh ./internal/platform/db ./internal/store/... ./cmd/mcp-orch/... -count=1`

### Phase 1：SQLite platform 与 sqlc 生成原型

改动文件：

- `internal/platform/config/config.go`
- `internal/platform/db/**`
- `sqlc.yaml`
- `internal/sidecar/orch/sqlc.yaml`
- 新增 SQLite schema baseline

任务：

- 新增 SQLite path config；删除产品运行时 PG env/config 入口，`DATABASE_URL` / `POSTGRES_CONNECTION_STRING` 残留时完全忽略。
- 抽象 DB 注入边界，从 `*pgxpool.Pool` 改为 SQLite 兼容 query/exec/tx 接口。
- 改写 `WrapStoreError`, `WithTx`, `OpenReadOnlyRows`, `RowsFieldNames`。
- 跑通最小 `sqlc` SQLite 生成，清理 generated pgx 类型。

验证：

- `make sqlc-verify`
- `make guard`

### Phase 2：主应用 store 迁移

改动文件：

- `internal/store/module.go`
- `internal/store/*`
- `sql/queries/*.sql`
- `internal/store/sqlc/**` generated

任务：

- 先迁移无并发复杂性的 CRUD：agent status、UI preference、prompt、command card、shared file、logs。
- 再迁移 thread/binding/turndedupe/cwdlock/hookstore/dbquery。
- 最后迁移 cron claim/run 状态。
- 所有 store wrapper 对外 DTO 不变。

验证：

- `./scripts/test_with_guard.sh ./internal/store/... ./internal/module/... -count=1`
- cron 并发测试必须通过。

### Phase 3：`cmd/mcp-orch` 迁移

改动文件：

- `cmd/mcp-orch/runtime.go`
- `internal/sidecar/orch/store/**`
- `internal/sidecar/orch/sql/queries/**`
- `internal/sidecar/orch/sqlc.yaml`
- `internal/sidecar/orch/fxadapter/**`
- `cmd/mcp-orch/dag_cron_runner.go`

任务：

- 用 SQLite DB 替换 standalone pgx pool。
- 重写 DAG OCC、node 状态、wakeup claim、worker lease、workspace CAS。
- 用 `runtime_locks` 替代 PG advisory lock。
- 用 Go 层实现 DAG run events append/truncate。

验证：

- `./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1`
- 两进程 scheduled DAG / wakeup / worker lease 压测通过。

### Phase 4：PG runtime/config 移除与启动兼容

改动文件：

- `internal/platform/embeddedpg/**`
- `internal/platform/config/**`
- `internal/app/**` 启动脚本测试
- `internal/contract/manifest.go`
- provider / sidecar env passthrough 相关测试

任务：

- 从产品 runtime 移除 `embeddedpg.Start`、`pgxpool.New`、PG auto-migrate 和 PG schema gate 调用链。
- `config.New()` 不再读取、解析、导出 `DATABASE_URL` / `POSTGRES_CONNECTION_STRING`。
- `cmd/mcp-orch` 不再依赖 owner 进程导出的 PG DSN，直接连接同一 SQLite 文件。
- 旧 PG data dir 存在时不启动 PG、不读取 PG、不迁移 PG、不阻断启动。
- provider manifest / env passthrough 不再把 `DATABASE_URL` 作为 DB 依赖传递。

验证：

- 启动测试覆盖：PG env 残留、旧 PG data dir 残留、无 PG runtime、无 SQLite 初次启动。
- 静态扫描确认产品 runtime 路径不再引用 bundled PG lifecycle。

### Phase 5：移除产品 PG runtime 与打包文档

改动文件：

- `README.md`
- 启动脚本和 `internal/app/*script*test.go`
- 打包 manifest / embedded PG smoke
- 新增 SQLite backup/restore 文档

任务：

- 移除 bundled PostgreSQL runtime 打包。
- README 改为 SQLite 默认运行；说明旧 PG 本地数据不会自动迁移，PG env 不再生效。
- 写清 `.db/.wal/.shm`、checkpoint、备份、恢复。

验证：

- `make build-plain`
- Windows/macOS/Linux smoke。

## 10. 发布 gate

| Gate | 等级 | 验收方式 |
|---|---|---|
| G1 SQLite runtime 启动 | P0 | 新装无 PG 依赖可启动；PRAGMA 验证通过 |
| G2 产品运行时无 PG 依赖 | P0 | 主应用和 `cmd/mcp-orch` 不需要 `DATABASE_URL`，不启动/连接 PG，不依赖 `pgxpool`，PG env 残留时仍按 SQLite 启动 |
| G3 SQLite schema baseline | P0 | `internal/platform/db/sqlite/migrations/001_baseline.sql` 覆盖全部本地持久化表、约束、索引，并保留 schema version gate |
| G4 主应用 store 等价 | P0 | thread/session/binding、prompt、cron、UI preference、logs/audit/trace、hook、feedback/insight、shared file、command card、dbquery 全部有 SQLite 实现和自动化测试 |
| G5 `mcp-orch` store 等价 | P0 | DAG/run/node/wakeup/worker lease/workspace run 等 `cmd/mcp-orch` 持久化能力正常 |
| G6 cron claim 等价 | P0 | 同进程/跨进程重复 claim = 0，遗漏 = 0 |
| G7 DAG wakeup claim 等价 | P0 | pending -> dispatching -> sent/retry/fail/reclaim 无重复 dispatch |
| G8 advisory lock 替代 | P0 | scheduled DAG 使用 SQLite `runtime_locks`，两个 runner 同时运行只启动一次 |
| G9 prompt recall lock | P0 | 同一 cwd/topic 并发写 recall section 重复=0；不同 cwd 同 topic 不互相阻塞 |
| G10 JSON event golden | P0 | DAG run events append/truncate 与当前 PG 行为一致 |
| G11 多进程 SQLite 写入 | P0 | 主进程 + `mcp-orch` 同写无不可恢复 `database is locked` |
| G12 打包去 PG | P0 | 发布包、manifest、verify smoke、启动脚本不携带/要求 bundled PG runtime |
| G13 旧 PG 数据忽略说明 | P1 | README / release note / 可选提示说明旧 PG 本地数据不会迁移，可选提供清理说明 |
| G14 回归测试、性能冒烟与备份 | P1 | SQLite 改写完成后运行完整回归、启动 smoke、并发压力和基础性能冒烟；备份/恢复文档覆盖 `.db/.wal/.shm` 和 checkpoint，恢复 smoke 通过 |

P0 全部通过前不能进入 RC；P1 关闭前不能正式发布；P2 只允许作为发布后性能优化。

## 11. 风险清单

### P0

- PG runtime/config 残留：`config.New()` 继续消费 `DATABASE_URL` / `POSTGRES_CONNECTION_STRING`，或产品启动路径仍调用 embedded PG / pgx pool，导致 SQLite-only 目标失败。
- 旧 PG 遗留状态误触发：旧 `postgres/data` 存在时误启动 PG、误读取 PG 或阻断 SQLite 新库创建。
- 并发 claim 漂移：cron / DAG wakeup 从 `SKIP LOCKED` 改到 SQLite 时重复领取。
- 跨进程锁失效：scheduled DAG 从 PG advisory lock 改动后重复启动。
- prompt recall topic 锁丢失：同一 cwd/topic 的 recall section 并发写可能绕过 duplicate scan，导致项目知识资料重复或覆盖规则漂移。
- JSON event 语义漂移：DAG run events 被覆盖、乱序或截断错误。
- 多进程写入锁错误：主进程和 `mcp-orch` 同时写 SQLite 造成不可恢复失败。
- sqlc 类型漂移：NULL、时间、JSON、RowsAffected 语义错映射。
- schema gate 丢失：低版本 SQLite 被错误启动。

### P1

- 搜索语义变化：`ILIKE` / regex / JSON key exists 与 PG 不完全一致。
- 回归与性能冒烟缺失：没有实现后的测试矩阵时，功能缺口、明显卡顿、锁等待或大表查询退化可能在发布后才暴露。
- 回滚/灾难恢复路径不清晰：SQLite 备份不完整或恢复流程不明确会造成数据不可恢复。
- 外部 `DATABASE_URL` 用户需要明确废弃说明，避免误以为 PG 仍是运行时入口。
- 打包脚本、manifest、smoke 残留 PG 路径。
- 备份/恢复文档遗漏 WAL/SHM。

### P2

- 单连接策略可能影响读并发，需要基于真实 telemetry 再拆 read pool / write queue。
- WAL checkpoint 策略不当导致磁盘占用增长。

## 12. 验证计划

### 12.1 静态与生成

```bash
make sqlc-verify
make guard
rg "pgx|pgxpool|pgconn|pgtype|embeddedpg|DATABASE_URL"
```

`rg` 命中只能出现在历史文档、废弃说明或已明确保留的测试 fixture；产品 runtime 路径不得继续消费 PG env 或启动 embedded PG。

### 12.2 Go 测试

```bash
./scripts/test_with_guard.sh ./internal/platform/db ./internal/store/... ./internal/module/... -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1
```

### 12.3 PG 解耦启动测试

- 无 PG runtime、无 SQLite：首次启动创建 SQLite 并通过 schema gate。
- 旧 `postgres/data` 存在、无 SQLite：启动仍创建 SQLite；不启动 PG、不读取旧 data dir。
- `DATABASE_URL` / `POSTGRES_CONNECTION_STRING` 存在：启动仍使用 SQLite；配置对象、日志和 sidecar env 不把这些值当作 DB DSN。
- `cmd/mcp-orch` 无 `DATABASE_URL`：仍连接同一 SQLite 文件并通过 DAG/wakeup smoke。

### 12.4 并发测试

- cron：100 个 due jobs，4 goroutine + 2 进程 claim，重复=0，遗漏=0。
- DAG wakeup：同样覆盖 pending -> dispatching -> sent/retry/fail/reclaim。
- scheduled DAG：两个 runner 抢同一 lock，只允许一个启动 run。
- prompt recall：同一 cwd/topic 两个事务同时写入，只允许一个成功；不同 cwd 同 topic 均可成功。
- SQLite 写入：主进程和 `mcp-orch` 混合写 5 分钟，无不可恢复 lock 错误。

### 12.5 打包 smoke

- clean machine 首次启动生成 SQLite。
- 旧 PG 目录存在时不启动 PG、不阻断 SQLite；可展示一次“旧 PG 数据不会迁移”的说明。
- PG env 残留时仍启动 SQLite；安装/升级说明明确这些变量已废弃。
- 启动 UI 可创建并读取新的 thread/prompt/skill/cron/DAG/workspace 数据。
- Windows/macOS/Linux 验证 SQLite 路径、权限、`.wal/.shm` 行为。

### 12.6 回归、性能冒烟与生产就绪

SQLite 改写完成后必须跑固定 fixture 和真实启动 smoke。结果必须随实现 PR 一起落盘到测试报告或 benchmark artifact，不能只写人工结论。当前 PG 可作为开发对照，但不是发布 gate 的必要输入。

最低 fixture：

- 中型 fixture：1,000 threads、10,000 system logs、1,000 prompts/versions、500 cron jobs/runs、500 DAG runs、10,000 wakeups/events。
- 大表 fixture：100,000 system logs、50,000 session insights、20,000 cron runs、20,000 DAG wakeups/events。

验收要求：

- 核心 store/RPC、dashboard 关键列表查询（thread、logs、prompt、cron、DAG、wakeup）必须跑回归测试和基础性能冒烟；不得出现明显卡顿、无限等待或整表加载导致的不可接受内存增长。
- 首次 SQLite 创建、schema gate、首屏 dashboard 查询必须纳入启动 smoke；失败或超时即阻断发布。
- 大表 fixture 用来证明 dashboard、日志和 DAG 列表不会把整表 JSON 或日志一次性载入内存。
- 5 分钟主进程 + `mcp-orch` 混合写压测中，不允许出现不可恢复 `database is locked`；可重试 busy 必须有上限、指标和错误上下文。

备份/恢复验收：

- 文档明确 SQLite WAL 模式下需要同时处理 `.db/.wal/.shm`，或在备份前执行 checkpoint。
- 至少一个 smoke 从备份恢复后能启动应用并读写 thread、prompt、cron、DAG 基础数据。

## 13. 外部参考

- sqlc SQLite tutorial: https://docs.sqlc.dev/en/stable/tutorials/getting-started-sqlite.html
- sqlc config reference: https://docs.sqlc.dev/en/latest/reference/config.html
- modernc SQLite driver: https://pkg.go.dev/modernc.org/sqlite
- SQLite WAL: https://sqlite.org/wal.html
- SQLite isolation: https://sqlite.org/isolation.html

## 14. 本轮可执行性复核结论

- 已确认真实可达且无现成上层防护的 P0：产品运行时无 PG 依赖、SQLite schema baseline、主应用 store 等价、`mcp-orch` store 等价、cron claim、DAG wakeup claim、scheduled DAG advisory lock 替代、prompt recall topic lock、DAG events append/truncate、多进程 SQLite 写入、sqlc SQLite 生成。
- 已降级或排除为非运行时 P0 的项：旧 PG 本地历史数据迁移、首次迁移 PG 读取能力、迁移窗口停写/一致快照不再属于本次目标；`RETURNING *` 属于文档示例可执行性问题，已改为枚举列；性能阈值不再作为当前文档的硬编码指标，改为实现完成后的回归测试、启动 smoke、并发压力和基础性能冒烟；当前 PG 的低版本 schema 已有 `VerifyMinSchemaVersion`，风险只要求 SQLite 等价复制。
- 文档现在具备执行性：baseline 路径固定、旧 PG 数据与配置废弃策略明确，发布 gate 明确到 G14，验证计划覆盖静态、生成、PG 解耦启动、并发、打包、回归测试和性能冒烟。
- 后续实现必须先完成 Phase 0 的 PG 行为锁定和 SQLite fixture，再进入 SQLite runtime 与 store 改造；任何 P0 gate 未通过都不能进入 RC。
