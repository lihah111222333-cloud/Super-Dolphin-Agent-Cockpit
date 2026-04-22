# P22 `fx / bus / run.Group` 分层纠偏总览

> 创建时间：2026-04-22 | 更新时间：2026-04-22 | 状态：**规划中**
> 当前 authoritative 文档：`README.md`、[`P0_RuntimeOwnershipSkeleton.md`](P0_RuntimeOwnershipSkeleton.md)、[`P1a_CodexAppPeerSupervisor.md`](P1a_CodexAppPeerSupervisor.md)、[`P1b_PlatformLoopRunners.md`](P1b_PlatformLoopRunners.md)、[`P1c_CodexAppSessionRuntime.md`](P1c_CodexAppSessionRuntime.md)、[`P2_BusRuntimeDecoupling.md`](P2_BusRuntimeDecoupling.md)、[`P3_OrchestrationWaiterAlignment.md`](P3_OrchestrationWaiterAlignment.md)、[`P4_DependencyDirectionAndHiddenContracts.md`](P4_DependencyDirectionAndHiddenContracts.md)
> 输入基线：2026-04-22 架构审查 findings 1-10；契约以 `docs/契约/modularity-convention.md §4.4 / §7`、`docs/契约/fx-convention.md §2 / §3`、`docs/契约/rungroup-convention.md §2 / §4` 为准

## 目标

把 `P22` 明确成一套 umbrella plan，而不是把所有 runtime / dependency 债务硬塞成一张实施单。本文统一采用契约角色术语：

- `fx.Module`：constructor、lifecycle、一次性恢复
- `BusModule`：subscriber wiring；callback 只做轻量状态更新或 non-blocking enqueue
- `RunnerModule`：长期 owner / `run.Group` actor
- `root runtime bridge`：仅指进程入口处把当前 runner 集合桥接到 `platformrunner.RunGroup(...)` 的入口

实施上分两条线：

- `P0-P3`：runtime ownership 收口
- `P4`：dependency direction / hidden contract 收口

其中 `internal/app` 与 `cmd/mcp-orch` 适用“双树同构”；`cmd/mcp-lsp` / `cmd/mcp-ida` 按 runner-only sidecar 处理，不强写成 bus 树。

## 当前基线约束

- 根进程边界允许存在一个“root runtime bridge”，例如进程入口在 `OnStart` 中拉起唯一的 `RunGroup`。这属于进程入口 bridge，不是模块级 runtime owner。
- 当前 root runtime bridge 的已知实现面是 [internal/app/runner.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/app/runner.go) 的 `BindRuntime(...)`、[cmd/mcp-orch/runtime.go](/Users/mima0000/Desktop/wj/super-agent-v3/cmd/mcp-orch/runtime.go) 的 `bindRuntime(...)`、[cmd/mcp-lsp/fx.go](/Users/mima0000/Desktop/wj/super-agent-v3/cmd/mcp-lsp/fx.go) 的 `bindRuntime(...)`、[cmd/mcp-ida/fx.go](/Users/mima0000/Desktop/wj/super-agent-v3/cmd/mcp-ida/fx.go) 的 `bindRuntime(...)`。P22 的守卫只豁免这种“汇总 `group:\"runners\"` 后调用 `platformrunner.RunGroup(...)` 的 root-entry bridge 形态”，不按整个文件豁免。
- root bridge 内只允许保留 `RunGroup` 桥接、退出日志、失败通知、`Shutdown()`、以及 `OnStop` 中的 cancel/join/drain；不允许继续在 bridge 内塞 ticker、watcher、supervisor 或业务 worker。
- [internal/app/app.go](/Users/mima0000/Desktop/wj/super-agent-v3/internal/app/app.go) 里的 `watchFXShutdown(...)` 归类为桌面线程失败通知辅助路径，不属于 root runtime bridge allowlist 本体，也不自动为其它 watcher 类代码提供豁免理由。
- 模块级 `fx.Invoke` 不得承担进程拉起、watcher 启动、ticker/sweeper/cleanup loop 启动，也不得承担 post-construction mutation 注入。
- bus 订阅回调内不得直接做 `Sleep`、`go`、进程/会话/watcher 创建、重 DB/网络 I/O；这类动作必须先出回调，再进入显式 worker/runner。
- `run.Group` actor 的 `Run(ctx)` 不得再 fire-and-forget 新的业务 goroutine；若确需辅助 goroutine，必须由明确 owner 封装并具备可等待、可回收的 stop 语义。
- 本轮不追求“顺手重构一切”；以 findings 1-10 为起点，连同 README / P2 / P4 已显式纳入同一 lane 的扩围项一起收口 ownership 退化点，并补守卫避免回归。

