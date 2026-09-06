# Orch 子 agent 升级验证记录

## 2026-09-07 正式二进制互通复核

本节只记录本轮独立客户端与正式 `mcp-orch` 二进制的真实运行证据，不把它提升为提交、发布或完整 S0 规格验收。

- 独立 Claude CLI（Haiku）通过独立 HTTP MCP 配置调用正式 `mcp-orch` binary；`launch_agent`、`get_agent_report`、`send_message` 均实际执行。
- `mcp-orch` 再启动独立 Codex CLI（`gpt-5.6-sol`），首轮和追加 turn 报告分别精确返回 `FIRST_CROSS_OK` 与 `SECOND_CROSS_OK`，`report_seq=2/3`，状态为 `idle`。
- 正式二进制 client 与 daemon 均以退出码 0 结束，ready 文件已移除；最新二进制 SHA-256 为 `c44e412e8ac218ade889696392ee45ba33c13c57c7b6544d6c78b04b2c8f4aa4`。
- Codex JSONL 通知现在由 wire reader 按接收顺序交付；新增顺序回归测试通过，修复了 delta 报告乱序。
- mcp-orch runtime shutdown 使用 owner 状态锁抑制 runner 自 shutdown 与 Fx `OnStop` 的重复广播；同一 E2E 证据中不再出现 `channels are blocked`。
- 受影响候选树在隔离临时 Git index 中通过入口守卫、代码守卫、`internal/archtest`、`codexwire` 顺序测试和 runtime 定向测试。

边界仍保持：当前工作树未提交、未合并、未推送；真实提交门禁需要将经审查的 baseline 和源码变更绑定到 exact index。完整 strict 全仓扫描仍包含历史债务，不由本轮局部修复关闭。

Status: Active — Local Evidence Only

日期：2026-09-04（含本轮候选增量复核）。对象为未提交工作树`.worktrees/20260831-orch-subagent-upgrade`。

当前状态锚点见[current snapshot（2026-09-04）](current-snapshot-2026-09-04.md)。该快照区分了本次
复核的 `make orch-upgrade-check` 与统一 light guard 结果；本记录中的更早耗时和 wrapper 失败
原因仅在有明确日期的小节内作为历史观测引用。

本表中早于本次稳定快照的 host-light、wrapper 和 full-archtest 耗时均是历史观测，
不能据此宣称当前 PASS。当前复核的命令结果和统一 light guard 失败项以
[current snapshot（2026-09-04）](current-snapshot-2026-09-04.md)为准，所有结果仍是
`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。

本文件按日期顺序追加记录；本节（2026-09-04）优先于下方所有 2026-09-03 及更早的历史观测。

## 2026-09-04 候选增量

- migration 136 wait-operation head/append-only target journal 通过 strict migration contract、
  embedded fresh/legacy/parity、baseline incremental 和 SQLC manifest ownership checks；全局
  `MinRequiredSchemaVersion` 仍为 134。
- AccessWait transaction-bound APIs 与 wait-operation `ProvisionTarget` 已通过 ordinary/race/vet；
  grant、wait 和 journal 在同一 `BEGIN IMMEDIATE` 中提交，失败时回滚，重复/并发/reopen 只返回
  原 durable identity。
- wait-operation `AdvanceState`、`CleanupTarget`、`ResumeTarget` candidate 已通过 targeted
  ordinary/race/vet 与 code-size/LSP；状态严格单赢家 CAS、聚合 proof、cleanup grant revision 连续性、未知结果
  不重试副作用和 durable wait read-back 均有回归。它们仍不提供 aggregate owner 或 public wait；
  跨重启 marker 仅由 opt-in candidate 记录，尚无真实驱动层 commit-unknown receipt。
- wait-operation commit-marker candidate 已覆盖六条 mutation owner path：canonical marker key、
  pre/post digest、pending marker reopen fence、explicit proof resolution、callback rollback、
  marker-enabled replay（含 omitted `RecordDigest` 与 optional plan references）。另有
  context-scoped owner seam 在成功 COMMIT 后注入一次 `ErrCommitUnknown`，验证重开、query-only、
  显式 proof resolve 与无重复 callback。marker schema 与 baseline incremental 分类通过；但
  真实驱动层 acknowledgement fault、recovery receipt 尚缺，marker-enabled constructors 未接 production Fx/provider caller，结果仍为
  `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。
- wait-operation 读取/恢复路径新增 migration-一致的阶段边、身份和资源连续性守卫，并以
  非法跳转、资源擦除和 identity drift 回归验证；这只是本机恢复输入的 fail-closed 防护，
  不替代跨进程 recovery receipt 或生产 owner。
- `CommandAttemptGateway.Execute` candidate 已通过 orchestrationcommand ordinary/race/vet、
  architecture guard 与 code-size；permit 先消费、typed outcome 收口，provider error/panic 和
  并发重复调用保持 unknown/单次 effect。该 seam 未接真实 provider、custody、Fx 或 dispatcher。
- EventStore watcher 仅在 commit 后提示，expiry/clock/panic/result/terminal/outbox/identity
  候选均保持 non-public、non-provider、non-authoritative。
- EventStore `RetentionPinVerifier` candidate 在 `RecordTombstone` 的同一 transaction-bound
  DBTX 中运行，verifier error 会回滚 tombstone/floor；AccessWait `CreateWaitTx` 在新幂等键
  插入前重读 source watermark/status，stale floor、cursor-ahead、gap/quarantine/closed 与
  missing source 均 zero-effect 拒绝，exact replay 保留。两者仍未接入 production retention
  index/GC、跨 daemon supervisor 或 Fx wiring。
- 独立 `retentioncandidate.Verifier` 已通过 SQLC bounded query 读取 pending AccessWait 与
  latest nonterminal wait-operation target heads；floor equality、terminal release、malformed
  lifecycle、overflow 和 transaction-bound DBTX 负测均通过 ordinary/race/vet。它仍是
  transport-neutral candidate，未进入 EventStore Fx、production index/GC 或 public wait。
