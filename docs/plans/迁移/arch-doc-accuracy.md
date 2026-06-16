# 架构合规审计：迁移文档准确性

> 日期：2026-03-21
> 范围：`v3-migration-plan.md`、`v3-module-migration-details.md`、`session-summary.md`、`p7-execution-plan-v2.md`、`p8-execution-plan.md`、`p9-execution-plan.md`、`audit-*.md`
> 方法：以 LSP 读取、搜索、符号定位为主；未使用 `grep/find/cat/sed/awk`

## 结论摘要

- `v3-migration-plan.md`：抽查 5 个关键声明，`1` 项基本一致，`4` 项已明显落后于当前代码布局。
- `v3-module-migration-details.md`：25 个编号模块条目中，`8` 项基本一致，`8` 项部分一致，`9` 项尚未落地；另外 `9.A/9.B/9.C` 三个“已废弃”子节与现状一致。
- `session-summary.md`：已过时。其进度口径停留在 `2026-03-20`，而当前代码和计划文档已推进到 `2026-03-21` 状态。
- `p7-execution-plan-v2.md`：修订方向基本与现状一致。
- `p8-execution-plan.md`：方向正确，但“可复用资产”行数已不是当前精确值，且对 `store/prompt` 的描述自相矛盾。
- `p9-execution-plan.md`：工具面拆分方向正确，但文中精确 LOC 统计在仓内没有第二来源可复核。
- 审查报告引用抽查：3 份 `audit-*.md` 中，`2` 份引用仍有效，`1` 份已失效；额外还发现 1 处已失效旧引用。

## 1. `v3-migration-plan.md` 抽查 5 个关键声明

| 声明 | 文档位置 | 代码现状 | 结论 |
|---|---|---|---|
| 三个 MCP 二进制入口都存在 `main.go + fx.go` | `v3-migration-plan.md:197-205` | `cmd/mcp-lsp`、`cmd/mcp-orch`、`cmd/mcp-ida` 当前都已有 `main.go` 和 `fx.go` | 基本一致 |
| `cmd/agent-terminal/` 采用 `main.go + fx.go + app.go + shutdown.go + wails_bindings.go` 布局 | `v3-migration-plan.md:191-196` | 当前 `cmd/agent-terminal/` 只有 `main.go:1-15`；桌面装配已移到 `internal/app/app.go:21-88` 与 `internal/ui/wails/*` | 不一致 |
| 存在 `cmd/migrate/main.go` 入口 | `v3-migration-plan.md:206-207` | 对 `cmd/` 做 LSP 搜索未发现 `migrate` 命中 | 不一致 |
| `internal/app/` 包含 `app.go/modules.go/lifecycle.go/logging.go/runner.go` | `v3-migration-plan.md:210-215` | 当前 `internal/app` 只有 `app.go`、`modules.go`、`runner.go`、`thread_orchestration_adapter.go` | 不一致 |
| `internal/contract` 与 `internal/dto` 按文档树形结构落地 | `v3-migration-plan.md:216-243` | `internal/contract` 当前只有 `approval.go`、`provider.go`、`session_resolver.go`；`internal/dto` 当前实际是 `agent/provider/shared/task/tool/turn/ui/workspace`，缺文档中的 `thread/skill`，反而多了 `provider/tool/ui` | 不一致 |

补充：

- `v3-migration-plan.md:379-421` 仍描述完整 `internal/tool/*` 与 `internal/mcpserver/{lsp,orch,ida}` 树。
- 当前代码中对 `internal/tool` 搜索无命中；`internal/mcpserver` 仅有 `common`，且 `lsp/orch/ida` 包均未发现。
- 结论：`v3-migration-plan.md` 现在更像“目标蓝图”，不能再当成“当前仓库树”。

## 2. `v3-module-migration-details.md` 与实际文件对照

判定口径：

- `基本一致`：目标包已落地，关键职责和主文件基本可对上。
- `部分一致`：目标包已落地，但文件边界、命名或拆分已明显漂移。
- `未落地`：当前代码中未发现该目标包。

### 2.1 25 个编号条目

