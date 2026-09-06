# Operator CONTENT union：C1 reservation 设计裁决（仅设计）

## 事实与问题

C1-0 已证明 union + Store Bind/Resolve API 可真实 PASS；但 migration 132 的 reservation source columns（task/agent、workspace/run、policy/profile、payload/object/ref 关联）全部 `NOT NULL` 且带 FK。operator 尚无 task/agent 身份，故无法先插入一条合法 132 reservation；先 union 后绑定不能解决首条 operator CONTENT 的 source 缺失。禁止把 operator 伪装为 agent 或默认 task。

## 方案裁决：A（新表）胜出

**A：migration 139 新建 operator-specific reservation metadata，并由统一投影表表达 object/ref union。** 132 task reservation 保持 legacy SSOT 与约束不变；139 operator 表拥有 operator selector、workspace/run、policy/profile、generation、状态及审计字段，严格 FK 到现有 workspace/run/profile（若某 FK 不是 operator 必需，应在 139 新建 operator profile/reference，而非 NULL 填 132）。reservation ID 使用一个全局 opaque namespace（同一 UUID/ULID 空间，`source_kind` 只作判别器），union binding 以 `(reservation_id, source_kind, generation)` 唯一。

**B：migration 139 修改 132 为 nullable/source_kind，不采纳。** 它会破坏历史 NOT NULL、CHECK/trigger 与 FK 假设；旧 sqlc 结构和查询无法区分“旧 task 缺失”与 operator 空值，回填/并发/删除语义复杂，且会把 task legacy SSOT 变成可变多态表。仅凭 nullable 不能满足 operator 的完整 source、profile、payload/object/ref 约束。

## 对象、引用、摘要与加密

139 union projection 表保留 `reservation_id`、`source_kind`、`generation`，object 表一行对应 `(reservation_id,generation,object_id,object_version)`；ref 表以 `(reference_id,canonical_ref,object_id,object_version,ref_version)` FK object，并带 source discriminator。operator metadata 必须先存在，union rows 才可绑定。content payload/profile FK 继续引用既有表，严禁跨 source 借用 task 行。canonical digest 为规范化字段序列（含 source_kind、IDs、generation、profile/schema version、object/ref selectors）SHA-256；AAD 同步绑定 digest、reservation namespace、workspace/run、profile version/key id，key rotation 或字段不匹配 fail-fast。

## operator source 与事务

operator source 的 current trigger 只允许在同一 reservation generation 内单赢家更新（旧 current 先失效，新行写入后再提升）；不得触发 task current。事务顺序固定：校验 caller/operator 与 scope → `BEGIN IMMEDIATE` → 锁定并验证 operator source/current generation → 插入 139 reservation metadata → 插入 union reservation/object/ref projections（FK 完整）→ payload/object promote → ref current-selector → 写 receipt/digest → commit。任一校验失败整体 rollback；不能依赖 profile AFTER INSERT 或 132 reservation trigger。

## API、sqlc 与 C2 launch

sqlc 生成独立 `CreateOperatorReservation`、`BindOperatorContent`、`ResolveContentByReservation`，返回 typed source discriminator；Store 层不得把 operator 映射为 agent/task。legacy `CreateTaskReservation` 保持原签名与 SSOT。C2 launch assembler 接受 union reservation handle，按 source_kind 分支读取完整 operator metadata 或 132 task 行，重算 digest/AAD 并拒绝 cross-source；assembler 不创建隐式默认 task/profile。

## 可拆交付

- **C1a schema**：139 operator metadata、namespace/generation/status、union projection、FK/UNIQUE/CHECK 与 migration guard。
- **C1b object/ref**：object/ref projection、payload/profile FK、canonical digest/AAD 规范、current trigger。
- **C1c Store**：sqlc queries、Store Bind/Resolve、事务锁与 typed errors。
- **C2 launch**：launch assembler、operator source current、replay/rollback/expiry 与命令入口。

## 必须具备的真实 contract tests

1. 首条 operator reservation 在无 agent/task 行时可创建；同一 ID namespace 下 task/operator 判别与碰撞拒绝。
2. 全部 object/ref/payload/profile FK、generation/current trigger、删除与回滚负测。
3. 并发两个 operator current 更新仅一赢家；stale generation、cross-workspace/run、digest/AAD/key rotation 均 fail-fast。
4. 132 历史 task reservation 全量回归（schema/query/trigger 不变），union resolve 不泄漏 task。
5. sqlc/Store 真实 SQLite 事务测试及 C2 launch assembler 端到端：operator 内容可 launch，缺 source 或 unknown outcome 零副作用。

本文件是设计修订，不修改 SQL/Go，也不宣称 C1/C2 完成。

## C1a schema evidence（2026-09-06）

operator-specific reservation metadata 已以 migration 140 落盘，并通过真实 host
wrapper 的 schema contract：

- `internal/platform/db/sqlite/migrations/140_orchestration_operator_reservations.sql`
- `internal/platform/db/sqlite/operator_reservation_migration_contract_test.go`
- 选择器：`TestOperatorReservationMigrationContract`
- 证据：`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/operator-source/operator-reservation-schema.log`
- archtest `151.642s PASS`；目标测试 `0.087s PASS`。

该切片只证明独立 operator reservation metadata 的 STRICT/FK/current-source
schema 边界；尚未接 object/ref projections、sqlc/Store producer、sealed/promoted
生命周期或 C2 launch。`CONTENT-Production` 仍为 `NO-GO`。
