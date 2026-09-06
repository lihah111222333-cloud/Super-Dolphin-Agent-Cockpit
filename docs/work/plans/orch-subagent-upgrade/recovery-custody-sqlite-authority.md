# RecoveryCustody 外部单实例 SQLite Authority

Status: Active — Local Authority, Service CLI and Coordinator Candidates Only

日期：2026-09-02；上级：[生产决策门](content-resource-custody-decision-gate.md)与[proof/custody 合同](pre-admission-proof-and-custody-contract.md)。

本候选的 S0 外部单实例、SQLite-only、unknown receipt、network-FS 与 trust-root/rollback 逐项 gate 见[RecoveryCustody 专项矩阵](recovery-custody-gate.md)；矩阵状态仍为 `pending`/`needs_revalidation`，决策为 `NO-GO`。

## 冻结方向与当前结论

RecoveryCustody 的生产方向已经冻结为“外部单实例服务独占私有 SQLite DB”：

```text
orch production adapter
  -> authenticated custody protocol client
  -> external singleton RecoveryCustody service
       -> private authority SQLite DB（唯一 writer）
       -> external EvidenceFinalizer / anti-rollback anchor
       -> signed terminal receipt
```

orch 不得打开、共享或迁移 authority DB；authority DB 不得复用产品主 SQLite、网络共享文件或 heteroci PostgreSQL。PostgreSQL 继续作为明确排除项：heteroci 的 PostgreSQL schema/role/state machine 属于 remote-CI，不是 orchestration RecoveryCustody authority，也不因本方向冻结而获得复用资格。

当前源码已实现本地 authority/transport candidate：`sqliteauthority` schema/state machine、`PinnedDirectoryLock`+`KernelAuthorityLease`，以及非 MCP 的 `rpcwire`+`authorityhttp`+`httpclient`；数据库 revision 3（持久化 external-service mode）与 DB-resident writer fence 已进入候选实现。authority HTTP 现在要求带私有 marker 的 `VerifiedClient`，由服务用显式注入 verifier 包装 Authority；独立服务 CLI 仍在外部 finalizer/verifier 缺失时 fail-closed，durable reconcile coordinator 也有可执行候选；它们仍不是 production composition，也没有签发任何可用于 command accepted/dispatch 的发布级 terminal proof。总计划保持 `Status: Active`，十四份完整规格保持 `Status: Draft`，cross-task send、managed provider、Room 与 release 保持 NO-GO。

## 已落地的静态候选边界

按当前源码静态核对，candidate 包含：

