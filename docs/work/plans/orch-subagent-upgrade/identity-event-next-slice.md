# ID-1/ID-2 身份与事件下一切片

Status: Active

日期：2026-09-01；承接 [Gate S0](README.md) 与 [命令生命周期日志](command-lifecycle-journal.md)。
本文件记录源码盘点和下一批实施边界，不把设计草案、旧表行或本机 fixture 当作生产身份/事件能力；Fx 中的 resolver/producer 只证明唯一 owner 构造接缝，不证明 transport 归因、完整事件生产与恢复 custody。

本轮新增的 `tasktransport` Authorization、`childissuer` 和
`taskeventsource.RuntimeEventTxCandidate` 均是未注册候选；它们不能替代
IdentityRepository 的 durable read-back、signed child credential 或生产事件 writer。

## 当前写入事实

源码盘点确认当前至少有两个相互独立的身份写者：

- `internal/module/thread` 的启动、恢复、fork 流程最终写 `agent_threads` 与 `agent_provider_binding`；
- `cmd/mcp-orch/standalone/execution.go` 经 `store/agent/SessionStore` 在同一事务写同两张旧表。

`agent_threads`、`agent_provider_binding` 和 `agent_codex_binding` 因而只能视为 legacy/兼容投影，不能直接升级为新的身份 SSOT。
`internal/provider/unified/session.go` 的 generation/session map、orchestration service 的进程/队列状态，以及 standalone `Execution.sessions` 都是内存状态；重启后不能证明 execution incarnation、process-start identity 或 source cursor 连续。
terminal-outcome store 只拥有执行后的终态投影，不能代替身份 reservation、binding preparation 或事件接收 owner。

## ID-1 目标与唯一 owner

新增 `IdentityRepository` 作为 reservation、execution incarnation、binding preparation/current binding 和 close/finalize/abort CAS 的唯一写 owner。
首批独立表名冻结为：

```text
orchestration_identity_reservations
orchestration_execution_incarnations
orchestration_binding_preparations
orchestration_current_bindings
```

reservation 要绑定 caller/source/target fence 引用、kind discriminator、稳定幂等命名空间和 request digest；first launch/recover/replace 必须引用 canonical command intent，provider discovery/a2a observation 明确作为无 command 的观测 reservation；两类都不得复制 prompt、凭据、permit 或 provider 正文。
incarnation 是不可变的 logical agent 代际记录；同一 external identity/source epoch 只能绑定一个 incarnation，旧 incarnation 迟到回执不能推进 current。
preparation 是不具授权性的绑定候选；创建时可以没有 active-process receipt，只有 active-process owner 的权威 receipt 回填并 finalize current binding。abort 与 finalize 竞争同一 preparation generation，不能用本地超时猜测 abort。
current binding 是唯一可读的当前投影，所有更新都必须带 expected version/generation/source fence 的 CAS；旧表不得并行写入。

本批不签发 permit/token、不消费 nonce、不实现 provider effect、不把非空 evidence ref 当授权；这些仍依赖 TRUST、CONTENT、RESOURCE、VERIFY/ROLLOUT 和真实 provider receipt conformance。

### ID-1 当前实现证据

- migration 128 四表合同：历史 host-light 观测曾 exit 0（架构前置 148.063s、业务 0.062s）；当前官方 wrapper 因未提交 `freeze_baseline.json` drift 阻断，不能把旧耗时当作当前发布证据。
- IdentityRepository 合同：历史 host-light 观测曾 exit 0（架构前置 176.959s、业务 0.458s）；统一 wrapper 当前在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段。
- `make sqlc-generate-worktree` 已生成 `identity_owner.sql.go`/模型；新增按版本读取查询后，`make sqlc-verify-worktree` exit 0，确认生成前后无漂移。
- direct code/entry guard、LSP diagnostics、`git diff --check` 当前通过；官方 `--light-guard-only` 在未提交 baseline drift 下按契约阻断。这些只证明结构/静态一致，不替代跨进程 recovery 或 release evidence。

验证对象是隔离 worktree 的本机候选，宿主标记为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；没有远程 Ubuntu/Wine、签名制品或 provider 账户证据。

