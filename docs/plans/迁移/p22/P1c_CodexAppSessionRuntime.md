# P1c: CodexApp session runtime 收口

## 目标

把 `internal/provider/codexapp` 中 peer supervisor 之外的 session 级长跑 runtime 单独收口，避免 `P1a` 完成后误判为 codexapp 全部 runtime ownership 已达标。本页默认把“每个 session 自持一个显式 `SessionRuntime` handle”作为首选方案，让 `fx.Module` 只保留 wiring，而把长期 owner 收回到 session 自身的局部 `RunnerModule`；这里的 `SessionRuntime` 指 **session-private handle**，不是新的 root `group:"runners"` 成员（旧契约本体仍有 `runner.actors` 命名债，但不作为本页 active 术语），也不是 provider-level shared runner。当前 HEAD 还没有 prod `SessionRuntime` 类型 / 符号，此处只是本页的规划术语，不是对现状命名的误报。本页不额外引入新的 `BusModule` 角色，只处理 session 内部 owner 分层。

## 覆盖问题

- `newSession()` 返回前启动 `startReadLoop()` / `startHealthLoop()`
- `startReadLoop()` 裸 `go s.runReadLoop(...)`
- `handleConnectionDead()` 使用 `SafeGo(context.Background(), ...)` fire-and-forget `attemptRecovery`
- recovery worker 没有 coalescing / owner / shutdown drain
- `attemptRecovery() -> startReadLoop()` 仍是第二个隐式 reader 启动点，不能只改 constructor 起飞
- inbound `connection.dead` 恢复信号链（`transport_helpers -> session.onNotification -> session_approval -> recovery`）与 outbound failure event（`factory/event_map`）必须分开冻结，不再混成同一个 owner 语义

## 现状校准

- `internal/provider/codexapp/session.go:63-105` 的 `newSession()` 在返回前直接启动 `startReadLoop()`（101）与 `startHealthLoop()`（102），constructor 仍在隐式拉起长期 runtime
- `internal/provider/codexapp/driver.go:157-172` / `174-197` 的 `StartSession()` / `ResumeSession()` 当前只是 `newSession()` 的生产 caller，并没有显式 `Start()/Join()` handle
- `internal/provider/codexapp/module.go:24-36` 当前也只有 `fx.Provide` / `fx.Invoke` wiring；HEAD 里没有 prod `SessionRuntime` 类型，也没有 `group:"runner.actors"` 一类 session runtime producer 可直接复用
- `internal/provider/codexapp/recovery.go:358-364` 的 `startReadLoop()` 仍裸 `go s.runReadLoop(...)`；`308-321` 的 `startHealthLoop()` 仍经 `runtimesafe.SafeGo(...)` 起长期 loop
- inbound `connection.dead` 真链路当前是 `transport.go:110-117,222-238 -> transport_helpers.go:230-241 -> session.go:107-122 -> session_approval.go:222-250 -> recovery.go:102-120`；outbound failure event 另一路是 `factory.go:251-259 -> event_map.go:131-142`，两者都能汇入 `attemptRecovery(...)`
- `internal/provider/codexapp/recovery.go:122-163` 当前只有串行化，没有 coalesce；重复 `connection.dead` 仍会消耗 recovery budget
- `internal/provider/codexapp/session.go:289-295` + `internal/provider/codexapp/factory.go:216-235` 的 `Close()/ForceStop()` 目前只做 cancel / shutdownTransport，不 join reader / health / recovery
- 现有测试仍靠 helper 手动补 wait：`driver_session_test.go:296-306` 用 `waitReadLoopStopped()`，`recovery_toolbridge_test.go:98-109` 用 `stopReadLoop() + waitReadLoopStopped()`

## 目标架构

- session runtime 必须由 session-private 的显式 `SessionRuntime` handle 持有；provider-level registry 最多只作为索引层，不再作为第二套共享 runner
- reader / health / recovery 都通过同一 session ctx、同一 shutdown gate、同一 drain 入口管理
- `Close()` / `ForceStop()` 必须先阻止新 recovery，再 cancel，再 join reader/health/recovery
- `connection.dead` 抖动必须 coalesce，不能无限派生 recovery worker

## 实施方式

