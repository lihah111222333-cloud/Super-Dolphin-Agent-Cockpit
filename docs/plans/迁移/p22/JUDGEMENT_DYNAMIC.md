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
  - `README.md` 去掉了对不存在的 `fx-convention` 章节号的直接引用，改为“历史误引”表述。
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
  - `TeamSyncService` xref：`internal/module/memory/team/module.go:18`、`internal/module/memory/module.go:165`（另有 tests）
  - `NewActiveAgentCounter` xref：`internal/ui/wails/module.go:24`
  - F9 toolbridge xref：`registerProxyLifecycle` <- `internal/platform/toolbridge/module.go:37`
  - `TestCodeSizeGuard`：PASS / **0 violations**
  - 契约章节存在性：`modularity §4.4` / `§7`、`fx §2` / `§3`、`rungroup §2` / `§4` 均经 `lsp_grep` 核实存在

## 15. 第 5 轮仲裁（Q-F 独立仲裁）

- 收报告：**19/20**
  - 缺失：`R1=agent-1776902029011-1776902029008690000`；`orchestration_list_agents` 未见该 agent，`get_agent_report` 返回空，按缺失/超时记账
- 分布：**🟢 2 / 🟡 16 / 🔴 1 / ⚪ 1**
  - `R19` 为无颜色 fact-only 报告，本轮按 🟡 计入
- 依 §10.18：存在 **🔴 BLOCK**，且全量 archtest 仍 FAIL，因此本轮总体定调继续为 **BLOCK**
- 新 Finding：**0 条**
- 契约死链修正：**1 条**（`JUDGEMENT_STATIC.md` 中把已失效的 README 死链遗留项改成“已修正，不再记为 live drift”）
- 工时 / LoC 实测：
  - Findings 1-10 锚点文件 LoC 合计：**3152**
  - 当前工时口径仍为：**9-16.5 工程日（多人并行日历时间约 4-6 天）**
- 本轮独立抽样（按 §10.32 硬规亲跑）：
  1. F1-F9 锚点逐条 `lsp_file` 实读：`module.go:35`、`peer_spawn.go:18-155`、`mcpcontrol/module.go:184-199`、`rpc/module.go:149-166 + 179-197`、`memory/module.go:456-467`、`team_sync_watcher.go:72-79`、`auto_dream_task.go:156-178`、`process_lifecycle.go:220-239`、`toolbridge/module.go:130-159`
  2. `lsp_xref(references)`：`spawnToolbridgePeers <- module.go:35`；`TeamSyncService <- team/module.go:18, memory/module.go:165`；`NewActiveAgentCounter <- ui/wails/module.go:24`；`registerProxyLifecycle <- toolbridge/module.go:37`
  3. `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v`：**PASS / 0 violations**
  4. `go test ./internal/archtest/... -v -count=1`：**FAIL / 3 violation entries / 2 unique files**
     - `internal/module/memory/ui_rpc.go`：`rule2_module_impls_no_fx` + `rule10_fx_import_scope`
     - `internal/module/prompt/classifier/claude_cli.go:59`：`TestTimeoutLocality`
  5. 四处 root bridge 函数体完整核读：
     - `internal/app/runner.go:34-87`
     - `cmd/mcp-orch/runtime.go:225-262`
     - `cmd/mcp-lsp/fx.go:203-237`
     - `cmd/mcp-ida/fx.go:99-133`
  6. 契约章节存在性复核：`modularity §4.4 / §7`、`fx §2 / §3`、`rungroup §2 / §4` 全部存在
- 本轮直修落盘：
  1. `README.md`：新增 `## archtest / 守卫现状数字`、`## 并行度矩阵（数字）`、`## 关键路径图（节点数字）`
  2. `P0_RuntimeOwnershipSkeleton.md`：补当前 archtest 真值、现有 guard 清单、`freeze_registry` 的 numeric-only 语义
  3. `P1a_CodexAppPeerSupervisor.md`：补启动早期误判 shutdown 与 stop 窗口晚到 spawn 两条竞态事实，以及对应验收项
  4. `P1b_PlatformLoopRunners.md`：把 `Sweeper.Run` 收窄为 `timer+jitter` loop，并补 `OnStop` 仍无 join 的事实
  5. `P1c_CodexAppSessionRuntime.md`：新增 `## 现状校准` / `## 实施步骤`，并把 shutdown / recovery / helper-wait 写进验收标准
  6. `P4_DependencyDirectionAndHiddenContracts.md`：补 `ida` family “路径可见、常规分类不可达”的事实
- 冲突调和：
  - 与静态叙事版无实质冲突；本轮只把并行度落到数字：`P2` 峰值 **5 lanes**，`P4` 第一批峰值 **2 lanes**
  - `TestCodeSizeGuard = PASS` 与 “全量 archtest = FAIL” 并不冲突；前者只证明 numeric budget/freeze 过关，不能代替 runtime/dependency 真值

## 16. 承接 Judge-S §12.3 的事实补抽样（Q-F / 主 agent）