### Task transport Binder 候选（未注册）

`cmd/mcp-orch/orchestration/tasktransport` 新增了 transport-neutral 的
`BindTaskToolScope` 候选。它只消费已由服务端绑定并通过校验的
`common.CallerAttribution` 与 `common.CallerPrincipal`，把 `Attribution.TaskID` 投影到
内部 `common.ToolScope.AgentID`；Family、CWD 和 workspace roots 必须来自显式
`ScopeTemplate`。调用方提供的 `ClaimedScope`（包括 MCP metadata 形成的 agent、thread、
call、cwd 或 roots）以及 context 中已有的 scope 均 fail-closed，分别覆盖 metadata
impersonation 与 prebound scope mismatch；缺失 attribution、缺失 principal、operator
principal 和非法模板同样阻断。

该包是本地 test-owned candidate：不导入、不注册 unified_v2 registry，不写 EventStore，
不启动 provider，也不改变现有 operator endpoint。回归测试为
`TestBindTaskToolScopeProjectsVerifiedAttribution`、
`TestBindTaskToolScopeRejectsMetadataImpersonation`、
`TestBindTaskToolScopeRejectsMissingAttribution`、
`TestBindTaskToolScopeRejectsAttributionWithoutPrincipal`、
`TestBindTaskToolScopeRejectsOperatorPrincipal`、
`TestBindTaskToolScopeRejectsPreboundScopeMismatch` 和
`TestBindTaskToolScopeRejectsInvalidTemplate`；本机 ordinary/race 测试通过，LSP
diagnostics 与 `git diff --check` 为零。以上仅证明候选绑定和拒绝路径，仍标记
`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不能替代真实 task listener、credential
activation、provider/runtime 事件或 production registry 接线证据。

## ID-2 目标与依赖

EventStore 只能在 ID-1 的 reservation/incarnation/fence 确认后接收 bound event。下一批需要 canonical envelope、按 `source/lane` 的 dense sequence、hash dedup/tombstone、materialization cursor 与 gap quarantine。
raw ingress 的 `(source_id, lane, source_sequence)` 是去重主键；同序异 hash 必须协议错误，高序越洞不能推进 cursor。事件正文继续只引用 CONTENT ref，UI/report/node 仅作投影。
事件 owner 不得扩展 direct-peer event outbox 或 terminal-outcome outbox 来承担身份真值；两者只能消费 canonical event/cursor。

## 字段链与故障验收

```text
caller/source fence
  -> identity reservation/digest
  -> immutable incarnation
  -> binding preparation/current CAS
  -> event envelope/source lane sequence
  -> custody/materialization cursor
  -> report/UI/wait/projection