- 独立绝对规范路径、产品 DB 路径隔离、private file mode、单 SQLite connection、schema/meta/head identity 校验；
- 明确数据库 revision 3（持久化 external-service mode；`Config`/请求/receipt 的 wire schema 仍为 v1，fence 行合同独立为 v1），并将 `authority_writer_fence` 纳入精确 DDL/manifest；八类 STRICT 表及 immutable/CAS trigger；
- submission 的 stable handle、同 operation 精确重放、异 request conflict，以及 `pending|available|aborted` 状态；
- submission 的跨 operation 同幂等键冲突明确返回 `ErrCustodyConflict`，不会把唯一键冲突误报为 unavailable，也不产生第二条 submission；
- resolution/request、expected-resolution 与 finalization 三层domain-separated digest；resolution 的 `active_resolution -> anchored -> terminal` 状态机、availability/abort 单赢家、head revision 与 anti-rollback predecessor 绑定；
- finalizer 查询使用严格 typed `FinalizationObservation`，封闭状态仅允许 `absent|inflight|finalized`，并强制绑定原 operation/digest、单调 observation generation、read fence 与状态互斥 evidence；
- SQLite scalar列、canonical JSON、DDL约束、row重读与terminal receipt/checkpoint/head join的交叉完整性校验；
- 显式 `BEGIN IMMEDIATE`、commit outcome unknown 后转 query-only；`Resolve` 在 tx1 后统一进入 `ReconcileResolution`，先查询原 operation，仅在 definitive `absent` 且本地 active/head claim 复验成功时才用原 operation/digest 调用一次原子 create-or-get `Finalize`，`inflight` 保持同句柄 indeterminate，`finalized` 提交已返回 evidence；
- 每个 authority mutation transaction 在业务回调前以当前 `writer_epoch/deployment_fence` 执行 DB-resident CAS；stale writer、lease 丢失或 commit outcome unknown 会 sticky query-only，stale 路径业务写入为零；lease-bound `Authority.Takeover` 与 successor `TakeoverAuthority` 原子推进 successor fence；
- `securefs.PinnedDirectoryLock`与`KernelAuthorityLease`将完整`AuthorityScope`绑定到固定私有目录/锁身份，并把取得租约时已存在的 SQLite 主库/WAL/SHM 文件对象 inode 绑定到同一 lease；启动的 Bootstrap/Open/Takeover 在锁内做 before/after identity read-back，运行期 mutation 在 callback 前后复验，DB/sidecar rename/recreate 或权限漂移均 fail-closed；
- 非MCP `rpcwire`只开放`identity|submit|query`；`authorityhttp`使用opaque bearer、严格JSON/大小/并发边界；`httpclient`只允许固定loopback IP、禁代理/重定向/重试，并在暴露terminal前重验authority identity与receipt；
- `ExternalReceipt` 的 quorum/signature proof 字段已纳入 receipt digest v2，旧 v1 摘要没有隐式兼容；独立服务配置与 credential 文件有 64KiB/4KiB 上限、0600/0700 和前后 identity 复核；
- 外部服务 authority path 先解析为 canonical physical path，再由 `securefs.CheckLocalFilesystem` 对 UNC、已知网络文件系统和未知/检测失败类型 fail-closed；Bootstrap/OpenAuthority/Takeover 继续使用同一 canonical path，避免中间目录 alias 在校验与打开之间改变目标。该守卫是本地候选，不替代受信发布环境和底层 backing mount 证明；
- authority 读路径也保存并复核主库/WAL/SHM 对象身份：主库 inode 固定，WAL/SHM 只按合法 SQLite 消失→重建生命周期在锁内解绑/重绑；同路径替换、权限/类型漂移以及 authority 主库与固定 lock sentinel 同名或同 inode/hardlink 均 fail-closed。该增强仍是本地候选，不能替代跨进程 supervisor 或发布级文件系统证明；
- 架构守卫禁止普通orch生产代码import `sqliteauthority`或直接打开authority SQLite DB。

这些仍是本地候选。`KernelAuthorityLease`、DB-resident writer epoch/takeover 与 lease-bound stale fencing 已在本地 authority 包实现；独立服务 CLI 只形成构造/生命周期测试候选，durable reconcile coordinator 可执行但仍只形成候选生命周期，HTTP handler/client 的 server-side verifier gate 仍是协议构件。缺少真实外部 finalizer/verifier、production supervisor/Fx/OUT、发布配置与受信部署时，测试替身中的 finalizer/verifier 不构成生产实现。

## Production composition 必须保持 fail-closed

当前包外没有 `sqliteauthority` 的生产消费者；现有 orch production composition 仍只允许：

- `UnavailableClient` 返回 stable `indeterminate` / `ErrCustodyUnavailable`；
- `RejectAllReceiptVerifier` 拒绝任何本地或远端形状 receipt；
- 不进入 `recovery_custody_pending -> accepted`，不创建 dispatch attempt，不签 permit/nonce，不调用 provider；
- 不注册 cross-task send 或 Room mutation/tool。

虽然专用 protocol service/client 构件与独立服务 CLI/coordinator 候选已存在，在外部 finalizer/verifier、production supervisor/Fx/OUT adapter 完成前，仍不得把 local authority 直接注入 orch Fx 图，不得让 orch 打开 authority DB，也不得用测试 handler 或进程内 adapter 冒充部署隔离。

## 仍缺的生产组件

以下项目全部是 release blocker：

