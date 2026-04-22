# P0: 公共运行时骨架与守卫

## 目标

先统一 `P22` 的契约角色与守卫边界：`fx.Module` 只做构造 / lifecycle / 一次性恢复，`BusModule` 只负责 subscriber wiring，`RunnerModule` 负责长期 owner；`root runtime bridge` 仅指进程入口处把当前 runner 集合桥接到 `platformrunner.RunGroup(...)` 的桥形态。这样各子计划就不再各自解释 `fx / bus / run.Group` 的边界。

## 现状校准

当前 findings 虽然落在不同目录，但退化模式高度重复：

- `fx.Invoke(...)` 里直接启动后台流程或做 post-construction mutation
- `fx.Lifecycle.OnStart` 里直接 `go ticker/watcher/sweeper/cleanup loop`
- bus 订阅回调里直接拉 session/watcher/scheduler，甚至 `Sleep` 后重试
- `Runner.Run(ctx)` 已经是 actor，却又在 execute 路径再 fire-and-forget goroutine

如果不先统一骨架，后续很容易出现“把违规从 A 包挪到 B 包”的伪修复。

## 目标架构模板

### 1. Root Runtime Bridge

允许根进程边界存在一个唯一桥接点，把 `group:"runners"` 汇总后交给 `platform/runner.RunGroup(...)`。该桥接点属于进程入口 bridge 形态；允许的现状实现是 `BindRuntime/bindRuntime` 这类入口 wiring，不按“整个文件”做豁免。

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

## 收口口径

- 本页关于 `Invoke` 的依据以 `docs/契约/modularity-convention.md §4.4 / §7` 为准；`docs/契约/fx-convention.md §2/§3` 只作为工厂职责补充。
- 文中优先使用 `fx.Module` / `BusModule` / `RunnerModule` 术语；当前 root bridge 用到的 `group:"runners"` 仅视为现实现 tag，不把命名清洗当作 `P0` 前置条件。
- `internal/app` 与 `cmd/mcp-orch` 适用双树同构；`cmd/mcp-lsp` / `cmd/mcp-ida` 按 runner-only sidecar 收口。runner-only sidecar 的最小标准是：拥有独立 fx root、root runtime bridge 与 runner 集合；若未实际 wiring dispatcher，就不强补 `BusModule`。
- root bridge allowlist 必须至少同时记录 **file path + symbol + bridge shape**，不接受只按 `BindRuntime/bindRuntime` 名称或整文件放行。
- 豁免的是 root bridge wiring 本身，不豁免 lifecycle callback / helper / subscriber callback 的 runtime ownership；“hook 只是注册订阅”不能成为 helper 内再起 watcher / fanout / reconnect 的挡箭牌。
- bus unsubscribe 只代表停止接收新事件，不等于 drain；drain 必须由显式 owner / worker / runner 证明。
- runtime 守卫的 allowlist / exception 应按语义形态管理，不塞进 code-size freeze 这类数值模型里。
- 静态守卫默认至少做到一跳 helper/caller 解析：`OnStart -> helper -> go/SafeGo`、`callback -> helper -> slow I/O`、`Run(ctx) -> helper -> waiter goroutine` 都不能靠浅 grep 漏放。
- `P22` 新守卫优先落到独立的 `*_guard_test.go` / `*_lifecycle_guard_test.go` 一类文件；不把 runtime 规则硬塞进现有 size/freeze/import-direction 测试壳里，避免语义混层。

## 守卫改动建议

### 守卫 1：`fx.Invoke` 用途守卫

在 `internal/archtest` 增加规则：

- 禁止 `fx.Invoke` 目标函数里出现 `exec.Command` / `exec.CommandContext`、`go `、`runtimesafe.SafeGo(...)`、`time.NewTicker`、`time.Sleep`
- 禁止 `Invoke` 仅用于 `bindXxx`/`setXxx` 的后置 mutation 注入
- 允许名单仅保留：handler 注册、subscriber 注册、runner 注册、restore、validate

### 守卫 2：`OnStart` 长跑守卫

禁止模块级 `OnStart` 直接启动长期 goroutine。允许名单仅保留根 runtime bridge 文件，例如：

- `internal/app/runner.go`
- `cmd/mcp-orch/runtime.go`
- `cmd/mcp-lsp/fx.go`
- `cmd/mcp-ida/fx.go`

> 这些只是当前承载 root bridge 的文件。真正的豁免单位应收窄到 `BindRuntime/bindRuntime` 这类“汇总 runners 后调用 RunGroup”的入口桥形态，而不是文件内所有 `OnStart`。

补充口径：

- 允许名单只豁免“汇总 `group:"runners"` 后调用 `platformrunner.RunGroup(...)` 的根桥”。
- 不豁免模块级 `OnStart -> go ticker/watcher/sweeper/reconnect loop`。
- 不豁免“模块内部自己再实现一个小型 runtime bridge”的变体。
- root bridge 内允许的附属动作仅限退出日志、失败通知、`Shutdown()`、`OnStop` cancel/join/drain；其它 watcher/supervisor/ticker 仍按普通业务 long-running 处理。