```

ID-1 先验收：

- `DuplicateIdentityOrIncarnation`：同稳定键重放只返回同 reservation/incarnation；异 digest 不产生第二代；
- `BindingPreparedCannotAuthorize`：prepared 或未 finalize current 不允许 prompt/message/effect；
- `FinalizeAbortRace`：同 generation 只有 finalize 或 abort 一方可提交；
- `StaleIncarnationEvent`：旧 fence 的回执只落旧审计，不能推进新 current。

ID-2 后验收：

- `DenseAppendHoleAndReplay`：乱序、高序越洞、重启重放和同序异 hash 均不跳 cursor；
- `AckBeforeRecoveryCustody`：未取得恢复域外 receipt 不返回 custody ack；
- `ControlGapFailClosed`：control lane gap quarantine 并撤销写能力，不能当 warning 继续。

每个故障测试必须先证明非法 fixture 可到达目标 writer，再断言零越权副作用；单纯迁移 CHECK 拒绝不能替代运行时 owner 测试。

当前源码盘点已确认：direct-peer event/outbox、terminal outcome outbox、DAG `events`、bus/UI timeline 都不是 canonical dense event owner；ID-2 已在独立 EventStore 完成内部验证，但尚未扩展这些表来冒充 SSOT。

## migration 130 接缝：AccessGrant 与 EventWaitHandle

`internal/platform/db/sqlite/migrations/130_orchestration_access_wait.sql` 只为下一切片提供持久化边界，不改变 ID-1 的唯一身份 owner：

- `orchestration_access_grants` 保存不可变的 caller principal/scope、source/target reservation + incarnation + binding fence、读/发方向、幂等摘要和有效期；metadata 由 trigger 保护，撤销/过期只能用 expected-revision CAS，历史行不可删除。
- `orchestration_event_wait_handles` 保存与 grant revision 绑定的单一 source/epoch/lane selector、canonical cursor、匹配事件引用和 pending→terminal CAS；句柄是 durable 查询状态，不是 bearer capability。
- 两表均为 `STRICT`，通过 FK 读取 ID-1 reservation/incarnation；`Store` 必须在每次授权、建 wait、读 wait 和取消前重读当前 binding。它们不写身份表、canonical EventStore、direct-peer、terminal outcome 或正文表。

AccessGrant/EventWaitHandle 的 owner、API 判定顺序和尚未接入的生产边界见 [授权与可恢复等待下一切片](access-wait-next-slice.md)。在接缝完成前，ID-1/ID-2 不得把 wait handle 当作授权，不得让 wait 轮询投影缓存越过 canonical event cursor。

### migration 130 验证状态（本机候选，非发布授权）

- `TestAccessWaitMigrationContract` 与 `TestAccessWaitStoreContract` 的本机固定缓存测试均已 exit 0；轻量代码守卫也已 exit 0。host wrapper 的新一轮受宿主 CPU 门或未提交 baseline drift 阻断，仍无受信 Ubuntu/Wine PASS。
- canonical owner 已由 `canonicalOwnerModules` 接入 standalone/legacy Fx 图，且新增唯一 `identity.Store -> taskprincipal.Resolver`、`eventstore.Store -> taskeventsource.Producer` 窄口；`go build -p=1 ./cmd/mcp-orch` exit 0。production standalone 文件配置已接入 configured local-operator issuer，并为每个 HTTP initialize 签发 CallerPrincipal session 快照。这只证明 owner 构造和 operator transport identity 接通，不证明 child issuer、credential activation、真实 event producer 调用、CONTENT/OUT 或 provider receipt 接通。Producer 还会在 appender 前拒绝 reservation/incarnation 的未知 schema（当前只接受 schema 1），但这不是迁移或 runtime 接线。
- `RuntimeEventTxCandidate` 现提供同一 DBTX 的 identity/source/appender 原子候选；`childissuer` 只做 parent→child identity 验证，`tasktransport` Authorization 只做 zero-effect preflight。既有 ID-1/ID-2 与本切片本机记录仍只代表隔离 worktree 候选；不得外推为 principal 认证、跨进程 custody 或发布证据。
- identity/event store 现在还有 additive Tx-compatible seam：`GetActiveSnapshotTx`、
  `GetSourceStateTx`、`AppendTx` 与 `WithTransaction` 共用真实 transaction-bound DBTX，
  并由 `RequireTransactionDBTX` 拒绝裸/typed-nil/未知实现。它只支持后续原子 caller
  的本地验证，不分配 sequence、不引入 writer epoch/commit receipt，也未接 runtime/Fx。

## 退出边界

本切片完成不等于 ST-11 或总计划完成。当前明确未闭合：standalone/legacy Fx 已构造 AccessGrant/EventWaitHandle 以及 resolver/producer 窄口，且 local operator `caller_principal_ref/scope_ref` 已有 configured issuer；但 child/task 专属 principal issuer、credential pending→active、真实 event producer 调用和 public accesswait 工具尚未接通，CONTENT 的正文存取/加密擦除与事件 receipt 尚未接通，OUT 的 command/receipt/unknown effect 尚未接通，Room 的成员授权、fanout/partial/关闭/恢复尚未实现；跨 incarnation cursor、恢复域外 custody 和 provider receipt 也未验收。所有本机 wrapper 只能标 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。只有真实 IdentityRepository/EventStore writer inventory、旧写者硬隔离、TRUST/CONTENT/RESOURCE join、恢复域外 custody、跨进程 crash/replay 和发布证据全部具备后，才可把 native subagent、消息等待、跨任务授权或 Room 接入这些 owner。