- 覆盖范围：补 Judge-S `§12.3` 留给 `Q-F` 的 README↔代码抽样、archtest baseline、契约章节存在性、merge gate 现况。
- README↔代码整体抽样：
  - 4 处 root runtime bridge 仍在 `internal/app/runner.go:34-87`、`cmd/mcp-orch/runtime.go:225-262`、`cmd/mcp-lsp/fx.go:203-237`、`cmd/mcp-ida/fx.go:99-133`
  - 对应 `platformrunner.RunGroup(...)` 调用仍在 `internal/app/runner.go:52`、`cmd/mcp-orch/runtime.go:238`、`cmd/mcp-lsp/fx.go:213`、`cmd/mcp-ida/fx.go:109`
  - 结论：`README.md` 当前关于“4 处 root runtime bridge”“app/orch 双树同构 + lsp/ida runner-only sidecar”的叙事，与 HEAD 代码仍一致。
- archtest baseline（2026-04-23 guarded 实测）：
  - `./scripts/go_with_guard.sh test ./internal/archtest/... -count=1 -v`：**FAIL**
  - 当前仍是 **3 条 baseline failure**：
    1. `TestDependencyDirection/rule2_module_impls_no_fx`：`internal/module/memory/ui_rpc.go imports go.uber.org/fx outside module.go`
    2. `TestDependencyDirection/rule10_fx_import_scope`：`internal/module/memory/ui_rpc.go imports go.uber.org/fx outside an assembly entry`
    3. `TestTimeoutLocality`：`internal/module/prompt/classifier/claude_cli.go:59 uses context.WithTimeout outside platform/config/timeouts.go`
  - `TestCodeSizeGuard` 仍为 **PASS / 0 violations**；可推知本轮 README 新增的 `P0` guard skeleton / allowlist 叙事尚未改变代码基线，只是把实施时机写实。
- 契约章节存在性补核：
  - `docs/契约/modularity-convention.md`：`§4.4` 在行 `555`，`§7` 在行 `781`
  - `docs/契约/fx-convention.md`：`§2` 在行 `17`，`§3` 在行 `121`
  - `docs/契约/rungroup-convention.md`：`§2` 在行 `17`，`§4` 在行 `129`
  - 结论：本轮 README 顶部引用口径有效；当前仍应把“章节存在性”与“契约本体命名债是否已收口”分开判断。
- merge gate / commit 级现况：
  - 本次恢复上下文时，工作树未提交改动仅落在 `docs/plans/迁移/p22/README.md` 与 `docs/plans/迁移/p22/JUDGEMENT_STATIC.md`
  - 结合本节补抽样，结论应是：**本轮只是文档层改判与事实补记，不构成 `P0`/archtest 守卫已可直接开工或先合 main 的代码级放行**
- 本节裁决：
  - Judge-S `§12.3` 的 5 条事实 gap 已补抽样落盘
  - 动态域总体判定 **不变**：archtest baseline 仍有 `3` 条 live failure，repo-level 契约命名债也仍 deferred，因此总判定继续保持 **BLOCK**

## 17. 第 6 轮仲裁（Round-9 / Q-F 延续）

- 收报告：**20/20**
- 分布：**🟢 2 / 🟡 15 / 🔴 3**
  - 🟢：`R14`、`R17`
  - 🔴：`R1`、`R7`、`R9`
- 依 §10.18：存在 **🔴 BLOCK**，本轮总体定调继续为 **BLOCK**
- Findings 行号 drift 修订：**1 条**
  - `P1c` 把 `connection.dead` 链路从误写的 `recovery.go:242-250` 修正为 `session_approval.go:242-250 -> recovery.go:102-120`
- 并行度矩阵数字终态：**✅**
  - `P2` 峰值并行度：**5 lanes**
  - `P2(memory 主切片) + P3`：**2 lanes**
  - `P4` 首波并行度：**2 lanes**
- critical path 节点数字终态：**✅**
  - 主 critical path 仍为 **5 节点**
- archtest / build 真值（本轮独立抽样）：
  - `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v`：**PASS / 0 violations**
  - `go build ./...`：**PASS**
  - `internal/archtest/` 当前统计：**19 files / 15 *_test.go**
  - runtime-specific guard 文件仍为 **0**：`fx_invoke_guard_test.go`、`lifecycle_onstart_guard_test.go`、`bus_callback_guard_test.go`、`runner_actor_guard_test.go` 均未落地
  - repo baseline failure 仍为 **3**
    1. `internal/module/memory/ui_rpc.go`：`rule2_module_impls_no_fx`
    2. `internal/module/memory/ui_rpc.go`：`rule10_fx_import_scope`
    3. `internal/module/prompt/classifier/claude_cli.go:59`：`TestTimeoutLocality`
- 本轮独立抽样（§10.32 硬规）：
  1. F1-F9 锚点实读：`module.go:35`、`peer_spawn.go:18-155`、`mcpcontrol/module.go:184-199`、`rpc/module.go:149-166 + 179-197`、`memory/module.go:456-467`、`team_sync_watcher.go:72-79`、`auto_dream_task.go:156-178`、`process_lifecycle.go:220-239`、`toolbridge/module.go:130-159`
  2. `lsp_xref(references)`：`spawnToolbridgePeers <- module.go:35`；`TeamSyncService <- team/module.go:18, memory/module.go:165`；`NewActiveAgentCounter <- ui/wails/module.go:24`；`registerProxyLifecycle <- toolbridge/module.go:37`
  3. `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v`：PASS
  4. `go build ./...`：PASS
