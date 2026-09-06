Spec-ID: CONTENT
Status: Draft
CONTENT-Production: NO-GO
Depends-On: ID
S0-Owner: ContentOwner for encrypted objects, references, tombstones, and erasure state
S0-Scope: encrypted ingest, content references, deletion fences, crypto-erasure, and retention
S0-Dependencies: ID
S0-Inputs: ticketed plaintext frames, source fences, media metadata, quotas, and retention policy
S0-Outputs: encrypted objects, versioned content refs, tombstones, and key/object receipts
S0-Failure: schema, size, key, or receipt mismatch aborts or quarantines before a custody acknowledgement
S0-Recovery: replay the deletion or ingest saga by original ticket and receipt; preserve deny-read on uncertainty
S0-Field-Chain: producer -> TRUST envelope -> CONTENT ticket -> ciphertext object -> ref -> authorized sink
S0-Named-Acceptance: RED-CONTENT-01 PlaintextBeforePreparedTicket; RED-CONTENT-08 OrphanGCBlockedByFence; RED-CONTENT-09 RestoreAntiRollback; RED-CONTENT-10 EraseReceiptRequired; GREEN-CONTENT-03 CryptoErasureCrashSaga; GREEN-CONTENT-05 OrphanGCCrashReplay; GREEN-CONTENT-06 RestoreTailAndApplyHead; GREEN-CONTENT-07 EraseReceiptChain

# 内容生命周期与加密擦除子规格

## 0. 定位与约束

本文件是 Gate S0 的独立实施子规格，不是已批准实现，也不表示当前 worktree 已完成正文保密或物理擦除。
它细化总规格 §1.18/§1.19、§5.2、§8.1/§8.4/§8.6、§9.3 和 §19 Gate S0 的第3项 CONTENT 节。
ID 是前置：每个对象必须绑定 logical agent、execution/incarnation 或 source fence；没有 ID 的稳定身份不得 ingest。
TRUST 的 provenance/trust/content-role 只作为消费字段，CONTENT 不得借加密把不可信正文升级为控制指令。
SQLite、object store、backup/rollback checkpoint 和 keystore 不是一个分布式 transaction，必须以 receipts 与 CAS 组合收口。

## 1. Owner 与存储边界

ContentOwner 是 ingest reservation、AEAD object/chunk、content ref、deletion intent、tombstone 和 crypto-erasure state 的唯一写 owner。
EventStore、OUT、Room、Resource、bridge、UI projector 和 provider adapter 只能取得 ticket/ref 并消费内容，不能写正文或直接删除对象/密钥。
ContentOwner 的 owner generation、writer/deployment fence、security-time 和 recovery-custody receipt 是所有 mutation 的前置门。
原文只允许存在于调用方受控内存、ContentOwner 的加密 prepared/active object 或有界加密 staging；不得在 event、command、mailbox、relay、temp、backup 产生明文副本。
具体 keystore/object-store 类型、迁移版本和跨故障域布局是 open decision，未冻结前不得以本地文件存在冒充 durable erasure。

## 2. Ingest ticket 与 wire schema

任何正文在首次 durable byte 前先创建 `content_ingest_reservation`，绑定 `object_id`、`object_version`、workspace、ID source fence、owner generation、size/quota/deadline 和 retention policy。
ticket 固定 AEAD algorithm/version、key-wrap version、per-object DEK reference、nonce allocation domain、AAD template、expected media type 和 permitted sink role。
ContentOwner 只把 wrapped DEK 或 opaque encryptor handle 交给已认证 bridge/gateway；raw DEK、handle、credential 和 plaintext hash 不入日志或 spool。
不超过 64KiB 的对象使用 `content.single`；大对象或 streaming 使用 `content.begin|content.frame|content.commit|content.abort`。
`content.single` 必须携 object/version、ticket、source fence、nonce、ciphertext、tag、ciphertext hash、size、media type 和 AAD digest。
`content.begin` 固定 expected size（可空）、hard cap、assembly deadline、algorithm/key version、wrapped DEK、AAD context 和 frame quota。
每个 `content.frame` 固定 offset、length、nonce、ciphertext、tag、ciphertext hash、source/lane sequence；单 frame 不超过 64KiB。
`content.commit` 只在连续 frame prefix、实际总 size、ciphertext root、ticket 和 ref version 都匹配时生成；失败使用 `content.abort` 保存 range/size/hash evidence。
任何 unknown algorithm/key version、nonce reuse、AAD mismatch、frame swap/replay、duplicate offset 或 size/cap 超限在 custody ack 前 quarantine。

