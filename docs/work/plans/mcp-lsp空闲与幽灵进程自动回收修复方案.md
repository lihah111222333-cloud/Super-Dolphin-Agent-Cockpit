# mcp-lsp 空闲回收与幽灵进程可观察性修复方案

> Bug ID：2026-08-04-mcp-lsp-idle-process-reclaim
> Status：Active
> Implementation：NOT_IMPLEMENTED
> Release：BLOCKED
> Priority：P1 / release blocker
> Owner：mcp-lsp lifecycle 维护者
> 创建日期：2026-08-04
> 基线：`main@345aa86f585b1d4217034688bec926ad134aacfb`

关联设计：`docs/doc/codemap/03-mcp-lsp-ida.md`、`docs/doc/codemap/06-mcpserver.md`、`docs/契约/fix-workflow-convention.md`。

范围：`cmd/mcp-lsp/multilsp`、`cmd/mcp-lsp/internal/hiddenexec`、产品启动的 Node LSP 进程树、gopls forwarder 与共享 daemon 的空闲契约和日志。

不在范围：Codex 宿主生命周期、stdio sidecar 因静默自动退出、没有存活产品宿主时的持续扫描、跨 owner 强杀、任意系统 Node/gopls 清理、`pkill` 或命令子串清理。

## 1. Intake 与结论

### 1.1 问题输入

- 来源：本地运行反馈与 LSP 日志调查。
- 实际现象：机器上累积多个低 RSS 的 Node、gopls 和 LSP 残留进程；资源总账可见 `active_leases=0`，但缺少足以回答“哪个 owner、从何时开始空闲、是否仍有完整进程树、为什么未回收”的关联日志。
- 预期行为：已接纳的每个 owner 内语言服务器实例，在最后一个操作完成后连续空闲 15 分钟，由该 owner 自动回收；实例数量、agent 数、RSS 或 pool cap 不参与回收资格判断。
- 权限边界：owner 已消失、无权限、PID 身份不确定或不属于当前 sidecar 的幽灵进程，只记录 `lsp_reclaim_blocked`，绝不自动发送信号。
- 证据缺口：当前反馈未保存同一时间点的完整 PID/start identity、父子树、owner、idleSince 与日志快照，因此“很多进程”不能直接证明都已连续空闲 15 分钟。实施前必须先采集基线快照。

### 1.2 唯一产品契约

1. 回收判定不以 agent、workspace 或进程数量为输入；所有已接纳实例适用相同策略。
2. Node LSP 和每个 gopls forwarder 独立计算空闲：`activeLeases == 0` 且从最后一个 lease 释放开始连续满 15 分钟；从未取得 lease 的 ready generation 从 bootstrap 成功的原子 `publishedAt` 开始完整计时。
3. owner 内 scanner 的目标周期为 30 秒；资格在下一次**完成的**扫描中发现，不承诺“15 分钟加至多一个周期”的硬上界。
4. 数量、RSS、pool cap、probe 失败和 owner heartbeat 缺失只能触发日志、拒绝新建、排队或显式背压，不能单独触发 kill。
5. 当前 owner 回收 Node/forwarder 时必须覆盖其持有的完整 `ProcessTree`，并在验证根进程与后代退出后销账。
6. gopls shared daemon 不属于任一 forwarder 的 `ProcessTree`。其 `remote.listen.timeout` 与产品有效 idle policy 使用同一配置真值，默认 15 分钟，由 gopls 自身退出；产品观察到残留 daemon 只记录日志。
7. 不依赖 `turn_id`、`thread_id` 或 `agent_id`。这些字段若宿主没有提供就省略，不能伪造、空串占位或影响生命周期判定。
8. 不创建独立常驻幽灵 reaper。没有存活产品宿主时不会产生新的扫描日志；后续 sidecar 启动或存活 sidecar 的周期观察只能报告幽灵，不能接管 owner。

### 1.3 自动空闲回收与确定性关闭

15 分钟规则只约束自动空闲回收。以下 owner 已知且原因确定的路径可立即关闭其持有的语言服务器：

- MCP EOF、显式 shutdown、owner context cancellation；
- spawn、initialize 或 bootstrap 失败；
- 已确认的 client/transport fatal 或 unhealthy 终态。

RSS、数量、cap、一次 probe 失败和未知 owner 不属于确定性关闭原因。

## 2. 当前事实与根因

### 2.1 已有骨架

- `recycler.go` 已有默认 30 秒 ticker 和 workspace 扫描入口。
- `idleTimeoutForLanguage` 当前返回 15 分钟。
- `detachWorkspaceClient` 已在 manager 锁内复核 client、活动截止和 lease。
- transport 已通过平台 `ProcessTree` 持有产品启动的语言服务器进程。
- stdio `newServer` 没有接入通用静默退出 timeout。

### 2.2 空闲起点错误

`leaseBoundClient` 在 lease 获取时刷新 `lastActivity`，`leasedClient.Release` 当前只递减计数。长请求结束后，scanner 可能读取请求开始时的旧活动时间，并在下一轮立即回收。正确起点必须是最后一个 lease 的 `1 -> 0` 时刻。

### 2.3 lease 覆盖不完整

`manager_diagnostics_readiness.go`、`manager_lifecycle.go`、`manager_symbols.go`、`manager_symbols_retry.go` 和 `manager_retry.go` 中的 diagnostics、bootstrap、retry、`DidOpen`/`DidClose` 等路径需要统一审计。所有生产 `Client.Request`、`Notify`、`DidOpen`、`DidChange`、`DidClose` 调用必须经过同一 lease guard；只在 detach 阶段二次检查不能保护从未建立 lease 的调用。

### 2.4 旁路回收与过早销账

当前 RSS/cohort、probe degradation、pool clone/cap 路径可能绕过完整 15 分钟 idle 条件。`client.Close` 又可能把 transport close 与 member 删除放在同一聚合流程中，造成退出未验证但账本已删除。结果是 `active_leases=0` 或 member 消失并不能证明进程已退出。

### 2.5 owner 丢失后的权限真空

普通 scanner 只能遍历当前 `ManagerPool` 和 `manager.workspaces`。sidecar 崩溃后，不再存在可证明持有原进程树的产品 owner。共享 gopls daemon 同样不在 forwarder `ProcessTree` 内。方案不以新常驻 reaper 填补这个权限真空，而是把它变为明确、可检索的 `reclaim_blocked` 观测状态。

### 2.6 gopls timeout 配置漂移

当前 `adapter.go` 的默认值是 `-remote.listen.timeout=1s`，并通过独立环境变量解析；它与 recycler 的 15 分钟策略不是同一真值。实现必须生成一个经过校验的 effective idle duration，并同时用于 workspace policy 和 gopls remote listen 参数；无效或不一致配置必须 fail-fast。

### 2.7 可复查源码锚点

| 事实 | 源码锚点与 provenance |
|---|---|
| scanner 与 idle 判断 | `[BASE@345aa86f] recycler.go` 的 `poolRecycler.Run/checkIdleWorkspaces/detachWorkspaceClient`；权威源主工作树的 `recycler.go` 为 `[WORKTREE-DIRTY@8aae30a3]`，不得混用行号 |
| lease 获取和释放 | `[BASE@345aa86f] manager_lifecycle.go:137-147,245-276` |
| 公共 lease 入口 | `[BASE@345aa86f] factory.go:125-148` |
| 直接 Client 调用候选 | `manager_diagnostics_readiness.go`、`manager_lifecycle.go`、`manager_symbols.go`、`manager_symbols_retry.go`、`manager_retry.go` |
| RSS/probe 旁路候选 | `[BASE@345aa86f] recycler.go` 的 `recycleIfNeeded/checkResourceCohort/checkManagerRecyclerHealth`；精确行号从 commit object 读取 |
| 关闭与 cohort 销账 | `[BASE@345aa86f] client.go:405-412`、`transport_conn.go:264-293` |
| ProcessTree 契约 | `[BASE@345aa86f] hiddenexec/process.go` 与平台实现 |
| gopls timeout 独立配置 | `[BASE@345aa86f] adapter.go:24-28,202-224` |

BASE 事实只允许从 `git show 345aa86f:<path>` 或同一 commit worktree 取证；权威源主工作树的 LSP 默认读取 `[WORKTREE-DIRTY@8aae30a3]`。RED/mutation/gate receipt 必须逐项标注 tree hash，优先使用符号名而非易漂移行号。

## 3. 权威边界与生命周期真值

### 3.1 三类事实源

| 对象 | 写入权威 | 能否授权 kill |
|---|---|---|
| 当前 owner 的 lease、generation、idleSince、closing/cleanup ownership | manager mutex 下的 workspace generation 状态 | 是，仅限该 owner 持有且每个 destructive edge 身份复核成功的 `ProcessTree` |
| gopls shared daemon 空闲 | gopls 有效 `remote.listen.timeout` 与其连接状态 | 产品不能授权 kill；只等待自清理并观察 |
| process receipt / cohort report / ghost observation | 持久化诊断证据 | 否；任何其他 sidecar 不得据此接管 owner 或发信号 |

manager 的 `workspaceClient` generation 记录是 owner 内唯一可写生命周期真值，必须在同一 `manager.mu` 下保存 `activeLeases`、`idleSince`、client identity、generation、draining/closing 状态和 cleanup ownership。现有 `ManagerPool.leases` 必须删除为生命周期写源，或降级成只消费 manager 快照的不可写聚合指标，禁止双写计数。

`leasedClient` 必须捕获 workspace key、client identity 和 generation，并通过 manager 方法执行 release；旧 generation 的迟到 Release 不得修改 replacement。锁序固定为“pool/shard 只做定位 -> 释放 pool/shard 锁 -> 获取 manager 锁”，禁止持有 `manager.mu` 回调需要反向取得 pool/shard 锁的操作。

