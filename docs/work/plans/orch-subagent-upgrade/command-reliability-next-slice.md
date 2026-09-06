# ST-11 下一实现批次：可靠命令与未知结果

Status: Active

日期：2026-09-01；父工作包：[Gate S0](README.md)。

## 当前源码复核

对象为隔离工作树 `.worktrees/20260831-orch-subagent-upgrade`，HEAD `d95c6379812d9d9b95e8831c4713d219752e3eaa` 加已有未提交改动。
下列路径已做源码审查。direct-peer 的首次 mark 失败场景另取得实际故障注入 RED；历史 B08 的 wakeup 分支仍需独立具名 RED/GREEN 证据才能关闭。

- `cmd/mcp-orch/tools/direct_peer_tools.go`：先 `Store.Send` 写 accepted，再调用 `Turns.SubmitTurn`。当前 error-only `TurnSubmissionPort` 的成功返回也不携带 provider receipt，因此不再调用 `MarkSubmitted` 或伪造 `submission_id`；保持 accepted 并返回不可授权的 `delivery_outcome_unknown`。一次性 `Fresh` 事务标记保证同 key 的 accepted 重放不再调用 `SubmitTurn`，这只是应急止重放，不是 provider receipt 或 OUT 完成。
- `internal/contract/orchestration.go` 的 `TurnSubmissionPort` 仅返回 error，不能承载 command/attempt/provider receipt/outcome；standalone 取得的 provider turn ID 没有完整传给该调用方。
- `cmd/mcp-orch/orchestration/wakeup_dispatcher.go` 的 legacy 路径仍是 LaunchAgent 后 MarkWakeupSent；reclaim 可使未知结果的 wakeup 再次进入 launch。
- DAG 的 `spawning_thread_id` 可以避免已记录 spawn 的某些重放，但不能覆盖 provider 成功到写回之间的崩溃。
- `store/commandcard` 管定义，`store/directpeer` 管消息/event outbox，`store/terminaloutcome` 管执行后的终态；这些均不能直接冒充执行前统一 command owner。

## 批次边界

先完成 OUT、ID、TRUST、CONTENT、RESOURCE 的适用 schema/owner 裁决与 VERIFY_S/ROLLOUT_S 的批准模型。
S0 离线清单通过不授权外部 dispatch。第一实现批次可以先建立内部类型、状态转移与隔离持久化 fixture，生产接线保持关闭。
不能把下面的最小字段摘要直接当生产 DDL，绕过完整 source/target fence、content/resource、recovery/authorization 合同。

建议唯一 command store 落点为 `cmd/mcp-orch/store/orchestrationcommand`，SQL 归现有 orch SQL 树和 sqlc 生成链。
不新增 `directpeer_commands`，不把 provider command attempt 塞进 direct-peer event outbox，不扩张 commandcard 为执行系统。

## 实现顺序与必须保持的语义

1. 冻结 canonical intent：caller principal、scope/source、operation kind、target reservation/execution fence、稳定 key、真实请求 digest。
   规范化字段与算法必须有动态 producer/consumer 证据；内容由 CONTENT owner 引用，禁止平行保存原始 prompt。
2. 实现唯一 accept/get-by-key：同 key 同 digest 返回原 command/submission，异 digest 冲突。
   pending custody/accept 不是 accepted，也不能进入 provider dispatcher；结果未知只能查原 operation。
3. 实现 attempt/dispatch receipt：任何 provider call 之前固定 durable intent 与所需 receipt/fence。
   lease 过期不等于未执行；dispatch 可能发生时进入 outcome_unknown，禁止凭超时再创建 attempt。
4. 建立稳定提交结果窄端口，将 command/submission/provider acknowledgment 与执行终态区分。
   更新 direct-peer、wakeup、DAG、standalone 和旧 adapter 的完整字段链，不在旧 error-only port 上局部猜测结果。
5. direct-peer/wakeup 只负责创建或找回同一 command，并从 command owner 投影状态。
   pre-submit 确定性拒绝与 post-submit unknown 必须分开；message_id 不能冒充 provider submission ID。
