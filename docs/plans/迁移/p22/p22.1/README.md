# P22.1 架构债子任务：§10.30 三层分工收口（P22 R10 deferred 遗留）

> **归属声明**：本子任务是 P22 R10 FINAL 阶段显式 deferred 的架构违规遗留，源自：
> - `docs/plans/迁移/p22/JUDGEMENT_R8_QA.md §R10.6` 代码层 deferred 债总账
> - `docs/plans/迁移/p22/JUDGEMENT_R8_QC.md §7` 契约本体 deferred 债
> - §10.30 三层分工铁律（2026-04-22 P22 R8 新教训）
>
> 不是独立新 lane，而是 P22 的子任务收口。文档按 P22 体系延续。

> 创建时间：2026-04-25 | 状态：规划中，禁止在本子任务文档阶段改代码  
> 输入：C3 §10.30 调研 11 处违例、`docs/1/会话习惯.md §10.30`、`docs/plans/迁移/p22/*`、HEAD LSP 核验  
> 关联文档：[`FINDINGS.md`](FINDINGS.md) · [`DAG.md`](DAG.md) · [`GATE_CONTRACTS.md`](GATE_CONTRACTS.md)

## 1. 目标

P22.1 的目标是把全仓 `§10.30 fx / bus / run.Group 三层分工铁律` 收口到可静态守卫、可动态验收、可持续回归的状态：

1. **BusModule 名实化**：所有 bus subscriber 注册由明确的 subscriber group / BusModule 承担；业务 module 不再在 `fx.OnStart` 或散落 lifecycle 中直接 `bus.Subscribe` / `ResilientSubscribe`。
2. **RunnerModule 名实化**：所有长跑 worker / actor 进入 `group:"runners"` 或同等 RunnerModule contract；业务 module lifecycle 不再直接 `Start/Stop` worker。
3. **fx.OnStart/Stop 不承载长跑 worker**：`fx.Module` 只做 constructor + 资源 open/close + root bridge 例外；不得把 worker owner、subscriber owner、drain owner混在 module lifecycle。
4. **shutdown 顺序对齐**：根 shutdown 顺序固定为 `ctx cancel → run.Group 全退 → bus dispatcher stop / subscriber intake stop → fx resource close`，并用 archtest 表达。

本子任务不把这些现存违例标为 P22 BLOCK。P22 R10 FINAL 已接受它们作为后续横切架构债；P22.1 是专项治理子任务。

## 2. 与 P22 的关系

- P22 R10 FINAL 已处理 Findings 1-10 的代码层主批，并将 `P0/P1a/P1b/P1c/P2/P3/P4` 收口为 READY / 持续回归状态。
- P22 的 guard 覆盖了原始 findings 与部分 owner/drain 问题，但 C3 发现的 §10.30 全仓 11 处违例已经超出 P22 gate 的语义范围：当前代码多处虽有 P22 注释或局部 worker 化，但 subscriber / worker ownership 仍停留在 module lifecycle。
- P22.1 不推翻 P22：P22 产物作为输入基线；P22.1 只补横切治理层（BusModule / RunnerModule / shutdown ordering / session-private runtime allowlist）。
- P22 `P3_OrchestrationWaiterAlignment.md` 已完成 orchestration wait/exit owner 的专项收口；P22.1 对照其模式，把同类 “owner 不在 lifecycle / callback” 约束推广到 memory、thread、rpc push、hooks、mcpcontrol、cachekeepalive、toolbridge、insight、observation 等模块。

## 3. 实施路线图（依赖顺序）

### Phase 0：BusModule subscriber group 骨架 + RunnerModule contract