receipt/report 是可恢复的观察快照，不是分布式租约，也不是跨进程回收授权。不得让 manager、cohort report 和 ghost observer 分别维护可写 `idleSince`。

### 3.2 workspace 状态

~~~text
publish ready + no waiting lease: state=IdleCountdown; activeLeases=0; idleSince=publishedAt
publish ready + waiting lease: state=Active; activeLeases=1; idleSince=zero
lease 0 -> 1: state=Active; activeLeases++; lastActivity=now; idleSince=zero
lease N -> N+1: state=Active; activeLeases++; lastActivity=now; idleSince=zero
lease 1 -> 0: state=IdleCountdown; activeLeases=0; lastActivity=now; idleSince=now
lease N -> N-1 (N-1>0): state=Active; activeLeases--; lastActivity=now; idleSince=zero
~~~

`initialize` 成功不启动 idle 计时；只有 required bootstrap 成功并原子 publish 为可 lease generation 后，若没有等待中的 lease，才以 `publishedAt` 进入 `IdleCountdown`。任何新 lease 都必须在同一 manager 临界区取消倒计时。lease 不得为负；stale generation acquire/release 必须 fail-fast 或成为无状态变更的明确错误，不能命中 replacement。

### 3.3 唯一 idle 谓词

~~~text
IdleEligible =
    state is IdleCountdown or Recheck
    AND owner process is current and alive
    AND client/process generation is current
    AND activeLeases == 0
    AND idleSince is not zero
    AND now - idleSince >= effectiveIdleTimeout (default 15m)
~~~

其他信号不能把 `IdleEligible=false` 改成 true。

### 3.4 状态机

~~~mermaid
stateDiagram-v2
    [*] --> Starting
    Starting --> Bootstrapping: spawn/bind/initialize 成功
    Starting --> Closing: spawn/bind/initialize 失败
    Bootstrapping --> Active: required bootstrap 成功且等待 lease 原子获取
    Bootstrapping --> IdleCountdown: required bootstrap 成功、无 lease、publishedAt 起算
    Bootstrapping --> Closing: bootstrap 失败
    Active --> IdleCountdown: 最后一个 lease 释放
    IdleCountdown --> Active: 新 lease 获取
    IdleCountdown --> Recheck: 已满 effectiveIdleTimeout
    Recheck --> Active: 新 lease 已获取
    Recheck --> IdleCountdown: identity/generation 仍属当前代但需重启完整窗口
    Recheck --> Closing: owner 锁内复核成功
    Active --> Closing: EOF/shutdown/cancel/fatal/unhealthy
    IdleCountdown --> Closing: EOF/shutdown/cancel/fatal/unhealthy
    Closing --> ShutdownSent
    ShutdownSent --> ExitVerifying: 已退出
    ShutdownSent --> TermSent: Unix graceful deadline 到期
    TermSent --> ExitVerifying: Unix 已退出
    TermSent --> KillSent: Unix TERM deadline 到期
    ShutdownSent --> KillSent: Windows TerminateJobObject
    KillSent --> ExitVerifying
    ExitVerifying --> Reclaimed: 根 PID 和后代已退出
    ExitVerifying --> CleanupPending: 仍存活或验证失败
    CleanupPending --> Closing: 同一 owner 下一次扫描重试
    Reclaimed --> [*]: 删除 owner ledger，保留终态观测
~~~

自动 idle 和确定性关闭共用 `Closing -> protocol/transport/process action -> ExitVerifying -> Reclaimed|CleanupPending` 事务；区别只是 EOF、显式 shutdown、owner cancellation、spawn/initialize/bootstrap failure 和 confirmed fatal/unhealthy 不等待 `IdleEligible`。任何关闭原因都不得绕过退出验证和延迟销账。

锁内一次性复核 workspace key、client identity、generation、lease、idleSince 和 cleanup ownership；随后标记 `Closing` 并从可复用集合摘除，再释放锁执行 I/O 与进程等待。已登记 client 的原 workspace generation 记录持续作为 cleanup owner，验证退出前不得删除、复用或替换；未登记的 client/tree 进入 owner-local `provisionalCleanups`，使用 `(workspaceKey,generation,client/tree identity)` 定址。不得为 cleanup 再创建第二套生命周期 SSOT。

`publish` 专指 workspace generation 从 `Bootstrapping` 原子转换为可 lease 的 `Active` 或 `IdleCountdown`，不等于提前插入 manager map。为防止重复 spawn，可以在 `manager.mu` 下先插入不可 lease 的 `Bootstrapping` reservation；并发 `EnsureClient` 只能等待该 generation 的 ready 结果，不能取得 client/lease。bootstrap 成功后，等待中的首个 lease 与 `Active` 发布在同一事务完成；没有等待 lease 时以 `publishedAt` 进入 `IdleCountdown`。失败后同一 generation 进入 Closing，完成验证前不得新建 replacement。`Starting`、`Bootstrapping`、`Closing`、`ShutdownSent`、`TermSent`、`KillSent`、`ExitVerifying`、`CleanupPending` 永远不能满足 `IdleEligible`。

`ManagerPool` 的 shard-cap、release-drain、clone retirement 或其他容量压力不能构造第二条销毁资格。任何会关闭 manager/client/进程的 eviction 都必须先释放 pool/shard 锁，再由 manager 对每个 generation 执行同一 `IdleEligible` 与 verify-before-release 事务；任一 workspace 未满完整窗口时只能排队、显式拒绝新 clone，或做不关闭 client/`ProcessTree` 的纯 metadata detach。容量超限、RSS、clone 年龄和 `activeLeases==0` 单独都不能触发 shutdown/signal。

owner 停止前执行有界最终清理；仍无法验证时持久化 blocked 终态后退出。后续 sidecar 只能观察，不能继承 cleanup authority。

### 3.5 scanner 调度语义

- 30 秒是目标 scan interval，不是回收完成 SLA。
- 记录 `scan_started_at`、`scan_duration_ms`、`scan_lag_ms`、扫描对象数和结果数。
- 同步扫描超时或耗时过长时输出 `lsp_idle_scan_delayed`；不得倒推 idle 起点或跳过锁内复核。
- 单元测试使用可注入 clock/scheduler；E2E 记录实际发现延迟，不写“最多一个周期”的无法保证断言。

## 4. Node 与 gopls 的执行边界

### 4.1 owner 内 Node/forwarder ProcessTree

Node 回收对象是当前 sidecar 启动并持有的完整树，例如：

~~~text
typescript-language-server
├── tsserver
├── tsserver
└── typingsInstaller
~~~

`typescript_navigation_fallback.go` 的一次性 `node -e` 不进入持久 LSP idle ledger。只看到父进程退出不得标记 `reclaimed`。

`ProcessTree` 接口必须提供 immutable identity snapshot、alive、descendants、graceful/force action、有界 wait 和 remaining-member verification。每个破坏性 edge 都在 `ProcessTree` 内重新授权；manager 锁内的早期 identity 复核不能代替 signal-time 复核。Unix spawn 必须建立当前 owner 独占的新 session/PGID；无法建立或验证时 spawn fail-fast。Linux 身份至少绑定 `/proc` starttime、UID、session/PGID，并强制使用 pidfd 对逐成员 signal 关闭 verify-to-signal 窗口；任一成员无法取得/复核 pidfd、内核不支持 pidfd 或安全绑定失败时，自动破坏性动作必须 `zero-signal -> CleanupPending`，绝不回退 PGID/parent kill。Darwin 必须使用 `proc_pidinfo` 等原生高分辨率 start token、UID、session/PGID，禁止用秒级 `ps -o lstart=` 充当破坏性授权。

- Unix：protocol shutdown -> transport close -> bounded graceful wait -> 复核 PID/native-start/session/PGID/generation 和完整成员闭包 -> `SIGTERM` -> bounded wait -> 再复核 -> 必要时 `SIGKILL` -> 根与后代复核。只有捕获的独占 session、根身份和 action-time 全量成员都匹配时才允许 PGID signal；出现外来/未知成员、成员闭包不完整或复核与 signal 无法安全绑定时必须零信号。禁止失败后退化为未复核的 parent PID/group kill fallback。
- Windows：protocol shutdown -> transport close -> bounded graceful wait -> 复核当前 owner 持有且未 release 的原始 Job handle、generation 和 Job membership -> 单次 `TerminateJobObject` -> Job 成员为空验证。不得虚构 Windows `TERM` 阶段；kill-on-close 的 handle release 也属于破坏性动作，只能在 Job 已验证为空后执行，或按 force-kill 事件记录并先授权。
- unsupported 平台：spawn 前 fail-fast，不能退化为 PID、PPID、命令子串或宽泛进程匹配。

任何 identity mismatch、PID/PGID reuse、Job handle 已替换/释放、权限错误或 unknown 都进入 `CleanupPending` 和 `lsp_reclaim_blocked(signal_sent=false)`，不能当作已经回收。

Unix 必须显式处理“根进程已退出、已知后代仍存活”：

- Linux 不允许 PGID signal，只能向已验证 pidfd 逐成员 signal；Darwin 只有根 PID/native-start、独占 session/PGID 和 action-time 全量成员闭包同时匹配时才允许向整个 PGID 发信号；根匹配本身不构成授权；
- 根已退出时禁止宽泛 PGID signal，只能对 `ProcessTree` 先前持续追踪、且在 action-time 逐个复核 PID/start/PGID/session/generation 成功的已知成员发送逐 PID signal；
- 当前组出现未知成员、成员 identity 不完整、新成员无法归属或任一复核失败时，保持 `CleanupPending` 并记录 blocked/no-signal；不得为了收敛而弱化验证；
- TERM 前、TERM 后 KILL 前和最终 release 前分别重新快照。根退出不是自动 reclaimed，也不是自动 identity mismatch。

