# 第 40 轮审查结论

## 审查范围

- `internal/platform/runner/module.go`（runner Module fx 注册）
- `internal/platform/runner/contract.go`（Runner、NoopRunner、Worker、AsRunner、AsRunnerGroup、WithStartedSignal、workerRunner.Run、closeOnce）
- `internal/platform/runner/group.go`（RunGroup、runOne、startSignalWatcher、preferRunGroupError）
- `internal/util/safego/safego.go`（Go panic-safe 协程启动器）
- `cmd/mcp-orch/store/commandcard/module.go`、`cmd/mcp-orch/store/sharedfile/module.go`（fx wiring）
- `cmd/mcp-orch/orchestration/nodeexec/stubs.go`（HybridExecutor stub，再回访验证）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `safego.go:26-28` | 静默 | `if fn == nil { return }` | nil fn 是 caller bug，被静默吞掉；caller 期望 goroutine 已启动但实际未启动，下游等待信号时会死锁 | 改 panic 或 return error |
| `safego.go:29-31` | 静默 fallback | `ctx == nil` 时 fallback Background | 与第 28/37 轮发现的 ctx-nil-fallback 同构问题；上游 nil ctx 是 bug 被掩盖 | 改 panic |
| `safego.go:32-52` | 静默 | recover 后只 log，不通知调用方 | fire-and-forget goroutine panic 后 caller 不知道；如果 caller 在等 goroutine 的副作用（如 channel send），永久阻塞 | 加可选 `panicCh chan<- any` 或 callback；或 metrics counter 让监控可见 |
| `contract.go:47-58` workerRunner.Run | 静默 | `worker.Stop` 错误如果是 context.Canceled 静默忽略；其他错误返回 | 同时 line 51 `r.worker.Start()` 无 error return —— Start 失败被吞掉 | Worker 接口 Start 应返 error；workerRunner.Run 校验 |
| `contract.go:14-16` Worker interface | 弱契约 | `Start()` 无 error 返回；`Stop(ctx) error` 接受 ctx | Start 失败语义不可表达；只能 panic，与 safego.Go 配合时 panic 被 recover 后 caller 不知 | Start() 改为 `Start() error` |
| `contract.go:60-63` closeOnce | 静默 | `defer func() { _ = recover() }()` 吞所有 panic | close-on-closed channel 的 recover 是合理的，但同时吞掉 nil-pointer panic 等不相关 panic | 用 sync.Once，或 recover 后判断 panic 值是否「close of closed channel」 |
| `group.go:32-35` RunGroup | 弱契约 | `len(runners) == 0` 时返 error；但 `[]Runner{nil, nil}` 合法（line 40-46 不 nil-check）| nil runner 导致 runOne→runner.Run nil pointer panic，被 recover 转 error | 入口校验 `runners[i] == nil` |
| `group.go:43-46` RunGroup | 性能/泄漏 | 每个 runner 独立 goroutine + panic 由 runOne 再 recover 一次 | safego.Go 已 recover；runOne 的 recover 是双重保险但 mask 了 safego 的日志（safego 看到 runOne 已处理就不 recover） | 实际上 safego.Go line 32-52 的 recover 在 fn 内 panic 时才生效，此处 fn 是 `func(ctx) { resultCh <- runOne(...) }`——如果 runOne 自己 recover 了就走正常路径不触发 safego recover。逻辑正确但绕。建议扁平化：runner 直接 safego，去掉 runOne |
| `group.go:97-105` preferRunGroupError | 弱契约 | next != nil && (current==nil 或 current is canceled) 时用 next | 如果 first error 是 canceled、second error 是 real error，会被替换；但 third、fourth real error 会被保留 first real error —— 行为对调用方不直观 | 改用 errors.Join 收集所有 error |
| `module.go:1-9` runner.Module | 弱契约 | 仅 fx.Provide(NewContract)；NewContract 返回 zero-state Contract | 这个 Module 实际无用——RunGroup/AsRunner 都是直接调用，不依赖 fx 注入。Contract{} 是 0-byte type | 如果只是为了让 fx graph 有锚点，加注释说明；否则删除 |
| `commandcard/module.go:1-7` Module | 弱契约 | 仅 fx.Provide(NewStore) | 与 sharedfile/module.go 不对称（sharedfile 还有 ProvideReader 拆 narrow port）| commandcard 是否也应拆 Reader narrow port？看 contract.go 已有 Reader interface |
| `nodeexec/stubs.go:13-21` HybridExecutor | 静默 | `Execute` 返回 `Status: NodeStatusDone, nil`；不做任何业务 | 第 29 轮已发现：生产环境创建 hybrid 节点会被静默标记 done。回访发现仍未修复 | P0：改 panic("HybridExecutor not implemented; configure node_type=agent until F3.1") 或 return error |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `safego.go:32-52` Go | 每次启动 goroutine，无并发上限 | 加 atomic counter + 阈值告警（>1000 并发 goroutine 时 Warn） |
| `group.go:32-70` RunGroup | runners 中如果有 runner 长时间不退出（卡 IO），cancel 后 drain 可能挂住整个 RunGroup | 已通过 `for remaining := len(runners); remaining > 0` 保证全部 drain；建议加 drain timeout——超时后强制 return（接受 goroutine 泄漏作为代价） |
| `contract.go:47-58` workerRunner.Run | Start/Stop 都是同步阻塞调用 | Start 加 duration 日志；Stop > drain timeout 时打 Error |
| `safego.go:51` fn(ctx) | 单个 goroutine 执行时间不可见 | safego 内部加 duration 监控（可选 label-scoped），>10s 打 Warn |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `safego.go:26-28` | nil fn 静默 return |
| `safego.go:29-31` | nil ctx 静默 fallback Background |
| `safego.go:32-52` | recover 后只 log，不通知调用方 |
| `contract.go:47-58` | Worker.Start 失败无 error 返回（接口设计） |
| `contract.go:53-56` | Stop 返回 context.Canceled 时静默忽略 |
| `contract.go:60-63` | closeOnce recover 吞所有 panic |
| `group.go:43-46` | runner == nil 时不校验 |
| `group.go:97-105` | 多个错误时只保留首个 non-cancel |
| `nodeexec/stubs.go:17-19` | HybridExecutor 静默返 Done（第 29 轮回访仍未修复） |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `safego.go:25` | label 仅文档约定「short, stable identifier」无 enum 或正则约束 |
| `contract.go:14-16` Worker | Start 无 error，与 Stop 不对称 |
| `contract.go:35-37` AsRunnerGroup | 与 AsRunner 完全等价（`return AsRunner(worker, opts...)`），存在意义不明 |
| `contract.go:39-45` WithStartedSignal | nil ch 时不替换默认 r.ready；非 nil 时替换——调用方需理解此区分 |
| `group.go:97-105` preferRunGroupError | 错误优先级隐式 |
| `runner/module.go` | NewContract 返 zero-state；module 存在意义不明 |

