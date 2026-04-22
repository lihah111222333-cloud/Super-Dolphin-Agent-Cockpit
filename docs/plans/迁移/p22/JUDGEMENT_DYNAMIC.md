# P22 裁决书（动态层 / Judge-D）

> 生成时间：2026-04-23
> 姐妹裁决：`JUDGEMENT_STATIC.md`

## 1. 收报告状态表

| Agent | 主题 | 状态 | 已收报告? | 备注 |
|---|---|---|---|---|
| D1 | README | idle | ✅ | scope / 工时 / root bridge / P21 递延口径 |
| D2 | P0 | idle | ✅ | 守卫可行性与漏抓面 |
| D3 | P1a | idle | ✅ | peer owner / restart 语义 |
| D4 | P1b | idle | ✅ | sweeper / approval loop |
| D5 | P1c | idle | ✅ | session runtime 文档密度与 owner 选型 |
| D6 | P2 | idle | ✅ | umbrella scope / 拆分建议 |
| D7 | P3 | idle | ✅ | waiter / exit owner |
| D8 | P4 | idle | ✅ | dependency / hidden contract |
| C1 | root bridge | idle | ✅ | 4 处 root-entry bridge 核对 |
| C2 | Finding 1-2 | idle | ✅ | codexapp peer anchors / 额外缺陷 |
| C3 | Finding 3-4 | idle | ✅ | platform loops |
| C4 | Finding 5-7 | idle | ✅ | memory anchors / extra slow-path |
| C5 | Finding 8 | idle | ✅ | orchestration waiter |
| C6 | toolbridge | idle | ✅ | P2/P4 双债 |
| C7 | P2 other | idle | ✅ | thread/hooks/config/keepalive/gopls/bootstrap |
| C8 | archtest freeze | idle | ✅ | freeze / guard 能力边界 |
| A1 | contracts vs P22 | idle | ✅ | 契约冲突清单 |
| A2 | shutdown flow | idle | ✅ | root cancel / drain 语义 |
| A3 | quad tree | idle | ✅ | 双树同构 vs sidecar |
| A4 | feasibility | idle | ✅ | scope / 工时 / 并行边界 |

## 2. 独立抽样结果（10 条硬验证）

| # | 验证项 | 期望 | 实测 | 匹配? |
|---|---|---|---|---|
| 1 | `lsp_grep spawnToolbridgePeers` | `internal/provider/codexapp/` 仅 module invoke + helper 定义 | 命中 `module.go:35`、`peer_spawn.go:18`（另 1 条注释） | ✅ |
| 2 | `lsp_grep waitForProcessExit` | orchestration 内存在 service 轮询函数 | 命中 `process_lifecycle.go:136-162` + `helpers.go:173` 调用 | ✅ |
| 3 | `lsp_grep "fx.Invoke"` | 全仓粗估约 37 | `rg -n 'fx\\.Invoke' internal cmd --glob '*.go'` = **37**；`lsp_grep` 代码面为 internal 31 + cmd 6 | ✅ |
| 4 | `OnStart -> go` 抽样 | 非 root 仍有 live 违规点 | `mcpcontrol/module.go:191`、`rpc/module.go:195`、`toolbridge/module.go:149-158` | ✅ |
| 5 | `lsp_xref references BindRuntime/bindRuntime` | 4 个 root bridge 定义都有 prod caller | `internal/app/runner.go:34` <- `app.go:133,154`；`cmd/mcp-orch/runtime.go:225` <- `fx.go:61`；`cmd/mcp-lsp/fx.go:203` <- `fx.go:89`；`cmd/mcp-ida/fx.go:99` <- `fx.go:67` | ✅ |
| 6 | 读 `internal/module/memory/module.go:456-466` | TeamSync 回调仍直达 start/stop helper | 实测范围已是 `456-467`；`458/463` 仍直达 `StartSessionFromThreadEvent` / `StopSessionFromThreadEvent` | ✅ |
| 7 | 读 `internal/module/memory/team/team_sync_watcher.go:72-78` | watcher 仍 `SafeGo` 起 loop | 实测范围已是 `72-79`；`76-78` 仍 `runtimesafe.SafeGo(...)` | ✅ |
| 8 | 读 `internal/module/memory/auto_dream_task.go:160-177` | auto-dream 回调仍直 `go` | 实测问题段为 `156-178`；`160-162` callback -> `165-178` -> `173 go func()` | ✅ |
| 9 | 读 `cmd/mcp-orch/orchestration/process_lifecycle.go:220-238` | actor 仍 `go waitForExit` | 实测范围已是 `220-239`；`222` 仍 `go a.waitForExit(...)` | ✅ |
| 10 | `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v` | 以实测为唯一真值 | **0 violations / PASS** | ✅ |