### 4.2 启动回执只用于观测

owner 在 spawn 前原子写 `starting` intent，spawn 后立即绑定 `client_pid/client_start/pgid-or-job/generation`。完整启动事务是 `starting intent -> spawn -> bind receipt -> initialize -> Bootstrapping reservation -> required bootstrap -> publish Active|IdleCountdown`：

1. POSIX 使用可信根的 dirfd/openat/no-follow 逐组件访问，`fstat` 校验对象类型、euid、目录 `0700`、文件 `0600` 和 `nlink==1`；临时文件在同一 dirfd 下以 `O_EXCL|O_NOFOLLOW` 创建，文件 fsync 后 `renameat`，再 fsync 目录。读、写、删都基于已验证 dirfd，拒绝 symlink、hardlink、parent rename 和目录替换竞态。
2. Windows 使用 current-user/Admin/SYSTEM private DACL，逐组件拒绝 reparse point，基于已打开 handle 校验 owner/type/identity，在同目录安全替换并复核最终对象。
3. intent 写失败则不 spawn。
4. spawn 后绑定回执失败，先把 exact `ProcessTree` 原子移交给 owner-local provisional cleanup，再执行与正常关闭相同的有界 action/wait/verify。仍存活或验证失败时保留句柄和 cleanup owner；初始化返回错误，cleanup 完成前同 workspace 不得再次 spawn。
5. initialize 成功但 required bootstrap 阻塞/失败时，workspace 仍为不可 lease 的 `Bootstrapping`；并发调用等待 ready。失败必须由同一 generation 进入统一 Closing 事务，不能返回 client，也不能二次 spawn。
6. owner 在 bind/publish 之间崩溃时，后续观察者记录 incomplete intent 或 possible ghost candidate；不得自动 kill。
7. 终态回执可保留有限时间用于审计；它不是 lease SSOT，也不能授权其他 sidecar 接管。

生产 observation store root 固定为 `<os.UserCacheDir()>/super-agent-v3/mcp-lsp/process-observations/v1`，不接受生产路径覆盖；测试只能通过显式 test-only constructor 注入临时根。root 内 `.store.lock` 使用 OS advisory/file-handle lock，仅保护 CRUD、capacity admission、reservation 和 terminal prune，绝不授予进程 owner 或 signal authority。

每次 intent/admission 在同一短临界区内重新验证 root/lock/file identity、预留文件数与字节预算并提交 `starting` record；prune/write 也在同一锁内，只能删除 verified terminal。锁持有者崩溃后 OS 自动释放锁，但已提交 reservation/starting record 继续计入配额并转为 ghost-candidate 观察，不能静默释放。双 sidecar 并发 admission 不得共同越过 10,000/64 MiB 上限；容量不足返回错误且不 spawn。

建议字段：schema version、本地随机 `receipt_id`、owner PID/start/instance ID、language/workspace hash、client PID/start、PGID/Job、binary realpath/digest、argv digest、generation，以及 `observed_active_leases`、`observed_idle_since`、`observed_state`、最近错误。字段名明确使用 `observed_`，避免伪装成跨进程权威。

新增或修改这些结构化字段时，实施回合必须使用字段守卫，覆盖生产者、消费者、序列化、权限、兼容与 fail-fast 测试。

### 4.3 幽灵观察，不接管

存活 sidecar 可在启动时和 owner-local scanner 中读取产品回执/报告，仅执行身份探测与日志输出。权限边界拆成两个 sealed 包：

- `cmd/mcp-lsp/internal/processprobe` 是唯一平台只读探测实现，输出不可变 `Snapshot` 值；`Snapshot` 使用不可导出字段、包内受控构造器和只读 accessor，禁止包外 struct literal、`unsafe`/reflection 写入或调用方伪造 platform proof；不暴露可注入 interface/function capability。Unix 仅允许 signal=0 的存在性探测和静态 allowlist 的只读 `ps` 查询，Windows 仅允许查询 handle/API；go/types/SSA guard 禁止非零 signal、`os.Process.Signal/Kill`、`TerminateJobObject`、`taskkill/pkill`、任意命令和包装/别名/函数字段绕过。
- `cmd/mcp-lsp/internal/processobserve` 只消费 `[]processprobe.Snapshot`、receipt store 和 logger，不导入 `multilsp` 或 signal-capable `hiddenexec`，也不接收任意调用方实现的 probe 接口。guard 同时覆盖 probe 实现、Snapshot 构造器、observer 和全部 wiring。

所有 `no_authoritative_owner`、`permission_denied`、`identity_mismatch`、`pid_reuse`、`probe_failed`、`unknown` 结果都原子更新一个 durable ghost-decision incident：同一 observation/`operation_id` 投影为 `lsp_ghost_candidate_observed`（gopls daemon 使用专用 ghost-candidate event）与 `lsp_reclaim_blocked(reason=...,signal_sent=false)`。identity 完整时按稳定 `ghost_dedup_key` 原位更新；identity 不完整时按仅用于观测的有界 `ghost_observation_bucket_key` 原位合并 `first_seen/last_seen/seen_count/latest_evidence_digest/missing_fields`，不得每个 scan 新建文件。candidate/bucket 都不表示已证明为同一个幽灵，也永远不能授权 signal；blocked 是唯一权限结论。session/executable/socket 探测失败不能当成“没有活 session”。

- 找到确切无 owner 的进程仍使用 candidate + blocked，不升级为自动 kill；
- 不发送 TERM/KILL，不建立 claimer/leader lock，不把 receipt 变成 owner；
- durable incident 先提交 no-signal 决策，再按稳定 `projection_id=event_id+projection_kind` 幂等 fan-out；每个 projection 记录独立 ack。第一条、第二条或 fan-out 失败时保留 `pair_pending` 并以同一 operation/event/projection ID 重试，绝不进入 signal 分支；
- 没有存活产品宿主时不承诺持续观察，依赖下次 sidecar 启动或外部运维监控产生新日志。

上线前已有的无回执残留只能形成审计清单，由人工使用进程身份、打开文件/连接和父子关系确认后另行处置；生产代码不得包含宽泛 `pkill node/gopls`。

### 4.4 gopls shared daemon

- 每个 forwarder 遵守 owner 内 lease/idle 规则，15 分钟后关闭自身 `ProcessTree`。
- 唯一配置类型为 `internal/contract/config.go` 的 `LSPConfig.IdleTimeout time.Duration`；`internal/platform/config/lsp.go` 只解析一次默认值/环境覆盖，默认 15 分钟，非法或非正数 fail-fast。
- canonical 环境键为 `MCP_LSP_IDLE_TIMEOUT`。旧 `MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT` 仅作一个发布周期的显式兼容 alias：canonical 未设置时可生成同一个 effective value 并记录 deprecation；两者同时设置但值不同立即失败。adapter 不再直接读任何环境变量。
- 完整注入链固定为：`cmd/mcp-lsp/fx.go -> runtime.go:newManager -> registerRuntimeAdapter -> createGenericManagerWithBinary -> multilsp.Config.IdleTimeout -> multilsp.NewManager -> NewManagerPool`。`newManager` 先取得一次 fully resolved `contract.LSPConfig`，同一值同时传给 `NewLanguageAdapterRegistryFromConfig` 和每个 generic manager。
- `cmd/mcp-lsp/multilsp/language_service_config.go` 的 default/clone/merge 必须保留 `IdleTimeout`；Go adapter 保存 resolved duration，只格式化 `-remote.listen.timeout=<same value>`；recycler/ManagerPool 只消费 `multilsp.Config.IdleTimeout`。删除 adapter 独立 env parser 和 recycler 独立硬编码，静态守卫禁止第二真值。
- `multilsp.NewManager` 改为可返回配置错误并对 `Config.IdleTimeout<=0` fail-fast；所有生产和测试调用点必须显式传入已解析值，禁止 constructor 内补第二个默认值。`manager.cloneForWorkspace` 与 `NewManagerPool` 复制/消费同一 duration，scoped clone 不得归零或重新解析。
- daemon 在最后一个真实连接消失后由 gopls 自身 timeout 退出。remote session 列表仅为辅助证据，不能授权产品 kill。
- 超过有效 timeout 后仍观察到 exact daemon PID/start/socket 时，原子记录 `gopls_daemon_ghost_candidate_observed` 与 `lsp_reclaim_blocked(reason=no_authoritative_owner,signal_sent=false)`；产品不发信号。
- Windows 生产路径会移除 `-remote` 与 `-remote.listen.timeout`，因此 shared-daemon contract 在 Windows 明确为 `N/A`；Windows 验收对象是 owner 内独立 gopls forwarder/Job。

## 5. 日志与观测合同

### 5.1 公共字段

所有事件包含：`event`、timestamp、language、workspace hash、owner PID/start/local instance ID、client PID/start、generation、active leases、idle age、effective timeout、authority decision、reason、scan duration/lag。字段不可得时省略并增加 `missing_fields`；不得虚构 Codex `turn_id/thread_id/agent_id`。

本地关联键不能依赖宿主私有 ID：

