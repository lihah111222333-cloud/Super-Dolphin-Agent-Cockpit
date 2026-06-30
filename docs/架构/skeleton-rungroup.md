# skeleton-rungroup.md — platform runner 运行期编排

> **当前实现**: `internal/platform/runner`
> **定位**: V3 的长跑组件运行期托管层
> **入口事实**: `cmd/agent-terminal` 通过 `internal/app.BindRuntime` 启动 runners；`cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 也复用 `platformrunner.RunGroup`。

---

## 0. 一句话定位

platform runner = V3 的运行期引擎。Fx 负责构造对象和收集 `group:"runners"`，`internal/platform/runner.RunGroup` 负责并发启动、统一取消、信号处理和错误传播。

历史文档名保留 `rungroup`，但当前源码事实不是直接把 `github.com/oklog/run` 暴露给业务模块；业务模块只看到 `platformrunner.Runner`。

---

## 1. Runner 契约

```go
// internal/platform/runner/group.go
type Runner interface {
    Run(ctx context.Context) error
}

type GroupOptions struct {
    EnableSignals bool
}
```

规则：

- `Run(ctx)` 必须在 `ctx.Done()` 后尽快返回。
- runner 不能吞掉根因错误；返回错误会触发其他 runner 统一取消。
- 没有 runner 是配置错误，`RunGroup` 会 fail-fast 返回 `no runners registered`。
- panic 会在 `runOne` 中转成错误并向上返回，不会静默丢失。

---

## 2. RunGroup 语义

```go
err := platformrunner.RunGroup(ctx, runners, platformrunner.GroupOptions{
    EnableSignals: false,
})
```

运行语义：

1. 为每个 runner 通过 `safego.Go` 启动受保护 goroutine。
2. 任意 runner 返回、父 context 取消或信号触发时，取消根 context。
3. 等待全部 runner 返回。
4. 返回第一个非取消根因；只有主动取消时才返回 `context.Canceled`。

`EnableSignals=true` 时，RunGroup 会监听 `SIGINT/SIGTERM`，并把信号转成统一取消事件。桌面主进程的 `internal/app.BindRuntime` 当前传 `EnableSignals=false`，由上层生命周期统一收尾；sidecar 可按自身入口决定是否启用。

---

## 3. Fx 集成

```go
// internal/app/runner.go
type RunnerResult struct {
    fx.Out
    Runner platformrunner.Runner `group:"runners"`
}

// internal/app/modules.go
func AsRPCRunner(server *rpc.Server) RunnerResult {
    return RunnerResult{Runner: server}
}
```

典型接线：

- `internal/platform/rpc.Server` 由 `AsRPCRunner` 接入 `group:"runners"`。
- 后台清理、push worker、cache keepalive、toolbridge proxy 等长跑组件也通过 `group:"runners"` 收集。
- `internal/app.BindRuntime` 在 Fx `OnStart` 中启动 RunGroup，在 `OnStop` 中取消、等待 runner 退出，并 drain 内存提取等收尾任务。

---

## 4. 当前入口

| 入口 | 当前运行期接线 |
|---|---|
| `cmd/agent-terminal` | `internal/app.Run` / `RunDesktop` 创建 root context，Fx 装配 `app.Module`，`BindRuntime` 启动 `platformrunner.RunGroup` |
| `cmd/mcp-orch` | standalone Fx 图在 `runtime.go` 中收集 runners 并调用 `platformrunner.RunGroup` |
| `cmd/mcp-lsp` | `fx.go` 中的 runtime binding 调用 `platformrunner.RunGroup` |
| `cmd/mcp-ida` | `fx.go` 中的 runtime binding 调用 `platformrunner.RunGroup` |

桌面态不内嵌 `cmd/mcp-orch/orchestration.Module`。DAG/agent orchestration 由独立 `mcp-orch` MCP 服务承载，避免桌面二进制被 launcher 当作子进程重复拉起。

---

## 5. 退出与错误传播

```text
runner A 返回 error
  -> RunGroup cancel root context
  -> runner B/C 观察 ctx.Done() 并退出
  -> preferRunGroupError 保留 A 的真实错误
  -> app.reportRuntimeExit 通知桌面生命周期
```

`internal/app.BindRuntime` 的 OnStop 顺序：

1. 取消 runtime context。
2. `waitForRuntimeDone` 等待 RunGroup 完全退出。
3. `drainRuntimeBeforeStop` 执行内存提取等 pre-drain。
4. 主动取消只视为正常停止；非取消错误向 Fx 返回并记录。

---

## 6. 测试范式

最低验证：

```bash
./scripts/test_with_guard.sh ./internal/platform/runner ./internal/app -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

代表测试：

- `internal/platform/runner/group_test.go`：runner 错误优先、取消传播。
- `internal/app/runner_test.go`：Fx runtime 等待 RunGroup 后再 drain。
- `internal/archtest/runner_actor_guard_test.go`：长跑 actor 不绕过 runner 约束。

---

## 7. 禁止行为

| 规则 | 原因 |
|---|---|
| 不在业务 constructor / `fx.Provide` 中启动长跑 goroutine | constructor 只创建对象，运行期交给 runner |
| 不让 runner 忽略 `ctx.Done()` | 会阻塞统一停止 |
| 不吞掉 runner 错误 | 错误是触发整体取消和桌面故障提示的根因 |
| 不手写全局启动列表 | Fx `group:"runners"` 是唯一聚合面 |
| 不把 `cmd/mcp-orch` orchestration 嵌入桌面 app 图 | orchestration 是独立 MCP sidecar |