## 3. Findings 1-10 行号 drift 表

| Finding | 文档行号 | HEAD 实测 | drift | 已修订? |
|---|---|---|---|---|
| 1 | `internal/provider/codexapp/module.go:35` | `internal/provider/codexapp/module.go:35` | 0 | ✅ |
| 2 | `internal/provider/codexapp/peer_spawn.go:18-109` | `internal/provider/codexapp/peer_spawn.go:18-155` | 尾段少记 `restartPeer(...)` | ✅ |
| 3 | `internal/platform/mcpcontrol/module.go:184-197` | `internal/platform/mcpcontrol/module.go:184-199` | +2 行 | ✅ |
| 4 | `internal/platform/rpc/module.go:149-196` | `internal/platform/rpc/module.go:149-166 + 179-197` | 现已拆成 lifecycle + helper 双段 | ✅ |
| 5 | `internal/module/memory/module.go:456-466` | `internal/module/memory/module.go:456-467` | +1 行 | ✅ |
| 6 | `internal/module/memory/team/team_sync_watcher.go:72-78` | `internal/module/memory/team/team_sync_watcher.go:72-79` | +1 行 | ✅ |
| 7 | `internal/module/memory/auto_dream_task.go:160-177` | `internal/module/memory/auto_dream_task.go:156-178` | 起点前移 4 行、尾段 +1 行 | ✅ |
| 8 | `cmd/mcp-orch/orchestration/process_lifecycle.go:220-238` | `cmd/mcp-orch/orchestration/process_lifecycle.go:220-239` | +1 行 | ✅ |
| 9 | `internal/platform/toolbridge/module.go:130-159` | `internal/platform/toolbridge/module.go:130-159` | 0 | ✅ |
| 10 | `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339` | `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339` | 0 | ✅ |

## 4. archtest 真值

- `TestCodeSizeGuard`: **0 violations**
  ```text
  === RUN   TestCodeSizeGuard
  --- PASS: TestCodeSizeGuard (0.18s)
  PASS
  ok  	github.com/anthropic-ai/super-agent-v3/internal/archtest	1.005s
  ```
- freeze_registry 条目变动：**无**
  - `internal/archtest/freeze_registry.go:19` 仍是 `var explicitFreezeRegistry = []explicitFreeze{}`
  - `go test ./internal/archtest -run 'TestFreezeRegistryIntegrity|TestDeadKeyGuard' -count=1 -v`：PASS
- 全量 archtest（`go test ./internal/archtest/... -count=1 -v`）：**FAIL**
  - `TestDependencyDirection/rule2_module_impls_no_fx`：`internal/module/memory/ui_rpc.go imports go.uber.org/fx outside module.go`
  - `TestDependencyDirection/rule10_fx_import_scope`：`internal/module/memory/ui_rpc.go imports go.uber.org/fx outside an assembly entry`
  - `TestTimeoutLocality`：`internal/module/prompt/classifier/claude_cli.go:59`

## 5. 新 Finding 追加（经独立核实）