## 修复优先级

### P0（必须本周修）
1. **`nodeexec/stubs.go:13-21` HybridExecutor 静默返 Done**——第 29 轮已发现，本轮回访仍未修复。生产环境创建 hybrid 节点会跳过所有逻辑直接标记 done，业务正确性问题。改为 panic 或 return error。
2. **`safego.go:26-28` nil fn 静默 return**——调用方期望 goroutine 已启动并最终触发某副作用（如 channel send、metrics inc）。nil fn 静默 return 让副作用永远不发生，下游等待会死锁。改为 panic。
3. **`contract.go:14-16` Worker.Start 无 error 返回**——Start 失败只能 panic（被 safego 接住）或 silently no-op。无法把失败传播给 RunGroup。所有 Worker 实现都受此契约限制。改为 `Start() error`（破坏性变更，需评估调用方）。

### P1（本月）
4. `safego.go:29-31` nil ctx fallback 改 panic
5. `group.go:43-46` RunGroup nil runner 校验
6. `contract.go:60-63` closeOnce 改 sync.Once
7. `group.go:97-105` preferRunGroupError 改 errors.Join
8. `safego.go:32-52` 加 panic counter（可观测性）

### P2（下个 sprint）
9. `safego.go:25` label enum / 正则约束
10. `contract.go:35-37` AsRunnerGroup 删除或与 AsRunner 区分
11. `runner/module.go` 评估删除或加意图注释
12. `safego.go` 加 goroutine 并发上限 + duration 监控

