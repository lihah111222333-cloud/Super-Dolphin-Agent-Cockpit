# P1a: CodexApp peer supervisor 收口

## 目标

把 `internal/provider/codexapp` 里由 `fx.Invoke(spawnToolbridgePeers)` 驱动的 peer 拉起与重启逻辑，改造成明确的 runtime owner，不再由模块装配阶段直接启动。本文默认用 `PeerSupervisor` 表述这一层 `RunnerModule` owner；`ServerManager` 继续留在 `fx.Module` 层，不再兼任 peer restart orchestrator。

## 对应 findings

- Finding 1: `internal/provider/codexapp/module.go:35`
- Finding 2: `internal/provider/codexapp/peer_spawn.go:18-155`

## 现状校准

- `Module` 当前通过 `fx.Invoke(spawnToolbridgePeers)` 直接进入运行时编排。
- `spawnToolbridgePeers` 在 `Invoke` 路径里先起后台 goroutine，再启动 `mcp-orch` / `mcp-lsp` peer。
- `watchAndRestartPeer` 进入无限重启循环，但它既不是 `Runner`，也没有统一的 `execute/interrupt` 生命周期。
- peer bookkeeping 目前直接散落在 `ServerManager` 的 `peerProcs/peerPipes/pidRegistry` 上，没有一个单独的 supervisor owner。
- `restartPeer(...)` 当前不会同步替换 `peerProcs`，成功替换后也没有主动关闭 `oldPipe`；这会把 stale process handle / FD 泄漏风险留在 stop 路径里。
- 当前 control-plane 等待不是 hard gate，而是读取 `GO_AGENT_CTL_RPC_ADDR` 后做最多 `30 x 200ms` 的 bounded best-effort 探测；若 P1a 要改成严格 gate，必须在文档里显式冻结兼容性取舍。
- 当前 peer 二进制定位依赖 `os.Executable()` 同目录下的 `mcp-orch` / `mcp-lsp`；缺失二进制的现状是 `warn+skip`，不是 fail startup。

## 目标架构

推荐把 peer 监督逻辑收口为 `PeerSupervisor`：

- `ServerManager` 继续负责 shared app-server 资源与 peer bookkeeping 容器
- 新增 `PeerSupervisor` 作为 `platformrunner.Runner`
- `Module` 只 `Provide(NewPeerSupervisor)` 并把它加入 `group:"runners"`
- `spawnToolbridgePeers` 删除；`watchAndRestartPeer` 逻辑并入 supervisor 的 `Run(ctx)`

示意：

```text
fx.Provide(NewServerManager)
fx.Provide(fx.Annotate(NewPeerSupervisor, fx.ResultTags(`group:"runners"`)))
fx.Invoke(RegisterTranslators)

app BindRuntime
  -> run.Group
     -> PeerSupervisor.Run(ctx)
```

## 实施方式

- 采用单一 `PeerSupervisor` owner；不把每个 peer 再拆成独立 Runner，也不在 `Run(ctx)` 内继续派生 fire-and-forget watcher goroutine。
- `ServerManager` 只保留 shared app-server 与 bookkeeping 容器；peer start / restart / stop / drain 全部转给 `PeerSupervisor`。
- `PeerSupervisor` 在降级场景下保持 owner 常驻：缺 binary、无 peer、或单次 restart 失败时记录降级状态并等待 `ctx.Done()`，不把单 peer 故障升级成 app-fatal。
- restart policy 默认沿用现状：固定 `2s` backoff，成功 respawn 后继续长期监督；若要改成指数退避或单次失败即退出，必须先在兼容语义里改判。
- control-plane 探测只作为 bounded best-effort startup assist；它改变的是启动前置准备，不改变 owner 分层。
- `warn+skip`、degraded 常驻、missing binary、restart-fail 不升级 app-fatal 这几条都视为一级兼容路径；实现与复审不能把它们当“异常分支可忽略”。

## 实施步骤

### Step 1：抽 supervisor owner

- 新增 `peer_supervisor.go`
- 定义 `type PeerSupervisor struct { mgr *ServerManager ... }`
- `Run(ctx)` 负责：
  - 对控制面做 bounded best-effort 探测，并明确探测失败后的固定行为
  - 启动 peers
  - 统一处理重启与退出
  - `ctx.Done()` 时停止并回收

### Step 2：收紧 `ServerManager` 接口

把直接修改 `peerProcs/peerPipes` 的逻辑改成方法：

- `RegisterPeer(...)`
- `ReplacePeer(...)`
- `UnregisterPeer(...)`

这样重启与 stop 路径都经同一 owner 收口。

其中 `ReplacePeer(...)` 必须原子维护三件事：

- 进程句柄
- `stdin` pipe
- pid registry

否则 stop 路径会向旧 PID 发信号，或遗漏新 peer 的回收；替换成功后还要显式关闭 `oldPipe`，避免把旧 write-end 留在父进程里。

### Step 3：移除 `Invoke` 编排

- 删除 `fx.Invoke(spawnToolbridgePeers)`
- 保留 `RegisterTranslators` 这类纯注册 `Invoke`

### Step 4：定义停止语义

`ctx.Done()` 后，supervisor 应：

- 停止继续 restart
- 取消正在 backoff 的 restart sleep，并在真正 respawn 前再次检查 shutdown 状态
- 关闭 stdin pipe / 发送 SIGTERM
- 等待有限 grace period
- 必要时 SIGKILL
- 最终同步 pid registry
- 在 return 前 wait/join 所有 peer monitor，不让 owner 抢先退出
- 接管 `cleanPeerDiscoveryFiles()` 这类 peer 附属文件回收，避免从 `ServerManager.stop()` 抽离后漏掉