| # | 文件:行 | 类型 | 证据 |
|---|---|---|---|
| 9 | `internal/platform/toolbridge/module.go:130-159` | `fx.Lifecycle.OnStart -> go` 长跑 proxy owner 漏出 | `module.go:149-158` 直接 `go func(...) { h.ServeProxy(...) }`；`lsp_xref` 证实 `registerProxyLifecycle` prod caller = `module.go:37`，`ServeProxy` prod caller = `module.go:155` |
| 10 | `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339` | bus callback 经 helper 同步读盘 | `module.go:435-437` 直接 `p.NestedRuntime.AddToolReadResult(...)`；`lsp_xref` 证实 prod caller = `module.go:436`；helper `extractNestedReadToolPaths(...)` 在 `persistedPath` 非空时同步 `os.ReadFile(...)` |

## 6. 死代码 / 空架子清单

- **Finding 1/2/5/8 对应 helper 不是空架子**：  
  `spawnToolbridgePeers` <- `module.go:35`；`StartSessionFromThreadEvent` <- `memory/module.go:458`；`StopSessionFromThreadEvent` <- `memory/module.go:463`；`waitForProcessExit` <- `helpers.go:173`
- **`waitDreamTask(...)` 仍是 test-only helper**：  
  `internal/module/memory/auto_dream_task.go:135-154` 的 `waitDreamTask` 只有 `auto_dream_test.go` 两处 caller；prod stop 路径仍未接上

## 7. 已执行修订

- 改：`docs/plans/迁移/p22/README.md` `## Findings 对照表` → 修正 Findings 2-8 锚点；追加 Finding 9；后续在 Round-3 再追加 Finding 10 并把工时总计更新到 `9-16.5 工程日（并行日历时间 4-6 天）`
- 改：`docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md` `## 守卫改动建议` → `fx.Invoke` / bus callback 守卫补 `runtimesafe.SafeGo(...)` 与 `NotifyConfigChanged(...)` / `DispatchAfter(...)`
- 改：`docs/plans/迁移/p22/P1a_CodexAppPeerSupervisor.md` `## 对应 findings / 现状校准 / 实施步骤 / 需冻结的兼容语义 / 验收标准` → Finding 2 扩到 `18-155`，补 `peerProcs` 不更新、`oldPipe` 未关、restart 失败语义与 drain 要求
- 改：`docs/plans/迁移/p22/P1b_PlatformLoopRunners.md` `## 对应 findings / ## 不在本单闭环的已知遗留点` → 修正 Findings 3/4 锚点，并显式记账 toolbridge proxy 同型债
- 改：`docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md` `## 现状校准 / ## 实施步骤 / ## 验收标准` → 补 `driver.go` caller、`session.go` 隐式起飞、`recovery.go` reader/health/recovery 旁路、`factory.go` shutdown 无 drain
- 改：`docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md` `## 对应 findings / ## 现状校准 / ## 验收标准` → 修正 Findings 5/6/7 锚点；追加 Finding 9；Round-3 再补 Finding 10 与 nested-runtime tool-read slow-path；并补 toolbridge/hooks/config/keepalive/rpc push 的精准代码锚点
- 改：`docs/plans/迁移/p22/P3_OrchestrationWaiterAlignment.md` `## 对应 findings / ## 现状校准 / ## 实施步骤 / ## 验收标准` → 修正 Finding 8 与 `waitForProcessExit` 锚点；补 `startProcessLocked` / recover 也必须接入同一 exit owner
- 新增：`docs/plans/迁移/p22/JUDGEMENT_DYNAMIC.md`
- 删：无（本轮仅修事实锚点与新增 Finding；未发现必须立删的 p22 失真章节）

## 8. 与 Judge-Static 工作域冲突风险

- `orchestration_get_agent_report(agent-1776895620374-...)` 已返回 **裁决完成**
- Judge-S 明确声明其工作域：  
  - `README.md`：`## 目标` / `## 依赖图（文本）` / `## 落地顺序建议` / `## 收口口径` / `## 实施方式` / `## 非目标`
  - `P0`：`## 目标` / `## 收口口径` / `## 非目标`
  - `P1a`：`## 目标` / `## 实施方式` / `## 收口口径` / `## 依赖图（文本）` / `## 非目标`
  - `P1b`：`## 目标` / `## 实施方式` / `## 收口口径` / `## 依赖图（文本）` / `## 非目标`
  - `P1c`：`## 目标` / `## 实施方式` / `## 收口口径` / `## 依赖图（文本）` / `## 落地顺序建议` / `## 非目标`
  - `P2`：`## 目标` / `## 收口口径` / `## 实施方式` / `## 依赖图（文本）` / `## 落地顺序建议` / `## 非目标`
  - `P3`：`## 目标` / `## 实施方式` / `## 收口口径` / `## 依赖图（文本）` / `## 落地顺序建议` / `## 非目标`
  - `P4`：`## 目标` / `## 收口口径` / `## 实施方式` / `## 依赖图（文本）` / `## 落地顺序建议` / `## 非目标`
