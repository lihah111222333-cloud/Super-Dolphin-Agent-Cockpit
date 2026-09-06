# C1 operator/content union：可执行设计复核（阻断）

## 结论
当前不应实现 C1 SQL/Go 骨架；无法证明 `<=500` 行生产实现安全。阻断点是 132 的历史 reservation 已将完整审计快照固化为大量 `NOT NULL` 列，且其触发器/外键链要求 reservation 先于 object/ref/payload；138 operator source 是单 active、单调 generation 的不可变来源历史。若直接新增 union 表而不重写 132，会形成第二真相源，不能保证每 reservation 唯一绑定或完整审计。

## 已核对的实际依赖
- `orchestration_content_ingest_reservations`：唯一 `(object_id,object_version)`、`ingest_ticket_id`、幂等键；FK 到 `orchestration_identity_reservations`、`orchestration_execution_incarnations`、`orchestration_current_bindings`；`trg_*exact_identity`、`immutable_metadata`、`state_machine`、`no_delete`。
- `orchestration_content_objects`：PK `(object_id,object_version)`，FK `(ingest_reservation_id,object_id,object_version)`；exact-reservation、immutable、prepared→active、no-delete 触发器。
- payload receipt 以 `(object_id,object_version,ingest_reservation_id)` FK object；refs 以同一三元组 FK object，并要求 active/ref_version=1；refs no-update/no-delete。
- `orchestration_command_intent_profiles` 插入时由 `trg_orchestration_command_profiles_content_relation` 校验 content 引用，并由 **AFTER INSERT** `trg_orchestration_command_profiles_create_content_fence` 创建 deletion fence；fence 再以 `(reference_id,canonical_ref,object_id,object_version,ref_version)` FK refs。故 139 若依赖 profile 插入，不能让 132 reservation 的 AFTER INSERT 逻辑占用/抢先产生绑定。
- 138 `orchestration_operator_sources`：PK source_id，UNIQUE fence/generation/(principal,scope,auth)，partial UNIQUE 仅一个 active；BEFORE INSERT 要求 active、generation=max+1 且无 active；UPDATE 仅允许 active→revoked，DELETE 拒绝。它本身没有 reservation FK，允许同一 source 被多 reservation 复用，但需在 union 绑定表中记录 source generation/fence 快照。

## C1 可执行模型（仅设计）
新增 migration 139 应建立**唯一 union binding 表**，主键 `ingest_reservation_id`（每 reservation 恰好一行），并以 FK 绑定 132 reservation；同时保存 operator source 的 `source_id, source_fence_ref, generation, principal_ref, scope_ref, auth_context` 完整快照及 content reservation 的完整审计摘要（至少 132 全部 identity/owner/fence/digest/quota/retention/deadline/schema 字段，不能只存 digest）。source 可被多行引用，不得对 source_id 做 UNIQUE；新增 `(source_id,generation)` 索引即可。

139 插入顺序必须是单 SQLite 事务：先验证/锁定 active operator source（读取 source row，并校验 fence/generation），再 INSERT union binding（FK 132 reservation），再执行 object→payload→promote→ref/current-selector 的既有 SQL；不得依赖 132 reservation 的 AFTER INSERT（132 reservation 没有此类绑定触发器），也不得把 binding 放在 profile AFTER INSERT 之后。任何校验失败整体 rollback。

object/ref/promote/resolve current selector 必须在同一事务内重验：reservation identity/digest、object exact reservation、payload exact relation、object active/ref_version、ref canonical selector 及 source generation/fence；使用 `UPDATE ... WHERE revision/state/...`，受影响行数必须为 1，否则 ABORT。禁止应用层先查后写的非事务窗口。

## sqlc 生成步骤
1. 将 139 migration 加入 `sqlc.yaml` schema 且保持严格顺序。
2. 新增 `cmd/mcp-orch/sql/queries/content_operator_union.sql`：insert binding、get by reservation/source、promote/resolve 事务内 guarded updates；每个 query 明确列名，禁止 `SELECT *` 作为跨版本契约。
3. 运行仓库固定 sqlc 生成命令，审阅 `internal/store/sqlc` 新 struct/接口及 migration schema hash；不得手改 generated 文件。
4. 增加 schema gate 对表、FK、唯一约束、触发器及 query 受影响行数的测试；本回合不执行测试，仅 `git diff --check`。

## C1/C2 边界
C1 只定义 union binding、快照、事务顺序和 guarded SQL；不改变 132 列、不迁移历史行、不改变 operator source 生命周期、不实现新的 payload backend。C2 才能处理历史 reservation backfill/quarantine、profile/fence 全链路接入、current-selector 读取 API、端到端 sqlc/store/命令调用及并发故障测试。未完成 C2 前不得删除旧入口或宣称可发布。
