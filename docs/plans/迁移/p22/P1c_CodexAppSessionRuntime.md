# P1c: CodexApp session runtime 收口

## 目标

把 `internal/provider/codexapp` 中 peer supervisor 之外的 session 级长跑 runtime 单独收口，避免 `P1a` 完成后误判为 codexapp 全部 runtime ownership 已达标。本页默认把“每个 session 自持一个显式 `SessionRuntime` handle”作为首选方案，让 `fx.Module` 只保留 wiring，而把长期 owner 收回到 session 自身的局部 `RunnerModule`；本页不额外引入新的 `BusModule` 角色，只处理 session 内部 owner 分层。

## 覆盖问题

- `newSession()` 返回前启动 `startReadLoop()` / `startHealthLoop()`
- `startReadLoop()` 裸 `go s.runReadLoop(...)`
- `handleConnectionDead()` 使用 `SafeGo(context.Background(), ...)` fire-and-forget `attemptRecovery`
- recovery worker 没有 coalescing / owner / shutdown drain

## 目标架构

- session runtime 必须由显式 owner 持有，例如 `SessionRuntime` 或 provider-level session runner
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

## 收口口径

- 一条 session 只有一个 runtime handle；reader / health / recovery 共用同一 session ctx、同一 stop gate、同一 drain 入口。
- `StartSession(...)` / `ResumeSession(...)` 是默认的生产启动点；其它路径只允许发信号，不允许绕过 owner 直接把 runtime 拉起来。
- `P1c` 只处理 session runtime；peer supervisor、shared app-server、root runtime bridge 都不在本页重复记账。
- `connection.dead` 的抖动处理必须是 coalesce，不是“来一个事件起一个 worker”；close 之后不得再派生新的 recovery。
- 若 runtime 尚未启动，恢复信号只能合并或静默跳过，不能临时发明第二个 background owner。
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

- 若 session runtime 尚未启动，来自 event / transport / health 的 recovery signal 只能合并、静默跳过或进入同一 owner 的待处理状态；不能临时发明第二个 background owner。
- recovery 成功后的恢复顺序必须在实现前写死：至少要明确 reader/health 恢复、session resume、以及任何 pending replay / turn settle 动作的先后关系，避免不同入口各自重放。
- `Close()` / `ForceStop()` 期间若仍有 in-flight recovery 或 health signal 到达，必须统一落入 stop gate：要么被合并成 no-op，要么被显式丢弃；不能在 stop 之后再补派新 worker。
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
- 修复后删除旧 `SafeGo(context.Background(), ...)` recovery 路径
- 修复后删除裸 `go s.runReadLoop(...)` 旁路，或降为 owner 内部可 join 原语

## 验收标准

- session reader / health / recovery 都有明确 owner
- shutdown 时能 cancel + join/drain
- recovery worker 有 coalescing 与 stop gate
- `P1a` 只闭环 peer supervisor，`P1c` 闭环 session runtime，二者验收互不替代
