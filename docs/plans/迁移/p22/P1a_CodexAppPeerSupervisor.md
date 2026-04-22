# P1a: CodexApp peer supervisor 收口

## 目标

把 `internal/provider/codexapp` 里由 `fx.Invoke(spawnToolbridgePeers)` 驱动的 peer 拉起与重启逻辑，改造成明确的 runtime owner，不再由模块装配阶段直接启动。

## 对应 findings

- Finding 1: `internal/provider/codexapp/module.go:35`
- Finding 2: `internal/provider/codexapp/peer_spawn.go:18-109`

## 现状校准

- `Module` 当前通过 `fx.Invoke(spawnToolbridgePeers)` 直接进入运行时编排。
- `spawnToolbridgePeers` 在 `Invoke` 路径里先起后台 goroutine，再启动 `mcp-orch` / `mcp-lsp` peer。
- `watchAndRestartPeer` 进入无限重启循环，但它既不是 `Runner`，也没有统一的 `execute/interrupt` 生命周期。
- peer bookkeeping 目前直接散落在 `ServerManager` 的 `peerProcs/peerPipes/pidRegistry` 上，没有一个单独的 supervisor owner。

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

## 实施步骤

### Step 1：抽 supervisor owner

- 新增 `peer_supervisor.go`
- 定义 `type PeerSupervisor struct { mgr *ServerManager ... }`
- `Run(ctx)` 负责：
  - 等待控制面可达
  - 启动 peers
  - 统一处理重启与退出
  - `ctx.Done()` 时停止并回收

### Step 2：收紧 `ServerManager` 接口

把直接修改 `peerProcs/peerPipes` 的逻辑改成方法：

- `RegisterPeer(...)`
- `ReplacePeer(...)`
- `UnregisterPeer(...)`

这样重启与 stop 路径都经同一 owner 收口。

### Step 3：移除 `Invoke` 编排

- 删除 `fx.Invoke(spawnToolbridgePeers)`
- 保留 `RegisterTranslators` 这类纯注册 `Invoke`

### Step 4：定义停止语义

`ctx.Done()` 后，supervisor 应：

- 停止继续 restart
- 关闭 stdin pipe / 发送 SIGTERM
- 等待有限 grace period
- 必要时 SIGKILL
- 最终同步 pid registry

## 同步约束

- 本单不要求重构 shared app-server 的 `ServerManager` 启动方式；若不触碰该逻辑，可先只收口 peer supervisor。
- 若实现中发现 `ServerManager` 仍承担过多 runtime owner 责任，可在代码里预留下一步拆分点，但不在本单强行扩 scope。

## 验收标准

- `internal/provider/codexapp/module.go` 中不再有 `fx.Invoke(spawnToolbridgePeers)`
- `peer_spawn.go` 不再包含模块装配路径直启 goroutine 的逻辑
- peers 异常退出仍可按既有策略重启
- app stop 时不再发生 peer restart 竞态
- 至少补以下测试：
  - 启动后能拉起两个 peer
  - 异常退出时会重启
  - shutdown 时不会再重启
  - pid registry 在替换/退出后同步更新
