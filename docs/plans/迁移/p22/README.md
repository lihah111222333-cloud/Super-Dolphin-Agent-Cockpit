# P22 `fx / bus / run.Group` 分层纠偏总览

> 创建时间：2026-04-22 | 更新时间：2026-04-24 | 状态：**P0/P1a/P1b/P1c/P2/P3/P4 主批收口；持续回归由 archtest 维持**
> 当前 authoritative 文档：`README.md`、[`P0_RuntimeOwnershipSkeleton.md`](P0_RuntimeOwnershipSkeleton.md)、[`P1a_CodexAppPeerSupervisor.md`](P1a_CodexAppPeerSupervisor.md)、[`P1b_PlatformLoopRunners.md`](P1b_PlatformLoopRunners.md)、[`P1c_CodexAppSessionRuntime.md`](P1c_CodexAppSessionRuntime.md)、[`P2_BusRuntimeDecoupling.md`](P2_BusRuntimeDecoupling.md)、[`P3_OrchestrationWaiterAlignment.md`](P3_OrchestrationWaiterAlignment.md)、[`P4_DependencyDirectionAndHiddenContracts.md`](P4_DependencyDirectionAndHiddenContracts.md)
> 输入基线：2026-04-22 架构审查 findings 1-10；契约以 `docs/契约/modularity-convention.md §4.4 / §7`、`docs/契约/fx-convention.md §2 / §3`、`docs/契约/rungroup-convention.md §2 / §4` 为准

## 目标

把 `P22` 明确成一套 umbrella plan，而不是把所有 runtime / dependency 债务硬塞成一张实施单。本文统一采用契约角色术语：

- `fx.Module`：constructor + 资源 lifecycle；启动期一次性恢复只允许作为单次 wiring / 合法 `fx.Invoke`（旧稿“lifecycle、一次性恢复”简写并列保留一轮，但以本句更正为准）
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
| **[P0](P0_RuntimeOwnershipSkeleton.md)** | 公共运行时骨架与守卫 | 统一整改模板、allowlist、archtest/grep guard | 0.5-1 天 | ✅ 已收口（42fef57 + 40f4677）|
| **[P1a](P1a_CodexAppPeerSupervisor.md)** | CodexApp peer supervisor 收口 | Finding 1, 2 | 1-2 天 | ✅ 已收口（42fef57，PeerSupervisor + 守卫 6 项）|
| **[P1b](P1b_PlatformLoopRunners.md)** | 平台长跑 loop 抽 Runner | Finding 3, 4 | 1 天 | ✅ 已收口（SweeperRunner + ApprovalCleanupRunner 入 group:"runners"）|
| **[P1c](P1c_CodexAppSessionRuntime.md)** | CodexApp session runtime 收口 | session read/health/recovery hidden runtime | 1-2 天 | ✅ 已收口（4dfed68 + 366c702，SessionRuntime 接管 readLoop/healthLoop）|
| **[P2](P2_BusRuntimeDecoupling.md)** | bus/runtime 解耦 | Finding 5, 6, 7, 9, 10 + thread/hooks/toolbridge/config fanout/keepalive/rpc push/memory hook/gopls/bootstrap runtime 遗留问题 | 2.5-5 天 | ✅ 已收口（Finding 5–10 + thread/hooks/keepalive/rpc push/memory + gopls-S1/S2/S3（549beba/8406372/35ce09b）+ bootstrap-S1/S2（a524f6c/9a8a609））|
| **[P3](P3_OrchestrationWaiterAlignment.md)** | orchestration wait/exit 归位 | Finding 8 | 1-1.5 天 | ✅ 已收口（processExitMonitor 单 owner + 守卫）|
| **[P4](P4_DependencyDirectionAndHiddenContracts.md)** | 依赖方向与隐藏契约收口 | `ui/wails`、`provider/claudecli`、`toolbridge`、`internal/sidecar/orch/orchestration`、`thread/turn` 的模块边界/隐藏 contract 违规 | 2-4 天 | ✅ 已收口（S1..S4c6 + S5a/S5b；最后 commit 1226cc4）|

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
| 8 | `P3` | `internal/sidecar/orch/orchestration/process_lifecycle.go:220-239` 的 actor 内再起 waiter goroutine |
| 9 | `P2` | `internal/platform/toolbridge/module.go:130-159` 的 proxy `OnStart -> go ServeProxy(...)` |
| 10 | `P2` | `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339` 的 NestedRuntime tool-read 回调经 helper 同步读盘 |

