# CONTENT / RESOURCE / Recovery Custody 生产决策门

Status: Active — Direction Approved; Local Candidate Only

日期：2026-09-02；父工作包：[Gate S0](README.md)。本文件记录已批准的生产方向、仍未冻结的部署值和实现证据边界；批准不授予 dispatch。

## 批准前源码审计结论

已按仓库路径发现契约读取 README、codemap README、02/07/08/10 分卷、AI_PROJECT_MAP 与 ai-index。早期增量审计曾记录 `node scripts/generate_ai_project_map.mjs --full` 的 5111 files/8 domains；该数字仅是历史快照，不能代表当前地图。最终 `make project-map-refresh PROJECT_MAP_ARGS=--full && make project-map-check` 已通过（5461 files/8 domains、`drift=OK`）。刷新后的索引仍未发现下列生产 owner，随后在 `cmd/mcp-orch`、`internal/{platform,module,store,contract}`、`sql` 与 `go.mod` 定向检索确认缺失：

- 无 `ContentOwner`、AEAD object/ref、wrapped DEK/KEK provider、key destroy/query receipt、crypto-erasure saga；
- 无 `ResourcePolicyRepository`、`AdmissionRepository`、durable reservation/usage/release ledger 或 effect permit；
- 无 `RecoveryCustodyOwner`、恢复域外 availability/abort 单赢家或 quorum attestation。

相邻实现不能冒充：

- sharedfile 是明文 SQLite/普通文件存储；rename/fsync/SHA-256/os.Remove 不是 encryption 或 crypto-erasure；
- prompt token budget、tool-result/LSP budget和 DAG `BudgetLimit` 是提示/内存/透传字段，不做 durable admission；
- appupdaterecovery 与 mcpcontrol 是同机文件/SQLite crash recovery；
- heteroci PostgreSQL 是 remote-CI 专用 schema/role/state machine，没有 orchestration availability-vs-abort CAS，也未证明同步 quorum/故障域；不得复用其 authority。

当前必须保持 disabled 的旁路：migration124 direct-peer 将 `body TEXT` 明文落库；普通 `send_message` 也直接构造明文 provider turn。它们不能接入 migration131 profile 并声称 canonical cross-task send。

## 2026-09-02 批准记录

用户已批准按本文件推荐方向继续实施：

- CONTENT 使用 encrypted-single、AES-256-GCM、per-object DEK、SQLite metadata/ref 与独立 private state-dir ciphertext backend；key provider必须显式配置且无fallback；
- RESOURCE 使用版本化显式policy、关闭的维度/单位集合、无默认预算和append-only reservation/usage；
- RecoveryCustody 使用独立外部单实例authority服务；该服务独占自己的SQLite，orch只经协议访问，不直接共享/打开authority文件。SQLite/WAL/FULL本身不能自证恢复域外可用性，terminal receipt仍必须绑定外部evidence/anti-rollback anchor。

这次批准关闭的是数据库方向选择，不关闭总计划，也不把未完成的KMS、RESOURCE lifecycle、SQLite authority生产服务/协议/anchor、Fx/tool接线或发布证据标成完成。OUT/ID/CONTENT/RESOURCE/ROLLOUT完整规格继续保持Draft。

## 推荐生产方案

### D-CONTENT：encrypted-single 第一阶段

推荐先实现 `CONTENT-1`，仅支持不超过64KiB的 `content.single`：

- Go 标准库 AES-256-GCM、每对象随机256-bit DEK、版本化 nonce/AAD canonical profile；
- metadata/ref/ticket/CAS 存 SQLite，ciphertext 存私有 state-dir object backend；绝不复用 sharedfile；
- DEK 只以 opaque wrapped handle存在；KEK adapter按平台/部署显式配置且无 fallback：本地桌面使用 OS secure storage，服务部署使用外部 KMS/Vault；
- `KeyCreatorSealer`、`KeyOpener`、`KeyDestroyer+QueryDestroyOperation` 分能力注入；第一阶段只装配 create/seal，不装 destroy/read-to-provider；
- send assembler 删除 caller-supplied candidate ref，只消费 ContentOwner 签发的 active ref/object/ref version/media/size/source fence；command/profile 与 command-content deletion fence 同事务写入。

### D-RESOURCE：显式 policy，无默认预算

推荐新建独立 `resourceadmission` owner，不扩写 identity/taskdag/send-profile store：

- 版本化 policy snapshot + precedence trace + canonical hash；
- 关闭枚举维度与单位：agent/count、in_flight/count、fanout/count、concurrency/count、wall_time/ms、input_tokens/token、output_tokens/token、cost/microunit、content_bytes/byte、ingress_bytes/byte；
- policy、适用 scope 或任一维度缺失即拒绝；客户端只能收紧；
- reservation/operation journal/usage ledger 全部 append-only/CAS；未知结果只查原 operation；
- launcher/provider/Room target 最终只接受有效 effect permit，不保留无 permit 兼容入口。

具体 hard cap 与 provider telemetry 来源必须作为配置/发布制品的版本化输入，不能由代码补默认值。

### D-CUSTODY：外部单实例SQLite authority

用户最新裁决为独立外部单实例服务独占SQLite，而不是PostgreSQL：

