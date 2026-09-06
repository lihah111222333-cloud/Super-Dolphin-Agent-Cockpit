# 2026-09-02/03 候选补强复核

Status: Active — candidate evidence only

对象是未提交隔离工作树（初始复核 2026-09-02，接线审计与最终验证更新于 2026-09-03）
`.worktrees/20260831-orch-subagent-upgrade`。本记录只描述本轮新增的候选和
局部负测，不改写 `current-blocker-registry.json` 的 `needs_revalidation`，也不签发
生产、发布或跨任务能力许可。

## B09：严格唤醒载荷（candidate_mitigated）

`cmd/mcp-orch/store/taskdag/wakeup_payload.go` 现在提供
`wakeup_payload_kind + schema_version` 判别联合：`node_start/manual_dispatch` 只接
`dag_downstream`，`start` 只接显式 `legacy_launch`。重复键、未知/缺失/混合字段、
错误类型、`null`、超大载荷、过量上游引用和目标不一致均 fail-fast。

`EnqueueWakeup`、事务内 `enqueueWakeupTx`（包括
`AssignNodeAndEnqueueWakeup`）和 dispatcher route 前使用同一校验；非法行在
NodeRouter/provider/launcher 前转 `FailWakeup`。`terminal_failure_compensation` 是
consume-only，`turn_complete_retry` 只接受受限 JSON object；两者固定目标为
`mcp-orch`，internal 分支同样受 raw 大小上限和补偿字段长度上限约束。legacy builder
不再生成 target-only fallback；stored payload version、幂等 key 和 DAG/legacy row
identity 也在 dispatcher/SQLite 边界复核，launch optional `null` 不再被静默清零。