### Finding -> gate / merge gate 速查

> `[1]-[5]` 是主 critical path 的**五门 gate**；Finding 3/4/8 不强塞进五门编号，而是走 README 明示的支线 gate。这样派工时既保留“五门 gate”主线，又不会把平台 loop / orchestration waiter 漏成无门位问题。

| Finding | README gate 位 | owning subplan | 进入实现前必须冻结的 blocker | merge / test gate 仍看哪份文档 |
|---|---|---|---|---|
| 1 | `[2]` peer owner 门 | `P1a` | 删除 `fx.Invoke(spawnToolbridgePeers)` 直启；统一 peer owner | `P1a` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 2 | `[2]` peer owner 门 | `P1a` | `PeerSupervisor` 接管 spawn/restart/stop/drain | `P1a` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 3 | 支线 A：platform loop gate | `P1b` | `mcpcontrol` 产出显式 runner producer；startup restore 与 cleanup loop 分层 | `P1b` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 4 | 支线 A：platform loop gate | `P1b` | `rpc` cleanup loop runner 化；startup restore 与 connect-time replay 分计数 | `P1b` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 5 | `[4]` runtime owner 门 | `P2` | TeamSync callback 不再直拉 session / watcher | `P2` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 6 | `[4]` runtime owner 门 | `P2` | watcher owner 回到显式 coordinator / runner | `P2` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 7 | `[4]` runtime owner 门 | `P2` | auto-dream 调度从 callback 链迁出，保住 busy-drop / throttle 语义 | `P2` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 8 | 支线 B：orchestration wait gate | `P3` | local process owner / exit contract / exactly-once drain | `P3` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 9 | `[4]` runtime owner 门 | `P2` | toolbridge proxy serve/stop/drain 归单一 owner；late setter 删除 | `P2` 验收标准 + `JUDGEMENT_DYNAMIC.md` |
| 10 | `[4]` runtime owner 门（memory hooks / NestedRuntime） | `P2` | `ToolCallEnd -> AddToolReadResult -> os.ReadFile` 不再停留在 callback 慢路径 | `P2` 验收标准 + `JUDGEMENT_DYNAMIC.md` |

## archtest / 守卫现状数字（2026-04-23 实测）

- `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v`：**PASS / 0 violations**
- `go test ./internal/archtest/... -run 'TestDependencyDirection|TestTimeoutLocality' -count=1 -v`：**FAIL / 3 violation entries / 2 failing suites / 2 unique files**
  - `internal/module/memory/ui_rpc.go`：`rule2_module_impls_no_fx` + `rule10_fx_import_scope`
  - `internal/module/prompt/classifier/claude_cli.go:59`：`TestTimeoutLocality`
- `internal/archtest/freeze_registry.go:19` 当前仍是 `explicitFreezeRegistry = []explicitFreeze{}`；freeze registry 只承载 numeric freeze，不承载 root-bridge / semantic allowlist
- Findings 1-10 锚点文件当前 LoC 合计：**3152**
- ⚠️ 2026-04-25 HEAD drift：当前 `TestDependencyDirection|TestTimeoutLocality|TestCodeSizeGuard` 全部 PASS；原 3 条 live failure（历史 shorthand：`archtest 3 live failure`；`memory/ui_rpc.go` x2 + `prompt/classifier/claude_cli.go:59`）已修复（C2 独立验证 + `go test ./internal/archtest/... -count=1` PASS）。

## 依赖图（文本）