6. 最后才启用有完整祖先/能力证据的生产 dispatcher，按 provider 独立验证，不补默认能力或盲重发。

## 首批回归目标

| 测试目标 | 故障点 | 必须断言 |
| --- | --- | --- |
| TestDirectPeerAcceptedReplayAfterMarkFailureIsFailClosed | error-only 提交端口成功返回但无 provider receipt（保留历史回归名） | external call count=1；不写 MarkSubmitted；重试不再调用 provider，返回 non-authorizing unknown |
| TestDirectPeerSubmitErrorIsOutcomeUnknown | error-only provider 提交端口无法证明是否已接受 | 保留 accepted；不写 definitive delivery_failed，返回 non-authorizing unknown 且不重发 |
| TestCommandAcceptSameKeyDifferentDigestConflicts | 同 key 异 payload/fence | 不产生第二 command/attempt |
| TestWakeupReclaimAfterProviderSuccessDoesNotDuplicateLaunch | provider 成功后 mark 前崩溃 | reclaim 只查询原 command，launch count=1 |
| TestWakeupDispatcherDoesNotLaunchBeforeCommandAccepted | accept/custody pending 或 unknown | provider call count=0 |
| TestDAGWakeupReclaimUsesExistingCommand | spawn identity 写回成功/失败两分支 | command owner 决定重放，不以 node 字段推断 |

表中尚未取得源码实现的 wakeup/DAG/command 测试仍是规划目标，不是当前可运行测试或通过证据；只有下文列出的现有 direct-peer 测试名才可作为本轮结果引用。

可复用现有 `directPeerStub/directPeerTurnStub`、`dispatcherStubStore/dispatcherStubLauncher` 和 `remoteTurnFenceLauncher` 构造确定性故障。
单测之后还需要真实 SQLite 事务、双进程 crash/reclaim、字段守卫和 provider conformance；fake call-count 不替代真实进程证明。

## 已复现的 direct-peer RED 与应急收敛

早期 duplicate-repro 临时测试（当前源码已删除）曾经过生产 `NewRegistry(...).Lookup("send_direct_peer")`、真实 SQLite directpeer.Store 和可信 ToolScope，首次 `MarkSubmitted` 注入失败且消息保持 accepted，然后用同一个 key 重试。精确 host light wrapper exit 1：`provider SubmitTurn calls=2, want=1; old handler resubmitted accepted message`。测试本体0.075s，archtest前置266.404s，前/中/后资源门均放行；复现输入保留在 [directpeer-duplicate-repro.gotxt](directpeer-duplicate-repro.gotxt)，不可再按测试名重跑。

这里的 SubmitTurn 是计数 fake port，不是外部模型调用；它证明旧生产 handler 会重复提交，不冒充真实 provider 计费/网络副作用证据。当前应急回归 `TestDirectPeerAcceptedReplayAfterMarkFailureIsFailClosed`（保留历史回归名）已证明 error-only 成功返回不会写伪造 receipt，重试 provider call count 保持 1，并返回 stable `delivery_outcome_unknown`；`Fresh` 是瞬时诊断元数据，不是 provider receipt。完整复现代码保存为 [directpeer-duplicate-repro.gotxt](directpeer-duplicate-repro.gotxt)，生产 OUT 接线时必须把该应急分支替换为原 command/attempt 查询与 reconcile；provider error 的 unknown、真实 receipt ID、wakeup/DAG 恢复和 ST-11 仍未关闭。

## 当前状态