## 实施路线图

| 优先级 | 子计划 | 覆盖问题 | 预计工时 | 当前状态 |
|---|---|---|---|---|
| **[P0](P0_RuntimeOwnershipSkeleton.md)** | 公共运行时骨架与守卫 | 统一整改模板、allowlist、archtest/grep guard | 0.5-1 天 | 🔲 未开动 |
| **[P1a](P1a_CodexAppPeerSupervisor.md)** | CodexApp peer supervisor 收口 | Finding 1, 2 | 1-2 天 | 🔲 未开动 |
| **[P1b](P1b_PlatformLoopRunners.md)** | 平台长跑 loop 抽 Runner | Finding 3, 4 | 1 天 | 🔲 未开动 |
| **[P1c](P1c_CodexAppSessionRuntime.md)** | CodexApp session runtime 收口 | session read/health/recovery hidden runtime | 1-2 天 | 🔲 未开动 |
| **[P2](P2_BusRuntimeDecoupling.md)** | bus/runtime 解耦 | Finding 5, 6, 7, 9, 10 + thread/hooks/toolbridge/config fanout/keepalive/rpc push/memory hook/gopls/bootstrap runtime 遗留问题 | 2.5-5 天 | 🔲 未开动 |
| **[P3](P3_OrchestrationWaiterAlignment.md)** | orchestration wait/exit 归位 | Finding 8 | 1-1.5 天 | 🔲 未开动 |
| **[P4](P4_DependencyDirectionAndHiddenContracts.md)** | 依赖方向与隐藏契约收口 | `ui/wails`、`provider/claudecli`、`toolbridge`、`cmd/mcp-orch/orchestration`、`thread/turn` 的模块边界/隐藏 contract 违规 | 2-4 天 | 🔲 未开动 |

## Findings 对照表

| Finding | 归属子计划 | 说明 |
|---|---|---|
| 1 | `P1a` | `internal/provider/codexapp/module.go:35` 的 `fx.Invoke(spawnToolbridgePeers)` |
| 2 | `P1a` | `internal/provider/codexapp/peer_spawn.go:18-155` 的 peer supervisor/restart loop |
| 3 | `P1b` | `internal/platform/mcpcontrol/module.go:184-199` 的 sweeper OnStart 裸跑 |
| 4 | `P1b` | `internal/platform/rpc/module.go:149-166 + 179-197` 的 approval cleanup lifecycle 启动 |
| 5 | `P2` | `internal/module/memory/module.go:456-467` 的 TeamSync runtime ownership 落在 bus 回调 |
| 6 | `P2` | `internal/module/memory/team/team_sync_watcher.go:72-79` 的 watcher 主循环脱离 `run.Group` |
| 7 | `P2` | `internal/module/memory/auto_dream_task.go:156-178` 的 auto-dream 调度塞进事件回调 |
| 8 | `P3` | `cmd/mcp-orch/orchestration/process_lifecycle.go:220-239` 的 actor 内再起 waiter goroutine |
| 9 | `P2` | `internal/platform/toolbridge/module.go:130-159` 的 proxy `OnStart -> go ServeProxy(...)` |
| 10 | `P2` | `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339` 的 NestedRuntime tool-read 回调经 helper 同步读盘 |

## 依赖图（文本）