```text
P22A runtime ownership
P0（骨架 / allowlist 先行；具体 guard 随 owning slice 接入）
├─> P1a
├─> P1b
├─> P3
├─> P2（memory/team/auto-dream/memory hooks；可与 P1a/P1b/P3 并行）
├─> P4（ui/wails + claudecli；仅 contract/facade/守卫先行）
├─> P1c（必须在 P1a peer 边界冻结后进入）
└─> 其余 P2 子切片
    ├─ thread/cachekeepalive/session users：依赖 P1c 的 session stop/drain contract
    ├─ platform event slices（hooks/config/rpc push/eventsurface）：与 `P1b` 共享 wiring，代码合入串行
    ├─ toolbridge runtime：依赖 P1a/P1c 冻结 codexapp peer/session 侧 contract
    └─ gopls/bootstrap runtime：sidecar 入口共享写集，同 lane 推进

P22B dependency / hidden contract
P4
├─ ui/wails 与 claudecli：P0 后可拆两个窄 PR 并行
├─ thread/turn 子域：代码合入排在 `P2(thread slice)` 之后，且 thread+turn 同 lane
├─ toolbridge 子域：排在 `P2(toolbridge runtime)` 之后串行
├─ orchestration 子域：排在 `P3(waiter/exit owner)` 之后串行
└─ gopls/bootstrap compatibility：排在 `P2(gopls/bootstrap runtime)` 之后串行
```

> 设计与文档澄清可以并行；共享 wiring / launcher contract / protocol shell 的代码合入按串行收口。真正的并行单位是子切片，不是 `P1a-P4` 六张计划单整块平推。

## 关键路径（叙事）

P22 的最长硬门不是“把 `P2` 一次做完”，而是先冻结公共 owner/drain 契约，再沿 codexapp session 与 thread/toolbridge 这条共享写集主链推进。按叙事层理解，这条主线依次跨过的是：公共骨架门、peer owner 门、session stop/drain 门、thread/toolbridge runtime 门、以及 thread/turn + toolbridge hidden-contract 门；它们分别解决“统一口径”“冻结 codexapp 边界”“冻结 session 合同”“收走 callback/runtime owner”“最后删除 side-channel / protocol shell”这五类阻塞。

- 主 critical path：`P0` 公共骨架 / allowlist -> `P1a` -> `P1c` -> `P2(thread / cachekeepalive / toolbridge runtime)` -> `P4(thread/turn + toolbridge contract)`
- 平台 loop 支线：`P0` -> `P1b` -> `P2(hooks / config fanout / rpc push / eventsurface)`
- orchestration 支线：`P0` -> `P3` -> `P4(orchestration shell / identity / report)`
- memory 模板支线：`P0` -> `P2(memory / team / auto-dream / memory hooks)`
- 低写集 contract 支线：`P0` -> `P4(ui/wails)` 与 `P4(claudecli)`

因此 `P2(memory...)` 是推荐先跑的模板批次，但不是卡住 `P3` 或全部 `P4` 的总闸门；真正的 hard gate 是共享 owner contract 的冻结顺序。README 这一节只负责派工顺序与阻塞关系，不替代各子计划自己的实施命令表或代码放行 gate。

## 关键路径图（节点数字）

```text
主 critical path（5 节点）
[1] P0 archtest allowlist + root-bridge 豁免模型
 -> [2] P1a peer supervisor owner
 -> [3] P1c session stop/drain contract
 -> [4] P2(thread / cachekeepalive / toolbridge runtime)
 -> [5] P4(thread/turn side-channel + toolbridge dependency/protocol)

并行支线
[A] [1] -> P1b -> P2(hooks / config fanout / rpc push / eventsurface)
[B] [1] -> P3 -> P4(orchestration shell / identity / report)
[C] [1] -> P2(memory / team / auto-dream / memory hooks)
[D] [1] -> P4(ui/wails + claudecli)
```