已进入首批实现：[不可变命令意图存储](command-intent-storage.md)，新增 migration126 与 Reserve/Get/GetByKey 组件；这只建立意图存储基础。direct-peer accepted replay 已有 fail-closed 应急止重放；新增的 `wakeupcommandcandidate` 还把已有 `effect_pending`/`outcome_unknown` attempt 的 `IssueDispatch` 重放锁死，并补了 nil-context fail-fast，但仍未切换到 durable OUT，也未修复 ST-11/B08 的跨进程 receipt/dispatcher 证据，不能关闭上表的 provider/wakeup 故障测试。
当前精确状态已复核：migration126/`Intent`保存独立principal/scope；migration131的[send_message_v1 profile](send-message-intent-profile.md)以一对一扩展保存完整CallerPrincipal、authorization request/grant/revision/time和双端最新ID-1 binding；migration132又加入[CONTENT-1](content-1-encrypted-single.md) metadata/ref/legacy quarantine与同事务command deletion fence。`BEGIN IMMEDIATE` owner、HMAC verifier、关系trigger、schema gate、mapper/field-chain guard与未装配composition assembler均已有本机候选验证。RecoveryCustody 数据库 revision 3（持久化 external-service mode）、DB-resident writer fence、独立服务 CLI 与 durable reconcile coordinator 也已有可执行候选。旧`accesswait.CommandAdmission/CommandResult.Accepted` fake seam已删除。当前 `orchestrationcommand` Module 只提供 test-owned 的 `recovery_custody_pending` genesis + 永不放行 verifier，Apply 不推进 journal；它不改变 production 规则。剩余阻断转为production CONTENT adapters、RESOURCE/custody真实producer、外部 finalizer/verifier、production supervisor/Fx/OUT、keyring/recovery-domain证据与OUT接线；依赖不全时 production 不得进入`recovery_custody_pending`、返回accepted或进入dispatcher。

本轮新增 `CommandAttemptGateway.Execute` candidate：在 typed provider seam 前原子消费既有 permit，
provider 返回 accepted/definitive failure 才可由原 attempt reconcile；调用错误、panic、unknown 或
重复执行均保持 sticky unknown/terminal，provider 调用次数不会因网关重试增加。该方法未进入 Fx、
dispatcher、provider adapter 或 public registry，不能替代真实 receipt/custody/recovery 证据。

对应的本机候选验收名称为 `TestCommandAttemptExecuteConsumesBeforeProviderAndReconciles`、
`TestCommandAttemptExecuteProviderErrorStaysUnknown`、
`TestCommandAttemptExecuteConcurrentHasOneProviderCall` 和
`TestCommandAttemptExecuteProviderPanicStaysUnknown`；它们只证明本地 typed seam，不是 OUT GREEN receipt。

随后已补[proof/custody fail-closed合同](pre-admission-proof-and-custody-contract.md)：assembler在写profile前必须取得owner-issued CONTENT ref；post-profile Gate重新验证HMAC和CONTENT/RESOURCE proof后才可构造custody request；local custody adapter只返回indeterminate并拒绝伪receipt。没有真实owner/authority时仍无profile生产接线、无lifecycle pending、无provider call。managed provider、Room与release继续NO-GO。
下一批先写能复现当前问题的 RED，再实现适用内部 owner；不得直接把历史规格中的未来架构当作已修复证据。

## 2026-09-04 源码冻结后增量

- direct-peer 的 error-only `TurnSubmissionPort` 成功返回不携带 provider receipt 时，handler
  不再把 `message_id` 当作 `submission_id`，也不调用伪造的 `MarkSubmitted`；accepted replay
  固定为一次 provider call，并返回不可授权的 `delivery_outcome_unknown`。这只收敛旧 handler
  的重复副作用，不是 OUT receipt 或生产 command owner。
- `TestWakeupDispatcherSQLiteCrashWindow_RED_NO_GO` 已将 legacy wakeup 的
  provider-accept→mark 前 crash/reclaim 窗口固定为 `RED/NO-GO`：第二连接 reclaim 后可再次
  launch，CAS 只能保护 stale write，不能证明 provider exactly-once。canonical command/
  attempt/recovery receipt 仍缺。
- 隔离 migration 137 `command_submission` durable owner 已完成 caller/source/target fence、
  intent digest、accepted/rejected command-fence projection 及 transaction-bound
  prepare/resolve/replay；`unified_v2` command-submission wait adapter 仍以
  `ErrCommandSubmissionWaitDisabled` fail-closed，未接 registry、provider 或 recovery owner。
- candidate `CommandAttemptGateway.Execute` 先原子消费 permit，再调用 typed provider；error、
  panic、pending/unknown 和重复执行均保持 sticky unknown/terminal，provider call 不因 gateway
  重试增加。它仍未进入 Fx/dispatcher/public OUT，不能替代真实 receipt/custody。
