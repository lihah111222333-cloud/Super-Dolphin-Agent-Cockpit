# Room contract candidate（Gate S0，未启用）

Status: Candidate / disabled / fail-closed

本候选位于 `cmd/mcp-orch/orchestration/room`，只冻结 transport-neutral DTO、状态约束、内存 CAS 单赢家、逐目标 receipt 和授权边界。新增的 `Owner` 是 transaction-neutral、进程内 candidate seam：它保存成员快照、幂等 operation、fan-out denominator 与 close/recovery 投影，但成员快照只是非权威投影，必须由显式 `PrincipalVerifier` 端口确认；未提供 verifier 时所有 owner mutation fail-closed。它不是 Room owner 的生产实现，也没有接入 public registry、Fx、provider、SQLite、RecoveryCustody 或任何 MCP tool。RecoveryCustody 的持久化方向仍是独立单实例私有 SQLite；Room 不得改成 PostgreSQL 或直接打开该 authority DB。

## 责任边界

- ROOM 只描述 room projection、operation envelope、dispatch admission head、inventory 摘要和 fan-out receipt。
- ID 负责 logical agent、execution/incarnation fence、caller source 与 membership subject；CONTENT 负责加密 content ref/version/taint；RESOURCE 负责 per-target reservation 与 policy；OUT 负责 command/attempt/receipt/outbox；TRUST 负责 CallerPrincipal、issuer binding 和 security-time lineage。
- SQLite 是持久 owner 的候选实现边界，不由 Room 打开、迁移或直接写表。
- RecoveryCustody 是恢复域外单赢家 authority，不由 Room 调用、实现、验证或保存其 receipt。

## 已冻结的安全行为

1. 所有公开 DTO 使用版本化 JSON 字段，canonical 文本、正代际和 lowercase SHA-256 摘要在 `Validate` 中 fail-fast。
2. `Operation.Authorize` 要求认证主体、issuer、grant 和精确 run scope；`system_scheduler` 仅允许 expire/cleanup。room membership 不推导 private peer read/direct peer send grant。
3. `AdmissionHead`/`Arbiter` 只接受 `open+(available|aborted)` safe floor。pending、closing、closed 或 generation mismatch 都拒绝；相同 operation+digest 只回放原 winner，异摘要冲突。
4. `FanoutResult` 强制每个 target 恰好一个 receipt，禁止重复、遗漏、aggregate 覆盖 item 状态；`capability_not_supported` 仍是 typed error，不是 receipt 状态。
5. `DisabledService` 仍是唯一 public Service 实现：在 context 取消之外始终返回 `capability_not_supported`，不产生 DB/provider/custody effect。`Owner` 只作为未注册的本地验证 seam，不实现 `Service`；其 `NewFixtureOwner` 明确只供本地 non-authoritative verifier 测试。

## 字段守卫与验证

- `field_guard_test.go` 从 Go struct 反射动态枚举 producer JSON 字段，校验 tag 唯一、可枚举，并对 operation digest 做 one-hot 变异；代码守卫拒绝 persistence/provider/Fx/network/recovery-custody 依赖。
- `contract_test.go` 覆盖跨 scope/unsigned actor 拒绝、单赢家并发 CAS 与精确 replay、逐目标 accepted/rejected receipt、disabled/cancellation 行为；`owner_test.go` 额外覆盖成员授权、target denominator、unknown 不完成、close/abort 和跨 room 隔离。
- 本候选的测试通过仅说明 DTO/状态边界成立，不构成 Room receipt-backed durable owner、崩溃恢复、HTTP bearer 或发布资格。

## 启用前置条件

ROOM、ID、CONTENT、RESOURCE、OUT、TRUST 五份子规格必须获批；随后需要 durable owner、真实 custody/command receipt、exact frozen inventory coverage、crash replay 和 unauthorized zero-side-effect 证据。完成这些前不得把 Room 标为 available 或注册到 public tool surface。