| 节点 | 出口条件（必须同时满足） |
|---|---|
| **[1] P0** | root bridge allowlist 收窄到 `file path + symbol + bridge shape`；runtime guard 进入独立 `*_guard_test.go`；不把语义 allowlist 塞进 numeric freeze / `TestCodeSizeGuard` |
| **[2] P1a** | `fx.Invoke(spawnToolbridgePeers)` 删除；peer start/restart/stop/drain 统一归 `PeerSupervisor`；codexapp peer 边界冻结 |
| **[3] P1c** | `StartSession/ResumeSession` 成为唯一显式启动点；`newSession()` 不再隐式起 reader/health；`Close/ForceStop` 完成 cancel + join/drain |
| **[4] P2** | callback 只 enqueue；单一 owner 负责 worker/timer/proxy stop-drain；late setter / `OnStart -> go ServeProxy(...)` / background goroutine 旁路删除 |
| **[5] P4** | `PendingLaunchSpawner` / `WaitForSessionReady` 等 side-channel 升格或删除；toolbridge 不再直吃 `provider/*` / `store/*` hidden contract；旧 shell / fallback 删除 |

> 主线节点与问题域的对应关系固定为：`[1]` 统一 guard / allowlist 基座，`[2]` 冻结 codexapp peer owner，`[3]` 冻结 session stop/drain，`[4]` 收 thread / cachekeepalive / toolbridge runtime ownership，`[5]` 删除 thread/turn 与 toolbridge 的 hidden contract / protocol shell。这样派实施 agent 时不必再在 README 与各子计划之间来回猜“这一站到底收哪类 blocker”。

### `P0-only` vs `owning-slice` gate（4 guard × 2 阶段表）

| guard 面 | `P0 PR` 必做 | `owning-slice PR` 必做 |
|---|---|---|
| root bridge allowlist | 定义 schema、seed root-entry bridge、固定 `definition_path/call_site_path/bridge_shape/exception_class` 字段 | 按本切片删临时例外、补 `remove_when`、把例外收窄到真实桥形态 |
| `fx.Invoke` guard | 落 matcher/helper 骨架与独立 `*_guard_test.go` 壳 | 用 live 样本补失败测试、修代码、收紧 allowlist |
| `OnStart` guard | 落 root-bridge 豁免模型与一跳 helper 解析 | 把本切片 lifecycle 长跑迁出，接入 red-green 测试 |
| bus callback / actor execute guard | 落 callback/actor hot-file guard 框架与统一诊断输出 | 对本切片真实 slow-path / waiter 回归样本完成 matcher、删旧旁路 |

> 这张表只定义 **`P0-only` gate 与 `owning-slice` gate 的分工**：`P0 PR` 先交骨架，不先把全仓打成 hard-fail；具体 matcher、失败样本、allowlist 收缩和代码修复必须跟 `P1/P2/P3` 的 owning-slice PR 同步落地。

## 并行度矩阵（叙事）

| 组合 | 裁决 | 叙事口径 |
|---|---|---|
| `P0` vs 后续子计划 | 🟡 | `P0` 先合骨架 / allowlist / guard helper；具体 `fx.Invoke`、`OnStart`、bus callback、actor execute 守卫随 owning slice 同 PR red-green，不做一次性全仓 hard-fail |
| `P1a` vs `P1b` | 🟡 | 可分支并行，但共享 `rpc`/root wiring 的最终合入要串行 |
| `P1a` vs `P1c` | 🔴 | 同包 `codexapp` + 共享 `ServerManager/module.go`，必须先 peer 再 session |
| `P1b` vs `P2(memory...)` | 🟢 | 只有 runner 模板复用，没有硬写集依赖 |
| `P1b` vs `P2(platform event slices)` | 🔴 | `mcpcontrol/rpc` 同包共享 wiring，代码合入必须串行 |
| `P2(memory...)` vs `P3` | 🟢 | 可并行，但 `P3` 的 exit contract 必须保持 package-local，不升级成 bus event |
| `P2` 内部切片 | 🟡 | 推荐上限为 5 路：`memory` / `thread+cachekeepalive(session users)` / `hooks+config+rpc push+eventsurface` / `toolbridge` / `gopls+bootstrap`；`cachekeepalive` 不再和 platform event lane 混写，`gopls/bootstrap` 也不是两个独立 merge lane |
| `P3` vs `P4(orchestration)` | 🔴 | 共享 `process_lifecycle/helpers/service/rpc` 热点，先 waiter/exit owner，后 shell/identity/report |
| `P4` 内部子域 | 🟡 | 真正可先独立开的只有 `ui/wails` 与 `claudecli`；`thread+turn` 必须合并成一条 side-channel lane；`toolbridge` / `orchestration` / `gopls-bootstrap` 只允许先做文档/contract 澄清，代码实现分别等 `P2/P3` 前置 |

