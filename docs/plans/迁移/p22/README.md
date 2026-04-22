# P22 `fx / bus / run.Group` 分层纠偏总览

> 创建时间：2026-04-22 | 更新时间：2026-04-22 | 状态：**规划中**
> 当前 authoritative 文档：`README.md`、[`P0_RuntimeOwnershipSkeleton.md`](P0_RuntimeOwnershipSkeleton.md)、[`P1a_CodexAppPeerSupervisor.md`](P1a_CodexAppPeerSupervisor.md)、[`P1b_PlatformLoopRunners.md`](P1b_PlatformLoopRunners.md)、[`P2_BusRuntimeDecoupling.md`](P2_BusRuntimeDecoupling.md)、[`P3_OrchestrationWaiterAlignment.md`](P3_OrchestrationWaiterAlignment.md)
> 输入基线：2026-04-22 架构审查 findings 1-8；契约以 `docs/契约/modularity-convention.md §7`、`docs/契约/fx-convention.md §2 + §4.4`、`docs/契约/rungroup-convention.md §2 + §4` 为准

## 目标

把当前仓库里混入 `fx`、bus 回调和业务 service 的长跑 side effect，重新收口到明确的运行时拥有者上：

- `fx` 只负责构造、一次性接线、资源初始化/释放、启动恢复
- bus 回调只负责订阅接线、轻量状态整理、非阻塞 enqueue
- `run.Group` 负责进程级长跑 actor 和统一 stop 语义

## 当前基线约束

- 根进程边界允许存在一个“root runtime bridge”，例如 `app/cmd` 层在 `OnStart` 中拉起唯一的 `RunGroup`。这属于进程边界，不是模块级 runtime owner。
- 模块级 `fx.Invoke` 不得承担进程拉起、watcher 启动、ticker/sweeper/cleanup loop 启动，也不得承担 post-construction mutation 注入。
- bus 订阅回调内不得直接做 `Sleep`、`go`、进程/会话/watcher 创建、重 DB/网络 I/O；这类动作必须先出回调，再进入显式 worker/runner。
- `run.Group` actor 的 `Run(ctx)` 不得再 fire-and-forget 新的业务 goroutine；若确需辅助 goroutine，必须由明确 owner 封装并具备可等待、可回收的 stop 语义。
- 本轮不追求“顺手重构一切”；只修正 findings 对应的 ownership 退化点，并补守卫，避免回归。

## 实施路线图

| 优先级 | 子计划 | 覆盖问题 | 预计工时 | 当前状态 |
|---|---|---|---|---|
| **[P0](P0_RuntimeOwnershipSkeleton.md)** | 公共运行时骨架与守卫 | 统一整改模板、allowlist、archtest/grep guard | 0.5-1 天 | 🔲 未开动 |
| **[P1a](P1a_CodexAppPeerSupervisor.md)** | CodexApp peer supervisor 收口 | Finding 1, 2 | 1-2 天 | 🔲 未开动 |
| **[P1b](P1b_PlatformLoopRunners.md)** | 平台长跑 loop 抽 Runner | Finding 3, 4 | 1 天 | 🔲 未开动 |
| **[P2](P2_BusRuntimeDecoupling.md)** | bus/runtime 解耦 | Finding 5, 6, 7 | 1.5-2 天 | 🔲 未开动 |
| **[P3](P3_OrchestrationWaiterAlignment.md)** | orchestration wait/exit 归位 | Finding 8 | 1-1.5 天 | 🔲 未开动 |

## Findings 对照表

| Finding | 归属子计划 | 说明 |
|---|---|---|
| 1 | `P1a` | `internal/provider/codexapp/module.go:35` 的 `fx.Invoke(spawnToolbridgePeers)` |
| 2 | `P1a` | `internal/provider/codexapp/peer_spawn.go:18-109` 的 peer supervisor/restart loop |
| 3 | `P1b` | `internal/platform/mcpcontrol/module.go:184-197` 的 sweeper OnStart 裸跑 |
| 4 | `P1b` | `internal/platform/rpc/module.go:149-196` 的 approval cleanup lifecycle 启动 |
| 5 | `P2` | `internal/module/memory/module.go:456-466` 的 TeamSync runtime ownership 落在 bus 回调 |
| 6 | `P2` | `internal/module/memory/team/team_sync_watcher.go:72-78` 的 watcher 主循环脱离 `run.Group` |
| 7 | `P2` | `internal/module/memory/auto_dream_task.go:160-177` 的 auto-dream 调度塞进事件回调 |
| 8 | `P3` | `cmd/mcp-orch/orchestration/process_lifecycle.go:220-238` 的 actor 内再起 waiter goroutine |

## 依赖图（文本）

```text
P0
├─> P1a
├─> P1b
├─> P2
└─> P3

P1a ─┐
P1b ─┼─> 总体验收 / 守卫收口
P2  ─┤
P3  ─┘
```

> 直接依赖口径：`P0 → P1a/P1b/P2/P3`。四个子计划可以并行设计，但代码合入前要统一通过 P0 的守卫口径。

## 落地顺序建议

1. 先做 `P0`：把“允许什么、不允许什么、哪些 root bridge 可以豁免”写死；否则后续每个修复都要重新争论边界。
2. `P1a` 与 `P1b` 优先，因为它们分别修掉 `codexapp` 和平台层最硬的 runtime owner 退化点。
3. `P2` 随后处理 memory 这类 bus/runtime 混层点，避免继续沿着事件回调堆 watcher/scheduler。
4. `P3` 最后收口 orchestration wait/exit 路径，因为它可能牵涉 launcher/process handle contract 微调。

## 收口口径

- `fx.Invoke` 仅保留 handler/subscriber/runner 注册、启动恢复、一次性图校验。
- `fx.Lifecycle.OnStart` 不再直接 `go` 长跑 loop；若是长期运行，必须有 `Runner` 或等价 owner。
- bus 回调默认只能做两类事：同步更新轻量内存状态；非阻塞写入 channel/queue。
- `run.Group` 内的 actor 不再自行散射 fire-and-forget goroutine；动态等待/监控必须通过显式 monitor/handle contract 收口。
- 每个整改点都要附带最小验证：启动、停止、异常退出、重复恢复、无 goroutine/进程泄漏。

## 非目标

- 本轮不重写 `internal/app/runner.go` 这类根进程 runtime bridge。
- 本轮不顺手统一所有 provider/session 内部辅助 goroutine，只处理 findings 命中的强违规路径。
- 本轮不改变业务语义，例如 peer restart policy、approval timeout 规则、TeamSync 功能范围；只改变 ownership 与 wiring。

**总计**：约 4-6 天。真正的难点不在代码量，而在把“谁负责跑、谁负责停、谁只负责接线”重新钉死。
