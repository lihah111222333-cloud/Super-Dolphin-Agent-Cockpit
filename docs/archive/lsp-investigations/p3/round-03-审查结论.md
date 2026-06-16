# 第 03 轮审查结论

## 审查范围

- `internal/platform/runner/contract.go`（Runner/Worker 接口、AsRunner 包装）
- `internal/platform/runner/group.go`（RunGroup 主调度、信号处理、runOne panic recovery）
- `internal/platform/runner/module.go`（fx 模块装配）
- `internal/platform/runtimesafe/safego.go`（SafeGo 全局 panic-safe goroutine 启动器）
- `internal/platform/runtimeenv/runtimeenv.go`（packaged/sidecar runtime env 装载、LSP bundle 加载）
- `internal/platform/runtimeenv/runtime_resolution.go`（runtime 模式解析、manifest 校验）

> 与第 01-02 轮已覆盖的 `cmd/mcp-lsp/`、`mcpserver/common/`、`bootstrap/` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `runner/group.go:32-35` `RunGroup` | 弱契约 | `len(runners) == 0` 返回 `errors.New("no runners registered")` | 0 个 runner 是装配 bug，不应当 runtime 报错；应在 fx providers 阶段就 panic | 改为 `panic("RunGroup called with empty runners")` 或在 fx graph 内拒绝注入空 group |
| `runner/group.go:43-46` 安全启动 | 静默 | `safego.Go` 的 panic recover 路径不会把 panic 错误塞回 `resultCh`，主循环只能等所有 runner 数完才退 | 单个 runner panic 后 goroutine 提前退出，但 `remaining--` 永远等不到那个 panic 的回执，最终循环卡死等其他 runner 也结束 | `safego.Go` 内的 fn 包一层 deferred recover，把 panic err 也写进 `resultCh`；或改用 `runOne`-内 recover（已有），不再走 safego 的 recover |
| `runner/group.go:53-68` 错误聚合 | 兜底 | `preferRunGroupError` 只保留第一个非 cancel 错误，**丢弃其余错误** | 多 runner 同时失败时仅看到首个错误，故障定位困难 | 用 `errors.Join` 聚合所有非 cancel 错误，或用 `multierror` |
| `runner/group.go:62-66` 信号路径 | 静默 | 收到信号写入 `firstErr`、`signalCh = nil` 但 **不 break 循环**；继续等所有 runner 优雅退出 | 信号触发后没有任何 timeout，runner 若卡住，进程拒绝退出 | 加 `signalDeadline := time.NewTimer(graceTimeout)`；超时强制 `os.Exit(2)` |
| `runner/group.go:97-105` `preferRunGroupError` | 兜底 | 当前 err 是 `context.Canceled` 时优先用新 err；但没考虑 `wrapped(context.Canceled)` | `errors.Is(current, context.Canceled)` 可识别 wrap，但若 wrap 链很深，性能/可读性都差 | 用专门的 sentinel；或改为简单的 nil-only 优先 |
| `runner/contract.go:47-58` `workerRunner.Run` | 兜底 | `r == nil \|\| r.worker == nil` 时返回 error 而非 panic | nil receiver 是调用方 bug；当作 runtime error 等于鼓励 nil 装配 | nil receiver/worker 直接 panic |
| `runner/contract.go:53-57` Stop 错误处理 | 兜底 | `if err := worker.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) { return err }` | "ctx canceled 时 Stop 返回 canceled" 是常见模式；这里把它转成 nil 是合理的，但**Stop 自身的非 canceled 错误被原样返回**，问题在 ctx canceled 路径里 Stop 失败后仍可能漏报"实际清理失败"——例如 Stop 返回 `wrapped(context.Canceled, ", but flush failed: ...")` | 仅在 `errors.Is(err, context.Canceled)` 且 ctx 也已 Done 时才视作正常退出；其它情况都返回 |
| `runner/contract.go:60-63` `closeOnce` | 静默 | `defer func() { _ = recover() }()` 完全空 recover，吞 close panic | `close(ch)` 重复调用会 panic；这里静默吞掉，等于把"重复 close"当合法 | 用 `sync.Once` 替代；或保留 recover 但 log 一行 warning（实际不应该走到） |
| `runner/contract.go:61` `closeOnce` 入参 | 弱契约 | 入参 nil chan 时 `close(nil)` 会 panic 然后被吞 | 同上 | 入口处 `if ch == nil { return }` |
| `runtimesafe/safego.go:25-28` `SafeGo` | 静默 | `if fn == nil { return }` 直接静默忽略 nil fn | 调用方传 nil fn 是 bug；这里完全无声 | nil fn 直接 panic |
| `runtimesafe/safego.go:32-50` panic recover | 静默 | recover 后**只 log，不 metrics、不通知调用方** | goroutine panic 静悄悄消失；调用方无法判断 fn 是否真的执行完 | 至少：①增加 metrics counter（按 label 聚合）；②保留 done channel 让调用方可选 wait；③结构化日志固定 `event` 字段方便仪表盘聚合 |
| `runtimesafe/safego.go:35-48` logger nil 兜底 | 兜底 | `if logger != nil` 用入参，否则用全局 | 调用方传 nil logger 是签名"可选"的隐式默认；与 fail-fast 冲突 | 要么强制非 nil，要么在签名上明示 `*Logger` 是 optional 并默认到 global |
| `runtimeenv/runtimeenv.go:84-112` `ConfigurePackagedApp` | 兜底 | `if resolved.RuntimeMode != RuntimeModePackaged \|\| resolved.PackagedRuntime == nil { return nil }` | 调用方意图是"配置 packaged app"，结果不是 packaged 模式就静默 return nil，掩盖装配错误 | 不是 packaged 就返回 error 让上游决策 |
| `runtimeenv/runtimeenv.go:201-212` `LoadLSPBundleFromEnv` | 兜底 | `bundleDir == "" && manifestPath == ""` 时返回 `(LSPBundle{}, false, nil)` | 该函数有三态返回 `(bundle, packaged, err)`，调用方必须三个都判断；漏判一个就会拿到空 bundle 当合法 | 拆成两个函数：`HasPackagedLSPBundle()` 和 `LoadPackagedLSPBundle()`，避免 boolean+error 混合契约 |
| `runtimeenv/runtimeenv.go:476-481` `setIfDir` | 静默 | `os.Stat` 失败（除 not exist 外）也走 return nil 路径 | 例如权限错误、IO 错误被当成"不存在"处理，packaged 安装时 Git 路径错误悄无声息丢失 | 区分 `os.IsNotExist(err)` 与其它错误；非 NotExist 错误必须 return |
| `runtimeenv/runtimeenv.go:483-491` `setIfFile` | 静默 | 同上 | 同上 | 同上 |
| `runtimeenv/runtimeenv.go:493-498` `setEnvIfEmpty` | 兜底 | 入参 value 为空就静默不设置 | 调用方传空 value 是 bug；当作 noop 容易隐藏错配 | 入口校验 value，空就 panic 或 error |
| `runtimeenv/runtimeenv.go:434-438` `newSessionToken` | 静默 | `rand.Read` 失败 `panic("...")` 但**整个进程级错误用 panic 字符串拼接**而非 fatal log | 真出问题时 panic stack 不结构化、metrics 不上报 | 改为 `pkglogger.Fatal` 或返回 error 让调用方决定 |
| `runtimeenv/runtime_resolution.go:84-93` `resolveProcessRole` | 兜底 | `""` 当作 `Owner`；`"desktop"` 也当作 `Owner` | 角色未设置 vs 显式设 owner 含义不同；混淆装配阶段 bug 与正常默认 | 空字符串走"默认 owner"是可接受的，但 `"desktop"` 别名不应再添加；建议显式记录 deprecated alias |
| `runtimeenv/runtime_resolution.go:128-151` `resolveOwnerRuntime` | 兜底 | manifest 不存在时 fallback 到 `devOwnerRuntime()`；只有 stat 错误（非 IsNotExist）才报错 | dev/packaged 误判 → 启动后跑成 dev 但用户期望 packaged，所有 packaged-only 资源都用不上；难定位 | 至少加 `pkglogger.Warn("resolveOwnerRuntime: falling back to dev mode", ...)`；或要求显式 RuntimeMode 才放行 |
| `runtimeenv/runtime_resolution.go:139-148` 三态判定 | 兜底 | manifest 缺失 + bundled sidecar sentinel 通过 → 仍当成 packaged | 没有 manifest 但 bin 目录有 mcp-orch 等是"半装"状态；当成 packaged 会跳过后续 manifest 校验 | 缺 manifest 必须 hard error |
| `runtimeenv/runtime_resolution.go:330-337` `envEnabled` | 弱契约 | 把 `1/true/yes/on` 当 enable，其它（含 `2`、`enabled`、`y`）都当 disable | 容忍范围窄但不显式拒绝；用户拼错 `enable` 不会得到任何提示 | 未识别值返回 error，让调用方决定如何处理 |
| `runtimeenv/runtime_resolution.go:339-349` `environmentMap` | 静默 | `strings.Cut(entry, "=")` 失败时 `continue`；丢弃格式坏的 entry | 不会发生（`os.Environ()` 保证格式），但函数对外是 public，第三方传错被静默 | 至少 log；或当 export API 时返回 error |
| `runtimeenv/runtime_resolution.go:351-358` `firstNonEmpty` | 兜底 | "首个非空"模式本身就是兜底；多调用点（`runtimeenv.go:142`、`runtime_resolution.go:105/129/130/226` 等） | 与"强契约"原则冲突——必填字段应在源头校验，而非沿调用链叠 first-non-empty | 限定 `firstNonEmpty` 只用于"已知合法多源"的场景；普通必填应直接报错 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `runner/group.go:43-46` | `safego.Go` 替代直接 goroutine，panic 不会进 resultCh |
| `runner/group.go:62-66` | 收到信号但不 break 循环、无 grace timeout |
| `runner/contract.go:60-63` | `defer func() { _ = recover() }()` 空 recover |
| `runtimesafe/safego.go:32-50` | recover 后只 log，无 metrics、无回调 |
| `runtimeenv/runtimeenv.go:476-481` | `setIfDir` 把所有 stat 错误当成"不存在" |
| `runtimeenv/runtimeenv.go:483-491` | `setIfFile` 同上 |
| `runtimeenv/runtimeenv.go:493-498` | `setEnvIfEmpty` 空 value 静默 noop |
| `runtimeenv/runtimeenv.go:401-410` `runEnvSetters` | 单个 setter 失败立即返回，但**已 setEnv 的不会回滚**；半状态 |
| `runtimeenv/runtime_resolution.go:139-148` | manifest 缺失静默 fallback dev |
| `runtimeenv/runtime_resolution.go:331-337` | envEnabled 默认值 false |
| `runtimeenv/runtime_resolution.go:343` | `strings.Cut` 失败 continue |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `runner/group.go:32` `RunGroup` | runners=nil/空切片靠 runtime 判断 |
| `runner/group.go:32-33` | options 内 EnableSignals 默认值靠零值；没有显式 builder |
| `runner/contract.go:25-33` `AsRunner` | worker=nil 不在构造期校验，靠 Run 内判 |
| `runner/contract.go:39-45` `WithStartedSignal` | nil chan 静默忽略 option |
| `runner/contract.go:60` `closeOnce` | 不接受 nil chan，但靠 recover 兜底 |
| `runtimesafe/safego.go:25-31` | fn=nil 静默；ctx=nil 兜底 background；logger=nil 兜底全局；label 无校验 |
| `runtimeenv/runtimeenv.go:114-122` `ConfigureSidecarRuntime` | 没有 input 参数，直接读 `os.Environ()`；不可测、副作用全局 |
| `runtimeenv/runtimeenv.go:124-140` `ResolveSidecarRuntimeContract` | mode/resources 校验 OK，但**没校验 resources 路径是否合法/存在**；只 `filepath.Clean` |
| `runtimeenv/runtimeenv.go:201-212` | 三态返回 `(value, present, err)` 调用方易漏判一项 |
| `runtimeenv/runtimeenv.go:214-235` `LoadLSPBundle` | 入参 trimspace 后接受，依赖空字符串校验；没用类型化"非空字符串" |
| `runtimeenv/runtimeenv.go:294-310` `defaultLSPLanguages` | 未知 serverID 返回 nil 而非 error |
| `runtimeenv/runtime_resolution.go:84-93` `resolveProcessRole` | `""` 与 `"owner"` 同义；`"desktop"` 兼容别名 |
| `runtimeenv/runtime_resolution.go:128-151` `resolveOwnerRuntime` | 多分支决策无显式优先级文档；很容易回归 |
| `runtimeenv/runtime_resolution.go:339-349` `environmentMap` | 不区分"未传 env"与"传了空值" |
| `runtimeenv/runtime_resolution.go:351-358` `firstNonEmpty` | 鼓励"任意一个非空就行"的弱契约风格 |