## 需冻结的兼容语义

- control-plane 探测是否只是 best-effort，还是 hard gate，必须写死
- 缺失 peer 二进制时是 `warn+skip` 还是 fail startup，必须写死
- restart backoff 必须可取消，且 stop 期间不得因 sleep 醒来而误重启
- restart backoff 的现状节奏是固定 `2s`；若要改判为指数退避或其它策略，必须显式写入兼容性说明
- 重启/替换路径的 bookkeeping 更新必须与 stop 路径共享同一 owner
- restart 成功后的现状是恢复到长期监督状态并继续 watch；不能在成功替换后把 monitor 提前收掉
- 单次 restart 失败后的现状是“记录 warning 并停止该 peer 的监督，不升级成 app fatal”；若要改判，必须显式写入兼容性说明

## 收口口径

- `P1a` 只闭环 peer supervisor；session reader / health / recovery 继续归 `P1c`，不能在 `P1a` 完成后宣称 codexapp runtime 已整体收口。
- 本页说的 `RunnerModule` 指 runtime owner 角色；接入 root bridge 时沿用当前实现的 runner group tag，不把 group 命名清洗当成 `P1a` 前置。
- restart / replace / stop 必须由同一 owner 维护；不允许 `ServerManager.stop()` 与新 supervisor 各管一半 lifecycle。
- 降级策略保持“peer 可以缺席、app 仍继续运行”的方向；除非文档显式改判，否则 `PeerSupervisor` 不以单 peer 启动失败作为全局 fatal。
- `PeerSupervisor` 作为 `RunnerModule` owner，既要保住现有 `2s` fixed backoff + 继续监督语义，也要把 degraded-path 测试当成与 happy-path 同等级的回归面。

## 依赖图（文本）

```text
P0 -> P1a -> P1c
        └-> P2（若 peer 侧 runtime owner 收口后仍残留 toolbridge runtime 边界问题，再由 P2 接续）
```

## 同步约束

- 本单不要求重构 shared app-server 的 `ServerManager` 启动方式；若不触碰该逻辑，可先只收口 peer supervisor。
- 若实现中发现 `ServerManager` 仍承担过多 runtime owner 责任，可在代码里预留下一步拆分点，但不在本单强行扩 scope。
- `PeerSupervisor` 只收口 `mcp-orch` / `mcp-lsp` peers；`ServerManager` 现有 shared app-server 生命周期继续保持原样，避免把本单扩成 provider 全面重构。

## 非目标

- 不重写 shared app-server 的整体 lifecycle；本单只移动 peer start / restart / stop owner。
- 不改判 peer restart policy / binary lookup / discovery file 业务语义，只要求把它们从装配期迁到单一 owner。
- 不处理 session 级 reader / health / recovery；这些问题继续由 `P1c` 负责。

## 残留债务说明

- `newSession()` 当前仍会在返回前启动 `startReadLoop()/startHealthLoop()`；这属于 codexapp 会话级 runtime owner 问题，不会因 `PeerSupervisor` 落地自动消失。
- `handleConnectionDead() -> attemptRecovery()` 这条 `SafeGo(context.Background(), ...)` 恢复链同样不在本单闭环范围内；P1a 完成后不能宣称 “codexapp runtime ownership 已整体收口”。
- 本单完成口径仅限 peer supervisor；session 级 reader/health/recovery loop 需另行记账和修复。

## TDD 与旧实现清理

- 先补失败测试：`Invoke` 不再拉 peer、shutdown 中 backoff 不重启、pid registry/pipe 替换一致性、discovery file 回收、固定 `2s` backoff、restart 成功后继续监督、restart 失败保持 degraded 非 app-fatal。
- 修复完成后必须删除旧的 `fx.Invoke(spawnToolbridgePeers)` 接线，以及模块装配路径直启 peer 的实现；不能留下“新 supervisor + 旧 invoke”双轨。
- `peer_spawn.go` 若只剩被新 owner 吸收的零散 helper，应继续内联/重命名到新 owner 文件，避免保留语义重复的 legacy wrapper。
- 若保留 `RegisterPeer/ReplacePeer/UnregisterPeer` 风格新接口，就要同步清掉直接改 `peerProcs/peerPipes` 的旧调用点，不留旁路。

## 验收标准

- `internal/provider/codexapp/module.go` 中不再有 `fx.Invoke(spawnToolbridgePeers)`
- `peer_spawn.go` 不再包含模块装配路径直启 goroutine 的逻辑
- peers 异常退出仍可按既有策略重启
- app stop 时不再发生 peer restart 竞态
- control-plane 探测、binary lookup、restart backoff 的兼容语义有文档化结论且测试覆盖
- 至少补以下测试：
  - 启动后能拉起两个 peer
  - 异常退出时会重启
  - shutdown 时不会再重启
  - pid registry 在替换/退出后同步更新
  - `peerProcs` / `peerPipes` / `pidRegistry` 在替换后保持一致，且旧 pipe 被关闭
  - backoff 中收到 shutdown 不会再重启
  - restart 成功后 monitor 会继续监督，不会成功一次后静默失管
  - restart 失败保持 degraded，不把单 peer 失败升级成 app fatal
  - discovery files 在 stop 后被回收