```text
P22A runtime ownership
P0
├─> P1a
├─> P1b
├─> P3
├─> P1c（建议在 P1a peer 边界收紧后进入代码实施）
└─> P2（umbrella；实现时拆多批，不按单 PR）
    ├─ memory/team/auto-dream/memory hooks：可先于其它 P2 子切片独立推进
    ├─ thread/cachekeepalive/session users：依赖 P1c 的 session stop/drain contract
    ├─ platform event slices（hooks/config/rpc push/eventsurface）：其中 `mcpcontrol/rpc` 子域与 P1b 共享 wiring，代码合入串行
    └─ toolbridge runtime：依赖 P1a/P1c 冻结 codexapp peer/session 侧 contract

P22B dependency / hidden contract
P4
├─ 依赖 P0 的统一术语与守卫口径
├─ thread/turn 子域：文档可先澄清，代码合入排在 P2(thread slice) 之后
├─ toolbridge 子域建议在 P2 的 proxy owner 收口后推进
└─ orchestration 子域建议在 P3 的 waiter/exit owner 收口后推进
```

> 设计与文档澄清可以并行；共享 wiring / launcher contract / protocol shell 的代码合入按串行收口。真正的并行单位是子切片，不是 `P1a-P4` 六张计划单整块平推。

## 落地顺序建议

1. 先做 `P0`：统一 `fx.Module / BusModule / RunnerModule / root runtime bridge` 口径，并把 root-bridge allowlist 收窄到“桥形态”，不按文件豁免。
2. `P1a` 与 `P1b` 可以并行；它们分别收口 codexapp peer owner 与平台长跑 loop。
3. `P1c` 紧接 `P1a`：peer supervisor 闭环后，再补 session-owned runtime；不要把二者混成“codexapp 一次收完”。
4. `P2` 作为 umbrella 分批推进：先 memory/team/auto-dream，再 thread/hooks/config/keepalive/rpc push/toolbridge runtime，`gopls/bootstrap` 作为 sidecar runtime 子批后置。
5. `P1b` 与 `P2` 只有 memory 主切片能真并行；`mcpcontrol/rpc` 同包子切片、以及 `toolbridge runtime` 需要与 `P1b` / `P1a/P1c` 串行收口。
6. `P3` 可在 `P0` 之后独立推进，但与 `P4` 的 orchestration shell / hidden contract 合入时要串行。
7. `P4` 放在 `P2/P3` 接口冻结之后，按窄守卫 + 分域收口推进；不把全仓 import 规则一把梭打开，`thread/turn` 也不再写成脱离 `P2(thread slice)` 的独立并行 lane。

## 风险矩阵（叙事）

- **crash-window / duplicate side effect**：凡是 owner 从 callback / lifecycle / helper 下沉到单一 `RunnerModule` 或 worker 的切片，都要先写清 crash-window、重放边界、幂等栅栏与 late callback 处理方式；尤其是 `P1b/P2/P3` 的 replay、exactly-once、lease/heartbeat 相关路径。
- **升级 / 回滚**：若某切片需要阶段性兼容层或双轨窗口，必须同时写出回滚触发条件、开关位置、状态回退方式与停用步骤；只有“删除条件”但没有回滚说明，不算可执行迁移计划。
- **可观测性**：队列 overflow、drop/coalesce、drain latency、degraded-path、retry/backoff、heartbeat、owner 启停都必须在实施前定义最低日志 / metric / trace 口径；不能只剩“退出日志”级别表述。
- **CI / flaky 控制**：涉及 timer / backoff / jitter / event storm / drain 的测试，默认采用 fake clock、确定性抖动、禁裸 `time.Sleep` 测试、显式 timeout 预算与 `-race` 路径，避免把不稳定测试带入 P22 主线。
- **历史教训映射**：P22 承接 `docs/会话习惯.md` 中的 §10.24 运行时 PoC、§10.27 缺失即硬报错、§10.31 只加不删；高风险判定类变更不得只靠单测绿灯签收。
- **跨文档同步**：与 `P21`、`session-summary`、signed-skill 相关的叙事变更，继续按 `H + O + M` 三独立视角复核，避免只在单份文档里看起来自洽。

## 收口口径

