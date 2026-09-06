# Wait Operation 下一切片

Status: Candidate-only / `NO-GO` for public enablement

## 已完成

- migration 136 新增 `orchestration_wait_operations` head 与
  `orchestration_wait_operation_target_journal` append-only journal。
- Plan 在同一 `BEGIN IMMEDIATE` 中冻结 operation key/digest、target set/order、source
  fence、selector/cursor、issued/deadline 和派生 grant/wait 引用；exact replay 使用已存
  `issued_at`，不因当前时钟过期而重建。
- AppendTarget 使用 revision、phase、predecessor digest 和 owner clock CAS；fresh
  provisioning 在 deadline 后拒绝，terminal/recovery/cleanup 仍须按显式 phase 处理。
- source/target current-binding、selector source union、wait `pending` 状态、grant/wait
  splice、cleanup continuity、aggregate head stability、one-hot phase 和 exact migration
  marker 均有 SQL/Go 守卫。
- AccessWait 提供 transaction-bound `CreateGrantTx`、`CreateWaitTx`、精确读取和 cleanup
  API；`ProvisionTarget` 用同一 outer transaction 完成 planned → grant_pending →
  wait_pending → waiting，任一失败整体回滚。
- operation-head `AdvanceState` 以 revision/status/uncertain/cleanup digest 做严格单赢家 CAS；
  `ready`、`cleanup_pending`、`cleanup_cleared` 都要求最新 target heads 的完整证明，状态重试
  必须先查询当前 head，不能把 successor-only replay 当作 predecessor 证明。
- `ResumeTarget` 只按原 grant/wait 幂等键做 durable read-back，未知结果不重试创建副作用；
  `CleanupTarget` 只消费终态资源，在同一 transaction 追加 cleanup pending/cleared journal，
  active grant 先由 AccessWait owner revoke，所有引用和 revision 保持连续。

## 2026-09-04 提交标记候选（仍为 candidate）

- migration 136 新增严格 lowercase-hex marker key、不可变 identity 和
  pending → post-proof → resolved 生命周期触发器；marker table 被 baseline 明确归入
  incremental candidate，不提升全局 startup floor 134。
- `NewStoreWithCommitMarkers` 与 `NewStoreWithAccessAndCommitMarkers` 只接受同一 `*sql.DB`
  的显式 marker owner；六条 wait-operation 写路径都在业务 callback 前安装 marker，并在
  callback 后记录 canonical pre/post digest。
- pending marker 在新 Store 构造时恢复 query-only fence；`ResolveCommitMarker` 只在当前
  operation/target digest 与 post proof 完全相等时收口。精确 replay 不重做 owner effect，
  callback 错误回滚 marker；marker key、optional target references 和 omitted output digest
  的 replay identity 均有回归。context-scoped candidate seam 还可在成功 COMMIT 后注入一次
  `ErrCommitUnknown`，覆盖重开 query-only、显式 proof resolve 和 callback 不重跑。
- 读取 target journal 时同步复核 predecessor→successor phase edge、不可变 identity 和
  grant/wait 资源连续性；即使恢复数据绕过 SQLite trigger，非法但摘要自洽的阶段跳转也会
  fail-closed。
- 仍没有真实驱动层 COMMIT acknowledgement fault、跨 daemon supervisor、recovery receipt
  或生产 Fx wiring；这些证据不能由本机 marker test 替代。

## 依赖关系

```text
identity current binding + canonical EventStore cursor
        ↓
AccessWait Tx owner (grant / wait)
        ↓
wait-operation Plan + target journal
        ↓
ProvisionTarget candidate
        ↓
future Resume / Cleanup / aggregate owner
        ↓
future strict wait_agents surface
```

`ProvisionTarget` 不调用 provider、direct-peer、OUT、RecoveryCustody、HTTP 或 public
registry；`NewStore` 仍是 plan-only，缺少显式 AccessWait owner 时直接报错。
其 AccessWait `CreateWaitTx` 还要求 selector source 已有 active canonical watermark；
新源必须先由 EventStore owner 建立 source state，不能用空状态默认为可读。

## 下一阶段必须补齐

1. commit-unknown 跨重启验收：把 marker 与恢复 receipt 接到可注入的
   COMMIT-after-callback fault seam 和跨 daemon supervisor，重启后先做 exact
   query/reconcile，再解除写 fence；当前 marker candidate 只能证明 schema/owner 逻辑，
   禁止把它当作真实 commit-unknown receipt。
2. watcher/retention：把 post-commit EventStore watcher 与当前
   `retentioncandidate.Verifier` 接入 operation 生命周期，补 production index/GC、跨进程
   pin owner，并证明 gap、quarantine、floor、取消、超时和 slow-consumer 边界。
3. aggregate owner 与 strict HTTP：完成 `wait_agents` 的 command/command_submission
   分支、child credential、SSE listen/resume、跨进程 recovery 和 bounded response，最后
   才能进入 Fx/manifest/public surface。

## 验收命名

- `TestWaitOperationMigrationContract`
- `TestStorePlanReplayAndTargetCAS`
- `TestProvisionTargetPlannedCommitsGrantWaitAndJournal`
- `TestProvisionTargetConcurrentReplayHasOneDurableWinner`
- `TestProvisionTargetWaitFailureRollsBackGrantAndJournal`
- `TestProvisionTargetReopensAndReplaysDurableResult`
- `TestAccessWaitTxOwnerAtomicRollbackAndCallerCommit`
- `TestAdvanceStateReadyAndStaleReplayConflicts`
- `TestCleanupTargetRevokesGrantAndReplaysDurableResult`
- `TestResumeTargetReadsExistingWaitExactlyOnce`
- `TestCommitMarkerPlanAndStateLifecycle`
- `TestCommitMarkerPendingReopenResolveAndReplay`
- `TestCommitMarkerResolvedReplayWithoutDerivedRecordDigest`
- `TestCommitMarkerProofMismatchFencesOwner`

这些名称只定位本机候选测试；它们不是 GREEN receipt、blocker closure 或发布授权。
