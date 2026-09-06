# Orch 子 agent 升级当前快照（2026-09-04）

这是截至 2026-09-04 的工作树状态锚点。对象为
`/Volumes/hello/wj/super-agent-v3/.worktrees/20260831-orch-subagent-upgrade`；本文件只记录
本机候选实现和可复查验证，不改变 Gate S0、B01–B18 或发布决策。

## 当前决策

- 总计划保持 `Active`；Gate S0 十四份子规格仍为 `Draft`，`current-blocker-registry.json`
  的 B01–B18 仍为 `needs_revalidation`。
- 所有新代码、迁移、守卫和测试仍属于
  `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。`unified_v2`、`wait_agents`、Room、跨任务
  canonical send、OUT/receipt/custody 生产接线、跨进程 recovery 和发布验收继续 `NO-GO`。
- 分支 `codex/20260831-orch-subagent-upgrade` 没有 upstream；本轮没有提交、合并、清理或推送。

## 本轮实现

### 身份与生命周期

- `internal/util/idgen` 增加进程级唯一协调器，同时保留每个 `Generator` 的独立本地状态，
  消除不同生成器在同一纳秒刻度产生重复 `agent_*` ID 的窗口。
- launcher exit-before-bind、binding projection/rehydrate、strict terminal route/recovery、
  buffered terminal recovery 和 standalone Codex/Claude canonical terminal retry 已落地为候选。
- terminal outbox 增加 outbox/history/public/private DAG 全身份交叉校验、poison/quarantine 和
  单坏项不阻塞后续有效项。

### 事件与等待

- EventStore watcher 只在 canonical append 成功提交后通知，rollback、gap、quarantine、replay、
  `AppendTx` 不唤醒；watcher 有 source/epoch/lane 过滤、容量上限、取消和关闭清理。
- EventStore 增加 opt-in transaction-bound `RetentionPinVerifier` seam；墓碑推进前先做同事务
  pin 校验，verifier 错误回滚并 fail-closed。新增 `retentioncandidate.Verifier` 与 SQLC
  candidate query，读取 pending AccessWait handles 和最新 wait-operation target heads，
  对 pin 数量和 malformed lifecycle 有界 fail-closed；尚无 production index/GC、跨 daemon
  supervisor 或 Fx wiring，不能视为 retention 闭合。
- AccessWait 增加显式 UTC clock、expiry candidate/runner 和 transaction-bound
  `CreateGrantTx`、`GetGrantTx`、`CreateWaitTx`、`GetWaitByKeyTx`、`GetWaitByIDTx`、`CancelWaitTx`、
  `RevokeGrantTx`。Tx API 拒绝裸/typed-nil/未知 `DBTX`，不创建嵌套事务。
- waitprovision 使用稳定 grant ID、canonical cursor 和 durable replay；waitaggregate 增加
  clock seam、panic/`Goexit` fail-closed 和 bounded sibling cancellation；unified wait 结果
  在 owner 返回后做严格 `WaitResult.Validate()`。
- migration 136 新增候选 wait-operation head/append-only target journal。其 Plan/Get/AppendTarget
  与 `ProvisionTarget` 只在内部工作：冻结 source/target identity、selector/cursor、派生
  grant/wait 引用、issued/deadline、revision/CAS、phase one-hot、cleanup continuity、
  aggregate head stability 和 exact schema marker。ProvisionTarget 使用一个外层 SQLite
  `BEGIN IMMEDIATE`，在同一 transaction-bound DBTX 内完成 grant + wait + journal；失败回滚，
  exact replay 不重新 mint。
- operation-head `AdvanceState` 已增加严格单赢家 revision/status CAS 与 ready/cleanup aggregate proof；
  `ResumeTarget` 只读回原 grant/wait durable 结果并追加安全 successor，`CleanupTarget` 在
  同一 transaction 保留资源引用、拒绝 unknown/pending 资源、撤销 active grant 后写入
  cleanup-cleared。两者均未接 provider、Fx、aggregate 或 public surface。
- wait-operation 新增 opt-in durable commit-marker candidate：Plan、AppendTarget、AdvanceState、
  ProvisionTarget、CleanupTarget 和 ResumeTarget 都在同一 `BEGIN IMMEDIATE` 中安装 marker，
  以 canonical pre/post-state digest 做回读证明；pending marker 可在重启后恢复并阻断新写，
  只有显式 `ResolveCommitMarker` 证明当前 post-state 后才解除 durable fence。marker owner、
  schema key/trigger 和 marker-enabled constructors 仍未进入生产 Fx/provider caller；新增的
  context-scoped candidate seam 可在提交成功、marker 尚未 resolve 的窗口注入一次
  `ErrCommitUnknown`，但真实 COMMIT acknowledgement fault、recovery receipt、retention pin、
  SSE/listen-resume 和 public tool 仍缺，因此仍是 candidate-only。
- wait-operation read/recovery path 另补齐了与 migration 136 一致的 predecessor→successor
  phase edge、identity 和资源连续性校验，防止触发器旁路或恢复数据篡改后仅凭 canonical
  record digest 接受非法阶段跳转；该校验仍只属于本机 candidate。

### 命令与边界

- migration 135 command-attempt candidate 增加 one-shot consume/unknown sticky 语义，但仍未接
  dispatcher、direct-peer、provider boundary、RecoveryCustody 或真实 receipt。
- `CommandAttemptGateway.Execute` 增加未注册 typed-provider 执行候选：先消费 durable permit，
  只把 accepted/definitive failure 交给原 attempt reconcile；错误、panic、unknown 和重复调用
  均保持 sticky unknown/terminal，避免 provider 重复 effect。未接 Fx/dispatcher/provider。
- 2026-09-04 增量候选新增隔离 migration 137 `command_submission` durable owner（prepare、
  resolve/replay 与 one-shot provider boundary），以及 `runtimeeventwriter`/
  `terminaleventbridge` terminal ingress、`waitoperation/recoverycandidate` 双库回执候选和
  未注册的 `unifiedv2` wait-operation adapter。上述源码/构造器均保持 candidate-only，未接
  production Fx/provider/public registry；本快照不把它们当作 OUT、terminal ingress、跨进程
  recovery 或 public wait 的实现。
- low-risk terminal handler/doc 结构整理、field-consumer registry、SQLC manifest ownership
  guard 和 migration 136 contract fixtures 已补齐；全局 startup schema floor 仍保持 134。

## 可复查验证

| 面向 | 结果 | 边界 |
| --- | --- | --- |
| wait-operation owner | `go test ./cmd/mcp-orch/store/waitoperation -count=1 -p=1`、`-race`、`go vet` 通过；marker lifecycle、提交后未知注入、pending reopen/proof/replay、state/cleanup/resume、read-side phase/continuity targeted tests 通过 | 本机 SQLite candidate；注入发生在已成功 COMMIT 后的 owner seam，仍无真实驱动层 acknowledgement fault、跨 daemon recovery receipt |
| AccessWait/EventStore/waitaggregate/waitprovision/unified wait | ordinary focused matrix 通过；AccessWait 全包 race 由 Tx seam 复核通过；CreateWait source admission、EventStore retention-pin seam 与 `retentioncandidate.Verifier` 定向 ordinary/race/vet 通过 | watcher、expiry、retention candidate 的 production wiring、aggregate 和 waitprovision 都未进入 public/Fx |
| migration 136 | `TestWaitOperationMigrationContract`、全部 migration contracts、embedded fresh/legacy/parity、baseline incremental/query-table 通过 | 136 仍是低于 startup floor 的 candidate migration |
| wait-operation provisioning | planned→grant_pending→wait_pending→waiting、16 路同 operation replay、deadline fail-fast、rollback zero rows、SQLite reopen replay、state/cleanup/resume candidate 通过 | concrete AccessWait owner；无 provider malformed-response adapter、aggregate/public Resume/Cleanup |
| identity regression | `idgen`、`nodeexec` ordinary/race/vet，`nodeexec` 100 次 stress 通过 | 仍是本机测试，不是跨进程 identity authority 证明 |
| full `mcp-orch` ordinary | 本次未重跑；历史稳定快照在 loopback socket approval 下通过 | 本机候选矩阵，不是远程/发布验收 |
| full `mcp-orch` race | 本次未重跑；历史稳定快照在 loopback socket approval 下通过 | 本机竞争矩阵，不是远程/发布验收 |
| candidate ordinary/race matrix | 候选 ordinary 与 race 矩阵均通过 | 仅为本机候选证据，不改变 `LOCAL_NON_AUTHORITATIVE`/`NOT_VERIFIED` 或任何 `NO-GO` |
| global code-size guard | production、test、priority SSA 三个范围全部通过 | 静态复杂度守卫通过不等于运行时、远程或发布验收 |
| light guard | `GOTMPDIR=<tmp> GOCACHE=<tmp> ./scripts/test_with_guard.sh --light-guard-only` 被既有 dirty `freeze_baseline` generated-file drift 阻断 | 未修改冻结基线；不把该阻断解释为 blocker closure 或发布授权 |
| full archtest | `go test ./internal/archtest -count=1 -timeout=12m -p=1` 返回 `EXIT=1`；运行约 720s 后因 `panic: test timed out after 12m` 终止，期间多处 `/tmp/archtest-run...` 报 `no space left on device`（含 Heteroci probe/cross-compile 等） | 未签发 full PASS；本轮 parity 常量定向 ordinary/race 已通过，不把本次失败归因于当前 frozen parity drift，也不把磁盘耗尽或 timeout 当作 blocker closure |
| SQLC shape | `go run ./scripts/sqlc_config_outputs --git-binary /usr/bin/git --verify-shape` 通过 | shape 通过；exact verify 还被 visible-but-untracked generated output 与 host GitRunner 权限边界阻断，不能暂存以规避 |
| project/codemap/Gate S0 | 使用临时 Git exclude 的 project-map 为 5,591 files/8 domains、drift=OK；codemap 为 393 files/1,474 refs；Gate S0 47 tests（29+9+9）通过 | 只证明索引/规划输入一致，不授予运行或发布资格 |
| pinned SQLC verify | `make sqlc-verify-worktree` 已运行生成/后处理，但在 `internal/store/sqlc/prompt_template_sql_test.go` 的 Git tracked-state 检查返回 `operation not permitted` | 这是当前宿主工具权限边界；未用 `--no-verify` 或手工伪造结果 |
| 文档/索引复核 | `orch_upgrade_gate.py --check` 在机械刷新 `dependency-map.generated.md` 后通过；inventory digest（current/final）由脚本计算 | 候选提案/索引仍在并行变更；不手工改 digest、schema 或状态，不把索引结果解释为验收结论 |

full race 已在本次源码冻结后通过；所有命令结果均只表示当前本机工作树，不表示 blocker closure、
production readiness 或 release GO。

## 仍然阻断

1. 真实 provider/runtime caller 尚未把 task lifecycle 接入 canonical EventStore；
   `runtimeeventwriter`、watcher、expiry runner、retention verifier 和 wait-operation ProvisionTarget 都没有进入
   production Fx/dispatcher 调用链。
2. OUT 仍缺通用 command boundary、CONTENT/RESOURCE/custody admission、actual-boundary
   permit/nonce consume、typed provider submission/receipt、unknown reconcile 和 reclaim 后
   exactly-once crash 证明；direct-peer/legacy send 仍不是 canonical cross-task send。
3. wait-operation 已有 opt-in durable unresolved-marker candidate，并有 owner 层提交后未知
   注入测试；真实驱动层 COMMIT acknowledgement fault、跨 daemon recovery receipt、retention pin 的 production index/GC 与
   aggregate operation recovery仍未证明；marker-enabled constructors 也未进入生产 Fx/provider caller。
4. `wait_agents`/`unified_v2` public registry、child/task credential activation、strict HTTP
   SSE/listen-resume、Room durable owner/event bridge 和 UI projection 均未启用。
5. provider terminal ingress/session-incarnation schema、真实外部 EvidenceFinalizer/trust root、
   RecoveryCustody supervisor、原生 Windows/远程/网络文件系统和发布回滚证据仍缺。
6. full light guard 被既有 dirty `freeze_baseline` generated-file drift 阻断；full archtest
   `go test ./internal/archtest -count=1 -timeout=12m -p=1` 返回 `EXIT=1`，约 720s 后因
   `panic: test timed out after 12m` 终止，期间多处 `/tmp/archtest-run...` 出现 `no space left on device`
   （含 Heteroci probe/cross-compile 等），故不记为 PASS。远程
   provider conformance 和 release artifact 验收尚未由本快照签发。

## 工作树边界

- HEAD 为 `d95c6379812d9d9b95e8831c4713d219752e3eaa`；git common dir 为
  `/Volumes/hello/wj/super-agent-v3/.git`。
- 工作树原有大量用户/代理 dirty 与 untracked 生成物；本轮只在目标候选文件和测试/快照中追加
  改动，未 reset、clean、stage、commit 或 push。
- 本文件不是验收签名、授权、receipt 或发布许可；后续任何源码、迁移或生成物变化都必须生成
  更新日期快照并重新运行相应验证。

## 2026-09-04 SQLite 驱动 acknowledgement-unknown 候选登记（追加）

- `cmd/mcp-orch/store/waitoperation/commit_marker_candidate_driver_test.go` 新增
  `TestCommitMarkerCandidateRealDriverUnknownAfterCommit`：以真实 `modernc.org/sqlite` 驱动和临时
  SQLite 文件执行 owner append，由 candidate driver wrapper 在底层 `COMMIT` 已成功后隐藏一次
  acknowledgement 并返回 `ErrCommitUnknown`。
- 测试关闭并以第二个 `sql.DB` 重开，先按原 operation/digest 做 query-only 回读，确认 successor
  已提交且 marker 仍 `pending`、durable fence 仍在；显式 `ResolveCommitMarker` 证明 post-state 后
  才 replay，并确认返回原 successor、journal 仍为两行且 callback 未重跑。
- 该测试在可写临时 Go cache 下 targeted `go test` 为本机 `PASS`；它是受控 wrapper 注入，不是
  原生驱动/OS acknowledgement 故障、跨 daemon recovery receipt 或 production owner 证明。实现和
  构造器继续 `LOCAL_NON_AUTHORITATIVE`/`NOT_VERIFIED`，真实 recovery、provider/Fx 接线、public
  wait、retention/receipt 与发布验收仍 `NO-GO`。

## 2026-09-04 源码冻结后追加事实

以下项目是源码与同包合同/守卫所覆盖的最新候选；它们均保持
`Active`/`Draft`、`LOCAL_NON_AUTHORITATIVE`、`NOT_VERIFIED` 或 `NO-GO` 边界，不能解释为
生产接线、外部 receipt 或发布授权。本次只做文档复核，没有重跑 Go、远程或 production wiring
测试。

- direct-peer 的 error-only `TurnSubmissionPort` 成功返回不再把 `message_id` 推导为
  `submission_id`，也不再写伪造 `MarkSubmitted`；accepted 重放保持一次 provider 调用并返回
  `delivery_outcome_unknown`。这是 fail-closed 应急候选，不是 provider receipt 或 OUT GREEN。
- wakeup 的跨连接 provider-accept→进程退出→lease reclaim 测试固定为 `RED`/`NO-GO`：旧
  dispatcher 可在 `MarkWakeupSent` 前崩溃后再次 launch，观察到 `accepted_launches=2`；尚未接
  canonical command/attempt/recovery receipt。
- 隔离 migration 137 `command_submission` 已完成 caller/source/target fence、intent digest、
  accepted/rejected `CommandFence` 与 transaction-bound prepare/resolve/replay 投影；
  `unified_v2` command-submission wait adapter 仍明确 disabled，未注册 registry、provider 或
  wait verifier。
- `runtimeeventwriter.AppendBound` 与 `terminaleventbridge` 共享同一 transaction-bound
  binding verifier/append seam；bridge 在传播前执行完整 `ValidateMaterializedResult`，缺失或
  伪造 materialized receipt 转为 `ErrOutcomeUnknown`。两者仍是未注册 terminal ingress
  candidate，没有 provider session、Fx 或 public surface。
- EventStore 新增有界 overflow watcher（溢出关闭订阅并要求 durable cursor 回读）与
  transaction-bound retention candidate index/verifier；两者只提供提示/阻断，不执行物理 GC、
  跨 daemon owner 或 production retention wiring。
- AccessWait 的显式 security-time expiry candidate/runner 和 transaction-bound Tx owner
  seam 已补齐；无注入 clock、坏行、竞争 CAS 或非事务 DBTX 均 fail-fast。runner 未接 Fx、
  `unified_v2` 或 RecoveryCustody security-time owner。
- Room `Owner` 现在要求显式上游 `PrincipalVerifier` 才能 mutation；成员快照、grant digest
  和本地 owner 都不能自证授权。Room 仍为 transaction-neutral、进程内 candidate，未接
  SQLite、Fx、provider、RecoveryCustody 或 public tool。
- tasktransport stream 与 `unified_v2` list 只接受显式 security-time `ValidateAt`/clock：
  session、task/room scope、caller、cursor/handle expiry 和 GET/POST/DELETE carrier 均逐字段校验；HTTP/SSE、
  task listener、credential 和 public registry 尚未启用。
- managed-runtime `credentialactivation` 仅保留 prepared→active 的外部 receipt、current
  binding、security-time 与 anti-replay owner seam；不读取或暴露凭据正文，未进入 production
  lifecycle/Fx/provider。
- command attempt 的 permit consume、provider call 与 unknown/terminal reconcile 已在
  candidate owner 内原子化并保持 sticky unknown；仍未接真实 provider receipt、OUT dispatcher
  或 RecoveryCustody。
- wait-operation 已补 predecessor→successor phase/资源连续性和 deadline fail-fast 检查；
  migration 136 commit-marker、recoverycandidate 与 public wait 仍 disabled/candidate-only，
  没有跨 daemon COMMIT acknowledgement 或恢复 receipt。
- standalone serve singleton guard/refactor 固定为先 `AcquireInstance`、再将数据库/app 路径
  rebind 到 canonical state dir，退出时释放 lock；这是启动顺序架构守卫，不等于多进程发布或
  RecoveryCustody singleton 证据。

## 2026-09-04 最终验证边界追加

- targeted race/vet matrix 继续为本机 `PASS`（覆盖 wait-operation、AccessWait/EventStore/
  retention、identity/tasktransport、runtime/terminal bridge、command attempt/submission、
  credentialactivation 与 singleton guard 等已列候选）；这些结果不改变 `LOCAL_NON_AUTHORITATIVE`
  `NOT_VERIFIED` 和各项 `NO-GO`。
- `./scripts/test_with_guard.sh --light-guard-only` 被既有 dirty `freeze_baseline` generated-file
  drift 阻断；不作测试计数结论，也不把该阻断解释为 blocker closure。
- full archtest `go test ./internal/archtest -count=1 -timeout=12m -p=1` 返回 `EXIT=1`；约 720s
  后因 `panic: test timed out after 12m` 终止，期间多处 `/tmp/archtest-run...` 报
  `no space left on device`（含 Heteroci probe/cross-compile 等），未签发 full PASS。只认可已完成的
  focused migration/SQLC/boundary archtests；本轮 parity 常量定向 ordinary/race 已通过，不写成当前
  frozen parity drift，也不把磁盘耗尽或 timeout 当作 blocker closure。
- 候选 ordinary/race 矩阵已通过；global `code_size_guard` 的 production、test、priority SSA
  范围全部通过。两者均为本机静态/测试证据，不改变 `LOCAL_NON_AUTHORITATIVE`、`NOT_VERIFIED`
  或现有 `NO-GO`。
- project-map 使用临时 Git exclude 后为 5,591 files/8 domains、drift=OK；codemap 为
  393 files/1,474 refs；Gate S0 47 tests（29+9+9）通过。inventory digest（current/final）
  由脚本计算，禁止手工改写。
- 全量 compile/build matrix 已通过（`GOWORK=off go build ./...` 及已记录的目标平台 test-binary
  compile）；这只证明本机编译完整性，不证明 runtime wiring、provider receipt、跨进程 recovery
  或发布 GO。