- 我本轮只改动态域：  
  `## Findings 对照表`、`## 现状校准`、`## 实施步骤（代码锚点段）`、`## 验收标准`、工时数字、`JUDGEMENT_DYNAMIC.md`
- 风险判定：**无冲突**（Judge-S 报告已显式确认 “与 Judge-Dynamic 工作域无冲突：✅”）

## 9. LSP 自验证据

- `lsp_grep`：
  - `spawnToolbridgePeers` -> `internal/provider/codexapp/module.go:35`, `peer_spawn.go:18`
  - `waitForProcessExit` -> `process_lifecycle.go:136-162`, `helpers.go:173`
  - `go sweeper.Run(` -> `internal/platform/mcpcontrol/module.go:191`
  - `go startApprovalCleanupLoop(` -> `internal/platform/rpc/module.go:195`
  - `go a.waitForExit(` -> `cmd/mcp-orch/orchestration/process_lifecycle.go:222`
  - `StartSessionFromThreadEvent` -> `memory/module.go:458`, `team/thread_metadata.go:63`
  - `go func() {`（memory） -> `auto_dream_task.go:173,286`, `extract_runtime.go:210`
  - `publishConfigChanged(` -> `internal/platform/mcpcontrol/config_change.go:34/37/40/43/46/49/52/55`
  - `time.AfterFunc(` -> `internal/platform/cachekeepalive/manager.go:275`
  - `backgroundResumeIfNeeded` -> `archive.go:92`, `events.go:151`, `history.go:51,102`, `service.go:356`
- `lsp_xref(references)`：
  - `spawnToolbridgePeers` <- `module.go:35`
  - `StartSessionFromThreadEvent` <- `memory/module.go:458`
  - `StopSessionFromThreadEvent` <- `memory/module.go:463`
  - `ServeProxy` <- `toolbridge/module.go:155`
  - `waitForProcessExit` <- `helpers.go:173`
- `lsp_inspect(definition)`：
  - `ServeProxy` -> `internal/platform/toolbridge/proxy.go:53-68`
  - `StartSessionFromThreadEvent` -> `internal/module/memory/team/thread_metadata.go:63-77`
  - `waitForProcessExit` -> `cmd/mcp-orch/orchestration/process_lifecycle.go:136-162`
- `lsp_structure(document_symbol)`：
  - `internal/app/runner.go` 顶层符号仅 `RunnerResult` / `runtimeParams` / `BindRuntime`
- `lsp_file`：
  - `memory/module.go:456-467`
  - `team_sync_watcher.go:72-79`
  - `auto_dream_task.go:156-178`
  - `process_lifecycle.go:220-239`
  - `toolbridge/module.go:130-174`

## 10. 第 2 轮复审汇总（Round-3）

- 收第 2 轮报告：**20/20**
- 原始分布：**🟢 2 / 🟡 12 / 🔴 6**
- 按 §10.18：原始轮次结论为 **BLOCK**
- 归并真 gap 后，本轮属于动态域的可直修项共 **8** 条：
  1. `README` 顶部 baseline 仍写 findings `1-9`
  2. `README` / 工时表里 `P2/P4` 估算仍偏紧
  3. `P1a` 未冻结固定 `2s` backoff / restart-success 继续监督 / degraded-path 测试
  4. `P1b` 的 “restore 只执行一次” 表述与 connect-time replay 语义冲突
  5. `P1c` 缺独立 `## 需冻结的兼容语义`
  6. `P2` 缺 NestedRuntime tool-read slow-path 记账
  7. `P2` 未把 non-silent-drop 域的 overflow 机制钉到实现口径
  8. `P3` 未把 actor 内 goroutine 的 AST/archtest 守卫写进 TDD