定向 taskdag/orchestration/wakeupreclaim 普通测试、race、vet、code-size 和 LSP
均通过（本机结果统一为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`）。

仍未关闭 B09：历史未标记 Go/camelCase payload 没有自动迁移。新增的
`taskdag/legacywakeupmigration` 只是操作员审核的一次性 planner，输出 migrate 或
typed quarantine，并要求 `updated_at + source digest` CAS 与稳定幂等键；它没有
生产 applier、quarantine receipt、跨进程恢复或完整 direct-SQL schema 校验。因此
旧行必须先显式迁移/审核，不能依赖 runtime 猜 schema。新增 migration 134 只提供
`payload_schema_version=0/1` 边界：旧行保持 0 且不再被 claim，新写入显式为 1，
基础 trigger 拦截 envelope/target 形状并把 quarantine 与源表解耦；它不能替代 Go
decoder、CAS/applier 或真实 receipt。启动 schema gate 现在会对 active version 0 行
fail-fast，要求操作员先完成迁移/隔离；dispatcher 也会在 executor/launcher 前复核
存储版本与 DAG/legacy 行身份，避免替代 store 或旧 writer 绕过 SQL claim。
planner 现按原始 payload 重算 digest、绑定完整 RunDigest，并拒绝 optional/null 或
不兼容 source version；合法 canonical snake_case legacy 字段可正常迁移。

## B08：唤醒 command/attempt（candidate only）

`cmd/mcp-orch/orchestration/wakeupcommandcandidate` 以 immutable command、wakeup
fence、唯一 attempt、dispatch receipt、permit、nonce 和 provider outcome 形成
record-before-effect/mark-after-effect 合同。unknown/reclaim 只允许查询和原 attempt
reconcile，不能递增 attempt 或盲目重发；旧 fence 在 permit 前拒绝。普通、race、vet、
code-size 和 LSP 均通过，但状态只在内存候选中保存，fake provider 计数不代表真实
provider exactly-once。当前 dispatcher 仍保持旧的外部副作用顺序，B08 仍为 NO-GO，
必须先接真实 OUT/receipt/custody owner 和跨进程 crash/reclaim 证据。

## B10：taskdag writer epoch（candidate only）

`cmd/mcp-orch/store/taskdagwriterfence` 定义显式 admission、DB-resident
`writer_epoch + deployment_fence` CAS、stale writer 零业务行和 sticky fence。
subprocess A(epoch N) / B cutover(N+1) 回归证明 callback 不执行。候选不导入
RecoveryCustody，不使用 PostgreSQL，也没有接入 shared taskdag mutation、Fx 或
发布切换；B10 仍需全量 mutation owner 的硬隔离和受信环境证据。

## B12：security-time owner（candidate only）

`cmd/mcp-orch/orchestration/securitytime` 定义可注入 wall/monotonic sample、持久
CAS state、boot/clock epoch、generation/revision、wall high-water 和双重 expiry。
回拨、前跳、sleep/resume、来源切换、freshness unknown、旧 snapshot、nil context
和 stale writer 均 fail-closed；本轮普通测试、race、vet、code-size、LSP 通过。

它不打开数据库、不实现 RecoveryCustody 协议，也不改 taskdag 旧 SQL；`Allocate`
的 lease 尚未形成外部 owner 的独立 allocation record。最终必须由外部 RecoveryCustody
单实例独占私有 SQLite 提供 security-time authority，orch 只能经认证 client 消费
快照，并把同一快照显式绑定到 taskdag mutation SQL。B12 仍为 NO-GO。

## RecoveryCustody 路径耦合修订

外部服务配置现在要求显式 `external_service=true`，服务 JSON 不包含
`product_sqlite_path` 或 `workspace_root`；private-only 模式只校验独立 authority
SQLite 的 owner-only 文件、WAL/SHM 和内核单实例锁。通用 authority caller 仍保留
产品库/工作区物理独立检查，防止把两种边界混在一起。服务依赖缺失时继续
fail-closed；mode 已持久化到 authority schema revision 3，旧 revision-2 文件不做
隐式迁移；PostgreSQL、产品 DB 直连和共享文件仍被禁止。

## 2026-09-03：生产接线审计与下一切片边界

对 B09/B10/B12 的生产接线做了只读 inventory，结论是当前候选不能通过局部包裹
而安全升级为 production：

- B09 的 additive migration 134 已实现：为 wakeup 增加显式 0/1 schema marker，
  ClaimDue 只选择 version 1，新 launch/internal envelope 由基础 trigger 拦截，并建立
  不依赖源行级联删除的 append-only quarantine 表。它不自动改写历史 version 0 行，
  也不能替代 Go 的重复键/未知字段/深层边界校验；生产 applier、CAS、quarantine
  receipt 和旧 active 行的跨进程验证仍缺失。
- B10 的 taskdag 共有约 42 个 mutation entrypoints，分布在 DAG/node/run/event/
  wakeup/worker-lease 及多个 tx helper。现有 `taskdag.Store`/Fx narrow ports 可绕过
  独立候选，且候选使用的普通 `BeginTx` 不保留 taskdag 的 `BEGIN IMMEDIATE` 与 busy
  retry 语义。下一切片必须先定义 tx-aware writer session，再按域迁移全部 writer 并
  加 exhaustive bypass guard；当前保持 isolated candidate。
- 本轮在不接入 taskdag/Fx/shared product DB 的前提下新增了隔离
  `taskdagwriterfence.WriterSession`：它复用 `sqlctx.WithImmediateTx` 的
  `BEGIN IMMEDIATE`/有界 busy retry，在同一 tx 内先 CAS DB-resident writer fence，再把
  tx-bound `sqlc.Queries`/`DBTX` 交给 DB-only callback；stale 在 callback 前返回并 sticky。
  callback 已成功但 COMMIT/连接结果不确定时，内部 replay barrier 返回
  `ErrCommitUnknown`、禁止第二次 callback，且 session 进入 query-only/fenced；callback
  自身返回 busy 的重试仍要求 DB-only、可安全回放。普通/race/vet/code-size/LSP 及
  tx-reuse、回滚、stale zero-row 负测通过。该候选不改变 B10 NO-GO，也没有宣称全量
  M01–M42 已迁移。
- B12 的 lease/auth/wait/credential/cron/taskdag 链路同时使用 Go wall clock、SQLite
  `strftime('now')` 和手写 SQL。只注入 `func() time.Time` 会丢失 security-time
  lineage/epoch/receipt，且留下 Go/SQLite split clock。下一切片必须由外部 RecoveryCustody
  提供已验证 snapshot/allocation，在每个 mutation 事务内重读并绑定 generation/
  revision/writer fence，再统一参数化 SQL；当前不做局部时钟替换。

- 终态 run 的 `terminal_failure_compensation` 已从普通 running-only claim 分支中拆出：
  它仍要求有效 DAG/run 身份，但允许在 run 已 succeeded/failed/cancelled 后被领取，
  并在 dispatcher 的 consume-only 分支收敛为 failed；普通 `node_start`、
  `manual_dispatch`、`turn_complete_retry` 仍受 running 限制。该切片只改善补偿 liveness，
  不提供独立消费 receipt/OUT owner，仍属 candidate evidence。

上述审计不改变 B01–B18 台账状态，也不把“可行的下一切片”当作已实现能力。

## 2026-09-03 继续执行：边界收口增量（不改变台账）

本轮新增的实现与验证仍属于 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，只提高候选
的 fail-closed 质量，不授予任何 production 或 release 资格：

- **RecoveryCustody 文件系统与路径身份**：外部 `sqliteauthority` 在计算 canonical
  authority path 后调用 `securefs.CheckLocalFilesystem`，对 UNC、已知 NFS/SMB 等网络
  类型、未知类型和检测失败均拒绝；`KernelAuthorityLease` 额外绑定取得租约时已存在
  的 SQLite 主库/WAL/SHM 文件对象，并让 Bootstrap/OpenAuthority/Takeover 在锁内完成
  before/after identity read-back，拒绝同路径 rename/recreate 或 sidecar 替换。后续也
  使用同一 canonical path，避免中间目录 symlink alias 在校验与打开之间改变目标。新增
  `authority_path_alias_test` 及 securefs 原生分类夹具通过；这不是受信发布环境或底层
  backing mount 证明。
- **RecoveryCustody 配置**：`mcp-recovery-custody` 的 JSON 重复键检查递归覆盖对象和
  数组，并设深度、成员数、总字节上限；嵌套 authority 字段和转义等价键不会再由
  `encoding/json` 的“最后值”静默覆盖。服务仍因缺少真实外部 finalizer/verifier
  保持 fail-closed。
- **managedlaunch mapping**：COMMIT/ABORT projection 现在逐字绑定 source 的
  `PreparedRequestDigest`，合法格式但不同摘要的投影被拒绝；未接 managedruntime
  supervisor、Fx 或 provider。
- **B10 inventory**：新增 `writer_inventory.json` 与 archtest 守卫，双向冻结 taskdag
  的 M01–M42 receiver/file/symbol 集合，新增/删除/重命名/重复会失败。清单明确
  `candidate_not_wired`；它不证明 writer session、BEGIN IMMEDIATE、epoch CAS 或
  全量跨进程硬切换已经接入。

上述增量不改变 B01–B18 的 `needs_revalidation` 状态，也不改变 B08/B09/B10/B12、
managed provider、cross-task send、Room 和 release 的 NO-GO 结论。

## 2026-09-03 额度恢复后继续执行：运行时与传输边界

本轮重新拉起的子 agent 在同一隔离工作树完成以下候选增量；结果仍是
`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`：

- standalone CLI 的 Codex/Claude `Wait`/reader 退出通知移出 provider worker，绑定真实
  provider thread/session 与 `launchSeq`，并以 `exitOnce`、stale-session、explicit-close
  和 late-launch-success fence 防止重复终态、旧代际写入和 callback 自等待死锁。直接
  `Execution` 调用缺少宿主 fence 时只使用实例内单调序列，不再把 `1` 当默认身份。
- `handleCLIProcessExit` 现在比较 provider 实际 session ID（不是 terminal outcome 的
  launch-generation 字符串）。managed Claude 的 process-exit、手动和 stall recovery
  都在缺少 durable resume supervisor 时 fail-closed，不会执行旧 `LaunchRequest.Command`；
  Codex managed recovery 与 legacy local agent 保持区分。
- legacy stdio/peer HTTP/bootstrap 统一隐藏并拒绝 `manage_direct_peer`、
  `send_direct_peer`、`read_direct_peer`、`read_direct_peer_events`，且拒绝发生在
  `ToolScope` 注入前；standalone operator HTTP 仅在完整 task principal+attribution
  存在时暴露 task-scoped direct-peer 工具。此举是防伪身份的禁用边界，不是 task
  authorization 或 unified_v2 接入。
- legacy 与 standalone Fx 图各自只装配一个 direct-peer outbox projector，成功投影后
  才 ACK；它不改变 canonical EventStore、OUT receipt、Room 或跨任务生产门。
- standalone lock 记录 canonical state root，serve 在打开产品 SQLite 前使用同一物理
  路径；RecoveryCustody 外部 authority 继续独立单实例私有 SQLite，并在 canonical
  parent 上执行本地/网络文件系统 fail-closed 检查。PostgreSQL 与产品库共享继续排除。
- B09 Go decoder、migration 134 insert/update trigger 同时拒绝 target/payload agent
  首尾空白；managedlaunch query projection 对 prepared/closed/unknown 分支执行严格
  one-hot receipt pair 校验。

这些运行时与传输增量没有新增 production supervisor、外部 evidence finalizer、OUT
command/attempt/receipt、历史 wakeup applier 或跨进程发布证据；随后补的
`taskdagwriterfence.WriterSession` 仍是隔离 candidate，不是生产接线。因此不关闭任何
blocker，也不改变总计划 `Active`、规格 `Draft` 或现有 `NO-GO` 决策。

## 2026-09-03 额度恢复后继续：证据与 task-only surface 收口

本轮仍只产生 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 候选证据：

- `scripts/orch_upgrade_gate.py` 与 `current-blocker-registry.json` 为 B01–B18 增加
  非空且唯一的 `red_evidence_refs`、`green_evidence_refs`、`guard_evidence_refs`。
  校验器会逐项检查 workspace-relative regular file、canonical path 重复/越界与
  registry/generated-map 自指，并生成明确标为 entries 的三类总计；当前每类计数均为
  18。source、spec/README 与引用文件内容还会纳入 deterministic document/evidence
  snapshot digest，内容漂移令 `--check` fail-closed。引用是 planned/observed 定位信息，
  不是已执行 receipt，不能改变 `needs_revalidation` 或签发 release。
- `standalone/taskhttp`/`taskruntime` 在没有 capability manifest 的情况下拒绝
  orchestration 控制/读取、direct-peer 与 unified 工具 allowlist，且拒绝发生在
  `base.ListTools` 前；安全本地工具仍要求显式 allowlist。该切片没有注册新工具、
  没有授予 task principal/grant，也没有改变 operator/legacy endpoint。
- task-only HTTP session 已将服务端验证的 attribution `TaskID` 投影为内部
  `ToolScope.AgentID`，使 task-scoped direct-peer 的 list/call 判定一致；operator
  profile 仍不带 AgentID，客户端 metadata 仍被拒绝。这只是候选 transport scope
  修复，不是 unified_v2 或 canonical cross-task 授权。
- `cmd/mcp-orch/unified_v2_surface_guard_test.go` 锁定 production registry、legacy
  provider 与 standalone provider 不暴露 strict unified_v2/Room surface，并保留
  legacy schema；缺 resolver/coordinator/Room owner 时返回稳定 disabled 哨兵。
- standalone operator 的 legacy `send_message(agent_id/pos, ...)` 继续作为已验证客户端
  兼容路径保留，但明确标记为 service-level 管理能力，不带 task attribution/grant，不能
  作为 canonical cross-task 发送；若改为强制 task grant 会破坏现有官方 CLI 双轮链路，
  应待未来 strict unified_v2 + 完整 owner/receipt 链后再切换独立 surface。
- RecoveryCustody 真实 CLI `run` 入口新增缺依赖顺序负测：完整 external-service 配置和
  私有 credential 仍在缺少真实 finalizer/verifier 时 fail-closed，且 authority/state
  目录在 Bootstrap、listener、kernel lock、WAL/SHM、ready 之前保持零新增；这只是
  本地候选顺序证据，不是 supervisor 或外部 receipt。
- `canonicalOwnerModules` 现在在 legacy 与 standalone Fx 图中只构造一次
  `identity.Store→taskprincipal.Resolver` 与 `eventstore.Store→taskeventsource.Producer`
  窄口，并以 typed-nil/缺数据库负测保护；构造不会自动 append 事件、不生成 task
  principal，也不注册 public unified/Room surface。child/task listener、credential
  activation、真实 event producer 调用和跨进程 custody 仍未实现。
- RecoveryCustody authority HTTP 现在只接受带未导出 marker 的 `VerifiedClient`；服务
  用显式注入的 verifier 包装 Authority，伪造、状态/handle 不匹配或 verifier 拒绝的
  terminal receipt 均折叠为 same-handle `indeterminate`。nil context 也 fail-fast；真实
  外部 verifier/trust root/supervisor 仍缺失，不能据此打开 production dispatch。
- authority 配置与底层 SQLite `Open` 现在都拒绝完整路径或叶子名首尾空格，避免
  canonical lock/identity path 与 SQLite 实际打开路径分裂；新增无副作用负测覆盖该边界。
- `taskeventsource.Producer` 在触达 appender 前拒绝 reservation/incarnation 的未知
  schema（当前只接受 schema 1），但不承担 schema migration、sequence allocation 或
  runtime event producer 接线；新增 `RuntimeEventTxCandidate` 只提供同一 DBTX 的
  transaction-bound candidate，仍未接 production。

当前源码稳定快照的 ordinary Go matrix 已通过；tasktransport Authorization、childissuer、
managedruntime `BindingCandidate`、Trust envelope、output-schema guard、process/exitmonitor
和 SQLite inode/sidecar guard 也已完成定向 ordinary/race/vet/code-size/LSP。Python
`unittest` 29 tests 与当前 `make orch-upgrade-check`（含 dependency-map check）均 exit 0；
此前 1018.025s、1066.951s、1120.714s 的 full archtest 均早于本轮新增
候选，不能称当前；其后源码冻结后的 full archtest 已完成并返回 `ARCHTEST_EXIT=0`
（包内 1019.957s、外层 real 1075.50s）。priority/wide 284 与 external 69 的 parity
digest 仍以当前定向测试为准。这些结果不关闭 managed provider、cross-task send、Room、
OUT/receipt、B08/B10/B12 或 release 门，且不把 planned refs 当作 GREEN attestation。

本轮新增候选的边界说明：`childissuer` 只消费 resolver 已验证的 parent 与外部 issuer
返回的 `TaskSessionIdentity`，不 mint bearer、不持久化、不做 durable replay；
`managedruntime/issuer` 已重命名为 endpoint/process `BindingCandidate`，即使有
`ProcessReceiptVerifier` 也不构成 child auth。`trustcandidate` 仅将 provider/metadata
包装成 quoted/inert candidate，`tools` output-schema guard 只校验显式成功值的
structuredContent。process/exitmonitor 修复只收口本地终态清理和 channel claim，不能
替代 durable event consumer。以上均不关闭 B01–B18。

## 复核边界

本记录新增的候选不改变总计划 `Status: Active`、十四份 S0 规格 `Draft`、
cross-task send/Room/managed provider 的 `NO-GO`。真实外部 finalizer/verifier、
签名 trust root、OUT accepted/attempt/receipt、生产 Fx/supervisor、旧进程硬隔离、
network filesystem 的发布级 backing/部署证据、跨进程 crash/reclaim 和发布证据仍是后续必要条件；
本地 `CheckLocalFilesystem` 只提供已知类型与检测失败的 fail-closed 守卫。
