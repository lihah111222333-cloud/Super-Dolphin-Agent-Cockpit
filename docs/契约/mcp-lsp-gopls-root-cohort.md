# gopls root cohort 契约（Phase A/B 最小稳定面）

`mcp-lsp` 的 gopls auto-remote cohort 由 canonical Git common-dir（非 Git 目录则使用规范绝对 root）标识。linked worktree 只要解析到同一个 common-dir，就必须进入同一个 root cohort；无关 root 不得复用该 cohort。

入口层把 canonical root 转成 `multilsp.GoplsRepositoryInstanceProof`，再注入 `multilsp.GoplsRootCohortConfig`。配置的 `CohortID`、`RepositoryInstanceProof` 和 `EffectiveConfigDigest` 是单个配置代际内的 immutable admission 字段。同一 canonical root 仍有活跃 member、当前 drain owner 或历史 cleanup owner 时，第二份不同配置必须 fail-fast，不能并行创建第二个 cohort。只有在同一把跨进程锁内清理并证明旧 member 全部失效、且不存在未完成 owner cleanup 后，才允许保留单调递增的 epoch、journal revision、member generation 和 sequence，原子轮换到下一配置代际。

`AcquireLease` 返回成员级 `{Epoch, JournalRevision, MemberID, MemberGeneration, LeaseID}` fence。release 后旧 fence 必须被拒绝；同一配置重新准入只能复用原 cohort ID。

生产 controller 使用共享 LSP cache root 下的私有 `state.json`、member lease 记录和 flock；每个 admission/release/drain/config-generation transition 都在同一把跨进程锁内完成，并在记录写入/删除后同步父目录。owner lease 保存 PID 与启动身份，新的 admission 必须先按旧状态的 config digest 回收已退出或 PID 已复用的 stale member，再判断同代复用、活成员冲突或安全配置轮换，避免 sidecar 崩溃或宿主环境更新永久阻断该 root。

`EffectiveConfigDigest` 只记录会改变 gopls/Go 构建语义的稳定身份。gopls 和 `go` 使用解析后的真实路径与二进制内容摘要；显式 `CC`、`CXX`、`FC`、`AR`、`GCCGO`、`GOCACHEPROG`、`PKG_CONFIG` 同时绑定原命令与解析后工具的真实路径、内容摘要。`GOCACHE`、`GOMODCACHE`、`GOPATH`、`GOROOT` 未设置与显式设置为同一 `go` 二进制报告的默认值属于同一语义身份；真正偏离默认值的配置仍必须进入 digest。原始继承 `PATH` 不得直接进入 digest，未列入 Go 构建语义白名单的同前缀变量（例如 `GOOGLE_*`）也不得进入。Codex arg0 临时目录、重复 PATH 目录或不改变最终工具链解析结果的 PATH 顺序变化不得拆分 cohort；若 PATH 确实解析到不同的 gopls、Go 或显式辅助工具，则必须产生不同配置并服从上述代际准入规则。

最后一个 member 释放时，state 记录 15 分钟 `IdleDeadline`、`DrainEpoch` 和 owner evidence。唯一 owner callback 负责关闭该 member 的 gopls forwarder；deadline 到达后先以 epoch/fence 二次复核，再执行 callback，成功写入 completion receipt。新的 admission 会在同一把锁内原子提升 epoch；跨 sidecar 不得被旧 owner 的 15 分钟 drain 阻断，而是把旧 fence 转入独立的 typed `PendingCleanups` journal。旧 sidecar 仍是该 fence 的唯一 cleanup owner，新 epoch 绝不执行旧 callback。callback 失败保留对应 fence 的 owner evidence、`cleanup_pending` 和 retry deadline；owner callback 不可达时继续保留该 pending 证据，绝不越权关闭旧 forwarder。不使用 RSS/ps 进程扫描或 kill 兜底。

Darwin 没有可替代 pidfd 的外部稳定进程句柄，因此生产语言服务器必须由当前 `mcp-lsp` 二进制的内部 supervisor 作为独立 session/PGID leader 启动，真实 gopls forwarder 及其后代继承该 owner 组。`ProcessTree` 只能通过启动时创建且不传给真实语言服务器的专用控制管道请求 TERM/KILL；supervisor 只向自己的稳定 PGID 发信号，禁止从持久状态、`ps`、进程名或裸 PID 重建信号权限。父控制管道 EOF 是 exact owner 已消失的稳定证据；此外 supervisor 在 `PPID<=1` 且 CWD 明确返回 `ENOENT` 时必须自行启动 TERM→有界 KILL。权限、I/O 或其他不确定的 CWD 探测错误只记录 probe failure，不得授权信号。真实语言服务器退出后仍有同组后代时，supervisor 必须在自己退出前强制清空该组；外层只有在 `Wait`、`Remaining=0` 和 `Release` 全部收敛后才能完成 cleanup receipt。

进程内 `NewGoplsRootCohortController` 仍只提供测试/本进程冲突与 fence 证据，不能被描述成跨进程 authority；生产 runtime 必须注入 durable controller。Windows 不启用 gopls auto-remote root cohort，并返回明确 unsupported 语义。

manager/recycler 的 Go workspace idle 分支只接受 `multilsp.IdleReleasableClient.ReleaseForIdle`。生产 `goplsRootCohortClient` 同时声明 `IdleReleaseRequiredClient`；若 owner 接口缺失，recycler fail-closed、保留 `CleanupPending`，不回退到裸 `Client.Close`。

生产 gopls 创建链在 durable cache owner 初始化失败时 fail-fast，绝不会回退到裸 `-remote=auto`。gopls 自身仍以 `-remote.listen.timeout=15m` 作为 daemon 的协议级 idle 超时；产品 owner 只通过上述 exact supervisor 关闭其持有的 forwarder transport，不通过 RSS/ps 路径越权终止 gopls daemon。