## 并行度矩阵（数字）

| 范围 | 推荐并行 lane 数 | 上限 lane 数 | 前置 / 合码硬门 | 数字来源 |
|---|---:|---:|---|---|
| `P0` | 1 | 1 | 先交 skeleton + allowlist schema/root-bridge seed；具体守卫按 owning slice 分批接入 | R11 / R20 |
| `P1a + P1b` | 2 | 2 | 先 freeze 一拍共享 `rpc` / identity 公共面，再各自改功能体，最后单点合 root/rpc 公共写集 | R12 |
| `P1a + P1c` | 1 | 2 | **推荐串行**：`P1a` 先、`P1c` 后；若硬并行，只允许分写集并行、串行合入 | R13 |
| `P1b + P2(memory 主切片)` | 2 | 2 | 仅 `memory/team + auto-dream + memory hooks + NestedRuntime` 可与 `P1b` 真并行；platform event/toolbridge 不在此列 | R14 |
| `P2` 内部 8 scope | 5 | 5 | 推荐拆成 `memory` / `thread+cachekeepalive(session users)` / `hooks+config+rpc push+eventsurface` / `toolbridge` / `gopls+bootstrap` 五路 | R15 |
| `P2(memory 主切片) + P3` | 2 | 2 | 前提是 `P3` exit contract 保持 package-local，不升级成全局 bus event | R16 |
| `P3 + P4` | 2 | 2 | 仅 `P4(ui/wails + claudecli)` 可与 `P3` 并行；`P4(orchestration)` 必须等 `P3` | R17 / R18 |
| `P4` 内部 | 2 | 2 | 第一批只开 `ui/wails` + `claudecli`；`thread+turn` 固定 1 lane；`toolbridge` 等 `P2`；`orchestration` 等 `P3` | R18 |

> 全局数字化结论：P22 不存在“`P1a-P4` 六张计划单整块并行”；当前文档支持的峰值并行度是 **5 lanes**，且只出现在 `P2` 内部分组阶段。

## 落地顺序建议

1. 先合 `P0` 的公共骨架：术语、root bridge allowlist、semantic allowlist、guard helper；不要一口气把所有具体守卫打成主干 hard-fail。
2. `P0` 之后可同时开首波：`P1a`、`P1b`、`P3`、`P2(memory...)`；`P4(ui/wails)` 与 `P4(claudecli)` 也可作为低写集 contract lane 并行准备。
3. `P1a` 完成后立刻接 `P1c`；不要把 peer supervisor 与 session runtime 混成一次 codexapp 总收口。
4. `P1b` 完成后再推进 `P2(platform event slices)`；同包 `mcpcontrol/rpc` 的 loop owner 与 callback slow-path 不并批改。
5. `P1c` 完成后再推进 `P2(thread / cachekeepalive / session users)`；先冻结 session stop/drain，再动 thread/keepalive。
6. `P2(toolbridge runtime)` 排在 `P1a + P1c` 之后；完成后再接 `P4(toolbridge dependency / protocol contract)`。
7. `P3` 完成后再接 `P4(orchestration)`；exit event / drain contract 保持 local process owner -> actor 主循环，不经全局 bus 中转。
8. `P4(thread/turn)` 只在 `P2(thread slice)` 之后串行合入；`gopls/bootstrap` 始终视为同一 sidecar lane，runtime 与 compatibility 分两拍。
9. README 只负责入口级派工与阻塞说明；真正进入“可实施”要再同时满足对应子计划验收标准、`JUDGEMENT_DYNAMIC.md` 的事实层放行，以及 `P21` / `session-summary` 的 debt banner / next-step 同步完成。
10. `P21` / `session-summary` 的 debt banner / next-step 默认视为**文档同步 gate**，不是每个 P22 子切片的 runtime rollback blocker；只有共享 contract 仍漂移、会让实现或回滚误判时，才升级成 blocker。
11. Finding 11 / Finding 12 与 `pre-drain / watchFXShutdown` 仍按 `JUDGEMENT_DYNAMIC.md` / Q-D 的 dynamic-only disposition 记账；README 五门 gate 与支线 gate 不代签这些 live code blocker。