- 默认选型：`session` owns `SessionRuntime`。`newSession()` 只构造 session 与 runtime handle，不再隐式拉起 reader / health / recovery。
- `StartSession(...)` / `ResumeSession(...)` 负责显式 `Start()` 这个 runtime handle；这样生产 caller 与 owner 启动点保持一致，不再让 constructor 偷跑长跑逻辑。
- `handleConnectionDead(...)`、health failure、transport call failure 三类入口都只把信号交给同一 runtime owner；notification path 不再自己持有后台 goroutine。
- event / transport / health 三条来源只负责上报 runtime signal；真正的 recovery 状态机、重试节奏与 stop gate 全由 owner 内部维护。
- recovery 入口统一做 coalesce / retry / stop gate；不再在 helper 里 `SafeGo(context.Background(), ...)`，也不允许“收到事件就再起一个补偿 worker”。
- `Close()` / `ForceStop()` 统一走“禁止新 recovery -> cancel session ctx -> join reader / health / recovery -> 返回”的单一路径，不再把 join 留给测试 helper 或外部调用方补做。
- 任何现有测试/辅助关闭入口若还需要手动补 wait，应在实现完成后内联回正式 shutdown contract，而不是长期保留双轨。
- 生产代码里的 session shutdown 入口也要收束到同一个 drain ingress；不能一条路径 wait、另一条路径只 cancel。
- 若后续必须引入 provider-level session registry，也只能作为 runtime handle 的索引层；不能回退成“provider 再管一套共享 session runner”。
- recovery 成功后的唯一顺序默认沿用现状：`Reconnect -> waitReadLoopStopped -> startReadLoop -> resumeThreadAfterRecovery -> replayPendingTurn`；不同入口不再各自重放。
- runtime 尚未启动时，event / transport / health signal 统一并到同一 pending/coalesce gate；它们可以记录待处理，但不能直接补派第二个 background owner。
- `attemptRecovery() -> startReadLoop()` 与 constructor 起飞同等视为 owner 接管点；文档、测试、验收都以这两处显式启动面为基线。

## 实施步骤

### Step 1：冻结显式启动点

- 先把 `StartSession(...)` / `ResumeSession(...)`（`driver.go:157-197`）钉成唯一生产启动点
- `newSession()`（`session.go:63-105`）只构造 session 与 runtime handle，不再在 constructor 里启动 reader / health

### Step 2：收口 reader / health owner

- 把 `startReadLoop()`（`recovery.go:358-364`）与 `startHealthLoop()`（`recovery.go:308-321`）下沉到同一个 `SessionRuntime`
- 删除 `go s.runReadLoop(...)` 与 `SafeGo(... runHealthLoop ...)` 这类 owner 外旁路；保留的原语必须可 join / 可 cancel

### Step 3：统一 recovery 信号入口

- transport call failure（`recovery.go:91-100`）、`connection.dead`（`session_approval.go:242-250` -> `recovery.go:102-120`）、health failure（`recovery.go:323-344`）都只上报给同一个 recovery owner
- recovery owner 必须实现 coalesce / retry / stop gate；不能继续在 helper 里 `SafeGo(context.Background(), ...)`

### Step 4：合并 shutdown / drain

- `Close()` / `ForceStop()`（`session.go:289-295`、`factory.go:216-235`）统一走“禁止新 recovery -> cancel -> join reader/health/recovery -> 返回”
- `driver_session_test.go:296-306`、`recovery_toolbridge_test.go:98-109` 这类手动 wait helper 要内联回正式 shutdown contract，不再作为生产语义补丁

## 收口口径

- 本页继承 `docs/契约/modularity-convention.md §4.4 / §7`、`docs/契约/fx-convention.md §2 / §3`、`docs/契约/rungroup-convention.md §2 / §4`：`fx.Module` 只保留 wiring / resource boundary；session 内部长跑 owner 由局部 `RunnerModule`/runtime handle 承担，不额外发明第二棵 `BusModule`。
- 一条 session 只有一个 runtime handle；reader / health / recovery 共用同一 session ctx、同一 stop gate、同一 drain 入口。
- `StartSession(...)` / `ResumeSession(...)` 是默认的生产启动点；其它路径只允许发信号，不允许绕过 owner 直接把 runtime 拉起来。
- `P1c` 只处理 session runtime；peer supervisor、shared app-server、root runtime bridge 都不在本页重复记账。
- `connection.dead` 的抖动处理必须是 coalesce，不是“来一个事件起一个 worker”；close 之后不得再派生新的 recovery。
- 若 runtime 尚未启动，恢复信号只能并入同一 pending/coalesce gate，并折叠成单个待恢复位；不能静默跳过，更不能临时发明第二个 background owner。
- `Close()` / `ForceStop()` 返回时即视为 session runtime 完成 drain；不再要求调用方额外补等 reader 或 recovery。
- session 进入降级或 no-op 状态时，仍按单 session 局部失败处理；除非文档显式改判，不把单 session 恢复失败升级成 provider/app 级 fatal。
- 本页不把 runner group 命名清洗当成前置；若 session runtime 需要被外层索引，只允许经显式 handle/contract 接入。