- `GOWORK=off go test ./cmd/mcp-orch/... -count=1 -p=1` 与
  `GOWORK=off go test -race ./cmd/mcp-orch/... -count=1 -p=1` 均在 loopback socket approval 下通过；本次
  `make sqlc-verify-worktree` 已完成生成/后处理，但 exact Git state 检查在
  `internal/store/sqlc/prompt_template_sql_test.go` 返回宿主 `operation not permitted`，不能记为
  SQLC exact PASS。
- 直接 `--verify` 还明确报告 `cmd/mcp-orch/store/sqlc/access_wait.sql.go` 为
  `visible but not tracked`；本轮不通过暂存或提交来绕过该边界。
- 所有本节结果仍是 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不关闭 B01–B18，不启用
  `wait_agents`/Room/跨任务 send，也不提供发布许可。

本次串行 `./scripts/test_with_guard.sh --light-guard-only` 在使用独立 `GOTMPDIR/GOCACHE` 后
真实返回 `EXIT=1`，被既有 dirty `freeze_baseline` generated-file drift 阻断；不作测试计数结论，
也没有修改 `freeze_baseline.json` 或使用 `--no-verify`。候选 ordinary/race 矩阵均通过，global
`code_size_guard` 的 production、test、priority SSA 范围全部通过。本次 full
`go test ./internal/archtest -count=1 -timeout=12m -p=1` 返回 `EXIT=1`；运行约 720s 后因
`panic: test timed out after 12m` 终止，期间多处 `/tmp/archtest-run...` 报 `no space left on device`
（含 Heteroci probe/cross-compile 等），因而不签发 full-archtest PASS。focused SQLC、迁移、边界和
public-surface archtests 均已通过；本轮 parity 常量定向 ordinary/race 已通过，不写成当前 frozen
parity drift。

## 当前结论

- 总计划保持`Status: Active`，十四份完整S0 specs均为`Status: Draft`。
- production RecoveryCustody composition仍是unavailable/reject-all；cross-task send与Room保持NO-GO。
- local候选已扩展到RecoveryCustody三层digest（receipt proof digest v2）、SQLite完整性join、数据库 revision 3（持久化 external-service mode）、DB-resident writer fence、kernel lease、typed reconcile、独立服务 CLI 构造/生命周期候选、可执行 durable reconcile coordinator、非MCP loopback HTTP transport，以及task attribution、task-only HTTP session认证、内存credential owner、current task principal/canonical task event source；本轮新增 B09 严格唤醒载荷/legacy migration planner、B10 taskdag writer-epoch 候选和 B12 security-time owner 候选。
- task identity/event owner 现在已在 legacy/standalone Fx 中构造为唯一的 resolver/producer 窄口；`orchestration/tasktransport` 的 Binder 与 Authorization claim、`taskeventsource.RuntimeEventCandidate` 与 transaction-bound `RuntimeEventTxCandidate` 都仍是未注册、transport-neutral candidate，只把服务端已验证的身份/围栏投影给窄端口，不接 public `unified_v2`。它们不把候选校验或分事务读取当作生产授权；尚无 production child credential issuer、runtime event caller 或 public list/wait/send。新增的 `orchestration/childissuer` 只验证未来外部 issuer 返回的 child identity，`standalone/managedruntime/issuer` 已明确收窄为 endpoint/process `BindingCandidate`，二者都不签发 bearer、不接 Fx/DB/provider。legacy `send_message`/directpeer没有完整task auth、event producer、CONTENT/RESOURCE/custody/OUT chain，不能升级为canonical cross-task send。
- PostgreSQL继续排除；外部 RecoveryCustody 明确采用独立单实例私有 SQLite，服务配置不再要求 product SQLite/workspace 路径；普通orch不得import `sqliteauthority`或打开authority DB。外部 authority 现在还必须通过本地文件系统类型检查，并以已解析的 canonical path 继续完成租约、bootstrap 和 open。
- B09 migration 134 之后，active `payload_schema_version=0` wakeup 会在启动 schema gate 直接 fail-fast，dispatcher 也会在任何 executor/launcher 前复核 version 与 branch/row identity；这只防止隐式执行，不提供历史 applier 或 receipt。
- legacy stdio/peer HTTP/bootstrap transport 现在在绑定 `ToolScope` 前隐藏并拒绝全部 direct-peer 工具；没有 server-owned task principal/attribution 时不再接受客户端 `_agentId/_cwd` 作为 operator 或 task 身份。standalone local HTTP 的 task-scoped direct-peer 工具同样只有完整 principal+attribution 才可见。
- standalone CLI 的 Codex/Claude 进程退出通知现在带 provider 真实 thread/session、宿主 launch sequence 和 unexpected/explicit-close 分类，异步交给 orchestration exactly-once fence；迟到 launch success 不会复活已退出代际，managed Claude 不会回退执行 `LaunchRequest.Command`。
- 本轮新增的 childissuer、managedruntime `BindingCandidate`、trustcandidate、tasktransport Authorization、RuntimeEventTxCandidate、tools output-schema guard 与 process/exitmonitor 生命周期修复均明确保持 candidate-only 或局部 fail-closed；它们不把 operator issuer、内存 replay map、channel enqueue 或自摘要提升为生产授权/receipt。

## 当前执行证据