> README 中凡写 `P2(thread slice)` / `P2(thread / cachekeepalive / toolbridge runtime)`，其中 thread 子 lane 统一指 `thread event / resume / task-handoff + cachekeepalive(session users)`；toolbridge runtime 与其同属 `[4]` gate，但代码实施仍按 disjoint 子切片串行落盘。

## 风险矩阵（叙事）

- **crash-window / duplicate side effect**：凡是 owner 从 callback / lifecycle / helper 下沉到单一 `RunnerModule` 或 worker 的切片，都要先写清 crash-window、重放边界、幂等栅栏与 late callback 处理方式；尤其是 `P1b/P2/P3` 的 replay、exactly-once、lease/heartbeat 相关路径。
- **升级 / 回滚**：若某切片需要阶段性兼容层或双轨窗口，必须同时写出回滚触发条件、开关位置、状态回退方式与停用步骤；只有“删除条件”但没有回滚说明，不算可执行迁移计划。
- **升级 / 回滚补充硬规则**：任何 compatibility shim / dual-path 只能通过显式 feature flag / env opt-in 打开，且默认关闭；文档里必须点名 gate carrier、rollback trigger、state rewind、disable steps，并区分“文档同步 gate”与“runtime rollback blocker”。
- **切片级 rollback card**：`P2` 与 `P4` 这种 umbrella / multi-subdomain 计划，正文必须附 slice / subdomain 级 rollback card；不能只在 README 总则里写“必须写清”。
- **可观测性**：队列 overflow、drop/coalesce、drain latency、degraded-path、retry/backoff、heartbeat、owner 启停都必须在实施前定义最低日志 / metric / trace 口径；不能只剩“退出日志”级别表述。
- **crash-window / 状态机**：`P1b/P1c/P3` 这类 owner 迁移页，必须把状态名、超时常量、drain 完成信号与 exactly-once fence 写成表，不接受只有“方向正确”的叙事稿。
- **fallback / 缺失硬报错**：凡涉及跨项目隔离、权限、信任域、`cwd/thread/runtime/identity` 解析的缺参路径，默认行为只能是 `ErrXxxRequired` / `InvalidParams`；禁止静默回退到全局默认、旧行为或更宽 scope。
- **死代码 / 空架子**：新 helper 若 `lsp_xref` 后 prod caller = 0（或只剩 `_test.go` caller），视为 blocker；文档必须写清“接入真实 owner”还是“直接删除/降级为 internal-only”。
- **CI / flaky 控制**：涉及 timer / backoff / jitter / event storm / drain 的测试，默认采用 fake clock、确定性抖动、禁裸 `time.Sleep` 测试、显式 timeout 预算与 `-race` 路径，避免把不稳定测试带入 P22 主线。
- **历史教训映射**：P22 承接 `docs/会话习惯.md` 中的 §10.24 运行时 PoC、§10.27 缺失即硬报错、§10.31 只加不删；高风险判定类变更不得只靠单测绿灯签收。
- **跨文档同步**：与 `P21`、`session-summary`、signed-skill 相关的叙事变更，继续按 `H + O + M` 三独立视角复核，避免只在单份文档里看起来自洽。

## 收口口径

