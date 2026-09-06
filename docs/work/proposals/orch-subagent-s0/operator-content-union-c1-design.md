# C1：operator CONTENT 联合来源迁移设计（未实现）

## 边界与不变量

现有 132 CONTENT reservation/object/ref 的 `source_*` 是 **ID-1 active task
binding**，138 operator source 只有 provenance，不能把 operator `source_id` 填入
`source_agent_id`。C1 采用显式二择一 `source_kind = task | operator`：task 行保留
132 当前所有字段、FK 和 trigger 语义；operator 行使用 nullable task 字段并由
operator selector（principal/scope/auth_context/source_id/fence/generation）精确
绑定 138 当前 active generation。禁止 `source_kind=operator` 携带任意 task
reservation/incarnation/agent/binding 字段，反之亦然；未知 kind、混合形状均拒绝。

## migration 139（不修改 132/138，也不启用 candidate 137）

新增 `orchestration_content_source_bindings`（或同一 owner 的 union shadow
表）保存 reservation_id/object/ref 的 source_kind 与完整 operator selector：
`source_id/source_fence_ref/principal_ref/scope_ref/auth_context/generation`，
`schema_version=1`，唯一 `(reservation_id, source_kind)`，并以 CHECK 保证 task
kind 全字段为空、operator kind 全 task 字段为空。推荐在 reservation/object/ref
各表增加 nullable `source_kind` 与 operator selector列，但由于 132 历史 NOT NULL
task 列不可重写，实际采用 union shadow 表可保持旧读取兼容并避免重写 132。

139 触发器：reserve 时 task 路径复用 132 exact-current-binding trigger；operator
路径在同一事务查询 138 `state=active`、全部 selector 相等、generation 为当前
最大且未撤销。promote/resolve/ref→command 每次重新检查对应 union 行和 138
current，代际变化 fail-closed。operator 不产生 `source_agent_id`，相关 object/ref
投影必须通过 source union 查询，不能用空串或 operator ID 占位。

## query / DTO / crypto 影响

* `cmd/mcp-orch/sql/queries/content_single.sql`：Create/Get/Promote/Resolve 与
  source-union 查询；sqlc 生成 `store/sqlc/content_single.sql.go`。
* `store/content/contract.go`：新增 `SourceKind`、`OperatorSourceBinding`，
  reservation/ref DTO 挂 `SourceUnion`；Validate 实施严格二择一。
* `mapping.go`、`reservation.go`、`promote.go`、`resolve.go`、`validate.go`：
  映射、current operator 校验及 replay 比较；task 分支必须保持逐字段旧比较。
* `aad.go`/`digest.go` 与 `contentcrypto`：operator AAD 不能复用 task AAD。新增
  `orch.content.operator.single.aad/v1` 与 prepare/seal request digest domain，
  DTO/schema 的 `aad_profile`/`schema_version` 明确版本；旧 v1 task 仅接受旧 profile。
  不修改已 sealed object 的解释。
* 132 的 `reference_kind='command_request'` 不足以表达 launch；139 为 operator
  launch 专用 `reference_kind='launch_request'`（或独立 launch content ref 表），
  禁止把 launch profile 填成 send_message target/current profile。

## 合法首启动顺序（C2 才接线）

`RequireCurrent(operator source + CallerPrincipal)` → 生成 launch-specific
`ContentRequest`（无 target binding）→ CONTENT Reserve → seal → promote/live ref
→ 独立 `launch_agent` command intent（其 request_content_ref 指向该 ref，source
  union/operator fence 精确相等）→ 若 launch 需要 child identity，再创建与 command
  一致的 identity reservation → producer/dispatch 仍是后续批准边界。不得把
  send_message assembler 的 AccessGrant/source-target 复用为 launch。

## C1/C2 文件与测试清单

C1：`migrations/139_orchestration_content_operator_source.sql`、
`sql/queries/content_operator_source.sql`、生成 sqlc、`store/content/{contract,
validate,mapping,reservation,promote,resolve,aad,digest}.go`、`content_*_operator`
合同/字段守卫/迁移测试、`content_schema_gate` 更新。测试覆盖 task regression、
operator current/revoke/rollover、二择一 NULL/mixed/unknown、无 operator ID 投影、
replay、跨代、AAD profile 与 migration ordering；不得启动 provider/dispatch。

C2（独立批准）：`orchestration/commandintent` 新建 launch assembler/DTO、
`orchestrationcommand` launch profile 与 query/trigger、launch tool handler 的
真实 Content→intent 调用链、binary 首启动 E2E；另行覆盖 producer/dispatch。

## C1 阻断复核（2026-09-06）

此前尝试的 139/query 骨架不得接线，原因：operator source 必须允许同一 active source 被多个 reservation 复用，不能全局唯一；task 分支必须保存完整 132 binding snapshot 或显式 task union FK，不能全 NULL；每 reservation 必须恰有一条 union binding，并由 reservation/object/ref/promote/resolve 触发器执行 current-selector 校验；跨代后读侧必须 fail-fast；selector 字段需严格 canonical/长度/digest/time/schema 约束。sqlc placeholder、query owner、生成流程和 migration ordering 也必须先验证。

因此 C1 拆分为：完整 139 schema 与迁移排序 → sqlc query/生成 → Go mapping/窄 owner API → reservation/object/ref current trigger 与读侧复核 → task/operator 回归及撤销/换代负测。未完成前不得提交或宣称生产接线。

## C1-1 实施阻断复核（2026-09-06）

本轮候选 139/query 已撤回，未进入实现基线。复核确认：

1. `operator_source_id, operator_generation` 上的 UNIQUE 索引错误地阻断同一 active operator source 被多个 reservation 复用；reservation 自身的主键已保证每 reservation 一条绑定，后续若需要仅可使用非唯一查询索引。
2. reservation `AFTER INSERT` 自动创建 task union 行会先占用 `ingest_reservation_id` 主键，导致 operator reservation 无法显式插入 union 行；因此不能同时声称 task 自动兼容和 operator 可用。正确方案必须移除该自动触发器，由 owner API 在同一事务中显式 `BindTask` 或 `BindOperator`，在真实接线完成前不得宣称 C1 可用。
3. sidecar query、sqlc.yaml、生成代码及真实 SQLite contract test 尚未形成闭环；139/query 仅可作为设计候选，不能进入生产工作树或被当作完成证据。