## 边界条件

1. **`safego.go` 是项目内 panic-safe goroutine 启动的唯一入口**：包注释明确「Every in-tree goroutine that is not a first-class part of the Go runtime must go through Go so panics are logged」。这是良好的强制规范——所有协程 panic 都被 recover + 结构化日志。但**审查应抽样验证项目内是否真的没有裸 `go func()`**——下轮可用 grep 检查。
2. **`safego.go:32-52` recover 后不通知调用方的设计取舍**：fire-and-forget 语义下，调用方不期望反馈。但许多实际调用并非 fire-and-forget——`group.go:43-46` 的 RunGroup 就是 fire-and-collect-result。当前设计强迫 collector 模式自己 recover（`runOne` line 73-77），双层 recover 是冗余的。建议提供 `safego.GoWithResult(ctx, label, fn) error` API，把 panic 转为 error。
3. **`contract.go:35-37` AsRunnerGroup 与 AsRunner 完全等价**：单纯 `return AsRunner(worker, opts...)`。命名暗示有 group 语义但实际没有。这是 dead code 或废弃 API 残留，应清理。
4. **`group.go:32-70` RunGroup 的 cancel 传播**：line 38 创建 `rootCtx, cancel := context.WithCancel(ctx)`，每次 result/signal/ctxDone 命中都调 cancel——但 cancel 是 idempotent 的，多次调用 OK。多个 runner 同时 fail 时会有多次 cancel 调用，无害但浪费。可以加 `var cancelOnce sync.Once`，但这是 nano-optimization，不必修。
5. **`group.go:51-67` 主循环的 channel select**：3 个 case（resultCh / signalCh / ctxDone）+ for remaining loop。signalCh 收到信号后置 nil 防止重复消费——这是良好的 channel 模式。ctxDone 同理。但 firstErr 累加用 `preferRunGroupError` 是 first-error wins 策略，与 errors.Join 的「全部收集」策略冲突。建议两种都支持（option）。
6. **`runner/module.go` Module 仅 Provide(NewContract)**：NewContract 返回 zero-state struct。这种 zero-state 在 fx 中常作为「依赖项标记」——例如某 Module 的 `fx.Invoke` 依赖 Contract，强制顺序在 Module 之后。但当前代码无 Invoke 依赖 Contract，所以 Module 实际无用。可能是为未来扩展预留——应加注释说明。
7. **commandcard vs sharedfile module 的对称性**：commandcard/module.go 仅 NewStore；sharedfile/module.go 多了 ProvideReader（narrow port）。对称性破坏：commandcard 也有 Reader interface（contract.go:9-11）但未拆出。建议补 ProvideReader 让两个 store 接口一致——便于消费方按 narrow port 注入。

---

**本轮总结**：发现 3 个 P0 问题：①HybridExecutor stub 第二次回访仍未修复（业务正确性 bug）；②safego.Go nil fn 静默 return 导致下游死锁；③Worker.Start 无 error 返回是接口设计缺陷。`safego.go` 是项目唯一的 panic-safe goroutine 入口，规范良好但应增加 GoWithResult API 让 collector 模式更顺手。`group.go` 的 first-error-wins 策略与 errors.Join 哲学冲突，建议支持两种。`AsRunnerGroup` 是 dead code 应清理。

**累计进度**：40 轮完成（含第27轮补漏）。cron `fd4b4728` 继续推进。
