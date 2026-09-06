# Security-time owner 候选（未接线）

Status: Candidate only — 不关闭 B12、不接入 taskdag/RecoveryCustody/product DB。

`cmd/mcp-orch/orchestration/securitytime` 只提供 transport-neutral 的状态机、快照和两个窄端口：

- `Clock` 提供同一来源的 UTC wall 毫秒、boot epoch、monotonic 纳秒和 freshness；
- `StateStore` 以 `(security_domain_id, expected_version)` CAS 持久化 owner 状态。

`ClockOwner.Observe` 先重读持久状态，再验证 source、freshness、monotonic 顺序及 wall/mono 偏差。首次观察建立 generation=1；boot epoch 变化只允许通过持久 CAS 将 `clock_epoch`、`generation`、`revision` 各推进一代并保留 wall high-water。回拨、超最大偏差的前跳、sleep/resume 跳变、来源切换、未知 freshness 或 stale writer 都进入持久 `StickyUnavailable`，后续只返回 `ErrUnavailable`，不会回退 `time.Now`、SQLite `strftime` 或内存默认值。

`Allocate` 只在成功 `Observe` 后生成同一 boot 的 monotonic deadline 与 wall expiry；`Validate` 要求 security domain、lineage、source、boot/clock epoch、generation 完整匹配，并在当前样本上再次检查 fence。跨 boot 的旧快照必然 `ErrSnapshotMismatch`，过期返回 `ErrSnapshotExpired`。

该候选刻意不实现持久化介质，也不导入 `sqliteauthority`、`database/sql`、产品 DB、PostgreSQL、provider 或 Fx。生产方向仍应是：

```text
orch -> authenticated security-time/custody client
     -> external RecoveryCustody singleton
     -> private authority SQLite (唯一 writer)
```

RecoveryCustody 的私有 SQLite 才是 security-time high-water/clock lineage 的权威 owner；orch 只能消费带 lineage/generation/revision/receipt 的快照。未来 taskdag 参数化应把同一快照显式绑定到 Claim/Renew/Mark/Retry/Fail/Reclaim 和 worker-lease 的 `BEGIN IMMEDIATE`，删除这些路径的 `strftime('now')`。产品 SQLite 若保存 tuple，只能是非授权 projection/fence，不能成为 authority 或 terminal proof。custody 不可达、commit unknown、anchor/receipt 不可验证时，taskdag 必须零写并保持 indeterminate。

## 最小验证边界

当前测试仅证明候选状态机：首次 observe/allocate、回拨 sticky fence、前跳与 sleep/resume fence、restart epoch 推进及旧快照拒绝、CAS stale writer、freshness unknown 和 expiry。它们不是 RecoveryCustody 外部服务、跨进程 authority、taskdag SQL 参数化或发布证据。B12 仍须完成 mixed-process clock、硬崩溃、authority outage、commit-unknown、旧 SQL writer zero-row 和真实 external receipt/anchor 负测后才可复评。
