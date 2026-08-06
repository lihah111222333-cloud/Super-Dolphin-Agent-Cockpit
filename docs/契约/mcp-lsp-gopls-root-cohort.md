# gopls root cohort 契约（Phase A/B 最小稳定面）

`mcp-lsp` 的 gopls auto-remote cohort 由 canonical Git common-dir（非 Git 目录则使用规范绝对 root）标识。linked worktree 只要解析到同一个 common-dir，就必须进入同一个 root cohort；无关 root 不得复用该 cohort。

入口层把 canonical root 转成 `multilsp.GoplsRepositoryInstanceProof`，再注入 `multilsp.GoplsRootCohortConfig`。配置的 `CohortID`、`RepositoryInstanceProof` 和 `EffectiveConfigDigest` 是 immutable admission 字段。同一 canonical root 的第二份不同配置必须 fail-fast，不能创建第二个 cohort。

`AcquireLease` 返回成员级 `{Epoch, JournalRevision, MemberID, MemberGeneration, LeaseID}` fence。release 后旧 fence 必须被拒绝；同一配置重新准入只能复用原 cohort ID。

生产 controller 使用共享 LSP cache root 下的私有 `state.json`、member lease 记录和 flock；每个 admission/release/drain transition 都在同一把跨进程锁内完成，并在记录写入/删除后同步父目录。owner lease 保存 PID 与启动身份，新的 admission 会回收已退出或 PID 已复用的 stale member，避免 sidecar 崩溃永久保留成员。

最后一个 member 释放时，state 记录 15 分钟 `IdleDeadline`、`DrainEpoch` 和 owner evidence。唯一 owner callback 负责关闭该 member 的 gopls forwarder；deadline 到达后先以 epoch/fence 二次复核，再执行 callback，成功写入 completion receipt。新的 admission 会同步取消本地 pending drain 并提升 epoch；跨 sidecar 若仍有存活 owner 则以 `ErrGoplsRootCohortDrainCleanupPending` 阻断重复启动，待 owner completion 或 stale-owner 回收后再重新准入。callback 失败保留 owner evidence，状态为 `cleanup_pending` 并按 retry window 重试，不使用 RSS/ps 进程扫描或 kill 兜底。

进程内 `NewGoplsRootCohortController` 仍只提供测试/本进程冲突与 fence 证据，不能被描述成跨进程 authority；生产 runtime 必须注入 durable controller。Windows 不启用 gopls auto-remote root cohort，并返回明确 unsupported 语义。

生产 gopls 创建链在 durable cache owner 初始化失败时 fail-fast，绝不会回退到裸 `-remote=auto`。gopls 自身仍以 `-remote.listen.timeout=15m` 作为 daemon 的协议级 idle 超时；产品 owner 只关闭其持有的 forwarder transport，不通过 RSS/ps 路径越权终止 gopls daemon。