- 本轮 **不纳入直修** 的红灯：
  - 纯实现未落地（如 `mcpcontrol/rpc/toolbridge` 代码仍未改）
  - 静态域问题（如 `P4` / 契约命名冲突 / `NewActiveAgentCounter` hidden contract）

## 11. 本轮新修订

- 改：`docs/plans/迁移/p22/README.md`
  - baseline 从 `findings 1-9` 更新到 `findings 1-10`
  - `P2` / `P4` 工时上调，`总计` 更新为 `9-16.5 工程日`
  - Findings 表追加 **Finding 10**
- 改：`docs/plans/迁移/p22/P1a_CodexAppPeerSupervisor.md`
  - Finding 2 锚点修正为 `18-155`
  - 补冻结：固定 `2s` backoff、restart-success 继续监督、restart-fail degraded 非 fatal
  - 补测试：`peerProcs/peerPipes/pidRegistry` 一致性、旧 pipe 关闭、继续监督 / degraded-path
- 改：`docs/plans/迁移/p22/P1b_PlatformLoopRunners.md`
  - Findings 3/4 锚点修正
  - 将 “restore 只执行一次” 收窄为 **startup restore 只执行一次**
- 改：`docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md`
  - 新增 `## 需冻结的兼容语义`
- 改：`docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md`
  - 追加 **Finding 10**
  - 补 NestedRuntime tool-read callback -> helper `os.ReadFile(...)` 现状与验收
  - 明确 non-silent-drop 域采用 `lossless pending-set / wake-signal` 或显式 fail-close
- 改：`docs/plans/迁移/p22/P3_OrchestrationWaiterAlignment.md`
  - TDD 补 `internal/archtest` / AST 守卫要求

## 12. Round-3 独立抽样（硬规 5 条）

| # | 抽样项 | 实测 | 结论 |
|---|---|---|---|
| 1 | `lsp_grep spawnToolbridgePeers` | `internal/provider/codexapp/module.go:35`、`peer_spawn.go:18` | ✅ |
| 2 | `lsp_xref references BindRuntime` | prod caller 仍为 `internal/app/app.go:133,154`（另有 `runner_test.go:58`） | ✅ |
| 3 | `lsp_file internal/module/memory/module.go:456-466` | 456-466 仍是 TeamSync start/stop callback 主体 | ✅ |
| 4 | `lsp_file internal/platform/toolbridge/module.go:130-159` | `OnStart` 里 `149-158` 仍 `go ServeProxy(...)` | ✅ |
| 5 | `go test ./internal/archtest/... -count=1 -v` | 全量 archtest **FAIL**，当前 3 条 baseline failure 已落盘 | ✅（事实已记录） |

## 13. 第 3 轮仲裁（Round-5）

- 收报告：**20/20**
- 分布：**🟢 2 / 🟡 12 / 🔴 6**
- 依 §10.18：存在 **🔴 BLOCK**，本轮总体定调仍为 **BLOCK**；但属于动态文档域的真 gap 已全部直修落盘。
- 本轮动态域直修清单：
  1. `README.md`：baseline 更新到 findings `1-10`；`P2/P4/总计` 工时上调；Findings 表追加 `Finding 10`
  2. `P1a_CodexAppPeerSupervisor.md`：Finding 2 锚点修正到 `18-155`；补固定 `2s` backoff、继续监督、degraded-path 测试与 stop/drain 细节
  3. `P1b_PlatformLoopRunners.md`：Findings 3/4 锚点修正；`restore 只执行一次` 收窄为 `startup restore`
  4. `P1c_CodexAppSessionRuntime.md`：新增独立 `## 需冻结的兼容语义`
  5. `P2_BusRuntimeDecoupling.md`：追加 `Finding 10`；补 NestedRuntime tool-read callback -> helper `os.ReadFile(...)`；明确 non-silent-drop 域的 overflow 机制
  6. `P3_OrchestrationWaiterAlignment.md`：TDD 补 `internal/archtest` / AST 一跳 helper 守卫要求
  7. `JUDGEMENT_DYNAMIC.md`：落盘 archtest 全量失败真值、Finding 10、Round-5 仲裁记录