- 本轮事实层直修：**6 条**
  1. `README.md`：补 `go build ./... = PASS`
  2. `P1b_PlatformLoopRunners.md`：补 `rpc` runner producer 接线形状；补 startup-fatal / warn-only / sweeper 三段状态迁移测试与验收
  3. `P1c_CodexAppSessionRuntime.md`：修正 `connection.dead` 真实链路锚点
  4. `P3_OrchestrationWaiterAlignment.md`：补 `service_launcher_bridge.go:195-198` 旁路、`claimMonitorTargets()` 与 `monitoredSeq/lastExitedSeq` 清理、以及 root wiring ripple
  5. `P4_DependencyDirectionAndHiddenContracts.md`：补 `NewActiveAgentCounter`、`thread/turn`、`gopls/bootstrap` 的精确 `file:line`
  6. `JUDGEMENT_DYNAMIC.md`：追加 Round-9 仲裁记录
- 交给 Q-E：**5 条**
  1. `README` 节点级出口条件仍偏形态描述，缺 `Finding -> 节点` 显式映射与 merge/test gate 叙事
  2. `P0` phased rollout / semantic allowlist / 4 guard 落地顺序仍需静态层写成可直接派工的实施蓝图
  3. `P4` 五子域 pairwise 矩阵与 codemap debt banner 同步仍未完成
  4. `P2` 内部 `cachekeepalive` 的 lane 归属在 README / P2 叙事之间仍需静态层统一
  5. `README` 级总体验收与 `P21 / session-summary` 交接矩阵仍不够操作化
- 实施就绪度建议：**BLOCK**
  - 理由：虽然 `go build ./...` 与 `TestCodeSizeGuard` 已绿，但 Round-9 仍有 `3` 路 BLOCK 审查，且 repo baseline 仍有 `3` 条 live archtest failure；当前只到“文档与事实更完整”，还没到“可直接放行实施”

## 18. 第 7 轮仲裁（Round-11 / Q-F 延续）

- 收报告：**20/20**
- 分布：**🟢 2 / 🟡 9 / 🔴 9**
  - 🟢：`R19`、`R20`
  - 🔴：`R1`、`R3`、`R4`、`R5`、`R6`、`R9`、`R10`、`R13`、`R18`
- 依 §10.18：存在 **🔴 BLOCK**，本轮总体定调继续为 **BLOCK**
- Findings 行号 drift：**0 条**
  - F1-F9 再次 `lsp_file` 实读后，当前锚点仍稳定在 `35` / `18-155` / `184-199` / `149-166 + 179-197` / `456-467` / `72-79` / `156-178` / `220-239` / `130-159`
- 并行度矩阵数字终态：**✅**
  - `P2` 峰值并行度仍为 **5 lanes**
  - `P2` 内部 scope→lane 数字映射已显式落盘：`memory=1`、`thread=2`、`cachekeepalive=2`、`hooks=3`、`config_change=3`、`rpc push/eventsurface=3`、`toolbridge=4`、`gopls+bootstrap=5`
  - `P2(memory 主切片) + P3` 仍为 **2 lanes**；`P3` exit contract 保持 package-local，不经全局 bus
- critical path 节点数字终态：**✅**
  - 主 critical path 仍为 **5 节点**
- archtest 真值：**PASS / 0**
  - `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v`：PASS / 0 violations
  - `go build ./...`：PASS
  - 守卫接入时机口径：**方案 B**（`P0` 先 skeleton/allowlist/helper，具体 guard 随 owning slice 同 PR red-green）
  - repo baseline 仍为 **3** 条 live failure（`ui_rpc.go` 2 条 + `claude_cli.go:59` 1 条）
- 本轮关键空架子 / 非空壳 xref：
  - `spawnToolbridgePeers <- internal/provider/codexapp/module.go:35`
  - `TeamSyncService <- internal/module/memory/team/module.go:18`、`internal/module/memory/module.go:165`
  - `NewActiveAgentCounter <- internal/ui/wails/module.go:24`
  - `registerProxyLifecycle <- internal/platform/toolbridge/module.go:37`
  - `waitForProcessExit <- cmd/mcp-orch/orchestration/helpers.go:173`
  - `waitDreamTask` 仍只有 `_test.go` caller，为 test-only helper
  - `startApprovalCleanupLoop <- internal/platform/rpc/module.go:195`
- 本轮事实层修订：**5 条**
  1. `README.md`：补 `P2` 内部 8 scope × 5 lane 数字映射，修正 `cachekeepalive` lane 漂移
  2. `P0_RuntimeOwnershipSkeleton.md`：把 archtest 接入时机显式标为 **方案 B**
  3. `JUDGEMENT_DYNAMIC.md`：修正文内残留的死章节号表述
  4. `JUDGEMENT_STATIC.md`：修正文内残留的死章节号表述
  5. `P4_DependencyDirectionAndHiddenContracts.md`：补 `NewActiveAgentCounter`、`thread/turn`、`gopls/bootstrap` 的精确 `file:line`