| 验证面 | 结果 | 边界 |
| --- | --- | --- |
| direct code/entry guard | PASS（`go run ./scripts/code_size_guard.go --`、`forbid_raw_go_test.sh`）；global `code_size_guard` production/test/priority SSA 全部通过 | 直接静态检查；light guard 另见 current snapshot，当前被既有 dirty `freeze_baseline` generated-file drift 阻断 |
| raw RecoveryCustody | `recoverycustody/...`全子包PASS | transport-neutral、sqliteauthority、rpcwire、authorityhttp、httpclient本地候选 |
| raw mcp-orch全包 | 历史源码冻结 ordinary `GOWORK=off go test ./cmd/mcp-orch/... ./cmd/mcp-recovery-custody -count=1 -p=1` EXIT=0（约 65s） | 不作为本轮全包复跑证据；本轮只认候选矩阵与 compile/build/vet 结果，仍不是发布验收 |
| identity/event owner wiring | `cmd/mcp-orch` canonical owner Fx + taskprincipal/taskeventsource ordinary/race/vet/LSP PASS | 唯一 identity.Store→taskprincipal resolver、eventstore.Store→task event producer 窄口已构造；Producer 在 appender 前拒绝未知 reservation/incarnation schema；不注册 public tools、不自动 append 事件；仍缺 child/task listener、credential activation、真实 producer 调用与跨进程 custody |
| command owner Fx seam | `orchestrationcommand` Module Fx 构造、UnavailableReceiptVerifier ordinary/race/vet/code-size/LSP PASS；Reserve→Prepare 后 Apply 被 `ErrEvidenceRejected` 拒绝且 journal 版本不变 | 仅接入唯一 SQLite command intent/journal owner 与默认永不放行的 receipt-verifier 候选；不注册 unified/send、不调用 provider；真实 receipt authority/verifier、accept custody 与 OUT/attempt 仍未接线，继续 NO-GO |
| runtime event producer candidate | split `RuntimeEventCandidate` 与 tx-bound `RuntimeEventTxCandidate` ordinary/race/vet/code-size/LSP PASS | 前者仍是分事务映射；后者要求 identity/source/appender 使用同一 transaction-bound DBTX、gap/unknown commit fail-closed；两者均未注册，不能接 production lifecycle 或 public wait |
| identity/event transaction seam | `sqlctx.RequireTransactionDBTX`、`identity.GetActiveSnapshotTx`、`eventstore.GetSourceStateTx/AppendTx/WithTransaction` ordinary/race/vet/code-size/LSP PASS；真实 identity/event store 共用 `*sql.Tx` integration PASS | additive local seam；不改旧 `Append`、不分配 source sequence、不接 Fx/provider/custody/OUT；仍缺 durable writer epoch/operation receipt/commit-unknown recovery 与 production lifecycle caller |
| secure filesystem | `internal/platform/securefs` PASS | 本机PinnedDirectoryLock候选；不证明network FS或原生Windows |
| RecoveryCustody local-volume/path boundary | `go test ./internal/platform/securefs ./cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority -count=1` PASS；主库/sidecar inode 与生命周期回归 PASS；Windows/Darwin/Linux test-binary compile PASS | `CheckLocalFilesystem` 对已知网络/未知类型 fail-closed；ExternalService 只接受 canonical authority parent，主库 inode 与 lease 绑定，WAL/SHM 仅按合法 SQLite 生命周期在 lock 内重绑；仍缺发布环境 network-FS/底层 backing 证明 |
| RecoveryCustody singleton/SQLite gate matrix | [recovery-custody-gate.md](recovery-custody-gate.md) 已登记六项 singleton/migration/unknown-receipt/private-SQLite/network-FS/trust-root 检查及 RED/GREEN/Guard 入口 | 仅 planning-only `pending`/`needs_revalidation`、`NO-GO`；不新增 DAG node/JSON，不签发 authority receipt，也不替代真实外部 supervisor、trust root 或发布文件系统证明 |
| RecoveryCustody config duplicate guard | `go test -race ./cmd/mcp-recovery-custody -count=1`、`go vet`、定向 code-size/LSP PASS | 递归对象/数组重复键、转义等价键、深度/成员/大小上限 fail-fast；仍是候选 CLI，processDependencies 缺真实依赖 |
| RecoveryCustody missing-dependency startup order | `TestRunMissingExternalDependenciesHasNoStartupSideEffects` ordinary/race/vet/code-size/LSP PASS | 完整 private-only 配置在缺真实外部依赖时于 authority Bootstrap/open/listen/lock/ready 前失败，state/authority 目录 zero delta；错误当前仍是稳定文本而非生产 receipt |
| RecoveryCustody verified-backend boundary | `VerifiedClient`/authorityhttp/service ordinary + race/vet/code-size/LSP PASS；unverified generic backend 与 verifier拒绝负测 PASS | authority HTTP 只接受带私有 marker 且实际调用 ReceiptVerifier 的 backend；伪造或 verifier 失败的 terminal outcome 折叠为 same-handle indeterminate；仍缺真实外部 verifier/trust root/supervisor |
| RecoveryCustody read/path identity hardening | sqliteauthority ordinary/race/vet/code-size/LSP PASS；Query sidecar replacement/disappearance lifecycle、lock-sentinel basename/hardlink collision RED/GREEN PASS | 主库与 WAL/SHM 快照在读写路径复核，合法 sidecar 生命周期可重绑；authority path 不得与固定 lock sentinel 同名或同 inode；仍缺发布 backing-FS、跨进程 supervisor 与签名 trust-root 证据 |
| RecoveryCustody coordinator wiring candidate | `cmd/mcp-recovery-custody` service coordinator test-owned adapter ordinary/race/vet/code-size/LSP PASS | 仅打开已显式 Bootstrap 的独立 coordinator SQLite、有限 startup scan；不提供 Serve/Run、不改 production service/Fx、不把 test finalizer/verifier 当真实依赖 |
| accesswait target-source adapter | accesswait/unifiedv2 ordinary/race/vet/code-size/LSP 与定向 archtest guard PASS；target-source、forged fence、revoke/rollover、same-source compatibility 负测/正测 PASS | durable owner 现在只在 active `CanRead` grant、完整 target fence 和 canonical target tuple 下接受 target selector；caller/source fence 仍独立重验；public `wait_agents`、child credential、CONTENT/OUT/custody 仍未注册 |
| RecoveryCustody candidate binary packaging | opt-in `build-recovery-custody-candidate`、Linux/macOS package/manifest guard、legacy manifest compatibility 与 cross-build PASS | binary 仅在 `RECOVERY_CUSTODY_CANDIDATE_PACKAGE=1` 时作为 `candidate`/`disabled` 资产复制，不进 bundled_execs、不自动启动；旧 manifest 无候选字段时 verifier 兼容放行；缺外部 EvidenceFinalizer/ReceiptVerifier 时 run 仍零副作用 fail-fast，未构成发布或运行授权 |
| standalone state-root binding | standalone/`cmd/mcp-orch` ordinary + standalone race/vet/LSP PASS | `AcquireInstance` 保存 canonical physical state dir，serve 在打开产品 SQLite 前绑定同一路径；RecoveryCustody 另有主库/sidecar lock→DB identity guard；仍缺发布级跨进程证明 |
| standalone singleton isolation | `go test ./cmd/mcp-orch/standalone -count=1` 与 `go test -race ./cmd/mcp-orch/standalone -count=1` PASS（`TestInstanceLockIndependentDirectoriesCanAcquireInParallel`、锁文件删除/替换回归、alias/canonical 与 owner kill 接管） | 仅本机 deterministic 单元/子进程证据：证明不同私有 state dir 可并行取得各自锁，持锁期间删除或替换 sentinel 不能产生第二 owner；仍缺真实 serve binary 双目录并发、原生 Windows 删除/替换及发布文件系统证明 |
| task transport Binder/Authorization candidates | tasktransport ordinary/race/vet/code-size/LSP 与 archtest guards PASS | Binder 负责 server-owned attribution→ToolScope 投影；Authorization claim 负责 discover/read/send、parent/grant/revocation/cursor 与 zero-effect preflight；均未注册、不落库、不接 EventStore/provider/Fx/unified_v2 |
| message/reply association candidate | `go test ./cmd/mcp-orch/orchestration/messageassociation -count=1` 与 `go test -race ./cmd/mcp-orch/orchestration/messageassociation -count=1`、vet/code-size/LSP PASS；包内 production import guard PASS | 仅 DTO/strict canonical JSON 候选：SourcePrincipalRef、source/target execution+turn fence、独立 parent/message/reply command 与幂等 tuple、content ref；固定 parent/schema 版本，payload 1MiB、JSON depth 16、对象/数组各 64 members 有界，自引用/自定义 Marshaler、duplicate/unknown/null/trailing/bare `correlation_id`/`reply_to` 均拒绝。当前不证明 authenticated principal/revocation、object/scope generation、授权 parent object、delivery/cursor/receipt；不接 OUT/DB/provider/Fx，不能视为 cross-task 已启用 |
| SQLite/authority path identity guard | `sqlite.Open` 与 `sqliteauthority.Config.Validate` ordinary/race/vet/code-size/LSP PASS；路径首尾空格负测 PASS | 调用方路径不再被静默 `TrimSpace`，authority lock/identity 与实际 SQLite 打开路径保持一致；不替代跨进程或发布文件系统证明 |
| legacy direct-peer transport gate | `cmd/mcp-orch` focused ordinary/race/vet/LSP/code-size PASS | legacy tools/list/call 在 scope 注入前隐藏/拒绝 manage/send/read/read_events；不改变 standalone direct registry fixture，也不授予 canonical cross-task auth |
| direct-peer projector wiring | `cmd/mcp-orch/...` ordinary/race/vet PASS；Fx 单 runner + SQLite pending→projected 回归 PASS | legacy 与 standalone 各自只有一个 RunGroup projector，sink 成功后才 ACK；不等于 canonical EventStore 或完整 delivery receipt |
| CLI process-exit lifecycle | standalone/orchestration ordinary + race/vet/LSP PASS | provider session/launch fence 校验、异步 callback 防自等待死锁、duplicate/explicit-close/late-launch 负测；尚缺真实发布 supervisor/硬崩溃矩阵 |
| managed Claude recovery boundary | `TestRecoverManagedClaudeRejectsLegacyCommandExecution` ordinary/race/vet/code-size/LSP PASS | 手动/stall/异常退出恢复均不执行旧 local command；Claude durable resume 能力仍未实现，返回 typed `thread_resume` capability error |
| build/vet | 当前稳定快照 `GOWORK=off go build ./...` EXIT=0（real 7.32s）、`GOWORK=off go vet ./...` EXIT=0（real 6.97s） | 使用独立外部 GOTMPDIR；本机构建/静态检查，不是发布验收 |
| local race | 历史源码冻结 `GOWORK=off go test -race ./cmd/mcp-orch/... -count=1 -p=1` 直接命令 EXIT=0；所有包 `ok`/`[no test files]`，无 `FAIL` 或 `WARNING: DATA RACE` | 不作为本轮全包复跑证据；本轮候选 race 使用独立 GOTMPDIR/GOCACHE，仍不替代受信 Ubuntu/Windows race 证据 |
| SQLite authority host-light | 历史观测：archtest 200.221s、business 0.044s、wrapper exit 0 | 仅 local schema/state-machine/kernel lease 候选；历史 wrapper 结果不代表当前守卫状态 |
| Reconcile host-light | 历史观测：archtest 143.217s、business 0.025s、wrapper exit 0 | 仅 definitive absent 后同 operation/digest 恢复候选；历史 wrapper 结果不代表当前守卫状态 |
| authorityhttp host-light | 历史观测：archtest 227.785s、business 0.011s、wrapper exit 0 | 仅非 MCP identity/submit/query HTTP handler 候选；历史 wrapper 结果不代表当前守卫状态 |
| S0规划门 | Gate S0 47 tests（29+9+9）通过；使用临时 Git exclude 的 project-map 为 5,591 files/8 domains/drift=OK，codemap 为 393 files/1,474 refs；dependency-map check 通过 | planning consistency and index freshness only；不授予 runtime/release 资格 |
| B08 command/attempt candidate | `wakeupcommandcandidate` ordinary/race/vet/code-size/LSP PASS；新增 pending/unknown replay 与 nil-context fail-fast | in-memory contract only；已有 effect_pending/unknown attempt 不再交出第二 permit，但 dispatcher 仍未接 OUT/receipt/custody，真实 crash/reclaim exactly-once 未验证 |
| B09 strict wakeup payload | taskdag/orchestration/wakeupreclaim ordinary + race + vet + code-size + LSP PASS | candidate mitigation only；历史未标记行仍需显式 operator migration/quarantine，DB direct SQL duplicate-key/完整 receipt 证据缺失；本轮补齐 target/payload agent canonical trim 拒绝 |
| B09 migration 134 boundary | `wakeup_payload_migration_test` ordinary/race PASS；migration 134、SQLC manifest、schema gate、direct-SQL fixture PASS；active-v0 startup/dispatcher fence tests PASS | additive version marker/claim filter/basic trigger/immutable branch+row identity/quarantine only；active legacy 行启动 fail-fast，不做历史自动迁移；完整 applier/CAS/quarantine receipt/跨进程 dispatcher 仍缺 |
| terminal compensation liveness | 终态 run 上 `terminal_failure_compensation` 可被 claim，普通 node wakeup 仍不被 claim；taskdag ordinary/targeted race/vet PASS | 仅修复 consume-only 补偿在 run 终态后的候选 liveness；仍使用现有 failed 状态/指标，没有独立 receipt/OUT owner |
| B10 writer-session next-slice | `taskdagwriterfence.WriterSession` tx-bound ordinary/race/vet/code-size/LSP PASS；tx-reuse、rollback、stale zero-row、commit-replay barrier 负测 PASS | 隔离 candidate 复用 `sqlctx.WithImmediateTx`，未接入 taskdag/Fx/shared product DB；仅覆盖 seam，不改变 B10 NO-GO 或 M01–M42 全量迁移要求 |
| B10 writer epoch candidate | `taskdagwriterfence` ordinary/race/vet/code-size/LSP PASS；subprocess A/B cutover PASS | isolated candidate；未接入全部 shared taskdag mutation owner/Fx/release hard isolation |
| B10 writer inventory guard | `writer_inventory.json` 与 `TestTaskDAGWriterInventoryIsExactAndCandidateOnly` 精确冻结 M01–M42；focused ordinary/race/vet PASS；全量 direct code-size guard PASS | 只冻结 receiver/file/symbol inventory 与 candidate_not_wired 状态，不证明任何 entrypoint 已受 writer session/fence 保护 |
| managedlaunch query one-hot | managedlaunch + managedruntime ordinary/race/vet/LSP/code-size PASS | prepared/committed/aborted/outcome_unknown 只允许对应 receipt pair，非当前状态或 partial pair fail-closed；仍无 supervisor adapter |
| B12 security-time candidate | `securitytime` ordinary/race/vet/code-size/LSP PASS | isolated state-machine candidate；未接 RecoveryCustody external allocation/receipt 或 taskdag SQL snapshot 参数化 |
| managedlaunch projection binding | `managedlaunch` ordinary/race/vet/LSP PASS；commit/abort mismatch digest 负测 PASS | 强制 target.PreparedRequestDigest 与 source.PreparedRequestDigest 逐字一致；仍无 managedruntime supervisor/Fx/backend 接线 |
| RecoveryCustody revision-2 fail-closed | `TestOpenAndBootstrapRejectRevisionTwoWithoutImplicitMigration` ordinary/race/vet/code-size PASS | 显式旧 v2 元数据夹具保持原样；证明无隐式迁移，不等于完整旧部署升级验收 |
| unified/Room production surface guard | `cmd/mcp-orch` registry/provider ordinary + race + vet + code-size/LSP PASS | strict unified_v2 与 Room 不进入 legacy/standalone public surface；legacy schema 保持兼容，候选依赖缺失返回 disabled；不关闭 cross-task/Room NO-GO |
| task-only capability allowlist guard | `standalone/taskhttp`、`standalone/taskruntime`、common HTTP scope ordinary + race + vet + code-size/LSP PASS | 未绑定 capability manifest 的 launch/list/report/send/stop/recover/interrupt/direct-peer/wait 工具在构造期拒绝，底层 provider 零调用；已验证 task attribution 的 TaskID 仅投影为内部 AgentID，operator endpoint 仍不带 task auth/grant |
| standalone legacy send compatibility boundary | 现有 standalone 官方 Codex/Claude 双轮 E2E 与 registry surface guard PASS | operator HTTP 的 legacy `send_message(agent_id/pos, ...)` 仅作为 service-level 兼容管理路径保留，不是 CallerAttribution/AccessGrant/canonical cross-task；strict grant send 仍未注册 |
| child task identity candidate | `orchestration/childissuer` ordinary/race/vet/code-size/LSP PASS | 仅验证 resolver 已确认 parent 与外部 issuer 返回的 child identity、双端 fence、stable child key、严格 JSON 和上下文隔离；不签发 bearer、不持久化、不接 credential/OUT/CONTENT/RESOURCE/provider/Fx，production issuer 仍缺 |
| managed-runtime binding candidate | `standalone/managedruntime/issuer` ordinary/race/vet/code-size/LSP PASS | 已收窄为 endpoint/process `BindingCandidate`；显式 ProcessReceiptVerifier、provider profile digest、strict wire/one-hot closure；不是 child auth/credential issuer，未接 production supervisor/Fx |
| managed-runtime supervisor candidate | `standalone/managedruntime` CandidateSupervisor ordinary/race/vet/code-size/LSP PASS；prepare/commit/abort/query replay/conflict/unknown fence 并发负测 PASS | 只消费外部已验证 prepared receipt 的进程内候选状态机；不启动 helper、不调用 provider、不持久化、不接 Fx/DB/registry；真实 process receipt、durable query/recovery、credential/bridge 仍缺 |
| TRUST envelope candidate | `orchestration/trustcandidate` ordinary/race/vet/code-size/LSP PASS | provider/metadata/link/image 只进入 bounded quoted/inert candidate；canonical event 不内联正文，sink decision 零副作用；未接真实 trust root、CONTENT key/object、EventStore/provider/UI/network |
| tool output-schema guard | `tools`/`mcp-orch` ordinary/race/vet/LSP/code-size PASS | registry 构造时编译显式 output schema 并包裹唯一生产 handler 调用链；无 schema 不猜测，handler error envelope 仍由 common writer 负责；不等于完整 strict unified wire contract |
| process terminal projection lifecycle | orchestration ordinary/race/vet/LSP/code-size PASS | process-exit terminal 在 outbox ACK 前保留 session generation，ACK 后才清理；旧代际只在可证明 superseded 时跳过 runtime；channel enqueue/内存 exit monitor 仍不等于 durable consumer ACK |
| Gate S0 blocker evidence refs | Gate S0 47 tests（29+9+9）通过；生成 map 已在最终文档冻结后重写并检查 | B01–B18 各有 RED/GREEN/Guard 非空唯一 workspace 引用与生成计数；这些只是 planned/observed 定位信息，不是执行 receipt 或 blocker closure |
| full archtest | `go test ./internal/archtest -count=1 -timeout=12m -p=1` 返回 `EXIT=1`；约 720s 后因 `panic: test timed out after 12m` 终止，期间多处 `/tmp/archtest-run...` 报 `no space left on device`（含 Heteroci probe/cross-compile 等） | 未签发 full PASS；本轮 parity 常量定向 ordinary/race 已通过，不写成当前 frozen parity drift；磁盘耗尽/timeout 不能作为 blocker closure，仍是本机非权威验证 |
| Windows | `mcp-recovery-custody`、`mcp-orch` test binary cross-compile PASS | 无原生Windows运行/ACL/进程接管证据 |