- `lifecycle_key = owner_instance_id + workspace_hash + client_pid/start + generation + receipt_id`，稳定标识一代进程生命周期；
- `operation_id` 每次 candidate/closing/retry/ghost observation 尝试唯一，连接 candidate -> shutdown -> platform action -> verify/blocked 全事件；
- `lease_id` 在 acquire 时生成并保存在 `leasedClient`，release 必须复用同一个 ID；事件自身另用 event ID。可选连接 MCP request ID，相同 JSON-RPC ID 跨重启不能冲突；
- `ghost_dedup_key` 的版本化公式为 `schema_version + platform + product_cache_scope + receipt_id + client_pid/start + binary_digest + evidence_digest`。identity 完整时可跨扫描/重启稳定；缺少 start identity/generation/receipt 时仍 blocked，不得做跨扫描的 identity 去重，只允许 scan-local identity suppression；持久层只能使用下一项 observation bucket 做有界证据压缩，不能声称是同一进程；
- `ghost_observation_bucket_key = schema_version + platform + product_cache_scope + reason + available_pid + binary/evidence_class`，只允许压缩身份不完整的观测证据；它不是 identity/dedup/owner key，合并不得提升 certainty 或授权 signal；
- `event_id` 唯一标识 durable decision，`projection_id=event_id+projection_kind` 唯一标识 candidate/blocked 投影；consumer/fan-out 必须幂等 ack；
- `lifecycle_key`、`operation_id`、`lease_id`、`ghost_dedup_key`、`ghost_observation_bucket_key`、`event_id/projection_id` 分工固定，任何一个都不能替代另一个。

本地必填键缺失必须 fail-fast 或记录明确 blocker，不能退化为 turn/agent ID。

路径、argv 和错误必须经过现有 redaction helper；不得记录源码、请求 payload、secret、完整用户路径或环境变量值。

### 5.2 事件

| 事件 | 关键语义 |
|---|---|
| `lsp_idle_countdown_started` | 最后 lease 释放，记录 owner 内权威 `idle_since` |
| `lsp_idle_reclaim_candidate` | 满足唯一谓词，尚未发送信号 |
| `lsp_idle_reclaim_skipped` | lease、identity、generation 或 idle 复核变化 |
| `lsp_idle_scan_delayed` | scanner lag/duration 超预算 |
| `lsp_reclaim_draining` | 当前 owner 获得 cleanup ownership，记录树大小 |
| `lsp_reclaim_term_sent` | 仅 Unix、只针对当前 owner 精确成员/树 |
| `lsp_reclaim_kill_sent` | Unix SIGKILL 或 Windows force kill；Windows 必含 `platform_action=TerminateJobObject` 且绝不产生 term event |
| `lsp_reclaim_failed` | owner 内协议、transport、wait 或验证失败 |
| `lsp_reclaim_verified` | 根 PID 与后代退出证据 |
| `lsp_reclaim_ledger_released` | owner ledger 在验证后销账 |
| `lsp_ghost_candidate_observed` | 无权接管或身份/权限/探测不完整的 Node/gopls candidate；不是幽灵定论 |
| `gopls_daemon_ghost_candidate_observed` | daemon 超时后仍可见或无法完整探测的 candidate 证据 |
| `lsp_reclaim_blocked` | `no_authoritative_owner`、`permission_denied`、`identity_mismatch`、`probe_failed` 等；必须包含 `signal_sent=false` |

聚合快照至少包含 tracked clients、active leases、idle countdown/eligible、cleanup pending、verified reclaimed、ghost candidates、blocked、Node/gopls PID 与 RSS。RSS 仅用于观测和验收，不参与 kill 资格。

所有无 owner/无权限/identity mismatch/PID reuse/probe failed/unknown 的同一次 observation 先原子提交或原位更新单个 durable `ghost_decision` incident，其中同时包含 candidate 和 blocked 投影、同一 `operation_id/event_id`、reason 与 `signal_sent=false`。外部 fan-out 可产生两条事件，但不能成为权限真值；任一 fan-out 失败保留各 projection ack 与 `pair_pending` 并幂等重试。状态变化立即记录；状态未变化时同一有效 `ghost_dedup_key` 最多每 15 分钟重发一次；身份不完整时更新同一有界 observation bucket，不逐 scan 创建 record。聚合事件携带 `suppressed_count`，全局稳定态重发默认上限 120 条/分钟。日志限流、store、compaction 或 fan-out 失败都不得授权 signal。

观察 store 的发布默认总上限为 64 MiB：owner lifecycle/terminal 区最多 56 MiB，terminal receipt TTL 7 天且最多 10,000 个 terminal 文件；observation-only unresolved bucket 区最多 1,024 个 bucket/8 MiB。owner 区只按 verified terminal 时间从旧到新原子 pruning；`starting`、`Closing`、`CleanupPending` 不得因 TTL/容量静默删除，spawn 前无法为 owner intent 预留容量必须 fail-fast。ghost bucket 不承担 cleanup authority，达到子配额时必须在同一锁内原位合并到按 product scope/reason 的 overflow bucket，保留总数、first/last seen、latest evidence 和丢弃样本计数；不得阻断新 spawn、不得提升身份结论、不得 signal。单轮 ghost 扫描必须有条目/时间预算和续扫 cursor，达到预算只延迟观察，不改变回收资格。

## 6. 实施任务 DAG

| 任务 | 内容 | 依赖 | Exit gate |
|---|---|---|---|
| T0 | 保存 PID/tree/RSS/owner/lease/log 基线与冻结 diff | 无 | 可复查 baseline artifact |
| T1 | 先写 RED：lease 释放、旁路、ProcessTree、gopls timeout、ghost no-signal | T0 | 未修源码稳定命中缺陷 |
| T2 | 统一 lease guard、idleSince、IdleEligible 与 scanner telemetry | T1 | 定点、静态守卫、race GREEN |
| T3 | owner 内 ProcessTree 关闭/验证/延迟销账 | T1,T2 | Unix/Windows 平台测试 GREEN |
| T4 | 合并 effective idle policy 与 gopls timeout | T1,T2 | 默认/覆盖/非法配置测试 GREEN |
| T5 | 安全观察回执与 ghost blocked 日志；禁止跨 owner signal | T2,T3,T4 | no-signal 与 redaction 测试 GREEN |
| T6 | 真实 Node/gopls、100-workspace、soak 与 artifact smoke | T3,T4,T5 | exact artifact 证据齐全 |
| T7 | codemap/运维文档、灰度和回滚 | T6 | release checklist 关闭 |

唯一允许的并行关系为：

~~~text
T0 -> T1 -> T2
             ├─> T3 ─┐
             └─> T4 ─┴─> T5 -> T6 -> T7
~~~

T3/T4 可在 T2 后并行；T5 必须等待 T3/T4。合入前由同一集成 owner 处理字段、状态机和测试冲突。

## 7. 实施文件清单

### 7.1 P0：lease、idle 和调用覆盖

- `manager.go`、`manager_lifecycle.go`、`factory.go`、`pool.go`、`recycler.go`
- `manager_diagnostics_readiness.go`、`manager_symbols.go`、`manager_symbols_retry.go`、`manager_retry.go`、`bootstrap_doc.go`
- 新增 AST/static guard，枚举所有生产 `Client.Request/Notify/DidOpen/DidChange/DidClose` 调用；新增旁路即 fail。
- 删除 `ManagerPool.leases` 的生命周期写权，增加 stale-generation Release、锁序、负计数和 acquire/release/scanner race 守卫。
- `pool.go` 的 shard-cap/release-drain/clone retirement 不得以零 lease 代替 `IdleEligible`；新增 bootstrap/scanner 与 cap-pressure/eviction 定点测试。

### 7.2 P0：ProcessTree 与关闭事务

- `client.go`、`transport.go`、`transport_conn.go`、`resource_cohort.go`、`resource_cohort_policy.go`
- `cmd/mcp-lsp/internal/hiddenexec/process.go`、`process_default.go`、`process_windows.go`
- `process_tree_unix.go`、`process_tree_windows.go`、`process_other.go`
- `transport_responder_drain_test.go`、`transport_process_tree_unix_test.go`、hiddenexec Unix/Windows/unsupported fake 与原生平台测试。
- Linux 新增 pidfd available/unavailable、独占 session、foreign-member、reuse-window 测试；pidfd unavailable 必须零信号/pending。Darwin 新增 `proc_pidinfo` 高分辨率 token 与 subsecond reuse 原生测试；静态守卫禁止秒级 `ps lstart` 或未复核 parent/group kill 进入破坏性路径。

所有关闭原因只有在当前 owner 的根 PID/start 和后代退出验证成功后才删除 owner member。失败进入 `CleanupPending` 并由同一 workspace generation 或 provisional cleanup owner 重试；不得新增第二权威表。owner 消失后只留下终态/阻断观测。

### 7.3 P0：gopls 有效 timeout

- `internal/contract/config.go`：新增 `LSPConfig.IdleTimeout time.Duration`
- `internal/platform/config/lsp.go`：唯一默认值/环境解析与 fail-fast
- `cmd/mcp-lsp/fx.go`、`cmd/mcp-lsp/runtime.go`：单次解析并沿 `newManager -> registerRuntimeAdapter -> createGenericManagerWithBinary` 注入
- `cmd/mcp-lsp/multilsp/manager.go` 的 `Config`/可失败 `NewManager`/`cloneForWorkspace` 与 `pool.go` 的 `NewManagerPool`
- `cmd/mcp-lsp/multilsp/language_service_config.go` 及其 clone/default tests
- `cmd/mcp-lsp/multilsp/adapter.go`、`recycler.go`、`generic_language_service_test.go`
- `cmd/mcp-lsp/runtime_gopls_cohort.go` 及 Windows remote-args N/A 守卫
- gopls daemon/forwarder E2E、canonical/legacy env 冲突测试、zero-config fail-fast、scoped clone 传播与配置双真值静态测试。

