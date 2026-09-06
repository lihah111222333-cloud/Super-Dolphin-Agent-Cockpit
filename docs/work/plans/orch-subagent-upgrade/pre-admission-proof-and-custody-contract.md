# ST-11 CONTENT/RESOURCE Proof 与 RecoveryCustody 合同

Status: Active

日期：2026-09-02；上级：[send_message_v1 Profile](send-message-intent-profile.md)与[生产决策门](content-resource-custody-decision-gate.md)。

## 已实现的 non-authorizing 链

本段记录先于migration132完成的fail-closed合同切片：它当时不新增migration132，也不实现CONTENT/RESOURCE/RecoveryCustody authority。后续[CONTENT-1实施记录](content-1-encrypted-single.md)、[RESOURCE-1实施记录](resource-1-reserved-skeleton.md)与[RecoveryCustody SQLite authority记录](recovery-custody-sqlite-authority.md)已分别新增local candidate；它们仍未形成production Fx/tool/provider/command lifecycle组合，production custody继续unavailable/reject-all。

### 1. profile 前的 CONTENT proof

`commandintent.Assembler` 现在要求必需的 `ContentAdmissionProofIssuer`：

```text
transport principal
  -> accesswait authorization
  -> source/target current binding
  -> CONTENT proof request（不含 candidate command ID）
  -> owner-issued canonical/versioned active ref
  -> profile HMAC
  -> atomic base intent + migration131 profile
```

caller 的 `RequestContentCandidateRef` 只作为CONTENT owner lookup候选，不能直接赋给 immutable `Intent.RequestContentRef`。proof request绑定caller-stable idempotency key、principal/scope、authorization request/grant revision和双端fence；candidate command ID改变时proof request完全相同。proof缺失、unavailable、摘要伪造、request/fence不匹配或过期时，signer和reserver调用数均为0。

当前production composition仍只装配`UnavailableContentAdmissionProofIssuer`；migration132 CONTENT Store尚无proof adapter/Fx接线，因此production仍不能新建profile draft。测试fake/key provider只在`*_test.go`中使用。

### 2. profile 后的 admissionproof Gate

`admissionproof.Gate` 读取完整 `CompleteIntentRecord`，先重新验证migration131 HMAC，再按不含content ref/object/ticket或resource reservation/policy的command-bound key调用两组窄口：

- CONTENT resolver + verifier：要求active object、live ref、readable key、object/ref版本、source fence、retention digest、owner/writer/security-time proof；
- RESOURCE resolver + verifier：要求reserved reservation、policy revision/hash、effective-limits digest、usage watermark/digest、release-condition digest、identity/content join与owner/writer/security-time proof。

snapshot的SHA-256只证明内部字段完整性，不证明owner authority；独立verifier必须回验current owner record和attestation。当前production只提供Unavailable resolver与RejectAll verifier，不提供allow-all/static/in-memory available实现。

Gate把完整authorization与source/target selector分别做domain-separated digest，再把CONTENT/RESOURCE snapshot digest、原幂等key一起绑定到batch digest，最后构造 `recoverycustody.SubmissionRequest`。Gate不持久化、不调用custody Client、不生成Outcome，也没有available/accepted/dispatch/provider/permit/token字段。

### 3. RecoveryCustody transport-neutral 合同

`recoverycustody` 定义：

- 完整 `SubmissionRequest` 与stable `SubmissionHandle`；
- `pending|indeterminate` unresolved形态；
- 未来外部authority的 `availability|abort_tombstone` receipt；
- external `ReceiptVerifier` 与availability/abort单赢家reducer；
- `UnavailableClient` 与 `RejectAllReceiptVerifier`。

产品本地SQLite、file、WAL与heteroci形状receipt均被RejectAll拒绝。`sqliteauthority`现已提供三层digest、数据库 revision 3（持久化 external-service mode）、DB-resident writer fence、私有schema/state-machine、SQLite完整性join和`KernelAuthorityLease`候选；非MCP `rpcwire`/`authorityhttp`/`httpclient`提供identity/submit/query、opaque bearer、loopback/no-retry transport候选。独立服务 CLI 目前只有构造/生命周期候选，durable reconcile coordinator 有可执行候选，但仍缺注入的真实外部 finalizer/verifier、anchor、发布验签能力和production orch消费端。

timeout/unavailable只返回同一handle的indeterminate；同operation异request digest冲突；availability或abort胜出后，另一分支的late receipt返回resolution conflict。默认adapter永远不能返回available/aborted或推进command。

## 当前验证状态

| 精确入口 | 当前状态 |
| --- | --- |
| raw recoverycustody全子包 | PASS；包含transport-neutral、sqliteauthority、rpcwire、authorityhttp、httpclient候选 |
| `TestSQLiteAuthorityLifecycle` host-light | 历史观测 archtest 200.221s、business 0.044s、wrapper exit 0；统一 wrapper 当前在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段 |
| authorityhttp host-light | 历史观测 archtest 227.785s、business 0.011s、wrapper exit 0；统一 wrapper 当前在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段 |
| 全局门 | direct code/entry guard、相关 build/vet 与 `make orch-upgrade-check` 当前通过（Python 29 tests 与 dependency-map check）；统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段 |

以上均为`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；Windows只有test-binary compile。它们不是真实CONTENT/RESOURCE owner、独立custody服务、外部anchor、发布制品或provider effect证据。

## 尚未实现

- CONTENT-1 migration/store/local object候选已实现；production key provider、proof/admission adapters、Fx/tool、decrypt/destroy/erasure仍未实现；
- RESOURCE-1 effective policy/reserved snapshot与usage-disabled schema已实现；production adapter、aggregate counters、claim/permit/usage/release仍未实现；
- 外部单实例SQLite RecoveryCustody的production supervisor/CLI生命周期、配置发布、network FS证明、真实恢复域外EvidenceFinalizer/anti-rollback anchor、可发布receipt签名/验签、以及durable coordinator的production runner接线；数据库 revision 3（含 external-service mode）、DB-resident writer_epoch/takeover、local kernel lease、typed reconcile、CLI与coordinator虽有可执行候选，仍不能关闭本项；
- command `recovery_custody_pending`/accepted、command_submission wait、dispatcher/attempt/permit/nonce/provider receipt；
- public cross-task send或Room。

因此总计划仍为Active，OUT/ID/CONTENT/RESOURCE/ROLLOUT完整规格仍为Draft，production cross-task send、managed provider、Room与release继续NO-GO。[生产决策门](content-resource-custody-decision-gate.md)已冻结外部单实例服务独占私有SQLite并排除PostgreSQL，但只有真实owner、协议、外部anchor、可发布验签terminal proof及orch adapter/Fx/tool/runtime齐备后，才可推进下一状态。