- 交给 Q-E：**4 条**
  1. `README` 虽可排派工顺序，但 merge/test gate 与 `Finding -> 节点` 显式映射仍偏叙事层收口
  2. `P0` 若要把“hard-gate”写成强版本，仍需静态层明确 `P0-only gate vs slice-owned gate` 判定表
  3. `P4` 仍缺 pairwise scope 矩阵与 codemap debt banner 同步
  4. `README` 级总体验收与 `P21 / session-summary` 交接矩阵仍不够操作化
- 实施就绪度（事实层）：**🔴 BLOCK**
- 理由：三轮独立覆盖后仍有 `9` 路 Round-10 环形交叉报告给出 🔴；`P1a/P2(memory)/P2(other)/P3/P4` 的 live code blocker 尚未消失，repo baseline 也仍有 `3` 条 archtest failure

## 19. 第 8 轮 Q-B 元裁决（2026-04-23，对应用户要求的 §15 模板；本文件按 §10.31 续号追加）

### 19.1 收报状态表

| 路 | agent | 轮询结果 | 结论 | 摘要 |
|---|---|---|---|---|
| V9 | `p22-R8-V9-JudgeStatic-selfaudit` | idle | 🟡 | 静态稿有旧轮自证老化，但大部分年轮仍可复测 |
| V10 | `p22-R8-V10-JudgeDynamic-selfaudit` | idle | 🟡 | 动态稿 10/10 硬验证与 §3 漂移表大体仍真；主要 stale 点在 `P2/F10` 与 `Finding 11/12` disposition |
| H5 | `p22-R8-H5-anchor-truth` | idle | 🟡 | `README+P0-P4` 主锚点大多仍对；旧范围漂移集中在 `JUDGEMENT_DYNAMIC` 历史描述层 |
| H7 | `p22-R8-H7-hours-consistency` | idle | 🟡 | 工时 / LoC / archtest 数字一致；`P2` 顶部 findings 漏 `Finding 10` |
| L1 | `p22-R8-L1-claim-vs-reality-grep` | idle | 🔴 | `JUDGEMENT_STATIC` 对死章节号清空的 claim 与当前 HEAD 不完全一致 |
| L5 | `p22-R8-L5-only-add-not-delete` | thinking → thinking → idle | 🟡 | 无 §10.31 实质删减，只有年轮自证漂移 |

- 收报结果：**6/6**
- 轮询说明：`orchestration_list_agents` 3 次调用均因 **34 agents / output too large** 无法完整展示；因此本轮改以 6 个指定 agent 的 `orchestration_get_agent_report(...).state` 做 idle 取报依据。`L5` 在第三次轮询才转 idle，无 `>300s` 超时项。

### 19.2 独立抽样复核结果

- 文档锚点抽样：对 `JUDGEMENT_STATIC` 声称“已命中”的锚点再抽 **14 条**，本轮 `lsp_grep` 结果 **14/14 仍命中**（README/P0/P1b/P1c/P2/P4 关键锚点均存活；详见姐妹裁决 `JUDGEMENT_STATIC.md §15.2`）。
- `JUDGEMENT_DYNAMIC §3` 行号 drift 表抽样 **7 条**，本轮 `lsp_file` 复核 **7/7 一致**：
  - F1 `internal/provider/codexapp/module.go:35`
  - F2 `internal/provider/codexapp/peer_spawn.go:18-155`
  - F4 `internal/platform/rpc/module.go:149-166 + 179-197`
  - F5 `internal/module/memory/module.go:456-467`
  - F7 `internal/module/memory/auto_dream_task.go:156-178`
  - F8 `cmd/mcp-orch/orchestration/process_lifecycle.go:220-239`
  - F10 `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339`
- archtest 真值复核：
  - `go test ./internal/archtest/... -run TestCodeSizeGuard -count=1 -v`：**PASS / 0 violations**
  - `go test ./internal/archtest/... -count=1 -v`：**FAIL / 3 live failures**
  - `internal/archtest/freeze_registry.go:19`：`explicitFreezeRegistry` 仍为空
  - `internal/archtest/guardlib.go:22-32`：numeric 守卫仍为 `600 / 800 / 30 / 10000`
- “旧行应 0 命中”复核：
  - ✅ `JUDGEMENT_DYNAMIC.md`：死章节号宽搜短语 = `0-hit`
  - ✅ `README.md`：`findings 1-8` / `只修 findings` = `0-hit`
  - ❌ `JUDGEMENT_STATIC.md`：R1 当轮仍有旧完整死链串命中；R2 需补到 `0-hit`

### 19.3 claim-vs-reality §10.21 违反清单

1. **`P2_BusRuntimeDecoupling.md` 的 `## 对应 findings` 仍未显式列 `Finding 10`**
   - `lsp_grep "Finding 10" docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md` 当前已命中 `P2_BusRuntimeDecoupling.md:5`；但 `:7-12` 的 `## 对应 findings` 列表仍只列 `5/6/7/9`。
   - authoritative 改判：**已补 F10 语义；顶部清单仍漏 explicit bullet。**