不得新增 `gopls_daemon_reaper`；shared daemon 的退出权威是 gopls 自身 timeout。

### 7.4 P1：观察回执和 ghost 日志

建议新增精确路径：

- `cmd/mcp-lsp/internal/processprobe/snapshot.go`、`probe_unix.go`、`probe_windows.go`、`probe_other.go`
- `cmd/mcp-lsp/internal/processobserve/observer.go`
- `cmd/mcp-lsp/internal/processobserve/receipt.go`
- `cmd/mcp-lsp/internal/processobserve/store.go` 与平台安全实现/测试

复用或扩展 `internal/platform/securefs` 与现有 POSIX dirfd/openat 安全范式，提供 Darwin/Linux/Windows 原生权限、symlink/hardlink/reparse/ACL/原子替换和跨 sidecar admission/prune 测试。

observer 只消费 sealed Snapshot 值；新增 go/types/SSA guard 禁止 probe/observer/wiring 的 signal、kill、任意 exec 能力和包装/别名绕过，并禁止包外 Snapshot 构造/写入。receipt/log/lifecycle/operation/dedup/bucket/event/projection/config 字段实施时必须执行字段守卫，覆盖全部生产者、clone/default、消费者、序列化和兼容边界。

### 7.5 P0：E2E、canonical gate 与发布 provenance

- `cmd/mcp-lsp/lsp_binary_idle_reclaim_e2e_test.go`：真实 Node、默认 15 分钟 gopls、Windows Job 与 exact artifact smoke。
- `scripts/mcp_lsp_idle_soak.sh`、`scripts/mcp_lsp_idle_soak_test.go`：固定 manifest、100-workspace 两阶段 smoke/soak 与 exact teardown receipt。
- `scripts/mcp_lsp_workload_catalog.json` 是唯一可写 workload catalog，schema 固定为 `super-dolphin/mcp-lsp-workload-catalog/v1`；每项定义 ID、runner target、平台、timeout、trigger class、receipt schema、producer workflow path/artifact name 和是否阻断 T6/release。`scripts/run_mcp_lsp_workload.sh` 只按 ID 读取 catalog 并执行，不在 shell 内复制命令真值。
- `Makefile`、`scripts/ai_maintenance/main.go`、`scripts/ai_maintenance_gates.sh`、对应 gate/catalog drift guard tests、`.githooks/README.md`：Make target、AI maintenance plan、launcher、hook 文档只能引用同一 catalog ID；`ai_maintenance_gates.sh` 仍只是 launcher，不能成为第二 catalog。缺任何必需 receipt 或 catalog digest 不匹配均 fail-closed。
- `.github/workflows/release.yml`、`scripts/release_rollout_validate.sh`、`scripts/release_rollout_workflow_guard_test.go`：在既有唯一发布阶梯内增加 mcp-lsp artifact/idle/soak prerequisite 和版本化 attestation validator；不得另建百分比控制面。

### 7.6 P2：进程产生效率

`node_modules` package 被解析成独立 workspace 是另一个效率 bug。它可减少进程产生量，但不能替代本修复，也不能改变 15 分钟资格。

## 8. RED -> GREEN 矩阵

| ID | RED 复现 | GREEN 断言 |
|---|---|---|
| IDLE-001 | lease 只在获取时刷新 | 长请求释放后的 15 分钟重新完整计时 |
| IDLE-002 | diagnostics/bootstrap/retry 绕过 lease | 静态守卫覆盖全部生产 Client 调用，scanner 并发不 detach |
| IDLE-003 | RSS/cap/probe 旁路 | 未满 idle timeout 只告警/背压，不 kill |
| IDLE-004 | scanner 同步耗时 | 记录 lag/duration，不承诺一个周期内完成 |
| IDLE-005 | pool 与 workspace 双写 lease | workspace generation 是唯一写源；stale Release 不修改 replacement；无锁反转/负计数 |
| IDLE-006 | bootstrap 阻塞超过 15 分钟 | Starting/Bootstrapping 永不 IdleEligible；ready publish 后才从 publishedAt 起算 |
| POOL-001 | shard-cap 在零 lease、未满 15 分钟时驱逐 clone | 零 shutdown/signal；只排队/拒绝/纯 metadata detach，进程关闭仍走统一 idle 事务 |
| CLOSE-001 | Close 提前销账 | 根或后代仍活时 member 保留且 `cleanup_pending` |
| CLOSE-002 | fatal/bootstrap/EOF 走不同清理路径 | 所有 reason 共用 verify-before-release 事务，仅 idle reason 等待 15 分钟 |
| CLOSE-003 | receipt bind 后关闭失败 | exact ProcessTree 先移交 provisional owner，完成前不二次 spawn |
| CLOSE-004 | initialize 后提前 publish，bootstrap 阻塞/失败 | Bootstrapping 不可 lease/idle；并发 Ensure 等待 ready；成功才 Active 或 IdleCountdown，失败同 generation Closing |
| TREE-001 | 只验证 Node 父进程或只按进程名枚举 | manifest 固定 package version/binary digest，并按 native identity/session 捕获全部实际后代；全员退出后才 reclaimed |
| TREE-002 | Unix TERM 被忽略 | signal-time identity 复核后 bounded TERM -> wait -> 再复核 -> KILL |
| TREE-003 | Windows 被伪装成 TERM/KILL | graceful wait -> `TerminateJobObject` -> Job empty；事件为 force-kill |
| TREE-004 | PID/PGID/Job handle 在 action 前变化 | 每个 destructive edge fail-closed、零 signal、进入 blocked/pending |
| TREE-005 | Unix 根退出但已知后代仍存活 | 禁止 PGID 广播；只逐个复核/信号已知成员，未知成员导致 blocked/no-signal |
| TREE-006 | Unix 根仍活但 PGID 混入外来/未知成员、pidfd 不可用或 verify 后复用 | 独占 session + action-time 成员闭包；Linux 仅 pidfd 逐成员；无法安全绑定 signal 即 blocked/no-signal；无 parent/group fallback |
| TREE-007 | Darwin 秒级 start token 命中快速 PID reuse | 原生高分辨率 token + UID/session 校验；subsecond reuse 零信号 |
| GHOST-001 | owner crash 后回执残留 | 原子 candidate+blocked decision，断言零 signal 调用 |
| GHOST-002 | PID reuse/权限/探测失败 | 每个 reason 原子 candidate + blocked，`signal_sent=false` |
| GHOST-003 | observer/probe/wiring 间接绕过 | go/types/SSA guard 拒绝非零 signal、kill、非 allowlist exec 及包装/别名/函数字段；observer 只收 Snapshot |
| GHOST-004 | durable pair/fan-out 中途失败 | pair_pending 按同一 operation ID 重试，权限结论始终 no-signal |
| GHOST-005 | 无 identity/probe_failed 每 30 秒新建 durable record | 原位 bucket/overflow 聚合；>10,000 次观测不耗尽 owner admission、不 signal |
| GHOST-006 | 第一条 projection 成功后崩溃 | event/projection ID + 独立 ack 幂等恢复，candidate/blocked 最终各一次语义 |
| RECEIPT-001 | symlink/hardlink/reparse/ACL/rename 竞态 | 原生平台 secure-store suite 全部 fail-fast；不 spawn、不覆盖旁路目标、不 signal |
| RECEIPT-002 | 双 sidecar 同时 admission/prune | canonical root + 短时 store lock 原子预留配额；崩溃释放锁但不静默释放 reservation |
| GOPLS-001 | 默认 remote timeout 为 1s | config->runtime->multilsp->adapter/recycler 同源且默认 15m；canonical/legacy 冲突 fail-fast |
| GOPLS-002 | 多 forwarder 共享 daemon | 关闭一个不影响另一个；最后连接消失后 daemon 自清理 |
| GOPLS-003 | daemon timeout 后仍残留 | 产品原子输出 daemon ghost-candidate + blocked 决策但不 signal |
| GOPLS-004 | Windows 移除 remote 参数 | shared daemon 明确 N/A；owner 内 gopls Job 走 15 分钟 idle 回收 |
| GOPLS-005 | 2 秒 legacy override 冒充默认策略证据 | 无 override 的真实 15 分钟 daemon E2E 独立 30m gate；短 override 只作非发布快测 |
| STDIO-001 | 把 MCP 静默当 LSP idle | stdio 静默后 `tools/list`/`tools/call` 仍成功 |
| OBS-001 | stdio 无 turn/agent ID | lifecycle/operation/lease/dedup/bucket/event/projection 本地键齐全，acquire/release 复用 lease_id，可选宿主字段缺失不影响关联 |
| OBS-002 | receipt/log 无界 | canonical store root、原子配额、terminal TTL/文件/字节、续扫和节流生效；pending 永不因 TTL 静默删除 |
| ARTIFACT-001 | binary E2E 临时自编译或现场自证 hash | release smoke 强制消费 exact artifact + 独立 CI receipt，缺路径/hash/revision/platform 即 fail |
| ARTIFACT-002 | 调用方伪造本地 receipt、catalog 漂移或 release 未消费 workload | CI run/workflow/head SHA/artifact uniqueness/schema/catalog digest 绑定；缺 canonical receipt 阶梯不能推进 |
| SOAK-001 | 100 workspace 计数存在但 owner/teardown 不可追溯 | frozen manifest 绑定 workspace->owner PID/start/generation/group-or-Job，teardown 输出 exact member receipt |

未修生产代码必须先命中目标 RED；若 dirty tree 已改变行为，使用可恢复 mutation 证明测试敏感性，并保存 mutation/diff receipt。

## 9. 验证门禁

### 9.1 单元、静态与 race