| # | 模块 | 结论 | 现状说明 |
|---|---|---|---|
| 1 | `module/thread` | 基本一致 | 当前已有 `archive.go/command.go/contract.go/history.go/lifecycle.go/module.go/rpc.go/rpc_types.go/service.go` |
| 2 | `module/turn` | 基本一致 | 当前已有 `assembler.go/contract.go/manifest.go/module.go/rpc.go/rpc_helpers.go/rpc_types.go/service.go/skills.go/tracker.go` 等 |
| 3 | `module/skill` | 基本一致 | 当前已有 `cards.go/exec.go/module.go/rpc.go/service.go/skills_fs.go/skills_match.go` 等 |
| 4 | `module/orchestration` | 基本一致 | 当前已有 `dag.go/events.go/recover.go/report.go/rpc.go/runner_actor.go/service.go/submission.go` 等 |
| 5 | `module/workspace` | 基本一致 | 当前已有 `contract.go/module.go/rpc.go/rpc_types.go/service.go/service_helpers.go/service_merge.go` |
| 6 | `module/uistate` | 未落地 | LSP 未发现 `package uistate` |
| 7 | `module/coderun` | 未落地 | LSP 未发现 `package coderun` |
| 8 | `module/ida` | 未落地 | LSP 未发现 `package ida`（业务模块层） |
| 9 | `module/dashboard` | 未落地 | LSP 未发现 `package dashboard`（业务模块层） |
| 10 | `platform/rpc` | 部分一致 | 包已存在，但当前是 `approval*.go/codec.go/errors*.go/handler.go/module.go/push.go/registry.go/request_context.go/server.go/strict.go/transport_ws.go`，与文档中的 `initialize.go/middleware.go/debug.go` 不同 |
| 11 | `platform/db` | 部分一致 | 当前只有 `errors.go/module.go/pool.go/tx.go`，未见文档中的 `migrate.go/queries.go/query_guard.go` |
| 12 | `platform/bus` | 部分一致 | 包已落地，但实际文件比文档更多，且为 `emitters.go/projection.go/resilient.go/router.go/sink.go/typed.go` 等，不是文档里的精简 5 文件结构 |
| 13 | `platform/statemachine` | 基本一致 | 当前已有 `factory.go/module.go` |
| 14 | `platform/runner` | 部分一致 | 当前只有 `group.go/module.go`，未见文档中的 `lifecycle.go` |
| 15 | `platform/config` | 部分一致 | 当前只有 `config.go/module.go/timeouts.go`，未见文档中的 `provider.go` |
| 16 | `provider/unified` | 部分一致 | 包已落地，但实际是 `client.go/registry.go/session.go/session_adapter.go/session_resolver.go/event_map.go`，与文档中的 `turn.go/mcp_manifest.go/capabilities.go/thread_config.go` 不同 |
| 17 | `provider/claudecli` | 基本一致 | 文档列的 `module.go/driver.go/transport.go/event_map.go/history.go` 均存在，另有 `config.go/session*.go/thread_identity.go` 等辅助文件 |
| 18 | `provider/codexapp` | 基本一致 | 文档列的 `module.go/driver.go/transport.go/event_map.go/recovery.go/history.go` 均存在，另有 `session*.go/support.go/input_map.go` 等 |
| 19 | `store/*` | 部分一致 | 主体已落地，但当前异常包名是 `dbquery/` 而不是文档中的 `rawquery/`；绑定层也统一成 `binding/`，没有文档中隐含的更细分命名 |
| 20 | `tool/lsp` | 未落地 | 当前仓库中未发现 `internal/tool/lsp` |
| 21 | `tool/code` | 未落地 | 当前仓库中未发现 `internal/tool/code` |
| 22 | `tool/orchestration` | 未落地 | 当前仓库中未发现 `internal/tool/orchestration` |
| 23 | `tool/ida` | 未落地 | 当前仓库中未发现 `internal/tool/ida` |
| 24 | `tool/registry` | 未落地 | 当前仓库中未发现 `internal/tool/registry` |
| 25 | `mcpserver/common + mcpserver/lsp + mcpserver/orch + mcpserver/ida` | 部分一致 | 当前只有 `internal/mcpserver/common/{manifest.go,server.go,stdio.go}`；`lsp/orch/ida` family 包未落地，但 `cmd/mcp-lsp`、`cmd/mcp-orch`、`cmd/mcp-ida` 三个二进制壳已存在 |

### 2.2 额外的废弃子节

- `9.A module/core（已废弃）`：与现状一致。当前无 `internal/module/core`。
- `9.B module/config（已废弃）`：与现状一致。当前没有独立 `internal/module/config`，配置相关兼容逻辑仍主要落在 `thread` 与 `platform/config`。
- `9.C module/debug（已废弃）`：与现状一致。当前无 `internal/module/debug`。