- 交给 Q-S 的 gap：**4 条**
  1. `P4` / 契约仍有命名与术语冲突（`runner.actors` vs `group:"runners"`）
  2. `P4` 的 `NewActiveAgentCounter` hidden contract 仍未记入文档
  3. `P4(thread/turn)` 与 `P2(thread)` 的串行依赖图仍需叙事层写实
  4. `signed-skill / H+O+M` 的跨文档叙事仍未在 P22 侧补明
- 独立抽样证据：见 `## 12`，另补本轮新增 Finding 10 的 prod caller 证据：`internal/module/memory/module.go:436` -> `NestedRuntime.AddToolReadResult(...)`

## 14. 第 4 轮仲裁（Round-7）

- 收报告：**20/20**
- drift 分布：**🟢 2 / 🟡 15 / 🔴 3**
- 依 §10.18：存在 **🔴 BLOCK**，本轮总体定调仍为 **BLOCK**；但属于动态事实域的 drift 已全部直修落盘。
- 本轮新追加 Finding：**0 条**（沿用并复核既有 `Finding 10`）
- 本轮死代码 / 空架子结论：**1 条** test-only helper 继续挂账——`waitDreamTask(...)`
- 契约章节死链修订：**1 条**
  - `README.md` 去掉了对不存在的 `fx-convention.md §4.4` 的直接章节号引用，改为“历史误引”表述。
- 本轮动态域直修清单：
  1. `README.md`：修正 baseline 到 `findings 1-10`；补 `Finding 9/10`；修正 F2-F8 锚点；上调 `P2/P4/总计` 工时；去除死章节号写法
  2. `P0_RuntimeOwnershipSkeleton.md`：补 `TestCodeSizeGuard` 仅代表 size/freeze 的提示；补 runtime 守卫应新增独立 `*_guard_test.go`
  3. `P2_BusRuntimeDecoupling.md`：把 bootstrap `SubscribeHooks` desired-state 现状改成代码真实语义；Step 1 改为 `bounded channel` 或 `lossless pending-set / wake-signal`；补 NestedRuntime tool-read ingest owner 约束
  4. `P3_OrchestrationWaiterAlignment.md`：TDD 明写 `internal/archtest` / AST 一跳 helper 守卫
  5. `P1b_PlatformLoopRunners.md`：补齐 Finding 3/4 的 HEAD 行号；补 toolbridge runtime debt 归口；修正 startup restore 测试口径
- 工时 / LoC 实测：
  - Finding 1-10 锚点文件 LoC 合计：**3152**
  - 当前文档工时口径：**9-16.5 工程日**（多人并行日历时间约 4-6 天）
- 交给 Q-S 的 gap：**3 条**
  1. 契约 / 静态域仍有 `runner.actors` vs `group:"runners"` 术语冲突
  2. `thread/turn` 与 `P2(thread slice)` 的串行依赖叙事仍需静态层写实
  3. `H + O + M` / signed-skill 的跨文档叙事仍属静态域补丁
- 本轮独立抽样证据：
  - F1-F9 锚点实读：见本文件 `## 2` 与本轮 LSP 抽样
  - `spawnToolbridgePeers` xref：`internal/provider/codexapp/module.go:35`
  - `NewTeamSyncService` xref：`internal/module/memory/team/module.go:13`（另有 tests）
  - `NewActiveAgentCounter` xref：`internal/ui/wails/module.go:24`
  - F9 toolbridge xref：`registerProxyLifecycle` <- `internal/platform/toolbridge/module.go:37`
  - `TestCodeSizeGuard`：PASS / **0 violations**
  - 契约章节存在性：`modularity §4.4` / `§7`、`fx §2` / `§3`、`rungroup §2` / `§4` 均经 `lsp_grep` 核实存在