~~~bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/multilsp -count=1
go test -race ./cmd/mcp-lsp/multilsp -count=1
./scripts/test_with_guard.sh ./cmd/mcp-lsp/internal/hiddenexec -count=1
./scripts/test_with_guard.sh ./cmd/mcp-lsp/... -count=1
~~~

T1 必须先落下并稳定命中至少这些 bug-locking tests：

- `TestLeaseReleaseStartsFullIdleWindow`
- `TestStaleGenerationReleaseCannotMutateReplacement`
- `TestRecyclerCannotDetachDuringLeaseAcquireReleaseRace`
- `TestBootstrappingNeverIdleEligible`
- `TestShardCapacityCannotCloseBeforeIdleTimeout`
- `TestIdleCloseFailureRetainsSameGenerationUntilVerified`
- `TestCleanupPendingCannotBeLeasedOrReplaced`
- `TestMultipleProvisionalGenerationsKeepDistinctTreeOwners`
- EOF/shutdown/cancel/fatal/initialize/bootstrap/idle 参数化 close-reason matrix
- initialize 成功后 bootstrap 阻塞/失败 + 并发 Ensure/lease/publish race tests
- receipt bind failure + terminate/wait/verify failure owner-handoff tests
- Unix root-gone/known-descendant/unknown-member、root-alive foreign-member、verify-to-signal reuse 与 TERM/KILL 两窗口 identity tests
- Darwin native high-resolution start token/subsecond reuse tests
- sealed/unforgeable processprobe Snapshot、go/types/SSA capability mutation guard、durable ghost-decision fan-out crash/idempotency tests
- `TestGhostObservationFloodCompactsWithoutBlockingOwnerAdmission`（至少 10,001 次 incomplete/probe-failed observation，signal fake 为零）
- 双 sidecar store admission/prune/quota/lock-holder crash tests
- `TestEffectiveIdlePolicy(Default|Override|LegacyAlias|RejectsConflict|RejectsInvalid|RejectsZeroManagerConfig|FlowsThroughRuntimeManagerChain|PreservesScopedClone|FeedsRecyclerAndGoplsCommand)`

上述门禁分别在 Darwin、Linux 和 Windows 原生 runner 执行，并在各平台追加 `-run 'ProcessTree|Idle|GoplsRemote|Ghost'` 的定点 receipt。跨平台只编译不能代替原生进程/Job 行为测试；无法执行的平台保持 `NOT_VERIFIED`，也不能用本机 diagnostics 替代。

### 9.2 真实 Node E2E

新增 `TestMcpLSPBinaryRealTypeScriptLanguageServerReclaimsOwnedTreeAfterIdle_E2E`：

1. 使用真实 `typescript-language-server`，冻结 lockfile package version 与入口 binary digest；`tsserver/typingsInstaller` 是该版本的预期角色断言，但回收正确性按 owner manifest 中 native identity/session 捕获的全部实际后代判断，不能只匹配进程名。
2. 14 分 59 秒保持，达到 timeout 后等待 scanner 实际完成并记录 detection lag。
3. 根 PID/start 与全部后代最终消失，下一请求可懒启动新 generation。
4. owner crash 场景只出现原子 `lsp_ghost_candidate_observed` + `lsp_reclaim_blocked(signal_sent=false)` 决策，不要求产品自动收敛。

已有 `TestMcpLSPBinaryRealTypeScriptLanguageServerUsesSixReadOnlyTools_E2E` 保留为真实工具冒烟，不能替代 idle E2E。

真实 15 分钟测试必须显式给足测试框架预算；测试内部 context 至少 25 分钟，gate 使用 30 分钟：

~~~bash
go test -tags=e2e ./cmd/mcp-lsp \
  -run '^TestMcpLSPBinaryRealTypeScriptLanguageServerReclaimsOwnedTreeAfterIdle_E2E$' \
  -count=1 -v -timeout=30m
~~~

### 9.3 真实 gopls E2E

保留并扩展：

- `TestMcpLSPBinaryConcurrentAgentsUseSharedGoplsDaemon_E2E`
- 将现有设置 legacy `2s` override 的 `TestMcpLSPBinaryRealGoplsDaemonExitsAfterLastForwarder_E2E` 明确归类为短时 override 回归，不得作为默认策略或发布 GREEN；
- 新增 `TestMcpLSPBinaryRealGoplsDaemonUsesDefaultFifteenMinuteIdleTimeout_E2E`：清除 canonical/legacy env，断言 resolved config 和实际 argv 都是 `15m`，两个 forwarder 共存时 daemon 保持，最后连接消失后真实等待默认窗口并验证退出；
- 新增 daemon 残留只日志、不 signal 的测试。

测试证明：forwarder 遵守 owner 内 idle；两个 forwarder 共存时 daemon 保持；最后连接消失后 gopls 按 effective timeout 自清理；若没有自清理，产品只输出 exact identity/socket 的 blocked 证据。

默认 15 分钟测试内部 context 至少 25 分钟，并独占 30 分钟 package timeout；不得与 2 秒 override 测试共享进程或预算：

~~~bash
env -u MCP_LSP_IDLE_TIMEOUT -u MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT \
  go test -tags=e2e ./cmd/mcp-lsp \
  -run '^TestMcpLSPBinaryRealGoplsDaemonUsesDefaultFifteenMinuteIdleTimeout_E2E$' \
  -count=1 -v -timeout=30m
~~~

Darwin/Linux 执行 shared-daemon E2E；Windows 因生产代码移除 remote args，shared-daemon 项标记 `N/A`，必须新增并在 Windows 原生 runner 执行：

~~~powershell
go test -tags=e2e ./cmd/mcp-lsp `
  -run '^TestMcpLSPBinaryWindowsGoplsForwarderReclaimsOwnedJobAfterIdle_E2E$' `
  -count=1 -v -timeout=30m
~~~

该测试验证 active lease 不被终止、15 分钟后 owner Job 根与成员全部退出、Job handle 验证后释放、新 generation 可启动。skip 不能记为 Windows GREEN。

### 9.4 100-workspace 压力与 soak

新增 `TestMcpLSPIdleReclaimHundredWorkspaces_E2E` 与 `scripts/mcp_lsp_idle_soak.sh`。固定 seed `20260804`，固定 owner 映射为：6 个健康 Node sidecar 各 8 workspace、6 个健康 gopls sidecar 各 8 workspace、4 个专用 crash sidecar 各 1 workspace（2 Node + 2 gopls），合计 16 owners/100 workspaces。每种语言各含 28 normal-idle、20 active/long-lease、2 owner-crash，总计 56 idle、40 active、4 crash。断言：

启动任何进程前，harness 必须生成只读 frozen manifest，逐 workspace 记录固定 workspace ID、role、language、owner slot、预期 generation 和 sandbox；spawn 后原子补齐 owner PID/native-start/instance、client PID/native-start、独占 session/PGID 或 Job handle identity、artifact SHA。manifest 条目缺失、重复或超出固定 100 项立即失败。teardown 必须输出逐条 exact member before/action/after receipt 和最终零残留断言；不能只写汇总计数，也不能把未在 manifest 的进程纳入清理。

- 数量、RSS、cap 不造成回收；容量不足只能排队或 fail-fast；
- 56 个从各自最后 lease 释放时完整计时，owner-local 根与后代最终精确为 0；
- 40 个无中断、无 lease 负数、无 generation 错配；
- 4 个 owner-crash 样本进入原子 candidate+blocked 决策且 signal fake 为 0，产品不要求自动收敛；
- scan lag/duration、PID 和 RSS 有时间序列，不能只看最终计数。

测量分两阶段，禁止把允许残留的 crash ghost 混入 owner-local 回收阈值：

1. 产品断言阶段：只验 96 个健康-owner workspace 的 56 idle/40 active 结果；4 个 crash 样本单独验 candidate+blocked/no-signal，global RSS/FD/PID 不在此阶段判收敛。
2. harness teardown 阶段：测试 harness 使用启动时保存的 sandbox/process-group/Job handle 精确清理自己创建的 4 个 crash 样本；这不是产品 reaper。清理验证完成后再计算全局资源阈值。

定量门禁：active 40 的 recycler-induced 请求错误/取消为 0；harness teardown 后 goroutine 与 FD/handle 增量分别不超过 `max(10, baseline*5%)`；排除 active 40 和合法 shared daemon 后，RSS 增量不超过 `max(64MiB, baseline*10%)`；scan lag p99 不超过 60 秒、单次不超过 120 秒；稳定态日志不超过 120 条/分钟；receipt store 满足 TTL/文件/字节上限。超过阈值即失败，不得只写告警。

先运行 30 分钟 smoke，再运行 2 小时混合 soak。owner 内 `CleanupPending` 必须转为 reclaimed 或明确 blocker；crash candidate 在产品断言阶段可持续存在，但每次观察都有原子 durable decision 和最近证据，最终由 harness teardown 清理。

~~~bash
./scripts/mcp_lsp_idle_soak.sh --mode=smoke --duration=30m --seed=20260804
./scripts/mcp_lsp_idle_soak.sh --mode=soak --duration=2h --seed=20260804
~~~

30 分钟 smoke 与 2 小时 soak 必须是两个独立 CI job/测试进程：smoke timeout 至少 40 分钟，soak timeout 至少 130 分钟；禁止把二者串行塞入同一个 130 分钟预算。两者分别保存 seed、artifact SHA、catalog digest、平台、PID/RSS/FD/goroutine/scan/log/receipt 时间序列和完整退出码。V4/V9 不阻断 T1-T5，但阻断 T6 和发布。

### 9.5 source harness 与发布 artifact

当前若干 `cmd/mcp-lsp/*_e2e_test.go` 会在测试内编译临时二进制，它们是 source harness，不是待发布 `bin/mcp-lsp` 的 artifact receipt。