2. **`Finding 11/12` / `pre-drain` / `watchFXShutdown` 的 R1 `0-hit` claim 已失效**
   - R2 authoritative disposition：`Finding 11 = 不升级`；`Finding 12 = 强候选 / deferred`；`pre-drain / watchFXShutdown = live handoff gap`。
   - 因此后续自证只能写 disposition，不再写这些词条在 `JUDGEMENT_DYNAMIC.md` 中 `0-hit`。
3. **R1 对 H-1 的 authoritative 更正 claim 过早**
   - R1 曾写 `JUDGEMENT_STATIC.md` 已修正文内残留死章节号表述；该说法构成 claim-vs-reality 违反。
   - R2 已通过断词改写把 `JUDGEMENT_STATIC.md` 的完整旧串补到 `0-hit`，并在姐妹裁决 `§16.2` 留痕。

### 19.4 §10.31 只加不删合规 self-check

- 本轮以文件末尾续写为主，另含少数历史 truth-correction；未删除 `§1-§18` 历史记录。
- 历史 code-anchor 复核仍成立：
  - `spawnToolbridgePeers <- internal/provider/codexapp/module.go:35`
  - `registerProxyLifecycle <- internal/platform/toolbridge/module.go:37`
  - `waitForProcessExit <- cmd/mcp-orch/orchestration/helpers.go:173`
  - `waitDreamTask` 仍仅 `_test.go` caller
- 当轮 diff / 行数快照已转入后续轮次续更；当前 rerun 见 `§20.6`。
- 若未来要真正改写旧轮 `P2/F10` 文字，需在保留年轮的前提下单独 justify。

### 19.5 行号 drift 表更新

| Finding / 条目 | 历史记录 | 现 HEAD | 结论 |
|---|---|---|---|
| F1 | `internal/provider/codexapp/module.go:35` | `internal/provider/codexapp/module.go:35` | 稳定 |
| F2 | `internal/provider/codexapp/peer_spawn.go:18-155` | `internal/provider/codexapp/peer_spawn.go:18-155` | 稳定 |
| F4 | `internal/platform/rpc/module.go:149-166 + 179-197` | `internal/platform/rpc/module.go:149-166 + 179-197` | 稳定 |
| F5 | `internal/module/memory/module.go:456-467` | `internal/module/memory/module.go:456-467` | 稳定 |
| F7 | `internal/module/memory/auto_dream_task.go:156-178` | `internal/module/memory/auto_dream_task.go:156-178` | 稳定 |
| F8 | `cmd/mcp-orch/orchestration/process_lifecycle.go:220-239` | `cmd/mcp-orch/orchestration/process_lifecycle.go:220-239` | 稳定 |
| F10 | `internal/module/memory/module.go:435-437 + internal/module/memory/nested/nested_runtime.go:314-339` | 同上 | 稳定 |
| `connection.dead` 链路（后续轮修订项） | `session_approval.go:242-250 -> recovery.go:102-120` | 同上 | 稳定 |

### 19.6 交给 Q-A / Q-C / Q-D 的 gap

- **Q-A（README / P0-P4 文档域）**
  1. `P2_BusRuntimeDecoupling.md` 若继续不在顶部列 `Finding 10`，需同步改写 README / `JUDGEMENT_DYNAMIC` 相关表述，避免编号 claim-vs-reality。
  2. `README / P4 / codemap / debt banner / 外部 followup` 的同步仍未闭环。
- **Q-C（契约域）**
  1. `runner.actors` vs `group:"runners"` 仍是 repo-level 契约债。
  2. `fx / bus / run.Group` 三层分工条文仍需继续与 P22 文档统一。
- **Q-D（动态 / 代码事实域）**
  1. archtest baseline 仍有 **3 条** live failure。
  2. `Finding 12` 是否升级、`Finding 11` 是否继续拒绝升级、以及 `pre-drain` / `watchFXShutdown` 最终 disposition，仍待代码事实域定案。
  3. `P1a/P2(memory)/P2(other)/P3/P4` 的 live code blocker 仍未解除；本轮只修 `JUDGEMENT_*` 事实表述，不代签代码 READY。

### 19.7 LSP 自证（Q-B 本轮）