## 3. 生命周期、reference 与 deletion state

object `content_state` 固定 `prepared|active|delete_pending|crypto_erased|deleted|corrupt`，key `key_state` 固定 `active|retired_readable|destroy_pending|destroyed`。
唯一正常路径为 `prepared -> active -> delete_pending -> crypto_erased -> deleted`；认证、对象或 key 不一致进入 `corrupt` 只读诊断，不回滚重建明文。
prepared 只接受同 ticket/fence 的 ciphertext；一次性 promote 必须原子校验 object/version、ciphertext root、content policy 与业务 ref。
每条 durable ref 固定 `reference_kind`、owner/source fence、`reference_state=live|revoked_by_deletion|released` 和 object version。
ref index 维护单调 `ref_version`；新增/续期必须以 expected ref version、`content_state=active`、key readable、无 deletion intent 的同库 CAS 完成。
non-terminal command/outbox/mailbox/room、未 materialized ingress、legal/audit hold、backup/rollback checkpoint 都是 deletion fence，不能被普通 read ref 撤销。
首次 deletion 以 expected ref version CAS `active -> delete_pending,key_state=destroy_pending`，关闭新 live ref，并写 `content.delete_pending` 与 deny-read tombstone。
获批 reason 只允许 generated `expired|redacted|policy_deleted` 等 registry 分支；不允许调用方随意定义可绕过 retention 的 reason。
delete_pending 后永远不能新建 live ref；read 只能返回结构化 `content_unavailable(reason,deletion_state)`，不能返回旧 queue/spool 字节。

## 4. Crypto-erasure saga 与恢复语义

deletion worker 只能以 deletion-worker generation、expected ref version、当前 retention/checkpoint fence claim；claim 后重读所有 live refs，不能在 unknown 时调用 keystore。
claim transaction 取得无不可撤销 ref 的 receipt 后，幂等销毁 primary 与 rollback boundary 内所有副本的 wrapped per-object DEK，并保存 keystore receipt。
只有 keystore receipt 成功，ContentOwner 才以同 destroy generation CAS `key_state=destroyed,content_state=crypto_erased`，追加 `content.crypto_erased` 与 `content.redacted|content.expired`。
随后删除 ciphertext/chunks；必须验证 object-store receipt 后才 CAS `content_state=deleted` 并追加 `content.deleted`。
SQLite commit、keystore destroy 或 object delete 任一响应未知时只查询原 deletion/destroy operation，保留 tombstone 和 deny-read，不宣称 crypto erased/deleted。
崩溃恢复按 intent、state、generation、key/object receipt 重放；不能重复创建 live ref、重建 DEK、回到 active 或以 timeout 猜物理结果。
未提交的 prepared ingest 在 deadline 后进入 fenced orphan intent；GC 只能在 object/key receipt 与 recovery-custody head 可读时清理，不能把 prepared 当作可读正文。
append-only event history 不等于永久保存明文；event 可保留 object id/tombstone，而正文和 key 按 retention/erasure 状态返回 unavailable。
provider CLI home、provider remote transcript、A2A remote artifact 不属于 ContentOwner；本地只能删除自身 cache/ref，并返回 remote erasure receipt 或 `remote_deletion_unverified`。

## 5. Tombstone、digest 与 anti-rollback

immutable tombstone 只保留 object id、ciphertext hash/size、workspace-scoped keyed decision/intent digest、policy/decision id、state cursor/time、key/object receipts 和 reason。
tombstone 不保留可离线枚举低熵正文的 plaintext hash；content DEK/KEK 与 idempotency/HMAC keyring 必须分离。
同 object/version/ticket/digest 的 ingest、frame、promote、delete、destroy 操作幂等返回原 record；同 key 异 digest 为冲突，不能创建旁路对象。
canonical idempotency tombstone 固定 digest domain、algorithm、key id/version 和 horizon；旧 verification key 在所有 checkpoint/rollback/audit horizon 越过前不能销毁。
key 缺失、版本未知、checkpoint 回退、tombstone root 不一致或恢复域外 receipt 不可读时进入只读阻断，不得用新 key 重算旧 digest。
checkpoint manifest 必须记录 live object/ref set、deletion intent/state、tombstone、DEK/KEK 与 keyring inventory 以及外部 erasure scope，使 restore 区分 ongoing deletion、erased、lost、remote-unverified。
`output.compacted` 只能在 replacement chunk 与 event 同 transaction 提交后删除旧 delta payload，并保持旧 cursor expired 语义，不得让 cursor 指向不同正文。

### 5.1 Orphan GC、restore 与 anti-rollback 硬字段

