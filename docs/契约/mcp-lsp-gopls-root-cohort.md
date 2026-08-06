# gopls root cohort 契约（Phase A/B 最小稳定面）

`mcp-lsp` 的 gopls auto-remote cohort 由 canonical Git common-dir（非 Git 目录则使用规范绝对 root）标识。linked worktree 只要解析到同一个 common-dir，就必须进入同一个 root cohort；无关 root 不得复用该 cohort。

入口层把 canonical root 转成 `multilsp.GoplsRepositoryInstanceProof`，再注入 `multilsp.GoplsRootCohortConfig`。配置的 `CohortID`、`RepositoryInstanceProof` 和 `EffectiveConfigDigest` 是 immutable admission 字段。同一 canonical root 的第二份不同配置必须 fail-fast，不能创建第二个 cohort。

`AcquireLease` 返回成员级 `{Epoch, JournalRevision, MemberID, MemberGeneration, LeaseID}` fence。release 后旧 fence 必须被拒绝；同一配置重新准入只能复用原 cohort ID。

生产 controller 使用共享 LSP cache root 下的私有 `state.json`、member lease 记录和 flock；每个 admission/release/drain transition 都在同一把跨进程锁内完成，并在记录写入/删除后同步父目录。owner lease 保存 PID 与启动身份，新的 admission 会回收已退出或 PID 已复用的 stale member，避免 sidecar 崩溃永久保留成员。

最后一个 member 释放时，state 记录 15 分钟 `IdleDeadline`、`DrainEpoch` 和 owner evidence。唯一 owner callback 负责关闭该 member 的 gopls forwarder；deadline 到达后先以 epoch/fence 二次复核，再执行 callback，成功写入 completion receipt。新的 admission 会在同一把锁内原子提升 epoch；跨 sidecar 不得被旧 owner 的 15 分钟 drain 阻断，而是把旧 fence 转入独立的 typed `PendingCleanups` journal。旧 sidecar 仍是该 fence 的唯一 cleanup owner，新 epoch 绝不执行旧 callback。callback 失败保留对应 fence 的 owner evidence、`cleanup_pending` 和 retry deadline；owner callback 不可达时继续保留该 pending 证据，绝不越权关闭旧 forwarder。不使用 RSS/ps 进程扫描或 kill 兜底。

进程内 `NewGoplsRootCohortController` 仍只提供测试/本进程冲突与 fence 证据，不能被描述成跨进程 authority；生产 runtime 必须注入 durable controller。Windows 不启用 gopls auto-remote root cohort，并返回明确 unsupported 语义。

manager/recycler 的 Go workspace idle 分支只接受 `multilsp.IdleReleasableClient.ReleaseForIdle`。生产 `goplsRootCohortClient` 同时声明 `IdleReleaseRequiredClient`；若 owner 接口缺失，recycler fail-closed、保留 `CleanupPending`，不回退到裸 `Client.Close`。

生产 gopls 创建链在 durable cache owner 初始化失败时 fail-fast，绝不会回退到裸 `-remote=auto`。gopls 自身仍以 `-remote.listen.timeout=15m` 作为 daemon 的协议级 idle 超时；产品 owner 只关闭其持有的 forwarder transport，不通过 RSS/ps 路径越权终止 gopls daemon。
