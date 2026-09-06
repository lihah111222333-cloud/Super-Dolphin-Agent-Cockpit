# CONTENT-1 encrypted-single 实施记录

Status: Active

日期：2026-09-02；上级：[CONTENT / RESOURCE / Recovery Custody生产决策门](content-resource-custody-decision-gate.md)与[CONTENT完整草案](../../proposals/orch-subagent-s0/content-lifecycle-and-crypto-erasure-spec.md)。

本记录只描述已批准CONTENT方向中的第一实现切片。它不把CONTENT完整草案、OUT、RESOURCE、RecoveryCustody、cross-task send或Room标成完成。

## 已实现范围

CONTENT-1当前只接受`content.single`，明文大小为1..65536 bytes。实现链为：

```text
ReserveIngest (BEGIN IMMEDIATE)
  -> stable prepare operation preflight (BEGIN IMMEDIATE)
  -> KeyCreatorSealer AES-256-GCM
  -> private contentobject Put + durability barrier
  -> prepared object + private receipt + sealed reservation (BEGIN IMMEDIATE)
  -> active object + live ref + promoted reservation (BEGIN IMMEDIATE)
  -> send_message profile insert trigger
  -> held command deletion fence in the same transaction
```

源码边界：

- `internal/platform/contentkeychain`已有`darwin && cgo`的真实Security.framework adapter源码，面向显式指定的私有file keychain提供AES-256 key lease create/replay；非darwin或关闭cgo时返回`ErrUnsupported`。下述K4修复及K3本机原生生命周期测试已通过，但不表示production接线完成或可生产使用；
- `internal/platform/contentcrypto`拥有AES-256-GCM密封原语、短期raw-key lease和opaque key metadata；raw key、plaintext和ciphertext不进入通用JSON；同operation下实际明文字节改变会改变provider request digest并触发冲突，防止key/nonce复用；
- `internal/platform/contentobject`拥有private state-dir ciphertext envelope、no-replace发布、文件与目录fsync、exact replay和outcome-unknown；SQLite不保存ciphertext/tag/raw key；
- `cmd/mcp-orch/store/content`是reservation/object/ref metadata的唯一Go写owner；它在外部I/O前登记由ticket派生的稳定operation，并在短事务中记录receipt或完成promotion；
- migration132创建CONTENT metadata表、command deletion fence和legacy quarantine；所有表为STRICT、canonical text/lower-hex约束，CONTENT表禁止BLOB及raw secret列；
- `SendProfileStore`读取或replay完整profile前，要求`profile -> held fence -> live ref -> active/key-active object -> promoted reservation`精确联接。

## Crash、重放与deadline语义

- reservation首次创建只允许未来assembly deadline；已经提交的exact reservation在deadline后仍可返回原ticket/object，不能把响应丢失误报成不存在；
- prepare开始前以`BEGIN IMMEDIATE`登记唯一stable operation和开始时间。登记成功后即使object已提交、metadata事务失败且重试跨过deadline，也只能用原operation恢复；
- caller选择的不同operation在crypto/object调用前返回replay conflict；
- seal result必须重新绑定实际SealRequest、AAD digest、plaintext size和key-wrap profile；object receipt必须绑定finalized Put request digest及完整object/hash/size tuple；
- object link后temp cleanup或目录fsync失败返回`ErrObjectCommitOutcomeUnknown`和零receipt。后续相同Put必须重新通过目录durability barrier，不能只因文件可读就升级为成功；
- `QueryPut`只表示当前封装可读且匹配，不是durable Put receipt。

## 身份、引用与旧数据升级

- reservation保存完整ID-1 current binding；创建时、Promote时和ResolveActiveRef时均要求latest active exact binding。source revoke或换代后，promotion/resolve fail-closed；
- Promote只允许`sealed -> object active/ref_version=1 -> live ref -> reservation promoted`，同ticket exact参数返回原ref，异参数冲突；
- migration132不从旧`request_content_ref`猜object、ticket或owner proof。非空v131 profile原样保留，并写入append-only `orchestration_legacy_command_content_quarantine`，原因固定为`legacy_missing_content_owner_ref`；
- legacy profile没有自动ref/fence。Get、GetByKey和Reserve replay返回`ErrIncompleteIntent`+`ErrLegacyContentProofMissing`；历史记录如需重试必须形成新的授权/content链，不能原地补权。

## 已验证

本机固定开发cache的精确测试已覆盖：

- contentcrypto真实AES-GCM 1 byte/64KiB roundtrip、AAD认证、同operation实际正文冲突、nonce slice所有权、raw key覆写和JSON secret guard；
- contentobject Put/Query/reopen/concurrency、no-replace、strict envelope、path脱敏、symlink静态拒绝和directory-sync outcome-unknown恢复；
- Content Store reservation replay/conflict/deadline、stable preflight、object-commit/DB-failure crash窗口、receipt伪造、prepared/promote/resolve、binding rollover、SQLite敏感字节扫描和动态字段链；
- migration132 fresh/idempotent、非空v131 quarantine升级、canonical column负测、state/relation/immutability、profile/fence原子回滚；
- schema floor/table/column/trigger和metadata-only/BLOB启动门禁；
- SendProfileStore fresh/replay/Get/GetByKey均要求完整CONTENT relation，legacy quarantine fail-closed。

