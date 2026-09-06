# B08 唤醒命令/attempt 隔离候选

Status: Candidate / Active / `LOCAL_NON_AUTHORITATIVE`，不能宣称修复 B08。

本候选位于 `cmd/mcp-orch/orchestration/wakeupcommandcandidate`，是一个
transport-neutral、内存内的合同模型。它不接入当前 wakeup dispatcher、B09
载荷迁移、Fx/provider、OUT、RecoveryCustody，也不使用 PostgreSQL 或 product DB。
生产路径仍保持原状；当前 B08 审计中记录的“先 `LaunchAgent`、后
`MarkWakeupSent`”风险没有因为本目录存在而关闭。

## 合同

- `WakeupCommand` 以 `wakeup_id`、`wakeup_fence`、intent key/digest 和首个
  `attempt_number=1` 形成不可变绑定。同一 key 的同一 digest 只能返回完全相同的
  命令；digest 不同返回 `ErrDigestConflict`，绑定字段不同返回
  `ErrReplayConflict`，不会覆盖原记录。
- `IssueDispatch` 是 record-before-effect 边界：只有已接受命令且 fence 精确匹配
  才能创建唯一 attempt，并同时固定 `dispatch_receipt`、`permit`、`nonce`。
  首次返回后，在 provider 结果尚未落定的 pending 窗口再次调用 `IssueDispatch`
  必须返回 `ErrAttemptUnresolved` 且不再交出 permit；原 attempt 只能通过
  `Reconcile` 查询。provider 调用方必须先通过 `ValidatePermit`；候选本身不执行
  provider 副作用。
- `MarkProviderOutcome` 是 mark-after-effect 边界，结果只能是 `accepted`、
  `unknown` 或 `definitive_failure`。回执材料必须精确绑定原 command、fence、
  attempt、receipt、permit 和 nonce。
- `Reclaim` 会把未标记窗口收敛为 `unknown`，`Reconcile` 只读取原 attempt，
  `ResolveUnknown` 只能在该 attempt 上落定回执。unknown/lease reclaim 永远不会
  递增 attempt 或重新发起 provider 调用。
- 旧 fence 在命令查找、permit 校验和结果写入前即失败；因此旧 writer 不产生
  provider call。终态 attempt 也不能再次 `IssueDispatch`。

## 测试边界

`owner_test.go` 是确定性矩阵：精确 replay、digest/绑定冲突、record-before-effect
与 mark-after-effect、pending attempt replay 屏障、伪造/旧 fence 零调用、crash window
后 reclaim/原 attempt reconcile、unknown 解析和 definitive failure。fake provider 的调用计数只表示
本地非权威观察，不能证明真实 provider exactly-once、跨进程崩溃恢复、持久化或
发布资格。

本切片的具名验收为：`RED-B08-PENDING-REPLAY`（旧实现把同一 pending permit
再次交给 provider，导致重复调用）、`GREEN-B08-PENDING-REPLAY`
（`TestIssueDispatchReplayBeforeOutcomeIsFailClosed`）以及
`Guard-B08-CANDIDATE-ONLY`（`TestCandidateProductionSurfaceRemainsTransportNeutral`、
`TestCandidateIsNotWiredIntoProduction`）。这些名称和本机测试结果仅是
`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 候选证据，不改变 B08 的 NO-GO。

后续若要生产接线，必须先定义真正的 OUT command/attempt/receipt owner、授权
permit/nonce、恢复域查询和 security-time/fence 证据，再替换旧 dispatcher 顺序；
不能把本候选的内存状态或 fake 计数升级为生产事实。