全部结果仍为`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不证明独立服务、真实外部evidence/anchor、签名trust root、production orch接线、远程/灾备或发布资格。`current-blocker-registry.json` 中每个 blocker 的 `red_evidence_refs`、`green_evidence_refs`、`guard_evidence_refs` 仅是定位 planned/observed 文档的非空引用，不是 receipt 或通过声明；它们不能把任何 blocker 从 `needs_revalidation` 推进为 closed。引用先 canonicalize 后在各字段内去重，并排除 registry/generated map 自指；派生物的 document/evidence snapshot digest 会在这些文档内容漂移时令 `--check` fail closed。2026-09-04 最新 light guard 被既有 dirty `freeze_baseline` generated-file drift 阻断；inventory digest（current/final）由脚本计算，未通过提交或 `--no-verify` 掩盖。

## 历史稳定快照（源码冻结前）

以下命令记录的是源码冻结前的历史观测；本节不覆盖 2026-09-04 继续增量后的 full archtest 状态。

### 历史观测（2026-09-03）

本轮在不改变 production NO-GO 的前提下完成以下窄切片：

- B08 `IssueDispatch` 对已有 `effect_pending`/`outcome_unknown` attempt fail-closed，重放只允许查询原 attempt；Store/Profile/Assembler 的 nil context 也在 owner 触达前拒绝。
- RecoveryCustody 外部模式继续使用独立单实例私有 SQLite；kernel lease 绑定主库 inode，WAL/SHM 只按合法 SQLite 生命周期在锁内解绑/重绑，Open/Bootstrap/Takeover 和 mutation/read 都有 lock→DB 身份回读。
- `RuntimeEventTxCandidate` 要求 identity/source/appender 共用同一 transaction-bound DBTX，gap、坏水位和 commit-unknown 不重放；旧 split-transaction candidate 仍保持 disabled。
- identity/event store 另提供 additive Tx seam：`RequireTransactionDBTX` 拒绝裸/typed-nil DBTX，`GetActiveSnapshotTx`、`GetSourceStateTx`、`AppendTx` 与 `WithTransaction` 可在同一 SQLite 事务内复用；它们仍不分配 source sequence、不接 runtime caller/custody。
- tasktransport 新增 discover/read/send 授权 claim（parent/grant/revocation/cursor/zero-effect），unified wait candidate 改为 target source 绑定；accesswait durable owner 仍有 selector-source 语义缺口，故 public wait 继续 disabled。
- childissuer 只做 parent→child identity 校验候选；managed runtime 目录只做 endpoint/process `BindingCandidate`；Trust envelope/quoted renderer 与 registry output-schema guard 均未接入 public unified surface。
- managedruntime 另有 `CandidateSupervisor` 内存状态机候选：只接受外部已验证 prepared receipt，严格处理 PREPARE/COMMIT/ABORT/QUERY 重放、冲突和 sticky `outcome_unknown`；不启动进程或 provider，不能替代 production supervisor。
- process-exit terminal projection 延迟 session cleanup 至 durable outbox ACK；exitmonitor 满缓冲未入 channel 时释放 claim 可重试，但 channel enqueue 后进程崩溃仍可能丢失内存事件。
- RecoveryCustody 读路径现同时复核主库/WAL/SHM 对象身份，并拒绝 authority path 与固定 lock sentinel 同名或同 inode；独立服务 coordinator wiring 及 Linux/macOS candidate binary packaging 均保持 opt-in/test-owned、disabled、未接 production runner。

当前稳定快照 ordinary 结果：

```text
GOTMPDIR=<external-temp> GOCACHE=$PWD/.gocache-final \\
GOWORK=off go test ./cmd/mcp-orch/... ./cmd/mcp-recovery-custody -count=1 -p=1  # EXIT=0
```

当前稳定快照 race/vet/build：

```text
GOWORK=off go test -race ./cmd/mcp-orch/... -count=1 -p=1                        # 直接命令 EXIT=0；无 FAIL/DATA RACE
go test -race ./cmd/mcp-orch/... ./cmd/mcp-recovery-custody -count=1 -p=1      # 此前稳定快照 EXIT=0, real 739.401s（历史观测）
go vet ./cmd/mcp-orch/... ./cmd/mcp-recovery-custody                       # EXIT=0, real 6.97s
go build ./cmd/mcp-orch/... ./cmd/mcp-recovery-custody                     # EXIT=0, real 7.32s
```

最终生成物与 parity 指纹（只用于复核，不是签名授权）：

- `dependency-map.generated.md` 已由 `make orch-upgrade-render && make orch-upgrade-check` 生成并通过只读漂移检查；使用临时 Git exclude 的 project-map 为 5,591 files/8 domains/drift=OK，`ai-index`/codemap 为 393 files/1,474 refs。
- 当前派生图与 evidence 指纹以 `dependency-map.generated.md` 的 Input digests 和 acceptance inventory 的机械输出为准；本记录不内嵌会随 evidence 内容变化的摘要，避免自引用漂移。
- `internal/archtest/freeze_baseline.json` 仍为 dirty 工作树输入且本轮未修改；light guard 当前被其 generated-file drift 阻断。inventory digest（current/final）由脚本机械计算，不手工改写。

race 使用外部 GOTMPDIR `/tmp/orch-race-go-clean.3E3Koi`、GOCACHE `/tmp/orch-race-cache-clean.iUxsQK`，vet/build 使用各自外部临时目录；
所有结果仍是本机非权威证据。历史 full archtest 曾用修正 wrapper 完成并返回 exit=0；
其 1019.957s/1075.50s 仅表示静态守卫通过，不表示任何 blocker 已关闭。更早的
1018.025s、1066.951s、1120.714s 仍仅作历史观测。Python Gate
S0 已在本轮文档冻结前重新 render/check，通过后仍只表示 planning consistency。

## 仍未实现

1. RecoveryCustody独立服务的 production supervisor/CLI 生命周期、严格配置与发布安装（当前 CLI 入口在缺依赖时 fail-closed，仅有构造/生命周期候选，缺生产依赖注入与监管；private-only SQLite 模式已不再依赖 product DB/workspace 路径）；
2. network filesystem 的发布级安全证明/受信部署证据（本地 `statfs`/volume 类型守卫已对已知网络和未知类型 fail-closed，但不替代底层 backing 与远程环境证明）；
3. 真实外部EvidenceFinalizer/anti-rollback anchor与签名trust root；
4. service-owned durable coordinator 的 production 接线（job/journal、active-resolution startup scan、唯一claim与runner已有可执行候选，仍缺 supervisor 生命周期、外部 finalizer/verifier 注入与发布审计）；
5. production orch HTTP client/Fx/OUT accepted/attempt/permit/nonce/wait接线；
6. 第二task listener、child/task credential pending→active、managed CLI credential注入、identity/event runtime producer 调用链；resolver/producer owner 窄口虽已进入 Fx，task event producer、childhandoff、`unified_v2` public list/wait/send仍未进入生产 registry；legacy direct-peer transport 现已 fail-closed，但不构成 task authorization 接入；
7. B09历史唤醒显式迁移/生产 quarantine 与真实跨进程 dispatcher 证据；B10全量 mutation owner 接线（当前仅有 M01–M42 inventory guard）；B12外部 security-time allocation/receipt 与 SQL 参数化；Room durable owner/event bridge、跨daemon/跨incarnation、原生Windows、远程与发布回滚证据。migration 134 对 run 已终态后的 terminal compensation pending liveness 仍需后续 owner/receipt 设计。

因此，本轮PASS只描述精确本地入口；managed provider、Room与release继续NO-GO，不得提升为production authority、cross-task send或发布GO。

## B01–B18 closure evidence slots (planning-only)

下表是 `current-blocker-registry.json.evidence_slots` 的人可读投影。每个 closure slot 只定义未来写入权威关闭证据的位置；当前全部为 `unverified`，不得把本表或任何本机测试结果解释为 blocker 已关闭或发布 GO。

| Blocker | Reproduction anchor | Closure anchor | Classification |
| --- | --- | --- | --- |
| B01 | `blocker-revalidation-2026-09-02.md:B01` | `verification-notes.md:B01` | `planned/non-authoritative` |
| B02 | `blocker-revalidation-2026-09-02.md:B02` | `verification-notes.md:B02` | `planned/non-authoritative` |
| B03 | `blocker-revalidation-2026-09-02.md:B03` | `verification-notes.md:B03` | `planned/non-authoritative` |
| B04 | `blocker-revalidation-2026-09-02.md:B04` | `verification-notes.md:B04` | `planned/non-authoritative` |
| B05 | `blocker-revalidation-2026-09-02.md:B05` | `verification-notes.md:B05` | `planned/non-authoritative` |
| B06 | `blocker-revalidation-2026-09-02.md:B06` | `verification-notes.md:B06` | `planned/non-authoritative` |
| B07 | `blocker-revalidation-2026-09-02.md:B07` | `verification-notes.md:B07` | `planned/non-authoritative` |
| B08 | `blocker-revalidation-2026-09-02.md:B08` | `verification-notes.md:B08` | `planned/non-authoritative` |
| B09 | `blocker-revalidation-2026-09-02.md:B09` | `verification-notes.md:B09` | `planned/non-authoritative` |
| B10 | `blocker-revalidation-2026-09-02.md:B10` | `verification-notes.md:B10` | `planned/non-authoritative` |
| B11 | `blocker-revalidation-2026-09-02.md:B11` | `verification-notes.md:B11` | `planned/non-authoritative` |
| B12 | `blocker-revalidation-2026-09-02.md:B12` | `verification-notes.md:B12` | `planned/non-authoritative` |
| B13 | `blocker-revalidation-2026-09-02.md:B13` | `verification-notes.md:B13` | `planned/non-authoritative` |
| B14 | `blocker-revalidation-2026-09-02.md:B14` | `verification-notes.md:B14` | `planned/non-authoritative` |
| B15 | `blocker-revalidation-2026-09-02.md:B15` | `verification-notes.md:B15` | `planned/non-authoritative` |
| B16 | `blocker-revalidation-2026-09-02.md:B16` | `verification-notes.md:B16` | `planned/non-authoritative` |
| B17 | `blocker-revalidation-2026-09-02.md:B17` | `verification-notes.md:B17` | `planned/non-authoritative` |
| B18 | `blocker-revalidation-2026-09-02.md:B18` | `verification-notes.md:B18` | `planned/non-authoritative` |

## 2026-09-04 文档复核增量（追加）

- 工作树可见隔离 migration 137 `command_submission` durable owner（prepare、resolve/replay 与
  one-shot provider boundary）、`runtimeeventwriter`/`terminaleventbridge` terminal ingress、
  `waitoperation/recoverycandidate` 双库回执候选和未注册的 `unifiedv2` wait-operation adapter。
  源码注释、构造器和边界守卫均将其限定为 candidate-only；本段只登记存在及 NO-GO 边界，不
  记录测试通过、OUT 接线、跨进程 recovery 或 public wait 启用。
- 本次文档复核运行 `orch_upgrade_gate.py --check` 时先发现
  `dependency-map.generated.md` 漂移；随后用同一脚本 `--write` 机械刷新后再次 check 为
  `OK check`，这只证明 planning projection 一致。`orch_acceptance_inventory.py` 的 inventory
  digest（current/final）应由脚本计算；候选输入冻结后再执行脚本，不得手工改 digest、schema、
  状态或把漂移解释为验收结果。
- 针对新增候选包的 focused `go test` 因隔离缓存无依赖且宿主无网络，在
  `proxy.golang.org` DNS 阶段失败；本次复核不声称这些包 ordinary/race/vet 通过，既有 PASS
  仅限其原始运行时点。

## 2026-09-04 源码冻结后候选登记

本节只登记源码和同包测试可复查的候选边界；本次文档更新没有执行 Go、远程、HTTP 或生产
Fx 测试，所有条目继续标记 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，除明确 `RED/NO-GO` 外
不构成通过证据：

- direct-peer error-only submit 成功不再伪造 `message_id` receipt；accepted replay 保持
  `delivery_outcome_unknown` 与单次调用。wakeup provider-accept 后 crash/reclaim 具名测试仍为
  `RED/NO-GO`，观察到第二次 launch。
- migration 137 submission owner 的 caller/source/target fence、command-fence projection、
  prepare/resolve/replay 与 transaction-bound read 已有合同测试；`unified_v2` wait projection
  仍 disabled，未接 registry/provider。
- `runtimeeventwriter.AppendBound`、`terminaleventbridge` atomic verifier/append 和完整
  materialized-result 校验保持 candidate-only；EventStore watcher overflow fail-closed，
  retention index/verifier 只做 bounded transaction-bound pin 检查。
- AccessWait expiry/Tx owner、Room `PrincipalVerifier`、tasktransport stream 与 `unified_v2` list
  security clock、credentialactivation seam、attempt atomic owner、wait-operation predecessor/deadline
  修复和 standalone singleton guard/refactor 均未获得 production caller、跨进程 receipt 或
  release authority。

不得把上述源码存在、具名测试或本机历史 PASS 重新解释为 OUT、terminal ingress、public
wait、跨任务授权、统一安全时间或发布 GO。machine digest 与 registry evidence 行由主代理在
输入冻结后重新计算。

## 2026-09-04 SQLite 驱动 acknowledgement-unknown 候选追加

- `cmd/mcp-orch/store/waitoperation/commit_marker_candidate_driver_test.go` 的
  `TestCommitMarkerCandidateRealDriverUnknownAfterCommit` 使用真实 `modernc.org/sqlite` 临时文件；
  wrapper 只在底层 `COMMIT` 已完成后注入一次 `ErrCommitUnknown`，不改变生产 Fx、registry 或公共
  wait 路径。
- 第二连接 query-only 读取原 operation/digest，观察已提交 successor、`pending` marker 和
  durable fence；显式 `ResolveCommitMarker` 后 replay 返回同一 successor，journal 计数保持 2，
  因而没有重新执行 callback。定向 ordinary 命令在可写临时 Go cache 下 `PASS`。
- 这是 candidate-only 的受控驱动 seam 证据，不是原生驱动/OS acknowledgement fault、跨进程
  recovery receipt、provider/Fx 接线或 release attestation；状态继续
  `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，相关 public wait、retention、receipt 和生产路径继续
  `NO-GO`。