### 2.3 对文档性质的判断

- 这份文档目前混合了“目标结构”和“当前结构”。
- 如果它继续承担“当前仓库地图”角色，就会误导后续审查。
- 建议把标题或前言改成“目标包边界设计”，并新增一列 `当前落地状态`。

## 3. `session-summary.md` 是否过时

结论：过时。

主要证据：

- `session-summary.md:3-4` 标注生成时间为 `2026-03-20`，会话范围停在 `P0-P5 波次 1`。
- `session-summary.md:48` 仍写“DAG 方法 TODO 骨架”。当前 `internal/sidecar/orch/orchestration/dag.go:14-79` 已实现 `CreateDAG/GetDAG/ListDAGs/UpdateNodeStatus`，`internal/sidecar/orch/orchestration/rpc.go:61-75` 已接入对应 handler，`orchestration/report` 也已改成 `GetReport` 读路径。
- `session-summary.md:58-63` 仍写 “P5 波次 2 审查+互辩中，P6 入口层待开工，P7 工具层待开工”。当前 `internal/app/modules.go:23-44` 已把 `skill/thread/turn/orchestration/workspace` 装入主应用，`cmd/agent-terminal/main.go:10-14` 已走 `app.RunDesktop()`，`internal/ui/wails/module.go:17-28` 与 `internal/app/app.go:29-50` 已形成桌面路径。
- `session-summary.md:19` 的 `~68%` 覆盖度不是当前 HEAD 的新鲜统计；仓内搜索这个百分比只落在 `session-summary.md` 和较早的 `v2-v3-alignment-report.md`。

建议：

- 把该文件改名为“2026-03-20 会话快照”。
- 若继续保留 `session-summary.md` 作为入口页，应刷新为 `2026-03-21` 状态，并删除已过期的波次状态。

## 4. `p7-execution-plan-v2.md` 修订内容与现状对照

结论：基本一致。

一致点：

- `p7-execution-plan-v2.md:12-17` 将 MCP 编排工具拆到 `P8`、LSP 工具拆到 `P9`、IDA 暂缓，这与当前代码现状一致：
  - `cmd/mcp-orch/fx.go:5-12` 仍为空 `fx.New(...)` 壳；
  - `cmd/mcp-lsp/fx.go:5-12` 仍为空 `fx.New(...)` 壳；
  - 代码中未发现 `internal/module/ida`、`internal/tool/ida`、`internal/mcpserver/ida`。
- `p7-execution-plan-v2.md:21-29` 把 Dashboard/UI State 作为 P7 剩余内容，也与当前代码一致：LSP 未发现 `package uistate` 和 `package dashboard`。
- `p7-execution-plan-v2.md:34-39` 的路线图至少没有被当前代码直接否定；当前仓库可以 `go build ./...` 通过，且桌面入口已存在。

需要修词的点：

- `P0-P6 ✅ 已完成（核心迁移+Wails集成）` 这句话若被理解成“主迁移蓝图中的文件树已经一比一落地”，则会与 `v3-migration-plan.md` 的目录树偏差冲突。
- 建议把这句改成“P0-P6 里程碑代码已落地并可构建，但当前仓库文件布局已偏离原蓝图文档”。

## 5. `p8-execution-plan.md` / `p9-execution-plan.md` 的体量数据

### 5.1 `p8-execution-plan.md`

结论：方向正确，但不是“当前最新精确统计”。

证据：

- 工具面总数 `20` 是对的，且与 `audit-mcp-orch-tools.md:13-16` 的最新审查一致。
- 但 `p8-execution-plan.md:21-24` 的 V3 可复用资产行数已经不是当前精确值：
  - 文档写 `orchestration/rpc.go = 218`，当前末行是 `internal/sidecar/orch/orchestration/rpc.go:218`，即 217 行正文。
  - 文档写 `workspace/rpc.go = 152`，当前末行是 `internal/module/workspace/rpc.go:137`。
  - 文档写 `skill/rpc.go = 103`，当前末行是 `internal/module/skill/rpc.go:102`。
  - 文档写 `skill/cards.go = 200`，当前末行是 `internal/module/skill/cards.go:199`。
