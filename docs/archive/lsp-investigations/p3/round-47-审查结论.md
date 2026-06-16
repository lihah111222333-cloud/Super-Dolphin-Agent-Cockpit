# 第 47 轮审查结论

## 审查范围

- `cmd/mcp-orch/orchestration/exit_monitor.go`（processExitMonitor、Arm、Emit、ExitEvents、Drain、publishExit、claimFire、exitMonitorFenceKey）
- `cmd/mcp-orch/orchestration/launcher_protocol.go`（LauncherMethod* 常量、LauncherParam* 常量、LauncherResp* 常量、launcherResponseAlias、resolveLauncherThreadStartAlias）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `exit_monitor.go:112-135` publishExit | 静默丢弃 | buffer 满 + 5s 超时后 line 132-134 **丢弃 exit event** 仅打 Error 日志 | 进程退出事件被丢弃 → runnerActor 永远不知道进程已退出 → 资源泄漏（agent 永远标记为 running）| 改为 unbounded channel 或 panic（exit event 不可丢弃） |
| `exit_monitor.go:113-115` publishExit | 静默 | `agentID == "" \|\| seq == 0` 时静默 return | 空 agentID 或 seq=0 是 caller bug（Arm 时应校验），被静默吞掉 | 改 panic 或 Warn 日志 |
| `exit_monitor.go:62-79` Arm | 弱契约 | `target.cmd == nil` 时返 false；`m.closed` 时返 false | 两种 false 含义不同（nil cmd = caller bug；closed = shutdown race），但返回值不区分 | 改为 `(armed bool, reason string)` 或 typed error |
| `exit_monitor.go:95-107` Drain | 协程延迟 | `go func() { m.wg.Wait(); close(done) }()` 等待所有 cmd.Wait 完成 | 如果某个进程 hang（不退出），Drain 会等到 ctx 超时。ctx 超时后 Drain 返回但 goroutine 仍在跑 → goroutine 泄漏 | 超时后 Kill 所有 tracked 进程；或 Drain 返回后 caller 负责 Kill |
| `exit_monitor.go:73-78` Arm goroutine | 协程延迟 | `target.cmd.Wait()` 无超时——进程不退出则 goroutine 永久阻塞 | 正常情况下进程最终会退出（被 Kill 或自然退出）；但 zombie 进程场景下 Wait 永不返回 | 加 health check：定期检查进程是否仍存活；超时后 Kill + 强制 publishExit |
| `launcher_protocol.go:128-145` resolveLauncherThreadStartAlias | 静默 | 所有 alias 都为空时返 `fallback`（通常是 ""） | 注释说「callers pass "" when an empty result should be treated as a protocol error」——但 caller 需自行判断空返回值是否 error | 改为 `(string, error)` 让 caller 不需要记住 fallback="" 的约定 |
| `launcher_protocol.go:137-139` | 静默 | `source == nil` 时 `continue` | nested map 为 nil（response 无 thread 子对象）时静默跳过 | 合理（兼容旧 peer），但应加 Debug 日志 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `exit_monitor.go:73-78` Arm goroutine | cmd.Wait() 阻塞直到进程退出；zombie 进程永不返回 | 1) 加 per-process 启动时间戳；2) 定期（30s）检查 `m.fired` 中未出现的 armed 进程；3) 超 5min 未退出打 Warn |
| `exit_monitor.go:95-107` Drain | wg.Wait 阻塞直到所有 goroutine 完成 | 已有 ctx 超时保护；但超时后 goroutine 泄漏——加 metrics 计数器「leaked_exit_goroutines」 |
| `exit_monitor.go:120-134` publishExit | buffer 满时 5s 阻塞 | 已有 publishBlockTimeout 保护 + Error 日志——这是正面可观测性设计 |
| `launcher_protocol.go:128-145` resolveLauncherThreadStartAlias | 纯内存操作，无延迟 | 无需监控 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `exit_monitor.go:113-115` | agentID 空 / seq=0 静默 return |
| `exit_monitor.go:132-134` | exit event 超时后被丢弃（仅 Error 日志） |
| `exit_monitor.go:116-118` | claimFire 返 false（重复 publish）静默 return |
| `launcher_protocol.go:137-139` | source nil 静默 continue |
| `launcher_protocol.go:128-145` | 所有 alias 空时静默返 fallback |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `exit_monitor.go:62-79` Arm | 返回 bool 不区分 nil-cmd vs closed |
| `exit_monitor.go:84-86` Emit | 与 Arm 共享 fence 但无 cmd 参数——语义不同但接口相似 |
| `exit_monitor.go:50-55` newProcessExitMonitor | buffer=32 硬编码；publishBlockTimeout=5s 硬编码 |
| `launcher_protocol.go:128-145` | fallback="" 约定靠注释 |
| `launcher_protocol.go:108-120` alias 列表 | 顺序是 load-bearing 但无 archtest 钉死顺序 |