prepared ingest 超过 `assembly_deadline` 后只能由 ContentOwner 以 expected `{object_id,object_version,ticket,source_fence,owner_generation}` CAS 写入 `orphan_state=fenced`，并生成 `orphan_gc_id`。orphan record 必须固定 `expected_ciphertext_root`、`expected_size`、`key_id/version`、`key_state`、`live_ref_root/count`、`deletion_fence_root`、`checkpoint_generation`、`recovery_custody_head/root`、`gc_deadline`、`deny_read_tombstone_digest` 和 `gc_receipt_or_absent`；prepared 不能因超时直接变为 deleted。

GC 只有在恢复域外 `recovery_custody/{security_domain_id}` head、object-store receipt、keystore/key-state receipt 和 live-ref/deletion-fence 再读均可验证，且 `live_ref_count=0`、无 non-terminal/backup/legal-hold fence 时，才可 CAS `fenced -> gc_pending -> gc_cleared`。每个 object/key 副作用必须带 `orphan_gc_id`、expected generation/root 和独立 receipt；任一响应 unknown 写 `gc_unknown` 并保持 deny-read/tombstone，只查询原 operation。GC 不得重建 DEK、创建 active ref、覆盖已有 object，普通本机 SQLite/WAL/同盘 backup 不能产生可用 receipt。

restore request 必须是 `{security_domain_id,restore_attempt_id,deployment,checkpoint_manifest_root,checkpoint_generation,checkpoint_absorbed_through,recovery_custody_head_before,tail_range,restore_namespace,writer_epoch}`，并保存 `live_object/ref_set_root`、`deletion_intent_root`、`tombstone_root`、`keyring_inventory_root`、`external_erasure_scope` 和 `namespace_read_back_root/count`。恢复域外唯一 authority 是外部单实例服务独占的私有 SQLite；`deployment`/`restore_attempt_id` 只是 attempt 记录键，不能建立新的 security-domain genesis 或并行 current head。

restore 只能连续重放 `(checkpoint_absorbed_through,recovery_custody.current_committed_head]`，并在 `recovery_apply_head/{security_domain_id}` 上以 expected previous generation 单赢家 CAS；`current_apply_grant_generation` 首次为 `1/0`，后续 deployment/attempt 必须 `previous+1`，旧/跨 domain head、gap、stale receipt、缺 tail 或 `production_open_head` 非 revoked floor 均 fail-first。完成后必须生成不可变 `restore_apply/{security_domain_id,deployment,restore_attempt_id}` receipt，绑定 apply/revoke receipt、manifest/tail/namespace read-back root、ordinary/security/side-effect heads 与 `state=revoked`；仅本地 apply/SQLite commit 不构成 restore 成功。

anti-rollback 校验每次 ingest/promote/read/delete/GC/restore 都必须比较 `security_domain_id`、immutable `installation_lineage`、`checkpoint_generation`、`previous_checkpoint_generation`、`tombstone_root`、`keyring_floor_generation`、`recovery_custody_head`、`security_journal_cursor/root`、`owner_generation` 和 `writer_epoch`。generation 倒退、同 deployment fresh genesis、checkpoint/tombstone/keyring root 不一致、旧 key 重算新 digest 或跨 lineage/domain replay 均进入只读 `rollback_detected|reconciliation_required`，不得 decrypt、创建 ref、销毁 key 或删除 object。

### 5.2 Erasure receipt chain（硬字段）

每次 deletion/GC/crypto-erasure 必须产生稳定 `erase_operation_id` 与 `erase_generation`，并按顺序保存 `deny_read_receipt`、`ref_fence_receipt`、`key_destroy_receipt`、`object_delete_receipt`、`tombstone_receipt`、`recovery_custody_receipt`、`receipt_digest`、`availability`、`state_cursor` 和 `retention_horizon`。receipt 逐项绑定 `{security_domain_id,object_id,object_version,source_fence,expected_ref_version,owner_generation,checkpoint_generation,tombstone_root}`；缺一项或不匹配不能写 `crypto_erased|deleted`。

keystore/object-store/SQLite 任一响应未知时，状态只能是 `outcome_unknown`（远端为 `remote_deletion_unverified`），保留 immutable tombstone 与 deny-read，并以原 `erase_operation_id+request_digest` 查询；禁止生成替代 operation、重建 DEK、清除 tombstone 或声称 erased。只有全部 primary/rollback copies 的 key-destroy 与 object-delete receipts 可验证，且恢复域外 receipt 可读、anti-rollback head 仍 current，才能逐步 CAS 到 `crypto_erased` 再到 `deleted`。

