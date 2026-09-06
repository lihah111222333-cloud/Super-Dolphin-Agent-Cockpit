# Managed runtime endpoint/process binding candidate

Status: Candidate — isolated, fail-closed, not production-ready

本候选只定义 standalone managed runtime 的 endpoint 与 prepared process receipt
绑定边界，位于 `cmd/mcp-orch/standalone/managedruntime/issuer`。它不是
`CallerPrincipal`/child credential issuer，也不是 provider bridge、supervisor 或
授权 owner；不读取或保存 raw bearer，不使用 operator principal，不启动 provider，
不接入 Fx、数据库、PostgreSQL 或 public tool registry。

## 固定边界

- Endpoint 只接受带显式端口的 `http://127.0.0.1:<port>/mcp`，拒绝 userinfo、
  query（包括空 `?`）、fragment（包括空 `#`）、非 loopback、路径漂移和非 canonical
  JSON；URL 与 instance ID 由 domain-separated digest 固定。
- `BindRequest` 只携带 credential lease/digest、provider/profile digest、parent
  execution incarnation/fence digest、connection nonce digest、endpoint identity 和
  managed runtime operation digest；不携带 token、principal、argv、环境或进程句柄。
- `ValidateBind` 必须先通过显式只读 `ProcessReceiptVerifier`，返回带私有 verified
  marker 的 `PreparedReceiptProof`，再逐字段比较 managedruntime `PrepareRequest`、
  `PreparedReceipt`、provider/session/runtime/process generation 与 binding receipt。
  缺 verifier、typed-nil、取消、receipt 拒绝或任一 digest/fence 漂移都 fail-closed，
  不产生 binding 结果。
- 该 port 与现有 `managedruntime.ProcessReceiptVerifier` 形状兼容，但当前没有 supervisor
  调用者或 production 注入；兼容性断言只防止字段/签名漂移，不构成实际进程证明。
- 生命周期只允许 `candidate -> prepared -> bound -> revoked`，仅 prepared 可经
  独立 cleanup `prepared -> aborted`；`outcome_unknown` 只能在 query projection
  出现，不能作为 mutation 结果或继续迁移。
- Abort/Revoke 必须各自使用独立 operation、expected receipt 和 closure evidence；
  abort 不能携带 binding operation，revoke 不能伪造 prepared-only closure。
- `DecodeBindRequest` 等 wire decoder 拒绝 unknown、duplicate、null、trailing、
  非 canonical JSON，并限制 payload、深度和成员数。

## Supervisor 状态机候选

`cmd/mcp-orch/standalone/managedruntime/supervisor.go` 现在提供一个明确标注为
candidate-only 的 `CandidateSupervisor`/`MemorySupervisor`。它只用互斥锁和进程内
值快照验证 PREPARE、COMMIT、ABORT、QUERY 的状态迁移：同 operation ID + digest
回放同一 receipt，异 digest 冲突，COMMIT/ABORT 对同一 prepared record 单赢家；
QUERY 严格返回 one-hot receipt 分支。由于真实 PREPARE 必须由外部 owner 提供
process/provider receipt，候选通过 `SeedPrepared` 接收已经独立校验的 receipt，绝不
合成 PID、provider session、opaque handle 或启动 helper。

候选构造要求显式 `ProductionPrerequisites` 证据形状；这些 ref/digest 不是实时依赖
证明。`MarkOutcomeUnknown` 只用于故障注入并把 owner 永久切到 query-only fence，
后续 mutation 统一返回 `ErrSupervisorFenced`，原 operation 只能通过 QUERY 读取
`outcome_unknown`。该 owner 不接 Fx、DB、registry、unified、Room、provider 或
transport，不能替代真实可重连、durable、带签名验真的 production supervisor。

## 当前证据与缺口

`contract_test.go` 覆盖 endpoint/receipt round-trip、verifier 缺失/拒绝/取消、
provider/profile 与 parent/nonce fence 双改、abort/revoke 状态一致性、完整生命周期
矩阵、query one-hot/unknown-only、raw bearer/operator/production import guard；
`json.go` 覆盖 strict wire decode。测试只证明本地 DTO/校验边界，不能证明真实
supervisor、credential activation、nonce redemption、provider eager-connect、
identity/current binding、resource/content/OUT/custody 或跨进程 recovery。

后续若要接线，必须先由独立 owner 提供真实 process receipt verifier、provider profile
probe/attestation、parent/current fence 来源、credential/nonce 生命周期、durable
operation/query 与 RecoveryCustody，再建立单向 adapter 和对应跨进程 crash/replay
证据；不得把本候选的测试 verifier、内存状态或 digest 形状升级为授权或 production
exactly-once 事实。