## 修复优先级

### P0（必须本周修）
1. **`exit_monitor.go:132-134` exit event 丢弃**——进程退出事件是 agent 生命周期的关键信号。丢弃后 runnerActor 永远不知道进程已退出 → agent 永远标记 running → 资源泄漏 + 状态不一致。改为 unbounded channel（exit event 数量有限，等于 agent 数量）或 panic。
2. **`exit_monitor.go:73-78` cmd.Wait 无超时**——zombie 进程场景下 goroutine 永久阻塞。虽然 Drain 有 ctx 超时保护，但超时后 goroutine 泄漏。改为：Drain 超时后 Kill 所有 tracked 进程（需维护 cmd 引用列表）。

### P1（本月）
3. `exit_monitor.go:113-115` 空 agentID/seq=0 改 Warn 日志
4. `exit_monitor.go:62-79` Arm 返回值改 typed（armed/closed/nil-cmd）
5. `launcher_protocol.go:128-145` 改 (string, error) 返回
6. `exit_monitor.go:95-107` Drain 超时后加 leaked goroutine 计数器

### P2（下个 sprint）
7. `exit_monitor.go:50-55` buffer/timeout 改为 cfg-driven
8. `launcher_protocol.go:108-120` alias 顺序加 archtest 钉死
9. `exit_monitor.go:73-78` 加 per-process health check ticker

## 边界条件

1. **`exit_monitor.go` 整体是项目内协程生命周期管理的正面案例**：exactly-once fence（claimFire）、bounded publish（buffer + timeout + Error 日志）、Drain 的 ctx 超时保护、Arm 的 closed 守卫——这些都是 production-grade 的并发设计。唯一缺陷是 exit event 丢弃路径（P0）和 zombie 进程场景（P0）。
2. **`exit_monitor.go:120-134` publishExit 的三层策略**：①try non-blocking send（line 120-124）；②buffer 满时 Warn + bounded block（line 125-131）；③超时后 Error + drop（line 132-134）。这是 backpressure 的标准三层策略。但 exit event 不同于普通消息——它是 exactly-once 的生命周期信号，丢弃意味着永久状态不一致。建议 exit event 用 unbounded channel（agent 数量有限，不会 OOM）。
3. **`exit_monitor.go:50` buffer=32 的设计取舍**：32 个 buffer 意味着最多 32 个进程可以同时退出而不阻塞。正常情况下足够（agent 数量通常 < 10）。但 batch shutdown（如 Drain 触发所有进程 Kill）时可能瞬间 32+ 个退出事件——此时 buffer 满，走 bounded block 路径。这不是 bug（bounded block 最终会成功），但增加了 shutdown 延迟。
4. **`launcher_protocol.go` 整体是项目正面案例**：明确的 protocol contract + archtest 钉死 + 注释解释 alias 顺序的 load-bearing 性质。`resolveLauncherThreadStartAlias` 的 fallback 设计让新旧 peer 兼容（旧 peer 用 camelCase，新 peer 用 snake_case）。这是 backward-compatible protocol evolution 的良好实践。
5. **`exit_monitor.go:84-86` Emit 的设计意图**：remote launcher 没有本地 cmd（进程在远端），所以不能 cmd.Wait。Emit 让 remote launcher.Stop 成功后手动触发 exit event。与 Arm 共享 fence 确保 exactly-once——如果 Arm 的 cmd.Wait 先返回，Emit 会被 fence 拒绝（no-op）。这是正确的 exactly-once 设计。
6. **`exit_monitor.go:95-107` Drain 的 goroutine 泄漏风险**：line 100 `go func() { m.wg.Wait(); close(done) }()` 本身也是一个 goroutine。如果 ctx 超时后 Drain 返回，这个 goroutine 仍在等 wg.Wait——但 wg 永远不会 Done（因为某个 cmd.Wait 永不返回）。这个 goroutine 持有 `m` 引用阻止 GC。但 `m` 本身也不大（几个 map + channel），所以内存泄漏量有限。真正的问题是 cmd.Wait goroutine 持有 cmd 引用（可能含 pipe fd），这些 fd 泄漏才是资源问题。

---

**本轮总结**：发现 2 个 P0 问题：①exit event 超时丢弃导致 agent 永远标记 running（资源泄漏 + 状态不一致）；②cmd.Wait 无超时让 zombie 进程场景下 goroutine 永久阻塞。`exit_monitor.go` 整体是协程生命周期管理的正面案例（exactly-once fence + bounded publish + Drain 超时保护），但 exit event 的不可丢弃性质要求 unbounded channel。`launcher_protocol.go` 是 backward-compatible protocol evolution 的良好实践。

**累计进度**：47 轮完成。cron `fd4b4728` 继续推进。