## 6. 字段消费链、权限与容量

消费链固定为：provider/peer/A2A/message/tool/output/artifact producer → TRUST envelope → CONTENT ticket → AEAD single/frame staging → prepared object → active ref → ID event/OUT command/Room transcript → authorized read decrypt → quoted/UI/provider user-data sink。
`object_id`、version、ticket、algorithm/key-wrap version、nonce、AAD digest、ciphertext root/hash、size、media type、ref_version、retention policy、deletion state 和 receipts 必须各有 producer/consumer。
读取必须由 transport-authenticated principal、workspace/run、content ACL/ref generation、current incarnation/source fence 和 retention state 联合授权；agent_id/handle 本身不赋予正文权限。
解密仅在有界内存临时完成；UI、bridge、projector、spool、event、report 和 provider transcript 不得写回 plaintext。
第一版单 message/prompt/final-output/tool-result object 上限 8MiB，本地 artifact 上限 64MiB；provider/room/A2A 超限立即 abort，不能动态放宽。
每 principal 最多 2 个并发 ingest、每 workspace retained content 最多 1GiB、进程全局最多 4GiB；具体数值在 RESOURCE/压测决策中复核，不能使用默认值绕过。
每 source 的 staging quota、全局 encrypted staging quota、assembly deadline、backup/rollback fence 和 recovery-custody head 必须纳入 resource snapshot；缺 snapshot 立即拒绝。

## 7. 当前差距（不关闭）