1. `lsp_file internal/provider/codexapp/module.go:32-37` -> `fx.Invoke(spawnToolbridgePeers)` 仍在 `:35`
2. `lsp_file internal/provider/codexapp/peer_spawn.go:18-155` -> F2 现 HEAD 全段仍在
3. `lsp_file internal/platform/rpc/module.go:149-197` -> F4 仍为 `149-166 + 179-197`
4. `lsp_file internal/module/memory/module.go:456-467` -> F5 TeamSync start/stop callback 仍在 `:458/:463`
5. `lsp_file internal/module/memory/auto_dream_task.go:156-178` -> F7 `go func()` 仍在 `:173`
6. `lsp_file cmd/mcp-orch/orchestration/process_lifecycle.go:220-239` -> F8 `go a.waitForExit(...)` 仍在 `:222`
7. `lsp_file internal/module/memory/nested/nested_runtime.go:314-339` -> F10 `os.ReadFile(...)` 仍在 `:320`
8. `lsp_file internal/archtest/freeze_registry.go:19-25` -> `explicitFreezeRegistry = []explicitFreeze{}`
9. `lsp_file internal/archtest/guardlib.go:22-32` -> numeric 守卫常量 `600/800/30/10000`
10. `lsp_grep docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md "Finding 10"` -> `命中 P2:5；## 对应 findings 列表仍缺 explicit bullet`
11. `lsp_grep docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md "NestedRuntime"` -> `P2_BusRuntimeDecoupling.md:5,313`
12. `lsp_file internal/platform/toolbridge/diff_fallback.go:44-61` -> callback 内仍 `resolveCWD/currentGitDiff`
13. `lsp_xref internal/platform/toolbridge/diff_fallback.go:44:31` -> prod caller `internal/platform/toolbridge/module.go:117`
14. `lsp_file internal/platform/toolbridge/module.go:110-128` -> `registerDiffFallbackLifecycle` 仍在 `OnStart` 注册 `tracker.handleToolCallEnd`
15. `lsp_file internal/mcpserver/common/server.go:92-145` -> `Run()` 仍 `go s.readLoop(results)`，但 readLoop 仍是同 owner 私有 helper
16. `lsp_xref cmd/mcp-orch/orchestration/process_lifecycle.go:136:19` -> `waitForProcessExit <- helpers.go:173`
17. `lsp_grep docs/plans/迁移/p22/JUDGEMENT_DYNAMIC.md "Finding 11"` -> `命中本文件“不升级” disposition`
18. `lsp_grep docs/plans/迁移/p22/JUDGEMENT_DYNAMIC.md "Finding 12"` -> `命中本文件“强候选 / deferred” disposition`
19. `lsp_grep docs/plans/迁移/p22/JUDGEMENT_DYNAMIC.md "pre-drain"` -> `命中本文件 handoff gap 记账`
20. `lsp_grep docs/plans/迁移/p22/JUDGEMENT_DYNAMIC.md "watchFXShutdown"` -> `命中本文件 handoff gap 记账`
21. `lsp_grep docs/plans/迁移/p22/JUDGEMENT_STATIC.md "<旧完整死链串>"` -> `R2 后 0-hit`

## 20. 第 8 轮 Q-B Round-2 补修（2026-04-23）

### 20.1 收报状态表
|路|state|结论|要点|
|---|---|---|---|
|V9|idle|🔴|H-1 与 `§15` 自撞需补修|
|V10|idle|🟡|F10 / `0-hit` 自证需改判|
|H5|idle|🟡|旧 stale literals 与新 HEAD drift 需分层记账|
|H7|idle|🟡|数字面统一，但 `P2/F10` 与 `P1b 30min/5min` 仍待 owning 文档收口|
|L1|idle|🔴|H-1 claim-vs-reality 成立|
|L5|thinking（已收 report）|🟡|§10.31 实质违规未见|
- `orchestration_list_agents` 仍因 `34 agents` 超限；本轮以 6 路 `orchestration_get_agent_report(...)` 收报。

### 20.2 H-1 修复证据
- before：`JUDGEMENT_STATIC` 完整旧串 `3-hit`；宽搜短语 `6 matches`。
- after：`JUDGEMENT_STATIC` 完整旧串 `0-hit`；宽搜短语 `0-hit`。
- before/after diff：`§4 line 68` 与 `§6 line 94` 已改为断词表述；`§15/§19` 的旧 claim 改写为“R1 claim 失实 / R2 0-hit 补齐”。
- 认错：R1 chairman 声称 authoritative 更正已达成不属实；R2 已补齐。

### 20.3 R9 销账表
- F10：✅ owning 文档已补齐 `## 对应 findings` explicit bullet，`§19/§20` 中“只补语义、列表未补”的说法降为历史年轮。
- F11：✅ 最终判定 = 不升级。
- F12：✅ 最终判定 = 强候选 / deferred。
- `pre-drain / watchFXShutdown`：⚠️ 仍为 live handoff gap。
- `§19.4` over-claim：✅ 已改成“以末尾追加为主 + 少数 truth-correction”。

### 20.4 本轮其他补修清单
- 改 `§19.2/§19.3/§19.4/§19.7` 的 `0-hit` 自撞与 F10 误报，并跟随 owning 文档补齐 `P2` 顶部/验收/TDD 的 F10 明示。
- 重跑 archtest / freeze / diff self-check 并在本节续记。
- 动态稿不越界代签 Q-A / Q-C 代码 READY。

### 20.5 交其他 Q 的 gap
- Q-A：`README / P4 / codemap / debt banner / 外部 followup` 的外围文档同步仍未闭环。
- Q-C：契约命名债与三层分工条文仍未统一。
- Q-D：repo baseline `3` 条 live archtest failure 仍在；代码 blocker 仍待实现层收口。

### 20.6 §10.31 self-check（净减少 %）
- 当前 `git diff --numstat -- JUDGEMENT_*`：`JUDGEMENT_STATIC +310/-3`、`JUDGEMENT_DYNAMIC +310/-2`。
- 当前行数为 `596 / 559`；两文件仍均 `<600`。
- 历史章节净减少 `0%`；本文件无 `§1-§19` 删除。