1. 独立服务进程的 production supervisor/CLI 生命周期、严格配置、credential加载、ready/health、安装与发布资源（当前 CLI 仅为构造/生命周期候选，缺生产依赖注入与监管）；
2. network filesystem 的发布级安全性证明或受信部署证据；本地 `statfs`/volume 类型守卫已对已知网络、UNC、未知类型和检测失败明确拒绝，但不能外推为所有底层 backing mount 的证明；
3. 真实外部`EvidenceFinalizer`：恢复域外复制/read-back、security-time、retention与anti-rollback anchor provider；当前仍只有接口/测试替身；
4. 可发布验签terminal proof：发行制品中的trust root/key rotation、签名生成与验证、artifact/provenance、tamper/replay/rollback负测；
5. service-owned durable coordinator 的 production 接线：可执行候选已提供启动期 active-resolution scan/list、durable job/journal、唯一 claim/takeover owner、重试调度与 `Run` runner；仍缺 production supervisor 生命周期、外部 finalizer/verifier 注入、审计发布与受信部署，这些缺口使自动恢复仍不可发布；
6. production orch client/Fx/OUT接线：配置、client lifecycle、identity probe、health/readiness、command admission/accepted/attempt、wait projection、审计与shutdown；
7. 受信环境与发布验收：真实双进程/硬崩溃/旧writer/commit-unknown/anchor unavailable/late receipt/灾备恢复矩阵。

缺任一项时，authority 只能保持 unavailable、pending 或 indeterminate；禁止生成 `available|aborted` authoritative terminal outcome，禁止推进 command accepted/dispatch。

## 生产状态机约束

生产协议必须保留 candidate 的单赢家语义，但 terminal 资格只能来自真实外部 evidence：

1. `Submit` 只持久化 pending submission；SQLite commit 本身不产生 availability。
2. `Resolve` 先唯一占用 resolution/head predecessor，再在 DB transaction 外调用外部 finalizer。
3. finalizer response unknown 必须先查询同一 finalization operation；`Query` 只有返回带 generation/read-fence 的线性化 definitive `absent` 时，才允许在重新确认本地 active resolution/head claim 后，以完全相同 operation ID 与 request digest 调用原子 create-or-get `Finalize`。`inflight`、查询错误、未知/无效 observation 均不得再次 finalize 或更换 operation/digest。
4. evidence 必须绑定 security domain、authority/profile/owner generation、head predecessor/result root、security-time、retention、anchor 与 signature。
5. receipt 通过发布 trust root 验签并与 checkpoint CAS 后，才允许 `active_resolution -> anchored -> terminal` 和 submission terminalize。
6. availability 与 abort tombstone 恰一赢家；late opposite/same-kind alternate receipt 均不得改写 terminal winner。
7. lease 丢失、commit outcome unknown 或 evidence 不可读时实例转 query-only/indeterminate，不能继续写或猜测结果。

## 当前本地证据

- direct code/entry guard PASS（代码指标与裸测试入口）；raw `recoverycustody/...`全子包、`taskprincipal`、`taskeventsource`、`eventstore`、`internal/mcpserver/common`与`internal/platform/securefs`测试PASS；相关build/vet PASS；官方 wrapper 因未提交冻结基线 drift 阻断；
- `TestSQLiteAuthorityLifecycle`、`TestReconcileResolutionRestartAbsentFinalizesSameKey` 与 `authorityhttp` 的 host-light 数值（200.221s/143.217s/227.785s）均为历史观测；统一 wrapper 当前在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段，不能写成当前 host PASS；
- 早期全量 `internal/archtest` 1018.025s（以及后来 1066.951s、1120.714s）仍是历史快照；源码冻结后的当前 run 已 `ARCHTEST_EXIT=0`（包内 1019.957s、外层 real 1075.50s），但该结果只证明本机静态守卫，不改变 production NO-GO；
- revision-3 writer fence/mode：stale `Submit`/`Resolve` zero-row regression、lease-bound takeover、old revision/no-migration 与 exact token Open checks通过；
- `make orch-upgrade-check` 当前 exit 0（Python 29 tests 与 dependency-map check）；最终 `make project-map-refresh PROJECT_MAP_ARGS=--full && make project-map-check` 亦 exit 0（5461 files/8 domains/drift=OK），这些只是规划/索引一致性，不是整体 production PASS；
- Windows只完成test-binary交叉编译，没有原生Windows运行证据。

以上全部仍是`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`开发证据，只证明当前候选与守卫，不证明独立服务、真实外部evidence/anchor、签名trust root、生产orch接线或发布资格。