批准前源码审计曾确认不存在ContentOwner、AEAD object/ref、wrapped DEK/KEK provider、destroy/query receipt或erasure saga。2026-09-02后续已实现[CONTENT-1 encrypted-single候选](../../plans/orch-subagent-upgrade/content-1-encrypted-single.md)：migration132 metadata/ref/legacy quarantine、AES-GCM/key-provider port、private object backend、stable prepare与active/live ref；assembler也已拒绝把caller candidate直接写入draft。真实key provider、decrypt/destroy/erasure、proof/Fx/tool和发布证据仍缺失，因此本完整规格继续保持Draft。sharedfile仍是明文存储，不能复用为CONTENT backend。
更直接的旁路是 migration124 direct-peer `body TEXT` 与普通 send_message 明文 turn submission；在 CONTENT owner 接线前必须保持旧路径与 canonical send profile隔离，不能启用生产 cross-task send。
总规格要求的 per-object DEK、所有大小正文 encrypted-first、ref_version CAS、删除/擦除 saga、anti-rollback 和 remote erasure classification仍无 GREEN 证据。
ST-08 已有 direct-peer 事件/outbox 最小闭环，但不证明完整 content object/ref、跨恢复域 custody、消息正文加密或 receipt chain；ST-11/16/18 仍待验证。
ST-04 的空凭据、权限、manifest/schema 副作用审计仍未完成，不能把配置 fail-fast 当成 content boundary 通过。
当前本地测试均不能证明 keystore/object-store/backup 的真实物理擦除；`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 证据不得升级为 deleted 或 crypto_erased。
具体 provider remote erasure 能力、content retention、KEK rotation/restore、ciphertext backend、跨故障域 evidence/anchor 与 migration writer 仍是 open decision；RecoveryCustody数据库方向已冻结为外部单实例服务独占私有SQLite并排除PostgreSQL，但local candidate不能关闭这些生产缺口。见[生产决策门](../../plans/orch-subagent-upgrade/content-resource-custody-decision-gate.md)。

## 8. 具名 RED/GREEN 验收

RED-CONTENT-01 `PlaintextBeforePreparedTicket`：无 ingest ticket 时写 relay/raw/staging/temp/event/backup 正文失败，且 durable byte、DEK 和 plaintext hash 均为零。
RED-CONTENT-02 `InlineSmallSecret`：小于 64KiB 的 prompt/output/secret 以内联明文进入 command/event/projection 时 fail-first；只能产生 encrypted content ref。
RED-CONTENT-03 `AEADSchemaViolation`：unknown algorithm/key version、nonce reuse、AAD/source fence mismatch、frame swap/replay 或 cap 超限不取得 custody ack。
RED-CONTENT-04 `RefAfterDeletePending`：delete_pending 后任何新 live ref/续期失败；旧 ref 只能按 generated revoke/release 分支收口。
RED-CONTENT-05 `DeletionFence`：non-terminal command、未 materialized ingress、backup/checkpoint 或 legal hold 存在时 deletion 不得 claim destroy。
RED-CONTENT-06 `UnknownDestroyReceipt`：keystore/object-store 响应丢失或崩溃时保持 deny-read/tombstone，不能展示 crypto_erased/deleted 或重建 key。
RED-CONTENT-07 `RemoteEraseNotLocal`：provider/A2A 远端无 erasure receipt 时返回 `remote_deletion_unverified`，不得伪造远端已擦除。
RED-CONTENT-08 `OrphanGCBlockedByFence`：prepared deadline 后若 live ref、non-terminal/backup/legal-hold fence、object/key receipt 或恢复域外 custody head 任一缺失，GC 保持 `fenced|gc_unknown`，无 delete/key-destroy 副作用。
RED-CONTENT-09 `RestoreAntiRollback`：restore 使用旧 generation、fresh deployment genesis、跨 security domain/lineage checkpoint、tail gap、stale apply head 或非 revoked production floor 时拒绝，apply/decrypt/DB commit 均为零。
RED-CONTENT-10 `EraseReceiptRequired`：缺任一 ref/key/object/tombstone/recovery-custody receipt，或响应 unknown/receipt digest 不匹配时保持 deny-read/`outcome_unknown`，不得写 `crypto_erased|deleted` 或重试新 operation。
GREEN-CONTENT-01 `PreparedPromotionAndFrameReplay`：single/frame 在重启后按原 ticket、nonce、sequence 幂等 promote，连续 root/ref 与 receipts 一致。
GREEN-CONTENT-02 `RefVersionDeletionCAS`：引用 acquire/renew 与 deletion 竞争只有一个 CAS 赢家，state/ref_version/tombstone 逐字段可重建。
GREEN-CONTENT-03 `CryptoErasureCrashSaga`：在 DB/keystore/object delete 每个窗口崩溃后恢复到唯一 state，不重复销毁、不回 active。
GREEN-CONTENT-04 `AuthorizedReadAndTaint`：合法 caller 只在临时内存读正文，TRUST role/provenance 保持 quoted，UI/network/file sink 无隐式副作用。
GREEN-CONTENT-05 `OrphanGCCrashReplay`：GC 在 fence 检查、key destroy、object delete、tombstone commit 各窗口崩溃后，按原 `orphan_gc_id` 恢复到唯一状态；不创建 ref、不重建 DEK，receipt/root/count 可重算。
GREEN-CONTENT-06 `RestoreTailAndApplyHead`：从 checkpoint 连续重放恢复域外 tail，`recovery_apply_head`/apply grant 严格 generation+1，生成 attempt-scoped immutable `restore_apply` receipt；并发/响应丢失只保留一个 winner。
GREEN-CONTENT-07 `EraseReceiptChain`：删除、DEK destroy、object delete、tombstone 与 recovery-custody receipts 逐字段链接，重启/重复请求幂等；只有完整 chain 才能从 deny-read 推进 `crypto_erased -> deleted`。

## 9. 退出条件与 open decision

退出必须具备 ContentOwner 唯一 writer、generated AEAD schema、prepared/active/ref/delete state graph、keyring/horizon、recovery receipt 和字段消费差集为零；orphan-GC、restore、anti-rollback 与 erase receipt chain 的字段/消费者差集必须为零。RecoveryCustody 必须保持外部单实例服务独占私有 SQLite；本地 SQLite、WAL、同盘备份或测试 fake 均不能升级 receipt/availability。
所有具名 RED/GREEN、ingest/crash/unknown/restore/remote-erasure fixture 需要真实可复查 attestation；普通 SQLite 单测不等于物理擦除。
必须证明 ID execution/source fence、TRUST taint、RESOURCE quota 和 OUT command/cleanup ref 在首次 durable byte至deleted全链路逐字段绑定。
Open decision A：算法/nonce/AAD canonical profile、KEK provider与key rotation/retire/destroy；RecoveryCustody已固定外部单实例私有SQLite方向，但production lease、协议、外部finalizer/anchor和验签proof仍待实现。
Open decision B：object/chunk store、backup/rollback/checkpoint 的具体引用枚举、retention/legal hold 与 GC 安全窗口。
Open decision C：provider/A2A remote erasure capability、receipt semantics、remote-unverified UI/API 错误和大小/配额的最终 policy。
Open decision D：generated content schema、sqlc/migration owner、staging quota、assembly timeout 及 `corrupt`/lost restore 分类。
在 ID 获批、上述决策关闭、真实 erasure/recovery evidence 齐全前，本文件保持 `Status: Draft` 与 `CONTENT-Production: NO-GO`，不启用正文外部 dispatch 或自动删除。
