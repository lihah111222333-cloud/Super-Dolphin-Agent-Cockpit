# B10 taskdag writer session candidate

Status: Candidate / isolated / not wired to production

日期：2026-09-03。此候选只扩展 `taskdagwriterfence` 的测试 seam，不接入
`taskdag`、Fx 或 shared product DB，也不改变现有 `Owner` 的公开行为。

## API 与事务边界

`OpenWriterSession(ctx, *sql.DB, Admission)` 先复用 `Open` 校验显式 admission 与
DB-resident fence；它不创建 migration、不开新数据库。`WriterSession.WithMutation` 复用
`sqlctx.WithImmediateTx`，因此每次尝试保持既有的
`BEGIN IMMEDIATE -> fence CAS -> callback -> COMMIT` 与有界 busy/locked retry 语义。
callback 拿到同一事务绑定的 `*sqlc.Queries` 与 `sqlc.DBTX`，可交给未来的 tx-aware
taskdag adapter；session 本身不构造 taskdag store。

CAS 影响行数不是 1 时，callback 不会执行，返回 `ErrStaleWriter` 并 sticky
`ErrAlreadyFenced`。callback 错误由 `sqlctx` 回滚，业务表不得部分提交。callback 只允许
数据库读写；provider、launcher、网络和 RecoveryCustody RPC 不得进入事务。

## commit unknown 防重放

共享 `sqlctx.WithWriteRetry` 会把 SQLite busy/locked 错误按完整事务重试。若 callback 已
成功但 COMMIT/连接结果失败，候选 session 在下一次 retry attempt 先返回
`ErrCommitUnknown`，不再执行 CAS 或业务 callback，并将 session sticky fenced；原始
commit 错误不会被当作业务失败重跑。callback 自身返回 busy/locked 时仍可重试，因为该
callback 尚未成功且事务已回滚；因此 callback 必须保持 DB-only、可安全重放。

## 当前测试证据与边界

`session_test.go` 覆盖 tx-bound callback、`WithImmediateTxOrReuse` 同事务可见性、成功与
错误回滚、Advance 后 stale zero-row/sticky、nil/typed-nil/canceled fail-fast，以及
commit replay barrier 的 callback=0。候选没有接入 42 项 taskdag writer inventory，
没有 shared product migration、Fx graph、跨进程全 inventory stale、OS 写权撤销或
真实 commit-unknown fault injection 证据；这些仍是接入前的 NO-GO 项。