## 修复优先级

### P0（必须本周修）
1. `RunGroup` 信号路径加 grace timeout，超时 `os.Exit`（`runner/group.go:62-66`）
2. `safego.Go` 包装的 runner panic 必须把错误传给 `resultCh`（`runner/group.go:43-46`），否则主循环死锁
3. `closeOnce` 用 `sync.Once` 替代空 recover（`runner/contract.go:60-63`）
4. `resolveOwnerRuntime` packaged-vs-dev 误判路径加 `Warn` 日志，并要求显式 RuntimeMode（`runtime_resolution.go:128-151`）
5. `setIfDir` / `setIfFile` 区分 NotExist 与其它 stat 错误（`runtimeenv.go:476-491`）

### P1（本月）
6. `RunGroup` 错误聚合改用 `errors.Join`（`group.go:53-68`）
7. `runEnvSetters` 部分失败需要回滚已设 env，避免半状态（`runtimeenv.go:401-410`）
8. `LoadLSPBundleFromEnv` 三态返回拆分为两个函数（`runtimeenv.go:201-212`）
9. `ConfigurePackagedApp` 非 packaged 模式直接 error，删除静默 nil（`runtimeenv.go:84-112`）
10. `safego.SafeGo` 增加 metrics + done channel 选项（`safego.go:32-50`）
11. `runner.AsRunner` worker=nil 改为构造期 panic（`contract.go:25-33`）