- `fx.Invoke` 的合法用途以 `docs/契约/modularity-convention.md §4.4` 为准；本文如提及 `fx`，只把 `docs/契约/fx-convention.md §2/§3` 当作工厂职责补充，不再沿用历史误引。
- 文中优先使用 `fx.Module` / `BusModule` / `RunnerModule` 这组角色术语；当前代码里的 `group:"runners"` 仅表示现 root bridge 的实现 tag，不把它误写成契约层最终命名。
- root runtime bridge 只豁免“进程入口处汇总当前 runner 集合并调用 `platformrunner.RunGroup(...)`”这一桥形态；`watchFXShutdown(...)`、其它 lifecycle hook、同名 `bindRuntime` helper 都不自动豁免。
- `internal/app` 与 `cmd/mcp-orch` 适用双树同构；`cmd/mcp-lsp` / `cmd/mcp-ida` 只按 runner-only sidecar 收口，不要求补 bus 树。runner-only sidecar 的最小标准是：拥有单独的 fx root、root runtime bridge 与 runner 集合；若未实际 wiring dispatcher，就不强补 `BusModule`。
- README 中写的 `ctx cancel -> run.Group -> bus stop-intake -> fx.OnStop` 是 app/orch 侧的目标语义顺序，不表示每个 root bridge 源码里都字面包含 bus 阶段：desktop 允许 bounded pre-drain，sidecar 只要求 root cancel -> wait done。
- `BusModule` callback 默认只做两类事：同步更新轻量内存状态；non-blocking enqueue。退订只表示“不再派发新事件”，**不等于 drain**；drain 由显式 owner / worker 负责。
- `RunnerModule` 负责长期 owner。若某处暂时不能直接进 `run.Group`，也必须有单一 owner 的 `Start/Stop/Drain` 语义；不允许 callback / helper 自己偷跑长跑 goroutine。
- 纯 wiring / registry registration 可继续视为模块装配动作；但只要 `Invoke` / lifecycle / callback / helper 进入 runtime ownership、late setter mutation 或业务编排，就仍算 `P22` 目标。
- `P2` 只收 runtime ownership；`toolbridge` 的依赖方向 / 协议 contract 留给 `P4`，`orchestration` 的 waiter/exit owner 留给 `P3`。
- `internal/module/thread`、`internal/platform/hooks`、`internal/platform/mcpcontrol/config_change.go`、`internal/platform/cachekeepalive/*`、`internal/platform/rpc/push.go` / `eventsurface`、`cmd/mcp-lsp/gopls`、`internal/mcpserver/common/bootstrap` 都已归入 `P2` 的 runtime lane；`P21` 递延的 signed-skill / native-skill contract 债归 `P4` 的 `provider/claudecli` 子域，并按 `H + O + M` 三独立视角复核（文档垂直 / 安全权限 / 跨文档一致）。

## 实施方式

- `P22` 默认按 umbrella 计划执行：先修文档与守卫口径，再按共享写集拆成多批实现；不把 `P1c + P2 扩围 + P4` 误写成单次 PR。
- runtime ownership 的统一模板是：bus callback 只 enqueue -> owner / worker / runner 消费 -> stop/drain 单点收口 -> 删除旧直达路径。
- dependency / hidden contract 的统一模板是：先冻结 authoritative contract，再抽 facade / contract carrier，最后删旧 shell / fallback。
- `P2` 代码实施必须按 disjoint write-set 分批；`memory/thread`、`toolbridge`、`orchestration` 这类共享 wiring 的子域不做“多人同批改同包”。
- 允许保留阶段性兼容层，但必须写清删除条件；没有删除条件的双轨视为未收口。

## 非目标

- 本轮不把契约三件套整仓重写；`P22` 只先把自身文案与契约角色术语对齐。
- 本轮不把 `cmd/mcp-lsp` / `cmd/mcp-ida` 硬写成具备 bus 树的“四树同构”。
- 本轮不把 `P2` 当成“一张单子一次做完”的 mega PR。
- 本轮不因 runtime ownership 修复顺手改业务策略；例如 peer restart policy、approval timeout、TeamSync 功能边界、auto-dream gating 规则都不在此改判。
- `P4` 不替代 `P2/P3`：它只签收 dependency direction / hidden contract，不单独签收 runtime owner 闭环。

**总计**：约 9-16.5 工程日（若多人并行，日历时间约 4-6 天）。真正的难点不在代码量，而在把“谁负责跑、谁负责停、谁只负责接线”重新钉死。