## 依赖图（文本）

```text
P0 -> P1a -> P1c
P1c -> P2（thread / cachekeepalive / 其它 session-related callback owner 复用同一 stop/drain contract）
```

## 落地顺序建议

1. 先由 `P1a` 冻结 peer supervisor 与 session runtime 的边界；不要在 peer owner 尚未收口前并改两套 codexapp runtime。
2. `P1c` 第一阶段先把 `StartSession(...)` / `ResumeSession(...)` 固化成显式 runtime 启动点，再清掉 `newSession()` 的隐式起飞。
3. 第二阶段再统一 `Close()` / `ForceStop()` 的 stop/drain 语义，并把 recovery 入口汇到单一 owner。
4. 与 session runtime 强耦合的 thread / keepalive / callback slow-path 迁移，应在 `P1c` stop/drain contract 冻结后交给 `P2` 对接，不反向把这些子域并进本页。
5. 若实现中发现 `StartSession(...)` / `ResumeSession(...)` 之外仍有生产态隐式启动点，应继续补写本页，而不是默认接受第二条 session owner 旁路。

## 需冻结的兼容语义

- 若 session runtime 尚未启动，来自 event / transport / health 的 recovery signal 统一写入同一个 pending/coalesce gate，并折叠成单个待恢复位；不能静默跳过，也不能临时发明第二个 background owner。
- recovery 成功后的恢复顺序必须在实现前写死：至少要明确 reader/health 恢复、session resume、以及任何 pending replay / turn settle 动作的先后关系，避免不同入口各自重放。
- `Close()` / `ForceStop()` 期间若仍有 in-flight recovery 或 health signal 到达，统一在 stop gate 上记为 `dropped_signal` 并显式观测；不能在 stop 之后再补派新 worker，也不再以 no-op 名义偷留旁路。
- degraded / no-op session 继续按单 session 局部失败处理，不把这类兼容路径升级成 provider/app 级 fatal。

## 风险提示

- 若 `Close()` / `ForceStop()` 与 recovery signal 没有被同一 stop gate 收口，最容易出现“关闭后又补派 recovery worker”的重复副作用。
- 若 thread / keepalive / 其它 session user 在 `P1c` contract 冻结前并行改写，很容易发明第二个 session owner；因此本页完成前，外部切片只能复用这里定义的 handle / drain 语义。
- 若 reader / health / recovery 的恢复顺序没有在实现前写死，不同入口会各自重放，最终把局部兼容路径演化成新的 split-brain。
- degraded / no-op session 必须继续按局部失败处理；把这类路径升级成 provider/app-fatal，会直接改变 codexapp 的现有容错语义。
- 对新接手实现的人，最容易犯的错是“把 `newSession()` 的隐式起飞改到另一个 helper 里”；只要启动点不回到 `StartSession(...)` / `ResumeSession(...)`，就仍是伪收口。
- 另一个常见误判是“close 只 cancel，wait 留给调用方”；本页已经把 drain 责任收回生产 shutdown contract，不接受调用方补等。
- 若实现期需要临时 registry/facade 挂住 session runtime，也只能作为索引层；任何能够独立 `Start()` 第二套 runtime 的中间层都视为回归。
- 若复审时还需要靠测试 helper 证明 drain 成功，应优先回头检查正式 shutdown path，而不是继续给 helper 补能力。
- 本页默认的阅读顺序是：`目标` → `现状校准` → `实施步骤` → `需冻结的兼容语义` → `风险提示`；新实现者不要跳过中间两节直接看 TDD。
- 若后续再拆 session runtime 子文档，也要保持这个阅读顺序与章节密度，不再回退到只剩骨架标题的短稿。

## 可观测 / crash-window / 回滚约束

- 代码真值常量必须落盘：`healthCheckInterval = 15s`、`healthCheckIdleThreshold = 30s`，若迁移触达 transport ready / reconnect 超时，也同步冻结对应 timeout；这些值只能在单独改判时变更
- session runtime 至少输出四类信号：`session_runtime_state` 结构化日志、`recovery_signal_total`、`recovery_coalesced_total`、`shutdown_drain_seconds`；能区分 `start / recover / stop / drained / dropped_signal`
- 若实现期必须短暂保留旧 helper / wrapper，只能通过显式 feature flag / env opt-in 启用，默认路径仍是单一 owner；缺 session / runtime / thread 这类必需上下文时返回 `ErrSessionRuntimeRequired` 一类 sentinel，而不是临时再起第二个 background owner

### 常量矩阵（代码真值）