- `fx.Invoke` 的合法用途以 `docs/契约/modularity-convention.md §4.4` 为准；本文如提及 `fx`，只把 `docs/契约/fx-convention.md §2/§3` 当作工厂职责补充，不再沿用历史误引。
- 本文前文若出现“`fx.Module = constructor、lifecycle、一次性恢复`”的历史简写，一律按 `docs/契约/modularity-convention.md §4.4 / §7` + `docs/契约/fx-convention.md §2 / §3` 更正解释：lifecycle 只做资源 open/close；启动期一次性恢复属于 `fx.Module` 的单次 wiring / 合法 `fx.Invoke`，不把恢复常态化写进 lifecycle。
- 文中优先使用 `fx.Module` / `BusModule` / `RunnerModule` 这组角色术语；当前代码里的 `group:"runners"` 仅表示现 root bridge 的实现 tag，不把它误写成契约层最终命名。
- root runtime bridge 只豁免“进程入口处汇总当前 runner 集合并调用 `platformrunner.RunGroup(...)`”这一桥形态；`watchFXShutdown(...)`、其它 lifecycle hook、同名 `bindRuntime` helper 都不自动豁免。
- `internal/app` 与 `cmd/mcp-orch` 适用双树同构；`cmd/mcp-lsp` / `cmd/mcp-ida` 只按 runner-only sidecar 收口，不要求补 bus 树。runner-only sidecar 的最小标准是：拥有单独的 fx root、root runtime bridge 与 runner 集合；若未实际 wiring dispatcher，就不强补 `BusModule`。
- README 中写的 `ctx cancel -> run.Group -> bus stop-intake -> fx.OnStop` 是 app/orch 侧的目标语义顺序，不表示每个 root bridge 源码里都字面包含 bus 阶段：desktop 允许 bounded pre-drain，sidecar 只要求 root cancel -> wait done。
- `BusModule` callback 默认只做两类事：同步更新轻量内存状态；non-blocking enqueue。退订只表示“不再派发新事件”，**不等于 drain**；drain 由显式 owner / worker 负责。
- `RunnerModule` 负责长期 owner。若某处暂时不能直接进 `run.Group`，也必须有单一 owner 的 `Start/Stop/Drain` 语义；不允许 callback / helper 自己偷跑长跑 goroutine。
- `P0` 是公共骨架 hard gate，不等于要求所有具体守卫一次性全仓 hard-fail；root bridge allowlist / helper 解析先收口，具体守卫按 `P1/P2/P3` 分面跟实现同 PR 接入。
- 纯 wiring / registry registration 可继续视为模块装配动作；但只要 `Invoke` / lifecycle / callback / helper 进入 runtime ownership、late setter mutation 或业务编排，就仍算 `P22` 目标。
- `P2` 只收 runtime ownership；`toolbridge` 的依赖方向 / 协议 contract 留给 `P4`，`orchestration` 的 waiter/exit owner 留给 `P3`。
- `internal/module/thread`、`internal/platform/hooks`、`internal/platform/mcpcontrol/config_change.go`、`internal/platform/cachekeepalive/*`、`internal/platform/rpc/push.go` / `eventsurface`、`cmd/mcp-lsp/gopls`、`internal/mcpserver/common/bootstrap` 都已归入 `P2` 的 runtime lane；`P21` 递延的 signed-skill / native-skill contract 债归 `P4` 的 `provider/claudecli` 子域，并按 `H + O + M` 三独立视角复核（文档垂直 / 安全权限 / 跨文档一致）。

### `H + O + M` sign-off 工位

| sign-off | 职责 | 主要落点 |
|---|---|---|
| `H`（文档垂直） | 判定 blocker 属于哪张子计划、哪一门 gate、哪条 lane | `README` + 对应 `P0-P4` 子计划 |
| `O`（安全 / 权限 / 信任域） | 检查 signed-skill、fallback、permission、identity、trust-domain 是否被错误放宽 | `P4` + `P21` + 对应安全 judgment |
| `M`（跨文档一致） | 校验 `README`、`P0-P4`、`session-summary`、`arch-import-direction`、相关 judgment 不漂移 | `README` authoritative pointer + handoff checklist |

> 这里的 `sign-off` 只定义工位，不替代 merge gate：每次放行仍要同时看子计划验收、`JUDGEMENT_DYNAMIC.md` 的事实层状态，以及跨文档同步是否完成。

## 实施方式