### 20.7 LSP 自证 ≥15 条
- `grep`：`P2 Finding 10` 命中 `P2:5,13`；`TestNestedToolReadIngestEnqueueOnly` 命中 `P2:413`；`ToolCallEnd -> AddToolReadResult(...)` 验收项命中 `P2:440`。
- `grep`：`Finding 11/12`、`pre-drain`、`watchFXShutdown` 已在本节写成 disposition / handoff gap，不再写 `0-hit`。
- `file`：`diff_fallback.go:44-61`；`toolbridge/module.go:110-128`；`server.go:92-145`；`process_lifecycle.go:220-239`；`freeze_registry.go:19-25`。
- `xref`：`handleToolCallEnd <- module.go:117`；`waitForProcessExit <- helpers.go:173`；`waitForExit <- startWaiters:222`。
- `inspect/structure/completion/diagnostics`：`diff_fallback.go/server.go` symbols；`DefaultApprovalTimeout` hover；`toolbridge/module.go` completion；`JUDGEMENT_*` diagnostics=`0`。
- `exec` 复核：`TestCodeSizeGuard=PASS/0`；全量 archtest=`FAIL/3`；`freeze_registry` 仍空。

## 21. 第 9 轮 Round-8→HEAD 回灌（2026-04-24）

本节只补 Round-8 之后落地的 P22 P2 事实，不改写历史年轮。按 §10.31 只加不删。

### 21.1 新落地 commit 锚（按 HEAD 逆时序）

| commit | 范围 | 触动文件 |
|---|---|---|
| `8062f91` | memory 域 4 条"既有 owner 行为断言"新增 | `internal/module/memory/module_drain_test.go`、`internal/module/memory/memory_behavioral_guards_test.go` |
| `cdbb8a4` | thread S2：`sessionRecoveryWorker` 收 `onAgentFailed -> 3s delay + evict + resume` | `internal/module/thread/session_recovery_worker{,_test}.go`、`events.go`、`service{,_constructor}.go` |
| `667a2df` | thread S4：`agentLaunchedWorker` 收 `onAgentLaunched -> binding store + prompt invalidation` | `internal/module/thread/agent_launched_worker{,_test}.go`、`events{,_test}.go`、`service{,_constructor}.go` |
| `f8c0fec` | thread S3：`taskHandoffWorker` 收 `onTurnCompleted -> refreshTaskHandoffFromThread` | `internal/module/thread/task_handoff_worker{,_test}.go`、`task_handoff{,_test}.go`、`events.go`、`module.go`、`service{,_constructor}.go` |
| `04e366d` | thread S1：删 `fx.Invoke(registerSubscriptions)` 里的 3 处 setter 注入，`promptStore/classifier` 改构造参数；翻红 `bus_callback_must_not_register_late_setter` | `internal/module/thread/module.go`、`router_resolve.go`、`service_constructor.go`、`internal/archtest/bus_callback_guard_test.go` |

### 21.2 live matcher 计数更新（`internal/archtest/bus_callback_guard_test.go`）

| 变动 | round-8 | HEAD |
|---|---|---|
| live matcher 条数 | 7 | **8** |
| skeleton (skip) matcher 条数 | 1 | **0** |
| 翻红的那一条 | — | `bus_callback_must_not_register_late_setter`（thread S1 对应） |

`grep -c "t.Run(" internal/archtest/bus_callback_guard_test.go` = 6 subtests；其中 forbidden-token subtest 5 条 + 目录 freeze 1 条 = 6。`live / skip` 统计以 `t.Skipf` 是否出现为准：round-8 有 1 处 `t.Skipf`，HEAD `grep -c "t.Skipf" internal/archtest/bus_callback_guard_test.go` = **0**。

### 21.3 已落地行为断言测试清单（`TestXxxCallbackEnqueueOnly` 系 + 延伸）

| 测试名 | 所在文件 | 断言目标（§验收标准锚） |
|---|---|---|
| `TestTaskHandoffCallbackEnqueueOnly` | `internal/module/thread/task_handoff_worker_test.go` | L429 task-handoff 重 I/O 不再直跑 callback |
| `TestAgentLaunchedCallbackEnqueueOnly` | `internal/module/thread/agent_launched_worker_test.go` | L429 binding store / prompt invalidation 不再直跑 callback |
| `TestAgentFailedCallbackEnqueueOnly` | `internal/module/thread/session_recovery_worker_test.go` | L429-430 delayed resume 不再裸 `context.Background()` goroutine；owner 可 drain |
| `TestAgentFailedCallbackDropsNonRecoverable` | `internal/module/thread/session_recovery_worker_test.go` | L430 非 recoverable 事件在 callback 层早返 |
| `TestSessionRecoveryWorkerStopCancelsCtx` | `internal/module/thread/session_recovery_worker_test.go` | L430 Stop 的 `cancel()` 中断 3s reconnect 延迟 |
| `TestSessionRecoveryWorkerDispatchesParallelForDifferentTargets` | `internal/module/thread/session_recovery_worker_test.go` | 多 agent 并发恢复不被 worker 单线程化 |
| `TestMemoryHookWorkerDrainsOnStop` | `internal/module/memory/module_drain_test.go` | L443 `registerMemoryHooks` shutdown 能证明 3 个 owner 都完成 drain；post-drain enqueue drop |
| `TestAutoDreamBusyDropsWithoutReplay` | `internal/module/memory/memory_behavioral_guards_test.go` | L455 busy 状态 drop 是终态，不 replay |
| `TestAutoDreamRequiresExplicitProjectScope` | `internal/module/memory/memory_behavioral_guards_test.go` | L443 `agentMemoryScope` 非空 thread 不走 auto-dream |
| `TestTeamSyncRuntimeSwapFinalFlush` | `internal/module/memory/memory_behavioral_guards_test.go` | L452 TeamSync runtime swap 下 coordinator FIFO + final flush；post-Stop enqueue drop |