- 定义 `bus.subscribers` 的统一注册 contract：subscriber 只返回 declarative registration / cancel owner，不在业务 module lifecycle 里直接订阅。
- 定义 RunnerModule contract：长跑 worker 统一包装成 `platformrunner.Runner` 或等价 actor，统一由 `group:"runners"` 收集。
- 建立 allowlist 结构：root runtime bridge 与 session-private runtime 例外只允许精确命中，不允许按整文件放行。
- 产出初版 archtest：见 [`GATE_CONTRACTS.md#testbussubscribergroup`](GATE_CONTRACTS.md#testbussubscribergroup) 与 [`GATE_CONTRACTS.md#testrunneractorownership`](GATE_CONTRACTS.md#testrunneractorownership)。

### Phase 1：Root bridge shutdown 顺序调整（`internal/app/runner.go`）

- 先修根顺序：当前 `BindRuntime.OnStop` 在 cancel 前执行 memory extraction drain，和 §10.30 的 `ctx cancel → run.Group 全退 → bus stop → fx close` 不一致。
- 明确 `watchFXShutdown` 的非 root ctx / `context.Background()` 例外边界，防止桌面辅助 watcher 继续扩大为 root runtime bridge 例外。
- Phase 1 是 Phase 2 的先决条件：若根 shutdown 顺序不冻结，后续 worker/subscriber 迁移无法证明 drain 顺序。

### Phase 2：9+ 模块 subscriber/worker 迁移

覆盖 C3 指定的横切模块：

- `memory`：`registerMemoryHooks` 内 scheduler / nested / teamSync worker 迁出 lifecycle，subscriber 进入 BusModule。
- `thread`：bus workers 从 `registerSubscriptions` lifecycle 迁入 RunnerModule；subscriptions 进入 BusModule。
- `rpc push`：`pushNotificationWorker` 进入 RunnerModule；core event pushes 进入 BusModule。
- `hooks`：`hookDispatchWorker` 进入 RunnerModule；event relay subscriptions 进入 BusModule。
- `mcpcontrol`：`configFanoutWorker` 进入 RunnerModule；config-change subscriptions 进入 BusModule。
- `cachekeepalive`：keepalive relay/timer 与 subscription 拆分为 subscriber + runner/manager drain。
- `toolbridge`：diff fallback subscriber ownership 收口；proxy runner 已有 runner 化，但 subscriber 仍散。
- `insight`：collector subscriber 从 module lifecycle 迁入 BusModule；flusher runner 保持。
- `turn/observation`：observation subscriber 从 module lifecycle 迁入 BusModule。

### Phase 3：session-private runtime 例外 archtest allowlist 精确表达

- 把 session-private runtime 例外写成最小集合：只允许明确 owner、明确 drain、明确 session lifetime 的 goroutine。
- 不把 `module.go`、`runner.go`、`app.go` 整文件加入豁免；allowlist 以函数/调用形态/owner 组合表达。
- 回收临时豁免，确保 `TestSessionPrivateRuntimeAllowlist` 可长期运行。

## 4. 依赖图

```text
Phase 0：contract + archtest skeleton
  ├─> Phase 1：root bridge shutdown ordering
  │     └─> Phase 2：module subscriber/worker migration
  │           ├─ memory slice
  │           ├─ thread + observation slice
  │           ├─ platform event fanout slice（rpc/hooks/mcpcontrol/cachekeepalive）
  │           ├─ toolbridge slice
  │           └─ insight slice
  └─> Phase 3：session-private runtime allowlist
        └─ depends on Phase 2 evidence to avoid broad allowlist
```

## 5. 并行矩阵

| Slice | 可并行性 | 共享 write-set 风险 | 建议 |
|---|---:|---|---|
| Phase 0 archtest + contract | 串行起步 | `internal/archtest/*` 与契约 helper 是公共写集 | 先单 agent 完成，冻结命名后再并行 |
| Phase 1 root bridge | 可与 Phase 3 文档/allowlist 调研并行 | `internal/app/runner.go`、`internal/app/app.go` 独占 | 代码合入排在 Phase 0 后 |
| memory | 可与 platform fanout 并行 | `internal/module/memory/*` 内部文件多，单 slice 独占 | 10-15 文件一组 |
| thread + observation | 可与 memory 并行 | `internal/module/thread/*` 与 `internal/module/turn/observation/*`；可能牵涉 turn contract | 与 P22 P4 thread/turn 边界串行确认 |
| rpc/hooks/mcpcontrol/cachekeepalive | 高并行但共享 platform bus/runner contract | `internal/platform/*` 多包，contract helper 共享 | 拆 2 组，先 rpc/hooks/mcpcontrol，后 cachekeepalive |
| toolbridge | 与 platform fanout 可并行 | `internal/platform/toolbridge/*` 与 proxy runner 写集 | 避免与 P22/P4 toolbridge hidden-contract 同时改 |
| insight | 可并行 | `internal/module/insight/*` 小写集 | 可作为 BusModule 模板样本 |
| Phase 3 allowlist | 后置收口 | `internal/archtest/*` | 依 Phase 2 最终形态精确化 |

## 6. 风险矩阵

| 风险 | 影响 | 概率 | 缓解 |
|---|---|---:|---|
| BusModule 名实化引入订阅时序变化 | 事件丢失或重复订阅 | 中 | Phase 0 定义 stop-intake / cancel 幂等 contract；每 slice 加订阅计数测试 |
| RunnerModule 迁移改变 drain 顺序 | shutdown hang 或 drop | 中高 | Phase 1 先冻结 root ordering；每 worker 增 bounded drain 测试 |
| broad allowlist 掩盖新违例 | archtest 失效 | 高 | Phase 3 禁整文件豁免，按函数和调用形态精确表达 |
| Phase 2 多模块并行冲突 | merge 冲突 / contract 漂移 | 中 | DAG 固定 write-set，公共 helper 先行，模块 slice 后行 |
| P22 已完成逻辑被误删 | 回归 | 中 | §10.31 只加不删；每 slice diff 审查 P22 注释与 tests 保留 |

## 7. 落地顺序

1. Phase 0：先落 contract + archtest skeleton，建立 BusModule/RunnerModule 命名真值。
2. Phase 1：修 root bridge shutdown 顺序与 `watchFXShutdown` 边界。
3. Phase 2a：用 `insight` 或 `turn/observation` 做最小 BusModule 模板。
4. Phase 2b：迁移 `rpc/hooks/mcpcontrol` 三个 fanout worker，复用同一 worker-as-runner pattern。
5. Phase 2c：迁移 `thread` 与 `memory`，处理复杂 drain 与订阅语义。
6. Phase 2d：迁移 `cachekeepalive`、`toolbridge`，清理零散 subscriber owner。
7. Phase 3：收紧 allowlist，删除临时 broad 豁免，跑全量 archtest。

## 8. Gate 表

| Phase | Pass 标准 | Fail 标准 | 对应 archtest |
|---|---|---|---|
| Phase 0 | BusModule/RunnerModule contract 可编译；archtest 能识别现存 11 类违例并用 TODO allowlist 明确记账 | guard 只能靠 grep 文本、或按整文件豁免 | `TestBusSubscriberGroup`、`TestRunnerActorOwnership` |
| Phase 1 | `internal/app/runner.go` root OnStop 顺序为 cancel → wait run.Group → bus stop/intake stop → fx resource close；`watchFXShutdown` 例外边界可测 | cancel 前 drain worker；`context.Background()` watcher 无边界 | `TestShutdownOrdering` |
| Phase 2 | 9+ 模块 worker 全部进入 RunnerModule；subscriber 进入 BusModule；module lifecycle 不再直接 Start/Stop worker 或 Subscribe | 任一 module lifecycle 仍直接启动长跑 worker / 直接订阅 bus | `TestBusSubscriberGroup`、`TestRunnerActorOwnership` |
| Phase 3 | session-private runtime allowlist 精确到 owner/function/shape；无整文件豁免 | broad allowlist、隐式 goroutine 无 owner/drain | `TestSessionPrivateRuntimeAllowlist` |

## 9. Findings 索引

11 条逐条证据见 [`FINDINGS.md#2-findings-逐条`](FINDINGS.md#2-findings-逐条)。子任务拆分见 [`DAG.md#2-子任务-dag`](DAG.md#2-子任务-dag)。硬契约见 [`GATE_CONTRACTS.md#1-archtest-gate-总表`](GATE_CONTRACTS.md#1-archtest-gate-总表)。