- `P22` 默认按 umbrella 计划执行：先修文档与守卫口径，再按共享写集拆成多批实现；不把 `P1c + P2 扩围 + P4` 误写成单次 PR。
- `P0` 先合公共 guard 骨架 + root bridge allowlist / semantic allowlist；具体 `fx.Invoke`、`OnStart`、bus callback、actor execute 守卫跟 owning subplan 同 PR red-green，不把主干先打成大红面。
- runtime ownership 的统一模板是：bus callback 只 enqueue -> owner / worker / runner 消费 -> stop/drain 单点收口 -> 删除旧直达路径。
- `P3` 的 exit event / drain contract 保持 local process owner -> actor 主循环，不升级成全局 bus topic；否则会把 waiter owner 与 `P2` 的 bus stop-intake/drain 语义重新耦合。
- dependency / hidden contract 的统一模板是：先冻结 authoritative contract，再抽 facade / contract carrier，最后删旧 shell / fallback。
- `P2` 代码实施必须按 disjoint write-set 分批；`memory/thread`、`toolbridge`、`orchestration` 这类共享 wiring 的子域不做“多人同批改同包”；其中 `cachekeepalive` 视为 thread/session-user lane，`gopls/bootstrap` 视为同一 sidecar lane，不再拆成互不感知的独立 merge 单元。
- README 只提供入口级调度草图，不代替各子计划自己的验证命令表；真正放行仍以子计划验收、动态事实核验和跨文档同步完成为准。
- 允许保留阶段性兼容层，但必须写清删除条件；没有删除条件的双轨视为未收口。

### `P21 + session-summary` handoff checklist

| 目标文档 | handoff checklist | authoritative 指针 |
|---|---|---|
| `README.md` | 更新 gate 位、lane、authoritative pointer、并发/串行口径 | README 是 umbrella 主线权威 |
| 对应 `P0-P4` 子计划 | 更新 owner 边界、依赖图、实施方式、非目标，不越权改验收/TDD/安全段 | 对应子计划负责本域细节 |
| `JUDGEMENT_DYNAMIC.md` | 只同步事实层 blocker 是否销账；README 不代签动态放行 | 事实层以动态 judgment 为准 |
| `docs/plans/迁移/session-summary.md` | 仅同步 debt banner / next-step / authoritative 指针，不把“下一步”反写成历史完成事实 | session-summary 只是会话交接页 |
| `docs/plans/迁移/arch-import-direction.md` | 仅同步 debt banner / authoritative pointer，避免历史扫描页反客为主 | `P4` 是 hidden-contract 权威页 |
| `docs/plans/迁移/p21/*` | 若触及 signed-skill / native-skill / verifier / trust 叙事，必须同步 note 或 debt banner | `P21` 继续承接相关背景约束 |

## 非目标

- 本轮不把契约三件套整仓重写；`P22` 只先把自身文案与契约角色术语对齐。
- 本轮不把 `cmd/mcp-lsp` / `cmd/mcp-ida` 硬写成具备 bus 树的“四树同构”。
- 本轮不把 `P2` 当成“一张单子一次做完”的 mega PR。
- 本轮不因 runtime ownership 修复顺手改业务策略；例如 peer restart policy、approval timeout、TeamSync 功能边界、auto-dream gating 规则都不在此改判。
- `P4` 不替代 `P2/P3`：它只签收 dependency direction / hidden contract，不单独签收 runtime owner 闭环。

**总计**：约 9-16.5 工程日（若多人并行，日历时间约 4-6 天）。真正的难点不在代码量，而在把“谁负责跑、谁负责停、谁只负责接线”重新钉死。

## P22.1 架构债子任务（deferred）

R10.6 代码层 deferred 债总账 + §10.30 三层分工 11 处违例移交至 P22.1 子任务集中收口：
- `docs/plans/迁移/p22/p22.1/README.md`
- `docs/plans/迁移/p22/p22.1/FINDINGS.md`
- `docs/plans/迁移/p22/p22.1/DAG.md`

关系：P22.1 ⊂ P22（不是独立 lane）。