- authority进程独占private SQLite DB和single-instance lease；启用WAL、`synchronous=FULL`、foreign keys与`BEGIN IMMEDIATE`，但客户端不得直接打开或通过网络文件系统共享DB文件；
- security-domain head、append-only submission/resolution/terminal receipt与availability/abort单赢家均由该进程唯一写；commit unknown只查询原operation；
- SQLite本机commit只允许返回`pending`。只有外部EvidenceFinalizer成功绑定恢复域外副本/anchor、security-time、head root、signature和retention后，才可写`available|aborted` terminal receipt；
- 普通SQLite文件、WAL、同盘backup或本机lock均不能冒充quorum、anti-rollback或恢复域外receipt；
- owner succession/takeover必须由外部单调anchor证明，不能通过复制/回滚SQLite文件重置generation。

authority未配置、single-instance lease缺失或外部evidence不可用时，production adapter只能返回`ErrRecoveryCustodyUnavailable`或stable`pending|indeterminate`；不得返回available/aborted，不推进accepted/dispatch。

当前源码已到[local SQLite authority/transport candidates](recovery-custody-sqlite-authority.md)：数据库 revision 3（持久化 external-service mode）、DB-resident writer fence、三层digest、SQLite完整性join、`PinnedDirectoryLock`+`KernelAuthorityLease`及非MCP `rpcwire`+`authorityhttp`+`httpclient`均已实现，并有架构守卫禁止普通orch import/open authority DB。独立服务 CLI 目前是构造/生命周期候选，durable reconcile coordinator 才有可执行候选；仍缺注入外部 finalizer/verifier、production supervisor/Fx/OUT接线与发布配置，production composition保持`UnavailableClient`+`RejectAllReceiptVerifier`。

## 已完成的本地候选切片（非生产）

- assembler候选已要求pre-profile CONTENT owner proof，caller candidate不再直接持久化；当前无production issuer时零profile写入；
- admissionproof Gate候选已要求post-profile CONTENT/RESOURCE resolver+verifier，并把两proof digest绑定到custody request；
- RecoveryCustody wire/handle/verifier/single-winner、SQLite authority/kernel lease、数据库 revision 3（持久化 external-service mode）、DB-resident writer fence、typed finalization observation/同key reconcile、独立服务 CLI 构造/生命周期候选、durable reconcile coordinator及认证loopback HTTP transport候选已在本地实现；仍缺注入真实外部EvidenceFinalizer/ReceiptVerifier、production supervisor/Fx/OUT接线、配置发布、network FS证明、anti-rollback anchor、签名trust root与发布验收；
- migration132和CONTENT-1本地owner候选已实现：stable prepare preflight、AES-GCM sealer port、private object file backend、metadata-only reservation/object/receipt、prepared→active/live promotion、current-binding复验和command deletion fence；详细边界见[CONTENT-1 encrypted-single实施记录](content-1-encrypted-single.md)；
- migration133和RESOURCE-1 reserved-only候选已实现：typed十维effective policy、reserved snapshot、完整command/ID/CONTENT/current-policy relation和物理禁写usage ledger；详细边界见[RESOURCE-1实施记录](resource-1-reserved-skeleton.md)；
- 非空v131 profile升级时只进入append-only legacy quarantine，零自动ref/fence；profile读取/replay要求完整CONTENT relation，否则fail-closed；
- production仍没有真实key provider、CONTENT/RESOURCE proof adapters或Fx/tool/runtime接线；RESOURCE claim/permit/usage/release与外部custody authority也未实现，因此上述候选不能启用cross-task send。

## 实施顺序与当前进度

按已批准方向顺序执行：

1. `CONTENT-1` contract、migration、AES-GCM/local object候选、active ref与command fence：候选实现和本机精确测试已具备；真实key provider、目录句柄级TOCTOU防御、owner adapters和生产接线仍待完成。
2. RESOURCE policy snapshot与reserved/usage-disabled skeleton：候选实现和本机精确测试已具备；真实precedence generator、aggregate counters、claim/permit/usage/release仍待实现。
3. 外部单实例SQLite RecoveryCustody authority：local schema/state-machine/kernel-lease/rpcwire/HTTP server-client、独立服务 CLI 构造/生命周期候选与 durable reconcile coordinator 候选已落地；仍缺注入真实外部 finalizer/verifier、production supervisor/Fx/OUT接线、配置发布、network FS证明、真实外部anchor/签名和发布验收。禁止复用产品主SQLite或共享文件直连。
4. CONTENT/RESOURCE真实owner adapter与OUT lifecycle接线：待前三项authority齐全后实施。
5. 依赖齐全后才讨论 `recovery_custody_pending`；在此之前Gate只构造request，不调用custody。

## 仍需冻结的部署值

下列值不改变已批准方向，但在生产接线前仍必须以版本化配置/发布制品冻结：

1. KEK：OS secure storage + 外部 KMS/Vault双 adapter，还是指定唯一外部 provider；
2. ciphertext backend：私有 state-dir object files，还是指定 S3兼容/object store；
3. RESOURCE各维度 hard caps、scope precedence、token/cost telemetry与舍入；
4. custody单实例服务的部署位置、opaque bearer加载/轮换、SQLite state-dir、DB writer_epoch/takeover、network FS policy、恢复域外evidence/anchor provider、receipt signing trust root与灾备责任；
5. custody未部署时继续保持“不可发送”的严格模式；migration132 legacy quarantine不得被人工补权。

总计划继续保持 `Status: Active`，十四份完整规格继续为Draft，production cross-task send、managed provider、Room与release保持NO-GO。当前本地测试证据见[验证记录](verification-notes.md)，全部仍为`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不等于真实KMS、外部custody服务、发布制品或跨进程验收通过。