## 2026-09-04 最终验证边界追加

- targeted race/vet matrix 当前观测为本机 `PASS`（wait-operation、AccessWait/EventStore/
  retention、identity/tasktransport、runtime/terminal bridge、command attempt/submission、
  credentialactivation、singleton guard 等候选）；这些结果仍是 `LOCAL_NON_AUTHORITATIVE`
  `NOT_VERIFIED`，不关闭任何 `NO-GO`。
- `./scripts/test_with_guard.sh --light-guard-only` 被既有 dirty `freeze_baseline` generated-file
  drift 阻断；不作测试计数结论，也不把该阻断解释为 blocker closure。
- full archtest `go test ./internal/archtest -count=1 -timeout=12m -p=1` 返回 `EXIT=1`；约 720s 后因
  `panic: test timed out after 12m` 终止，期间多处 `/tmp/archtest-run...` 报 `no space left on device`
  （含 Heteroci probe/cross-compile 等），未签发 full PASS。focused migration/SQLC/boundary archtests
  的 PASS 不替代完整 archtest 或发布验收；本轮 parity 常量定向 ordinary/race 已通过，不写成当前
  frozen parity drift。
- 候选 ordinary/race 矩阵和 global `code_size_guard`（production/test/priority SSA）全部通过；
  project-map（临时 Git exclude）为 5,591 files/8 domains/drift=OK，codemap 为 393 files/1,474 refs，
  Gate S0 47 tests（29+9+9）通过。inventory digest（current/final）由脚本计算。
- 全量 compile/build matrix 已通过（`GOWORK=off go build ./...` 及已记录的目标平台 test-binary
  compile）；这只证明本机编译完整性，不证明 production wiring、provider receipt、跨进程 recovery
  或 release GO。
