# C1 修订：132 作为 task SSOT，139 只保存来源联合绑定

状态：设计裁决，尚未实现；CONTENT/launch 仍为 NO-GO。

## 为什么修订

132 `orchestration_content_ingest_reservations` 已经是 task 来源字段的唯一生产
owner，并且全部 source/owner/fence/quota/retention 字段为 `NOT NULL`。把这 50
列复制到 139 会制造第二个可写真相源，且容易遗漏 CHECK、FK 或 trigger。相反，
139 只应保存“该 reservation 采用哪一种来源”以及 operator selector；task 的
字段真值仍从 132 读取并在同一事务重算绑定摘要。

这不是放宽约束：139 的 task binding digest 必须由 canonical 132 row 计算，
每次 promote/resolve 重新读取 132 并比较 digest；132 的 current-binding trigger
仍是 task 的权威校验。139 是不可变联合来源索引，不是 task 字段的替代 owner。

## 139 最小结构

`orchestration_content_source_union`：

- `ingest_reservation_id`：PK，并 FK 到 132 reservation，`ON DELETE RESTRICT`；
- `source_kind`：`NOT NULL`，只允许 `task|operator`；
- task 分支：`task_snapshot_digest`、`task_binding_version`、`schema_version`；
  不复制 132 的 50 个字段；digest 由 owner 对 132 canonical row 计算；
- operator 分支：`operator_source_id`、`operator_source_fence_ref`、
  `operator_principal_ref`、`operator_scope_ref`、`operator_auth_context`、
  `operator_generation`；
- `created_at` 与 digest/schema 的严格 canonical 约束；immutable update/delete；
- operator source 只建普通 `(operator_source_id, operator_generation)` 索引，
  允许同一 active source 绑定多个 reservation；
- CHECK 必须使用 `IS NULL/IS NOT NULL`，防止 SQLite 三值逻辑绕过二择一；
- operator INSERT trigger 精确匹配 138 当前 active、全局最大 generation 及全部
  selector 字段；不允许 reservation AFTER INSERT 自动创建 union 行。

## 事务与字段链

`ReserveIngest` 在同一个 `BEGIN IMMEDIATE` 中：

1. 创建或重放 132 reservation；
2. 读取 132 canonical row 并计算 task digest，或读取 138 current operator
   selector；
3. 显式 `BindTask`/`BindOperator` 139 行；失败整体回滚。

`PutSinglePrepared`、`Promote`、`ResolveActiveRef` 必须在各自已有事务内重新读取
132/139/138，并拒绝 digest、binding version、operator generation/fence 任一变化。
对象/ref 仍由 132 的 exact-reservation trigger 约束；139 不替代 object/ref FK。

## 生成与验证顺序

1. 先以真实 132/138 SQLite 数据库运行 migration 139 contract：STRICT、FK、
   二择一、operator 多 reservation、旧代/撤销/错误 selector、immutable 负测；
2. 再加入 sidecar `sqlc.yaml` 并生成 query/模型，禁止手改 generated；
3. 再实现 content Store 窄 API 和 mapping；
4. 最后接 Reserve/Promote/Resolve 的事务读侧；未完成前三步不得接 C2。

## 明确不在 C1

C1 不创建 `launch_agent` command intent，不把 launch profile 伪装成
`send_message`，不创建 child identity，不启动 provider，也不声称 accepted/dispatch、
RESOURCE、OUT、RecoveryCustody 或 crypto-erasure 已完成。

## C1-0 schema 落地证据（2026-09-06）

已落盘并通过真实 host wrapper 的 C1-0 migration/schema contract：

- `internal/platform/db/sqlite/migrations/139_orchestration_content_source_union.sql`
- `internal/platform/db/sqlite/content_source_union_migration_test.go`
- 选择器：`TestContentSourceUnionMigrationContract`
- 证据：`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/operator-source/content-union-schema-green.log`
- archtest `141.970s PASS`；目标测试 `0.077s PASS`。

该切片只证明 139 的 STRICT/FK/index/trigger/schema 边界；它没有实现
sqlc、Store、BindTask/BindOperator、reservation/object/ref 的读写重验，
也没有证明 operator selector 的行为矩阵。因此 C1-0 不是 CONTENT 接线，
总计划仍保持 `CONTENT-Production: NO-GO` 和 ST-01～ST-18 Active。