round-8 之前已经存在的同形测试不重复列（`TestTeamSyncCallbackEnqueueOnly`、`TestNestedToolReadIngestEnqueueOnly`、`TestHookRelayDrainAfterShutdown`、`TestToolbridgeProxyOwner`、`TestConfigFanoutWorkerUsesCancelableContext`、`TestCacheKeepaliveDrainCancelsPendingPing`、`TestRPCPushQueuePreservesLegacyExpansion`）。

### 21.4 消失的违规形（pre-S1/S2/S3/S4 → HEAD）

四条都在 `internal/module/thread/` 域内，原 §3 drift 表不涉及：

| 违规形 | round-8 位置 | HEAD 状态 |
|---|---|---|
| `fx.Invoke(registerSubscriptions)` 里 `svc.bindDispatcher/bindPromptStore/bindClassifier` setter 型后置注入 | `internal/module/thread/module.go:52-79` | 三处 setter 删除；`promptStore` / `classifier` 移到 `NewServiceWithPromptAssemblyAndSharedFiles` 构造参数；matcher `bus_callback_must_not_register_late_setter` 翻红并 PASS |
| `onAgentLaunched` 直接 `bindingStore.UpdateSessionUUID` + `invalidatePromptAssembly` | `internal/module/thread/events.go:36-67`、`69-95` | callback 只 `agentLaunchedWorker.Enqueue(key, ev)`；原 body 搬到 `processAgentLaunched(ev)` |
| `onAgentFailed` 裸 `runtimesafe.SafeGo(context.Background(), ...)` + `time.Sleep(3 * time.Second)` + 嵌套 SafeGo | `internal/module/thread/events.go:119-153`、`service.go:390-401` | callback 只 `sessionRecoveryWorker.Enqueue(target, ev)`；worker 用 `sync.WaitGroup.inflight` 追踪并发恢复；3s 延迟改 ctx-aware `select` |
| `onTurnCompleted` 同步 `threadStore.GetByThreadID + sharedFiles.Upsert` | `internal/module/thread/task_handoff.go:459-474` | callback 只 `taskHandoffWorker.Enqueue(threadID, seed)`；worker 按 threadID 合并，last-write-wins |

### 21.5 §10.31 only-append self-check

- round-8 末尾：DYNAMIC `559 行` / STATIC `596 行`。
- 本节追加后：`git diff --numstat docs/plans/迁移/p22/JUDGEMENT_DYNAMIC.md` 将显示 `+N/-0`（无删除）。
- §1-§20 未改动；HEAD 事实只在 §21 追加。
- 历史章节净减少保持 `0%`。

### 21.6 LSP / exec 自证锚

- `git log --oneline` HEAD 5 条：`8062f91 / cdbb8a4 / 667a2df / f8c0fec / 04e366d`
- `go test ./internal/archtest/... -run TestBusCallbackGuard -count=1 -v`：`--- PASS` × 6 subtests（含翻红的 `bus_callback_must_not_register_late_setter`）
- `go test ./internal/module/thread/... ./internal/module/memory/... ./internal/app/... -count=1`：全部 `ok`
- `grep -c "t.Skipf" internal/archtest/bus_callback_guard_test.go` = `0`
- `grep -c "runtimesafe.SafeGo" internal/module/thread/events.go` = `0`（round-8 时该文件里出现 1 次 outer SafeGo）
- `grep -c "time.Sleep" internal/module/thread/events.go` = `0`（round-8 时出现 1 次 3s sleep）
- 对应 test 文件均已落盘：`task_handoff_worker_test.go` / `agent_launched_worker_test.go` / `session_recovery_worker_test.go` / `module_drain_test.go` / `memory_behavioral_guards_test.go`

### 21.7 本节不宣称的边界

- `backgroundResumeIfNeeded` 自身仍含 1 处 inner `runtimesafe.SafeGo(...)`，被 `archive.Unarchive` / `history.ReadMessages` / `history.ReadThreadHistory` 3 条 RPC 路径使用。这 3 条 caller 不在 bus callback 路径上，不是 P2 本轮范围。本节仅对 `onAgentFailed -> backgroundResumeIfNeeded` 这一条 bus callback 路径生效。
- `lifecycle_onstart_guard_test.go` / `runner_actor_guard_test.go` 里仍有 6 条 P3 范围外 skeleton matcher。按原规划"非本优先级"。
- P4 dependency direction / contract cleanup 的未收口项未触动。
