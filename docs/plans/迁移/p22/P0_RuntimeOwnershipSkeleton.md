# P0: 公共运行时骨架与守卫

## 目标

先统一 P22 全批修复的术语、整改模板和守卫口径，避免每个子计划各自解释 `fx / bus / run.Group` 的边界。

## 现状校准

当前 findings 虽然落在不同目录，但退化模式高度重复：

- `fx.Invoke(...)` 里直接启动后台流程或做 post-construction mutation
- `fx.Lifecycle.OnStart` 里直接 `go ticker/watcher/sweeper/cleanup loop`
- bus 订阅回调里直接拉 session/watcher/scheduler，甚至 `Sleep` 后重试
- `Runner.Run(ctx)` 已经是 actor，却又在 execute 路径再 fire-and-forget goroutine

如果不先统一骨架，后续很容易出现“把违规从 A 包挪到 B 包”的伪修复。

## 目标架构模板

### 1. Root Runtime Bridge

允许根进程边界存在一个唯一桥接点，把 `group:"runners"` 汇总后交给 `platform/runner.RunGroup(...)`。该桥接点只允许出现在 app/cmd 入口，不允许下沉到模块包。

### 2. Module Runner

凡是具备以下特征之一的逻辑，都必须拥有显式 `Runner`：

- 自身会阻塞等待直到 stop
- 内部带 ticker / retry / reconnect / sweep loop
- 启动后会长期持有进程、socket、watcher、lease、worker queue

推荐模板：

```go
type MyRunner struct { ... }

func NewMyRunner(...) platformrunner.Runner { ... }

func (r *MyRunner) Run(ctx context.Context) error {
    // 阻塞直到 ctx.Done() 或 fatal error
}
```

通过 `fx.Annotate(..., fx.ResultTags(\`group:"runners"\`))` 输出，不再从 `Invoke`/`OnStart` 启动。

### 3. Startup Recovery

一次性恢复仍允许保留在 lifecycle：

- 读取 store 恢复内存态
- 重新注册现有 active connection 的 in-memory map
- 做一次 graph/manifest/config 校验

但恢复之后的长期 cleanup/reconcile/sweep，必须移交给 `Runner`。

### 4. Bus Queue Worker

bus 回调只做：

- 轻量、同步、可快速返回的内存更新
- `select { case ch <- cmd: default: ... }` 这类非阻塞 enqueue

watcher/scheduler/session/process 的真正启动，必须在 dedicated worker/runner 中消费这些 command。

## 守卫改动建议

### 守卫 1：`fx.Invoke` 用途守卫

在 `internal/archtest` 增加规则：

- 禁止 `fx.Invoke` 目标函数里出现 `exec.Command`、`go `、`time.NewTicker`、`time.Sleep`
- 禁止 `Invoke` 仅用于 `bindXxx`/`setXxx` 的后置 mutation 注入
- 允许名单仅保留：handler 注册、subscriber 注册、runner 注册、restore、validate

### 守卫 2：`OnStart` 长跑守卫

禁止模块级 `OnStart` 直接启动长期 goroutine。允许名单仅保留根 runtime bridge 文件，例如：

- `internal/app/runner.go`
- `cmd/mcp-orch/runtime.go`
- `cmd/mcp-lsp/fx.go`
- `cmd/mcp-ida/fx.go`

> 这些属于进程边界桥，不属于模块内部 runtime owner。

### 守卫 3：bus 回调慢路径守卫

对 `bus.ResilientSubscribe(...)` 的回调闭包增加 grep/archtest 规则，默认禁止：

- `go `
- `time.Sleep`
- `exec.Command`
- `StartSession`
- `Start()`
- `Pull()/Push()` 这类明显慢路径

若确需例外，必须先出回调再由 queue/runner 消费。

### 守卫 4：actor execute 脱管 goroutine 守卫

对实现 `Run(ctx)` 的类型增加 review/archtest 约束：

- `Run(ctx)` 内默认禁止 `go `
- 若因资源层技术原因必须起辅助 goroutine，必须满足“同 owner 可等待、可 cancel、可 join”，并写进 docstring/测试

## 推荐验证集

- `go test ./internal/archtest/...`
- 针对每个新 runner 的单测：`ctx cancel`、fatal error、double stop
- goroutine 泄漏检测或 waitgroup/drain 断言
- 进程类 owner 的僵尸进程回收断言

## 完成定义

- P22 的后续子计划统一复用本页口径，不再各写一套“什么算 Runner”
- 至少补 2 条自动守卫：`fx.Invoke/OnStart` 一条，bus 回调/actor execute 一条
- README 中的四个子计划都能映射到本页的统一模板