### P2（下个 sprint）
12. `firstNonEmpty` 的滥用普查；列出所有调用点逐个评估是否真的合法多源
13. `envEnabled` 未识别值返回 error（`runtime_resolution.go:330-337`）
14. `resolveProcessRole` 移除 `"desktop"` 别名（如确认无线上用户）
15. `setEnvIfEmpty` 入参空 value 改为 panic
16. `newSessionToken` 替换 panic 为 fatal log
17. `runtimeenv.ConfigureSidecarRuntime` 改为接受 input 参数，与 `ResolveSidecarRuntimeContract` 一致

## 边界条件

1. **runner 信号路径加 timeout 不能影响测试**：`group_test.go` 可能依赖"信号后等所有 runner 退出"的语义。修复时要让 grace timeout 可注入（默认值大），并保留旧行为开关。
2. **`runOne` 已有 panic recover、`safego.Go` 又有一层 recover**：当前 `runOne` 已经把 panic 转 error 写入 resultCh，所以 group.go:43-46 的 P0 可能是误报——需要 trace 一下 `safego.Go` 是不是 `internal/util/safego.Go`（旁路），不是 `runtimesafe.SafeGo`。修复前先确认两者实现：若 `internal/util/safego.Go` 实现里 panic 不通过 fn 的 deferred return 反馈，则确实有死锁风险。
3. **manifest 缺失 fallback dev 是 dev 体验关键**：日常开发时不会有 `runtime-manifest.json`，强制 packaged 会破坏 `go run ./cmd/...`。修复时只对"明确声明 packaged"的路径 hard error，dev 默认仍可走 fallback。
4. **packaged session token 用 panic**：`newSessionToken` 走 `crypto/rand.Read` 失败的概率几乎为 0；改 fatal log 与 panic 体验差不多，但 metrics/告警链路更完整。低优先级。
5. **`environmentMap` 的 `strings.Cut` 失败**：`os.Environ()` 文档保证 `KEY=VALUE` 格式，但被 fork 的子进程可能注入畸形 entry（罕见）。改为 log + continue 即可，不必 hard fail。
6. **`closeOnce` 的修复要兼容已有调用模式**：`workerRunner.Run` 中 `closeOnce(r.ready)` 是 single-use；改 `sync.Once` 后要把 once 字段挂到 workerRunner 上而不是包级变量。
7. **`safego.Go` vs `runtimesafe.SafeGo` 双实现**：本轮发现 `internal/util/safego.Go` 与 `internal/platform/runtimesafe.SafeGo` 同时存在，调用点混用。建议下一轮单独审查，确认是否需要合并。

---

下一轮范围建议：
- `internal/util/safego/`（与 runtimesafe 对照检查双实现差异）
- `internal/platform/config/`（config 加载、env 解析、默认值兜底）
- `internal/platform/toolbridge/`（tool 调用桥接）