历史 host-light 观测中 `Content Store`（archtest 139.469s、业务0.960s）与
`contentobject` durability（archtest 159.245s、业务0.057s）曾 exit 0；当时官方
`--light-guard-only` 在 production-ratchet 25 项阶段 fail-fast、未进入未提交 freeze
baseline drift 阶段，是历史快照，不代表本次原 guard 仍失败。上述证据仍限于
`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；race、受信 Ubuntu/Windows 逻辑运行时、
发布制品和远程证据均未取得。

### 2026-09-06：历史真实keychain生命周期RED与根因

- 原单元合约`TestKeychain`以及field-loss RED/GREEN验证已通过，但当时K3真实生命周期仍为RED；不能以单元合约替代原生生命周期验收。
- 本次真实日志：`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/content-keychain-live/native-lifecycle.log`。原archtest guard **142.627s PASS**，随后`TestContentKeychainNativeLifecycle`首次seal在**0.01s**失败，错误为`ErrUnsafePath`。这不是全守卫仍失败，也不是全仓库或远程PASS。
- 观察到native操作前后目标keychain inode由`204763683`变为`205002024`，文件mode保持`0600`。[Apple Security的AtomicFile.cpp](https://raw.githubusercontent.com/apple-oss-distributions/Security/main/OSX/libsecurity_filedb/lib/AtomicFile.cpp)中`AtomicTempFile::commit`使用临时文件rename覆盖；这一源码依据支持OS正常写入可以改变inode，不能把原Go pinned-inode不变测试视为最终正确契约。
- 原host medium精确入口的test-only forwarding recorder诊断已完成：`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/content-keychain-live/native-lifecycle-diagnostic.log`记录archtest **149.574s PASS**；`initial-read`时inode不变，`Add`返回ok且inode改变，`readback`返回ok，最后`post-identity`报`ErrUnsafePath`。该因果链将本次失败定位到native写入后的Go身份复查，而不是Add或readback失败；诊断完成本身不等于K3生命周期通过；该诊断时点仍为RED、尚未改production行为，阻止了当时的production接线。

### 2026-09-06：K4受控身份刷新修复与K3本机实测GREEN

- K4单元验证：`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/content-keychain-live/atomic-replacement-green.log`记录archtest **173.055s PASS**、`TestKeychain` **0.021s PASS**。
- provider/record/identity仍保留precall检查；仅在`Add`成功或duplicate分支之后，完整readback与lease均已成功时，才允许受控刷新文件身份。pinned父目录和坏ack等负测继续保留；这不是无条件接受任意新inode。
- K3本机真实测试：`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/content-keychain-live/native-lifecycle-green.log`记录`TestContentKeychainNativeLifecycle` **0.03s PASS**、package **0.036s PASS**。覆盖真实Add/readback、受控重新pin、跨独立进程使用同一built binary的replay、AES-GCM解密固定测试明文、equal-length plaintext冲突、新operation产生不同key/nonce、密文落盘及本测试DEK有限负面扫描。
- 跨进程证据仅限同一built binary，不涵盖跨签名或安装升级；固定测试明文解密不等于production授权读取能力，有限DEK扫描也不是全面无泄漏证明。跨平台实机、发布和远程证据仍未取得。
- 锁定实测已PASS：`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/content-keychain-live/native-locked.log`记录archtest **173.241s PASS**、`TestContentKeychainNativeRejectsLocked` **0.01s PASS**、package **0.017s PASS**。测试使用先真实锁定的全新fixture，独立bounded child返回分类`ErrNative`而非超时；测试后helper再次inspect并delete。
- 四个任务-owned keychain文件已全部清理；最终结果`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/content-keychain-live/EC0B296A-A673-48C8-8371-8C4AF997FF21.result.json`为`operation_succeeded=true`、`side_effect_detected=false`，最终default/search快照与初次create前完全一致。密码文件仍以0600保留在任务私有目录，原manifest与日志保留；这不表示所有磁盘痕迹已清除，也不是crypto-erasure验收。
- K4最初`atomic-replacement-red.log`中的四个预期FAIL已由上述修复与GREEN验证闭环。map-refresh/check/codemap-check已PASS，`maps-final.log`记录5612files/8domains；`s0-final.log`中的S0 47项离线测试及planning-only NO-GO检查已PASS。这些均不代表production装配、全目标完成或可发版。
- LSP仅确认production四个文件诊断为0；LSP仍回报与当前源码位置/真实编译矛盾的旧接口诊断，未记为LSP全绿。总`TestKeychain`真实编译执行和`go vet -tags=e2e`均已PASS。

## 明确未实现或未启用

- 已有上述macOS OS secure storage adapter候选及限定范围内K3本机生命周期GREEN，但production KeyLeaseCreator仍未接线；没有KMS/Vault adapter，测试key provider不能代替真实OS验证；
- 没有production decrypt授权读取/read-to-provider、destroy/query-destroy、delete/GC、crypto-erasure或remote deletion receipt；
- private object backend仍有外部攻击者在检查与文件操作之间替换目录的跨平台dir-handle/open-at TOCTOU残余，不能在生产接线前忽略；
- 没有commandintent CONTENT proof issuer、post-profile admissionproof adapter、config/Fx/tool/manifest/runtime生产接线；production仍使用unavailable/reject-all seam；
- RESOURCE-1 reserved候选已实现，但没有claim/permit/usage/release；外部单实例SQLite RecoveryCustody authority与协议候选虽已落地，生产服务仍未装配；
- migration124 direct-peer明文`body TEXT`及普通明文send路径仍必须与canonical profile隔离；
- 无accepted、permit/nonce、provider receipt、outcome reconcile、public cross-task send或Room能力。

因此CONTENT-1当前仍是未装配owner候选：已有本机合同测试及限定K3真实OS生命周期GREEN证据，但production装配和完整授权/销毁生命周期尚未完成；不是可发送、可解密、可擦除或可发布的生产能力。总计划必须继续保持Active。