- `p8-execution-plan.md:25` 把 `store/prompt` 标成“完整”，但 `p8-execution-plan.md:60` 又写“prompt store 缺 Delete/SetEnabled”。当前 `internal/store/prompt/contract.go:9-14` 也确实只有 `Get/InsertVersion/Upsert/List`，没有 `Delete/SetEnabled`。这说明文档内部自相矛盾。
- 仓内搜索 `1,997`、`4,612` 只命中 `p8-execution-plan.md` 本身，没有独立统计来源文件，因此无法把这些数字认定为“可复核的最新精确统计”。

判定：

- `工具数量`：可信。
- `当前 V3 复用资产行数`：已过时。
- `V2 精确 LOC`：仓内缺少第二来源，无法完全复核。

### 5.2 `p9-execution-plan.md`

结论：工具面拆分正确，但“精确 LOC”缺少仓内可复核来源。

证据：

- `audit-mcp-lsp-tools.md:14` 明确 V2 正式 LSP MCP tool 为 `7` 个，`audit-mcp-lsp-tools.md:43` 额外给出 `code_run` / `code_run_test` 两个工具，因此 `p9-execution-plan.md` 以 `9` 个工具规划拆分是成立的。
- 当前 V3 端确实还是空壳：`cmd/mcp-lsp/fx.go:5-12` 为空 `fx.New(...)`，`internal/mcpserver/common/server.go:8-21` 只有空 server，`manifest.go:3-12` 只有类型定义，`stdio.go:1-3` 只有占位注释。
- 但仓内搜索 `10,876`、`14,143`、`42,994` 只命中 `p9-execution-plan.md`，没有第二份统计文档或原始统计输出；因此无法证明这些数字是“最新精确统计”，只能说它们目前缺少本仓内的可复核来源。

判定：

- `工具面规模判断（7 + 2）`：可信。
- `当前 V3 仍为骨架`：可信。
- `V2 精确 LOC 总量`：在当前仓库内不可独立复核。

## 6. `audit-*.md` 行号引用抽查

### 6.1 抽查结果

| 报告 | 抽查引用 | 当前状态 | 说明 |
|---|---|---|---|
| `audit-provider.md` | `internal/provider/unified/session_resolver.go:34-45` | 有效 | 当前这些行仍是 `GetByThreadID -> 取 AgentID -> sessions.Get(agentID)` 的 canonical thread id 解析路径，支持 `audit-provider.md:102` 的结论 |
| `audit-p2-fix-A.md` | `internal/module/workspace/module.go:9-13` | 有效 | 当前这些行仍通过 Fx 注入 `*bus.WorkspaceEmitters` 并构造 `workspace.Service`，支持 `audit-p2-fix-A.md:15` |
| `audit-p6-fix-A.md` | `internal/ui/wails/module.go:19-27` | 失效 | 当前这些行已经包含 `NewWailsLifecycle`、`NewEventBridge` 以及对应的 `fx.Invoke(bindWailsLifecycle)` / `fx.Invoke(bindEventBridge)`，不再支持 `audit-p6-fix-A.md:22` 当时的判断 |

### 6.2 额外发现

- `audit-p2-fix-A.md:20` 引用的 `internal/module/workspace/service.go:219-221` 也已过时。
- 当前 merge 失败路径已移入 `internal/module/workspace/service_merge.go:32-40,58-96`，并且会转 `statusFailed`、发 `emitRunMergeErrorEvent(...)`；原报告里“conflict/error 提前返回且不发事件”的结论不再成立。

结论：

- 旧 audit 文档不能再被当成“当前代码现状说明”直接引用。
- 至少应为这些报告补一列 `验证日期 / 适用提交`，或在文首标注“历史审查快照”。

## 建议修订清单

1. 把 `v3-migration-plan.md` 和 `v3-module-migration-details.md` 明确标成“目标态文档”，并新增“当前落地状态”列。
2. 将 `session-summary.md` 改为历史快照，或按 `2026-03-21` 代码现状整体刷新。
3. 刷新 `p8-execution-plan.md` 中的 V3 复用资产行数，并修正 `store/prompt` 的“完整”表述。
4. 为 `p9-execution-plan.md` 增加统计来源说明；若 V2 源码不在仓内，应注明“本仓无法复核原始 LOC”。
5. 给 `audit-*.md` 增加“适用时间/提交”标记，避免历史行号继续被当成当前结论引用。