### 守卫 3：bus 回调慢路径守卫

对 `bus.ResilientSubscribe(...)` 的回调闭包增加 grep/archtest 规则，默认禁止：

- `go `
- `runtimesafe.SafeGo(...)`
- `time.Sleep`
- `exec.Command`
- `StartSession` / `StopSession`
- `NotifyConfigChanged(...)`
- `DispatchAfter(...)`
- `Pull()/Push()` 这类明显慢路径

若确需例外，必须先出回调再由 queue/runner 消费；对“重 I/O”这类开放集合，默认采用 AST denylist + grep 补位，而不是宣称单条纯 AST 规则能完美覆盖。

补充口径：

- 像 `StartSession/StopSession`、`Schedule/Recover/Resume` 这类会触发 watcher/process/session lifecycle 的调用，也应默认视为慢路径。
- 允许的回调形态是“轻量状态更新 + non-blocking enqueue”；默认不允许回调里直接持有 runtime ownership。
- `thread/module.go` 这类通过 `fx.Invoke(registerSubscriptions)` 再调用 `bindDispatcher` / `bindPromptStore` 的后置 setter 注入，也应视为 post-construction mutation 示例，不因为“发生在 subscriber wiring 旁边”就自动豁免。
- `platform/hooks/event_relay.go` 这类 callback 里直接 fanout peer callback / escalate 持久化 / fire-and-forget `go` 的模式，也属于首批禁止样本；不能因为它叫“relay”就被当成轻量转发豁免。
- `platform/toolbridge/module.go` 这类通过 `fx.Invoke(...)` 调 `SetToolHandler/SetListTools` 做 late setter 注入的写法，也属于首批禁止样本；不能因为它是在“平台适配层”就被当成 wiring 豁免。

### 守卫 4：actor execute 脱管 goroutine 守卫

对实现 `Run(ctx)` 的类型增加 review/archtest 约束：

- `Run(ctx)` 内默认禁止 `go `
- 若因资源层技术原因必须起辅助 goroutine，必须满足“同 owner 可等待、可 cancel、可 join”，并写进 docstring/测试

补充口径：

- 辅助 goroutine 不能直接旁路写业务状态；最终的业务 side effect 仍应汇回 actor 主循环或统一 owner。
- `execute` 路径若需要观察进程/连接退出，优先暴露只读事件流或 `Done()/Err()` contract，而不是在 actor 里为每个对象现起 waiter。

## 非目标

- `P0` 不直接改写各子计划的业务语义，只冻结“谁能启动、谁能停止、谁负责 drain”的公共口径。
- `P0` 不把全仓 group tag / 契约命名一次性清洗完；命名冲突先在 `P22` 文档里显式标注，后续另行统一。
- `P0` 不代替 `P4` 处理 dependency direction / hidden contract；若某条违规同时含 late setter / protocol shell，只先锁 runtime ownership 侧的禁止边界。
- `P0` 不把 runtime allowlist 误写成 numeric freeze；若需要阶段性语义白名单，应另建 semantic allowlist，而不是复用 `freeze_registry`。

## 推荐验证集

- `go test ./internal/archtest/...`
- `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v` 只校验 size/freeze；**PASS 不代表 runtime 守卫已落地**
- 针对每个新 runner 的单测：`ctx cancel`、fatal error、double stop
- goroutine 泄漏检测或 waitgroup/drain 断言
- 进程类 owner 的僵尸进程回收断言

## TDD 与清理要求

- 先补会失败的 archtest/grep 守卫，再做实现修复；不要先改代码再补守卫。
- runtime 守卫优先新增独立 `*_guard_test.go`（如 `fx_invoke_guard_test.go`、`lifecycle_onstart_guard_test.go`、`runner_actor_guard_test.go`、`bus_callback_guard_test.go`），不把 runtime 规则硬塞进现有 size/import/timeout 测试文件里混写。
- 守卫落地后，要反向删除旧的 allow-by-default 写法；不能让新旧规则同时放行。
- 若某条守卫需要阶段性 allowlist，必须在文档和测试里写明删除时点；默认不接受永久例外。
- P0 完成标准不仅是“有新规则”，还包括“旧的宽松路径已被收紧”，避免守卫变成只提醒不拦截。

## 完成定义

- P22 的后续子计划统一复用本页口径，不再各写一套“什么算 Runner”
- 至少补 2 条自动守卫：`fx.Invoke/OnStart` 一条，bus 回调/actor execute 一条
- README 中的 `P1a-P4` 子计划都能映射到本页的统一模板
- root bridge allowlist 必须明确标注为永久架构例外还是阶段性守卫例外；当前口径应视为进程入口永久例外，而不是待删除临时白名单