实施必须新增：

- Makefile target `build-mcp-lsp-release-artifact`：在干净 commit 构建 artifact，并把 SHA-256、`vcs.revision`、`vcs.modified`、GOOS/GOARCH 和构建命令写入版本化 `super-dolphin/mcp-lsp-release-attestation/v1`；attestation 还必须绑定 kind、repository、CI run ID/attempt、producer workflow path、head SHA、artifact 唯一名称、canonical workload receipt IDs 和 exact previous artifact 坐标/digest；
- `TestMcpLSPReleaseArtifactSmoke_E2E` 与 target `test-e2e-mcp-lsp-release-artifact`：必填绝对路径 `MCP_LSP_RELEASE_ARTIFACT` 和独立的 `MCP_LSP_RELEASE_RECEIPT`。测试从 receipt 读取 expected hash/revision/platform，缺失、不可信、不可执行或不匹配立即失败，禁止现场重算被验文件 hash 充当 expected，也禁止回退为临时编译；
- exact-input smoke 至少覆盖 `initialize -> tools/list -> tools/call`，并确认执行 PID/path 就是输入 artifact。

`MCP_LSP_RELEASE_RECEIPT` 只是同一 CI job 内传递 receipt 的文件路径，不是信任根。发布控制面必须由 `scripts/release_rollout_validate.sh` 通过 GitHub API 取得 exact producer run，验证 `conclusion=success`、`head_sha==BUILD_COMMIT`、catalog 固定的 workflow path/artifact name、artifact 名称唯一且只含一个根 attestation、schema/kind/platform/tuple/previous digest/catalog digest 全匹配；本地伪造 JSON、其他 workflow、重复 artifact、过期 run、错误 commit、catalog 漂移或缺 workload receipt 一律 fail-closed。长 Node/gopls/100-workspace release-mode E2E 也必须消费同一 exact artifact，禁止回退为测试内临时编译。

canonical gate 至少拆成 `mcp-lsp-idle-quick`、`mcp-lsp-native-process-tree`、`mcp-lsp-default-15m`、`mcp-lsp-100-workspace-soak`、`mcp-lsp-release-artifact` 五个 workload ID。`scripts/mcp_lsp_workload_catalog.json` 是 ID、runner target、timeout、platform、receipt schema、producer workflow/artifact name 的唯一 owner；`Makefile`、`scripts/ai_maintenance/main.go`、hook 和 release workflow 只消费 ID/runner，不复制命令。catalog guard 必须枚举所有消费面并证明 ID 集合、命令、timeout、receipt schema、producer coordinates 和 catalog digest 一致。PR/pre-push 可只跑 quick，T6/release 必须检查其余平台/长时 receipt，缺项不能推进阶段。

发布候选必须在干净集成提交上执行：

~~~bash
MCP_LSP_RELEASE_RECEIPT="$RUNNER_TEMP/mcp-lsp.release-receipt.json" \
make build-mcp-lsp-release-artifact
go version -m bin/mcp-lsp
shasum -a 256 bin/mcp-lsp
make test-e2e-mcp-lsp-resource-cohort
make test-e2e-mcp-lsp-idle-quick
MCP_LSP_RELEASE_ARTIFACT="$PWD/bin/mcp-lsp" \
MCP_LSP_RELEASE_RECEIPT="$RUNNER_TEMP/mcp-lsp.release-receipt.json" \
make test-e2e-mcp-lsp-release-artifact
go test -tags=e2e ./cmd/mcp-lsp \
  -run 'TestMcpLSPBinary(StdioRemainsAvailablePastLegacyProcessIdleTimeout|ConcurrentAgentsUseSharedGoplsDaemon)' \
  -count=1 -v -timeout=15m
MCP_LSP_RELEASE_ARTIFACT="$PWD/bin/mcp-lsp" \
MCP_LSP_RELEASE_RECEIPT="$RUNNER_TEMP/mcp-lsp.release-receipt.json" \
  env -u MCP_LSP_IDLE_TIMEOUT -u MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT \
  go test -tags=e2e ./cmd/mcp-lsp \
  -run '^TestMcpLSPBinaryRealGoplsDaemonUsesDefaultFifteenMinuteIdleTimeout_E2E$' \
  -count=1 -v -timeout=30m
MCP_LSP_RELEASE_ARTIFACT="$PWD/bin/mcp-lsp" \
MCP_LSP_RELEASE_RECEIPT="$RUNNER_TEMP/mcp-lsp.release-receipt.json" \
  go test -tags=e2e ./cmd/mcp-lsp \
  -run '^TestMcpLSPBinaryRealTypeScriptLanguageServerReclaimsOwnedTreeAfterIdle_E2E$' \
  -count=1 -v -timeout=30m
~~~

每个长 idle E2E 使用独立 `go test` 进程和独立 package timeout；不得与其他测试共享 30 分钟总预算。必须保存 source commit、clean status、artifact SHA-256、`go version -m` 的 `vcs.revision/vcs.modified=false`、平台 attestation、命令和完整退出码。证据分类固定为：测试内临时编译是 source harness；Git ignored 的 `bin/mcp-lsp` 可能陈旧，只是 local artifact；只有干净 commit 构建、由独立 CI receipt 固定 hash/revision/platform 并通过 exact-input smoke 的文件才是 release artifact。

所有修改文件执行 LSP `file(diagnostics)`；Error、Warning、Information、Hint 全部修复或登记 blocker。

## 10. 发布、回滚与完成定义

不得另造 mcp-lsp 百分比控制面。发布复用 `.github/workflows/release.yml` 和 `docs/运维/update-recovery-schema-rollout.md` 的唯一阶梯：`internal-20 -> 10-percent -> 30-percent -> 100-percent`；但当前 workflow 只有 update-recovery 证据，实施必须在同一控制面增加 mcp-lsp producer/prerequisite jobs，而不是假定现状已经覆盖。每阶段独立审批，百分比阶段至少观察 8 小时，并同时满足前驱 rollout attestation、exact mcp-lsp artifact attestation、canonical workload receipts、Darwin/Windows 原生证据和本方案专项指标。

既有发布控制面守卫必须通过：

~~~bash
./scripts/test_with_guard.sh ./scripts \
  -run 'TestReleaseRollout(WorkflowRequiresOneApprovedStageAndNativeARM64Evidence|RunbookDefinesExactLadderMetricsAndStopActions)|TestGitHubReleaseContinuityUsesExactPreviousTagEndpoint|TestMcpLSPRelease(WorkflowRequiresCanonicalWorkloadReceipts|AttestationRejectsWrongRunWorkflowCommitOrDuplicateArtifact|RollbackUsesExactPreviousArtifact)' \
  -count=1
~~~

出现以下任一情况立即停止：活跃 lease 被回收、未给足完整 idle window、owner 内树不收敛、gopls timeout 配置漂移、身份/权限失败仍 signal、账本提前删除、stdio 因静默退出、日志泄露、scan/log/receipt/FD/handle/goroutine/RSS 超过冻结阈值。

按现有 runbook 回滚 attestation 中绑定的 exact previous version/mcp-lsp artifact；先从受信 producer run 取得唯一 previous artifact，复核 digest/revision/platform，再执行 exact-input smoke。previous 坐标、artifact 或 validator receipt 缺失时停止而不是猜测本地 `bin/mcp-lsp`。保留 receipt、日志、PID/tree/RSS 快照，不执行“顺便清理”幽灵进程。

只有以下条件全部成立才能标记 fixed：

- T0-T7 exit gate 关闭，RED -> GREEN 全部有 receipt；
- client 在 required bootstrap 成功前始终不可 lease/idle-eligible；publish 与首个 lease 或 `IdleCountdown/publishedAt` 原子一致；
- 所有生产 Client 操作经过统一 lease，`idleSince` 从最后 lease 释放开始；
- pool capacity/clone retirement 未满完整窗口时不会关闭 client/ProcessTree；
- owner 内 Node/forwarder 15 分钟回收、树验证和延迟销账成立；
- gopls effective timeout 同源，zero config fail-fast、scoped clone 保值且无 override 的真实默认 15 分钟 Darwin/Linux E2E 成立；Windows shared daemon 明确 N/A，独立 forwarder/Job E2E 通过；
- Unix 独占 session/PGID、Linux pidfd/成员绑定、Darwin native high-resolution identity、Windows Job authority 的原生误杀防护 gate 通过；
- sealed/unforgeable processprobe Snapshot、processobserve/wiring capability guard、durable ghost-decision、projection 幂等和 no-signal 测试成立；
- canonical observation store 的跨 sidecar admission/prune/quota/security gate 与 >10,000 次 incomplete observation 有界聚合 gate 通过；
- 100-workspace frozen manifest/逐成员 teardown、两阶段测量、race、平台、soak、独立 CI receipt exact artifact 和 redaction gate 通过；
- 五个 canonical mcp-lsp workload receipt 被 release workflow/validator 消费，`internal-20 -> 10-percent -> 30-percent -> 100-percent` 既有发布/回滚控制面证据齐全；
- codemap 与运维说明同步。

在此之前只能报告 `red_locked`、`fixing`、`partial` 或 `RELEASE_BLOCKED`。

## 11. 代码审查维度台账