| 常量 | 当前真值 | owner | 锚点 |
|---|---|---|---|
| `healthCheckInterval` | `15s` | session health loop | `internal/provider/codexapp/recovery.go:26` |
| `healthCheckIdleThreshold` | `30s` | health idle 判定 | `internal/provider/codexapp/recovery.go:27` |
| `transportReadyTimeout` | `30s` | transport ready / startup | `internal/provider/codexapp/transport.go:17` |

### recovery replay 顺序（冻结口径）

1. transport / event / health 只上报 signal 到同一 owner
2. 先过 stop gate，再决定是否允许 recover
3. reconnect / ready 成功后先恢复 reader / health
4. 再恢复 pending replay / turn settle
5. 最后发出 `drained` 或 `dropped_signal` 观测信号

### 最低 observability contract

- `log`：`session_runtime.start`、`session_runtime.stop`、`session_runtime.drained`、`recovery.signal`、`recovery.dropped`
- `metric`：`recovery_signal_total`、`recovery_coalesced_total`、`shutdown_drain_seconds`、`session_runtime_degraded_total`
- `trace`：`codexapp.session_runtime`、`codexapp.recovery`
- panic/recover telemetry 仍要显式挂到 session runtime owner；不能只靠 `SafeGo` 隐式吞掉
- health / reconnect / drain 测试默认使用 fake clock、deterministic shutdown、禁裸 `time.Sleep`

## 非目标

- 不重写 peer supervisor 或 shared app-server；这些继续由 `P1a` 或更外层 wiring 负责。
- 不改判 recovery 的业务策略细节，例如是否允许恢复、如何回放、approval/transport 的具体行为；本页只改 owner、coalesce 与 stop/drain。
- 不顺手清扫其它 provider 内部 goroutine；只有与 session reader / health / recovery 直接同域的 runtime 才在本页闭环。
- 不把 transport 协议、peer 生命周期或 signed-skill/native-skill contract 混进本页；这些问题继续留给其它子计划。
- 不以“测试 helper 还能补 wait”为验收兜底；凡属正式 shutdown contract 的责任，都必须回收进生产路径。

## TDD 与旧实现清理

- 先补失败测试：`newSession()` 不再隐式起飞，或起飞必须有显式 owner handle
- 先补失败测试：`Close/ForceStop` 后不再发布新的 `recovery.attempt`
- 先补失败测试：重复 `connection.dead` 不会派生多个并发 recovery worker
- 测试名固定到可派单级别：`TestSessionRuntimeStartOwnedByStartSession`、`TestSessionRuntimeCloseDrainsReadHealthRecovery`、`TestSessionRuntimeConnectionDeadCoalescesRecovery`、`TestSessionRuntimeCloseSuppressesNewRecovery`、`TestSessionRuntimeRecoveryOrderDeterministic`、`TestSessionRuntimeUsesFakeClockForHealthIntervals`
- 验证命令固定写法：`go test ./internal/provider/codexapp/... -run 'Test(SessionRuntimeStartOwnedByStartSession|SessionRuntimeCloseDrainsReadHealthRecovery|SessionRuntimeConnectionDeadCoalescesRecovery|SessionRuntimeCloseSuppressesNewRecovery|SessionRuntimeRecoveryOrderDeterministic|SessionRuntimeUsesFakeClockForHealthIntervals)' -count=1 -v`
- 对 `newSession()` 是否隐式起飞、`Close()` 后是否再接收 recovery signal 这两类高风险判定补运行时 PoC；不能只靠 helper/mock 绿灯签收
- 修复后删除旧 `SafeGo(context.Background(), ...)` recovery 路径
- 修复后删除裸 `go s.runReadLoop(...)` 旁路，或降为 owner 内部可 join 原语

## 验收标准

- session reader / health / recovery 都有明确 owner
- `StartSession(...)` / `ResumeSession(...)` 成为唯一显式启动点，`newSession()` 不再隐式起飞
- shutdown 时能 cancel + join/drain；`Close()` / `ForceStop()` 返回时不再要求调用方手动补等
- recovery worker 有 coalescing 与 stop gate；重复 `connection.dead` 不再派生多路并发 recovery
- `handleConnectionDead(...)` / transport failure / health failure 不再各自持有后台 goroutine
- `driver_session_test.go` / `recovery_toolbridge_test.go` 不再依赖 `waitReadLoopStopped()` 之类 helper 才能证明生产 shutdown 成功
- `healthCheckInterval = 15s`、`healthCheckIdleThreshold = 30s` 与 recovery-order / drain 指标已有文档与测试护栏
- session runtime 的 crash-window 与 stop/drain 信号可观测，close 之后不再靠沉默 fallback 掩盖缺失上下文
- `P1a` 只闭环 peer supervisor，`P1c` 闭环 session runtime，二者验收互不替代