| 维度 | 状态 | 权威/可达性与证据 | 修复/测试/门禁 |
|---|---|---|---|
| D01 架构边界 | Applied | manager 只拥有自身树；gopls 自身 timeout；sealed processprobe Snapshot -> processobserve | go/types/SSA wiring guard + no-signal E2E |
| D02 Fail-fast | Applied | 配置、身份、权限、probe 异常不降级 | invalid config 与 blocked tests |
| D03 MCP 协议 | Applied | stdio sidecar 不受 LSP idle 静默退出 | STDIO-001 |
| D04 LSP 工具 | Applied | Client 调用面与真实 Node/gopls | static guard + E2E |
| D05 Provider/runtime | Applied | 不依赖 Codex 私有 ID；本地 lifecycle/operation key 完整 | OBS-001 与重启/并发 join tests |
| D06 Orchestration | Applied | workspace generation 是 lease/idle/closing 唯一 owner；Bootstrapping 非 idle；pool capacity 无旁路；DAG 已固定 | clock/bootstrap/cap-pressure/race/reason-matrix tests |
| D07 Store/sqlc | N/A | 不涉及 DB/sqlc | N/A |
| D08 Skill/Memory/Prompt/Thread | N/A | 不改这些面 | N/A |
| D09 Frontend | N/A | 无 UI | N/A |
| D10 Security | Applied | action-time identity、独占 session/PGID、native start/pidfd、Job handle、dirfd/ACL、observer capability、禁止跨 owner kill | Darwin/Linux/Windows native security tests |
| D11 Observability | Applied | durable incident、operation/lifecycle/lease/dedup/bucket/event/projection keys、幂等 fan-out、节流/上限 | schema/crash-retry/redaction/flood/soak tests |
| D12 Testing | Applied | RED/GREEN、real process、race、100、soak、独立 30m Node/default-gopls gate及 canonical workload catalog | T1/T6 + missing-receipt fail-closed guards |
| D13 Release/Install | Applied | source harness/local artifact/release artifact 分离；CI run/workflow/head/artifact uniqueness/previous digest 受信绑定 | exact-input artifact + rollout validator receipt |
| D14 Performance | Applied | 数量不参与 kill；扫描、FD/handle/RSS/log/store 阈值；incomplete ghost 原位有界聚合 | 100-workspace/flood/soak |
| D15 UX/Product | N/A | 无 UI；错误与日志为运维面 | N/A |
| D16 Git/Workflow | Applied | 冻结 HEAD/dirty 边界，不覆盖用户修改 | clean integration receipt |
| D17 字段守卫 | Applied | 计划新增 config/receipt/log/lifecycle/operation/dedup/bucket/event/projection/attestation 字段 | 实施回合必须做生产者/消费者/default/schema 守卫 |
| D18 DRY | Applied | 单一 lease guard、IdleEligible、effective policy；`scripts/mcp_lsp_workload_catalog.json` 单一拥有 workload ID/target/timeout/platform/receipt/provenance | duplicate-path/catalog consumer drift guard |
| D19 SSOT | Applied | workspace generation 是 lease/idle/cleanup 真值；pool 无销毁旁路；effective config 单源并显式传播；receipt 与 bucket 分区；gopls 自身 timeout；release validator 是 provenance 真值 | authority/config/store/gate/attestation drift tests |

## 12. 当前 review object 与证据边界

本方案修订基线：

- 两个车道均从 `HEAD=345aa86f585b1d4217034688bec926ad134aacfb` 起；加入本文档前各自 staged/dirty 为空；当前文档车道仅 staged 新增本文档，集成车道保持 clean。
- 权威源主工作树仍是 `main@8aae30a3f133b77a0f00619a4b2936792a8a2e77` 的 dirty review object；其已有用户修改为：`cmd/mcp-lsp/multilsp/recycler.go`、`internal/mcpserver/common/http_transport.go`、`server.go`、`tool_payload_log.go`、`tool_payload_log_test.go`、`pkg/logger/logger.go`、`pkg/logger/trace_context_test.go`；
- 权威源主工作树已有未跟踪文件：`cmd/mcp-lsp/multilsp/recycler_observability_test.go`、`internal/mcpserver/common/tool_call_observability.go`、`tool_call_observability_test.go`；本车道不解释、覆盖或回退这些源码修改。
- 复核 `8aae30a3..345aa86f` 的目标面漂移：`cmd/mcp-lsp/**` 无提交路径变化；唯一 mcp-lsp 定向脚本变化为 `scripts/mcp_lsp_resource_cohort_e2e_gate_guard_test.go`，当前版本从 `../Makefile` 读取 Makefile、移除 `.githooks/README.md` 断言，仍锁定 `test-e2e-mcp-lsp-resource-cohort`、同一 `-run` 表达式以及 canonical backend 单次选择。`.githooks/README.md`、`.githooks/pre-commit`、`.githooks/pre-push` 与 `scripts/test_with_guard.sh` 同期收敛为 remote ECI 入口；这只更新路径/runner 事实，不改变本方案已裁决的 idle、ProcessTree、ghost、timeout 或 release 合同。
- `scripts/mcp_lsp_workload_catalog.json`、`scripts/mcp_lsp_idle_soak.sh` 等仍是本方案规划中的待新增路径；当前基线不存在这些文件，不能据此写成已实现或发布 GREEN。
- 本文档是新增未跟踪文件；不表示生产修复完成。

本方案的 LSP 证据按 provenance 分层：

- `grep` 定位到 `defaultGoplsRemoteListenTimeoutArg="-remote.listen.timeout=1s"` 及其测试；
- `file(read_file)` 确认 `ServerCommand -> goplsRemoteListenTimeoutArg` 和独立环境解析；
- `inspect(definition)`、`xref(references/call_hierarchy)` 确认配置函数定义、调用与测试引用；
- `grep` 确认 `leaseBoundClient` 的生产入口和测试引用；
- `file(read_file)` 确认 Windows runtime 会删除 `-remote` 和 `-remote.listen.timeout`，所以 Windows shared daemon 为 N/A；
- 三方裁决的 `[WORKTREE-DIRTY]` diagnostics receipt 曾返回 `recycler.go:207 [Hint minmax]` 与 `resource_cohort.go:580 [Hint stditerators]`；本次收窄批次未复现。由于 dirty tree/LSP snapshot 结果不一致，这一项登记为 provenance blocker，不能写成 PASS，也不能归属 HEAD；
- 第二轮 lifecycle lane 对 Windows-only `process_tree_windows.go` 收窄 diagnostics 仍得到 duplicate/missing-field 类错误，而 authority lane 的本机收窄批次无诊断；两者都不能替代 Windows 原生 runner，本项继续登记为 platform/provenance blocker；
- Windows-only 分支必须在 Windows 原生 runner 验证；混合 build-tag diagnostics 或本机无输出都不能证明平台 GREEN；diagnostics 也不是进程收敛证据。

实施和发布复验必须在干净集成 commit 上重新取得定位、理解、影响面、精读和 diagnostics 五类证据，并处理所有 Error、Warning、Information、Hint。

## 13. 第二轮三方复核裁决

第二轮只读复核冻结对象为本文档修订前 SHA-256 `2a0292f8d0e60f615918287763104836c564255e3d08adfdbba4c38bf279200b`、604 行与同一 HEAD。三条 lane 均未修改文件：

- lifecycle：`APPROVE_WITH_CHANGES`，无 P0，2 个 P1；
- authority/observability：`REJECT`，无 P0，3 个 P1、2 个 P2；
- validation/release：`REJECT`，无 P0，3 个 P1、2 个 P2。

主裁决不按票数，按可达证据合并为以下修订：

1. 接受 lifecycle P1：`IdleEligible` 增加 state gate，bootstrap ready/publish 前不计 idle；pool capacity/clone retirement 不得以零 lease 绕过完整 15 分钟窗口。
2. 合并 authority 两个 Unix P1：独占 session/PGID、action-time 成员闭包、Linux pidfd/identity-bound signal、Darwin native high-resolution start token；禁止未知成员与未复核 parent/group fallback。
3. 接受 ghost quota P1：身份不完整时原位更新 observation bucket/overflow，observer 子配额耗尽不能阻断 owner intent admission，也不能授权 signal。
4. 接受 observer 两个 P2：Snapshot 使用不可导出字段/受控构造器；durable pair 增加 event/projection ID、独立 ack 与 crash-safe 幂等重试。
5. 接受 validation P1：新增无 override 的真实默认 15 分钟 gopls E2E，并与 2 秒 legacy override、Node idle E2E 分进程/分 30 分钟预算。
6. 接受 gate/release P1：五个 canonical workload 映射到 Makefile、AI maintenance、hook 文档和现有 release workflow；release validator 必须绑定受信 CI run/workflow/head SHA/唯一 artifact/schema/previous digest，本地 receipt 路径不构成信任根。
7. 接受 validation P2：100-workspace 增加 frozen owner manifest 与逐成员 teardown receipt；`NewManager` 对零 timeout fail-fast，scoped clone 必须保留同一 resolved duration。

对 SHA-256 `e10ee57f3b3eaacebe52f8285e5196f80a2976e6fb7a97c9c2bc4329c0ebf85d`、684 行版本的首次 post-fix 回看结果为：lifecycle `APPROVE`；authority/observability `APPROVE_WITH_CHANGES`，仅要求明确 Linux pidfd unavailable 必须零信号；validation/release `APPROVE_WITH_CHANGES`，要求指定 workload catalog 唯一文件/schema/producer coordinates，并拆开 smoke/soak 预算、固定 Node topology evidence。上述 residual 已继续并入当前合同：Linux 无 pidfd 不回退，`scripts/mcp_lsp_workload_catalog.json` 成为唯一 owner，smoke/soak 分 job，Node 使用冻结版本/digest 与 native manifest，而非只靠进程名。

状态保持 `NOT_IMPLEMENTED / RELEASE_BLOCKED`：以上是实施合同修复，不是生产代码、原生平台、长时 E2E、soak 或发布证据。
