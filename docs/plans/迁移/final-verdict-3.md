# 最终裁定（3/3）

裁定口径：按 10 份总审报告的“主要发现 / 结论摘要 / 核心结论”逐项复核，共计 46 项。结论以当前仓代码为准，不沿用旧报告的时间点假设。

## 全量编译

build: ✅
vet: ✅
archtest: ✅
diagnostics: ❌

- `go build ./...` 通过。
- `go vet ./...` 通过。
- `go test ./internal/archtest/... -count=1` 通过，输出为 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.100s`。
- `lsp_file(action="diagnostics")` 的全仓输出超出预算，只拿到汇总：workspace 级 diagnostics 总数为 `818`，因此不能判定 diagnostics 全绿；但本次复核涉及的定向文件 diagnostics 为 `0`。

## 总审报告复审

### review-platform-rpc

- ⏳ 推迟 P7 — `Cleanup` / `RestorePending` / `PendingSnapshot` 仍未接线；当前 references 只有各自声明点，见 `internal/platform/rpc/approval_lifecycle.go:10`, `internal/platform/rpc/approval_lifecycle.go:23`, `internal/platform/rpc/approval_lifecycle.go:36`。
- ⏳ 推迟 P7 — handler 注册仍无去重保护；`registerAllHandlers` 直接 `server.Register(...)`，`Server.Register` 用 `maps.Copy` 静默覆盖重复 key，见 `internal/platform/rpc/module.go:47-49`, `internal/platform/rpc/server.go:36-39`。
- ⏳ 推迟 P7 — approval callback 默认 method 仍固定为 `tool/approval/request`，仓内也仍无 `CallbackMethod:` 覆盖点，见 `internal/platform/rpc/approval_events.go:13`, `internal/platform/rpc/approval_events.go:37-39`。
- ⏳ 推迟 P7 — `request_context.go` 仍只提供 `WithCWD/CWDFrom` 且无调用者，ThreadID 仍走 `ThreadScope`，AgentID 仍无 context helper，见 `internal/platform/rpc/request_context.go:7-14`, `internal/platform/rpc/handler.go:44-68`, `internal/platform/rpc/handler.go:98-101`。
- ⏳ 推迟 P7 — `codec.go` 仍是未接线 payload wrapper，不是实际 JSON-RPC codec；运行态仍由 `jrpc2` 启服，见 `internal/platform/rpc/codec.go:3-22`, `internal/platform/rpc/server.go:61`, `internal/platform/rpc/transport_ws.go:34`。

### review-platform-infra

- ⏳ 推迟 P7 — `config` 仍只有环境变量 + 默认值，没有文件层，`LogLevel` 也仍未在配置装载层外扩展，见 `internal/platform/config/config.go:15-40`。
- ⏳ 推迟 P7 — bus 真实业务消费者仍很少；`LogSink` 仍覆盖 28 个 typed event，而非日志消费者主干仍集中在 orchestration 与 rpc push，见 `internal/platform/bus/sink.go:43-87`, `cmd/mcp-orch/orchestration/module.go:25-53`, `internal/platform/rpc/push.go:75-92`。
- ⏳ 推迟 P7 — “听了没人发”的事件族仍成立；`TurnStalled` / `TurnResumed` / `Task*` / `UI*` 仍只在 `LogSink` 订阅，当前全仓对 `TurnStalled{}` / `TurnResumed{}` / `TaskDagCreated{}` / `UIProjectionUpdated{}` 等构造仍是 0 命中，见 `internal/platform/bus/sink.go:51-87`。
- ✅ 已确认 — state machine force fallback 已移除；`fireOrForceLocked` 只做 `fireAndPublishLocked`，失败时返回 `illegal state transition`，见 `cmd/mcp-orch/orchestration/service.go:266-279`。
- ⏳ 推迟 P7 — `TriggerUserInputRequested` / `TriggerUserInputResolved` 仍只存在于状态表声明，没有 fire 点；`StateAwaitingUserInput` 仍不可达，见 `internal/dto/agent/state.go:28-29`, `internal/dto/agent/state.go:96`, `internal/dto/agent/state.go:100`。
- ⏳ 推迟 P7 — DB pool 配置仍然偏薄，仍只硬编码 `MaxConns = 4` 并在 `OnStart` 做 `Ping`，没有把 timeout 配置接入，见 `internal/platform/db/module.go:19-39`, `internal/platform/config/timeouts.go:8-20`。
- ⏳ 推迟 P7 — `RequireNonEmpty` 仍零引用，`internal/platform/shared/idgen.go` 与 `internal/dto/shared/ids.go` 仍保留重复 `NewID` 实现，见 `internal/platform/shared/validation.go:9-14`, `internal/platform/shared/idgen.go:10-14`, `internal/dto/shared/ids.go:10-14`。

### review-module-thread

- 🔴 仍 Blocker — 大量 handler 仍只是 `SendCommand` 骨架；`thread/config/get`、`thread/config/set`、`thread/compact/start`、`thread/realtime/*` 等仍被统一下沉，但 `SendCommand` 仍只真正支持 `/model`、`/personality`、`/approvals`、`/interrupt`，其余直接 `unsupported command`，见 `internal/module/thread/rpc.go:58-82`, `internal/module/thread/command.go:19-37`。
- 🔴 仍 Blocker — RPC shape 仍明显不等价于 V2；例如 `thread/start` 只返回 `StartResult{ThreadID,AgentID}`，`thread/read` / `thread/resolve` 仍复用 `svc.Get`，见 `internal/module/thread/contract.go:28-43`, `internal/module/thread/rpc.go:20-24`, `internal/module/thread/rpc.go:47-50`。
- 🔴 仍 Blocker — lifecycle / history / archive 仍是最小闭环，不是 V2 语义；`ReadHistory` 仍无 RPC 入口，`thread/messages` 仍仅返回 `[]dto.Message`，`Archive/Unarchive` 仍只是状态位 + binding 位切换，见 `internal/module/thread/history.go:13-45`, `internal/module/thread/archive.go:5-20`, `internal/module/thread/lifecycle.go:44-107`。

### review-module-turn

- 🔴 仍 Blocker — `turn/start` / `turn/steer` 的 RPC 参数与返回仍不兼容；`turnStartParams` 仍只有 `prompt/images/files/model/effort`，`turnSteer` 仍直接 `PrepareTurn + StartTurn` 起新 turn，见 `internal/module/turn/rpc_types.go:5-17`, `internal/module/turn/rpc.go:33-57`, `internal/module/turn/service.go:104-110`。
- 🔴 仍 Blocker — `review/start` 仍未实现，且 RPC 参数仍只有 `threadId`，见 `internal/module/turn/rpc.go:74-77`, `internal/module/turn/rpc_types.go:24-26`。
- ✅ 已修复 — 旧报告里“manifest 在 RPC 路径上拿不到 `BinaryDir`，会生成 `/go-agent-mcp-*` 根路径”的断言已失效；当前 `NewService` 已在构造时注入 `resolveBinaryDir()`，`manifestBuilder` 对空 `input.BinaryDir` 会回退到这个默认值，见 `internal/module/turn/service.go:30-45`, `internal/module/turn/manifest.go:17-31`, `internal/dto/provider/manifest.go:36-42`。
- ⏳ 推迟 P7 — `approval/respond` 仍不是 V2 同构 RPC；当前参数是 `callId/requestId/approved/decision`，返回仍是 `nil`，`decision` 也仍直接落入 `ApprovalDecision.Detail`，见 `internal/module/turn/rpc_types.go:28-37`, `internal/module/turn/rpc.go:79-91`。

### review-module-orch

- ✅ 已修复 — 旧报告里“`agent.submit*` 只会入本地队列、submission 内容在 queue 后被丢弃、runner 不做真实执行”这条结论已不成立；`claimTurnWork` 现在保留完整 `submission`，`startTurnExecution` 已调用 `turnStarter.StartTurn(ctx, work.submission)`，而 `orchestrationTurnStarter` 会把 `Inputs/SelectedSkills/OutputSchema` 送入 `PrepareTurn`，见 `cmd/mcp-orch/orchestration/service.go:301-321`, `cmd/mcp-orch/orchestration/helpers.go:140-151`, `internal/module/turn/orchestration_starter.go:22-62`。
- 🔴 仍 Blocker — V2 的 `agent.saveSubAgent` / `agent.deleteSubAgent` / `agent.persistSubAgentBinding` 仍缺失，`agent.launch` 的 wire 仍非 V2；当前 `launchParams` 仍只有 `agentId/name/cwd/command/parentId/env`，见 `cmd/mcp-orch/orchestration/rpc.go:15-76`, `cmd/mcp-orch/orchestration/rpc_types.go:8-17`。
- ⏳ 推迟 P7 — report 链仍是最小内存版；`RememberReportRequest` 仍只是记 requester，`HandleReportEvent` 仍只 drain requester IDs，不做实际投递，`SetReport` 仍只有声明与实现本身，见 `cmd/mcp-orch/orchestration/report.go:73-95`, `cmd/mcp-orch/orchestration/report.go:97-133`, `cmd/mcp-orch/orchestration/contract.go:19`, `cmd/mcp-orch/orchestration/report.go:39-49`。
- 🔴 仍 Blocker — stall auto-recover 仍可能误伤长 turn，且恢复仍是有损的；runner 仍按 30s 阈值轮询，恢复仍直接 `stopProcess -> activeTurnID = "" -> startProcessLocked`，见 `cmd/mcp-orch/orchestration/runner_actor.go:27-44`, `cmd/mcp-orch/orchestration/recover.go:16-25`, `cmd/mcp-orch/orchestration/recover.go:43-54`。
- ⏳ 推迟 P7 — `StopAgent` 的时序问题仍在；当前仍在 waiter 回收前先 `removeSession` 与 `publishAgentStopped`，见 `cmd/mcp-orch/orchestration/service.go:127-140`, `cmd/mcp-orch/orchestration/service.go:155-163`, `cmd/mcp-orch/orchestration/service.go:342-381`。

### review-module-skill

- 🔴 仍 Blocker — `command/exec` 协议仍从 V2 的 `argv + env + cwd` 收缩到 `command + args + cwd`，调用方仍无法 overlay `env`，见 `internal/module/skill/rpc_types.go:26-30`, `internal/module/skill/contract.go:13`, `internal/module/skill/rpc.go:51-53`。
- ⏳ 推迟 P7 — skills 写路径仍无 V2 的 `skills/changed` notify；`collectChangedSkillNames` 仍只有声明点，见 `internal/module/skill/skills_match.go:189-204`。
- ⏳ 推迟 P7 — `skills/config/read` 与 configured auto-match 仍是 stub；当前 `ReadConfig` 仍返回 `binding_source: "stub"`，见 `internal/module/skill/skills_fs.go:143-157`, `internal/module/skill/skills_match.go:59-80`。
- ⏳ 推迟 P7 — 覆盖率问题仍成立；当前复跑 `go test -cover ./internal/module/skill` 仍是 `42.4%`，而零覆盖主区仍集中在 card CRUD / RPC wrapper / remote/local write 路径，对应实现文件见 `internal/module/skill/cards.go:18-111`, `internal/module/skill/rpc.go:42-87`, `internal/module/skill/skills_fs.go:111-179`。

### review-module-workspace

- ✅ 已修复 — `runKey` 路径逃逸已补上；`buildRun` 现在会调用 `validateRunKey`，显式拒绝 `..` 与路径分隔符，见 `internal/module/workspace/service.go:77-90`, `internal/module/workspace/service.go:115-122`。
- 🔴 仍 Blocker — `MergeRun` 仍不是 V2 的真实 merge；`evaluateMergeFile` 仍保留 `TODO: copy workspace content into sourceRoot`，`applyFileUpdates` 仍只做 `UpsertFile`，见 `internal/module/workspace/service_helpers.go:128-148`。
- ✅ 已修复 — “没有 `merging` / `failed` 状态门闩”这条已失效；当前已有 `statusMerging/statusFailed`，`MergeRun` 先 `active -> merging`，失败时再 `merging -> failed`，见 `internal/module/workspace/service.go:29-33`, `internal/module/workspace/service.go:220-240`, `internal/module/workspace/service_merge.go:58-77`。
- 🔴 仍 Blocker — bootstrap 守卫仍弱于 V2；`copyFile` 仍通过 `os.Open + Stat` 跟随 symlink，仍无 `Lstat` / 单文件上限 / 总量上限校验，见 `internal/module/workspace/service_helpers.go:32-50`。V2 仍有 `workspaceRunKeyRe` 与更强 bootstrap 守卫，见 `go-agent-v2/internal/service/workspace.go:176-183`, `go-agent-v2/internal/service/workspace_file_ops.go:98-120`。
- ⏳ 推迟 P7 — `ListRuns` 仍只在 `limit <= 0` 时回退到 200，仍未补 `limit > 5000` 钳制，见 `internal/module/workspace/service.go:193-201`。

### review-provider

- 🔴 仍 Blocker — `claudecli.Configure` 仍只改本地字段，不会把配置应用到已运行 CLI；`Configure` 只写 `s.model/s.config`，而 CLI 参数仍只在 `launchCLI/buildCLIArgs` 启动时生效，`restartIfNeededLocked` 也仍只观察 per-turn overrides 与 manifest，见 `internal/provider/claudecli/session.go:237-255`, `internal/provider/claudecli/transport_config.go:23-76`, `internal/provider/claudecli/session.go:303-340`。
- 🔧 当场修复 — `claudecli` 仍声明 `context_compact`，但 thread 命令通道仍不支持 `/compact`；最小修法是从 capability 声明移除 `dto.CapContextCompact`，或补 `/compact` 实现，见 `internal/provider/claudecli/driver.go:13-17`, `internal/module/thread/rpc.go:63`, `internal/module/thread/command.go:19-37`。
- ⏳ 推迟 P7 — `turn_override` 仍是“实现了但不可达”；`applyTurnSettingsLocked` 仍能消费 `req.Overrides`，但 `claudeCapabilities` 仍不声明 `turn_override`，`buildOverrides` 仍会在 capability gate 前把 override 裁掉，见 `internal/provider/claudecli/session.go:333-340`, `internal/provider/claudecli/driver.go:13-17`, `internal/module/turn/service.go:252-264`。
- ⏳ 推迟 P7 — `SessionResolver` 仍不是唯一 `threadID -> session` 入口；turn/rpc 与 capability gate 用 resolver，但 `module/thread` 仍走自己的 binding + session provider 路径，见 `internal/provider/unified/session_resolver.go:23-46`, `internal/module/turn/rpc.go:20-29`, `internal/platform/rpc/handler.go:20-30`, `internal/module/thread/service.go:213-241`。
- ⏳ 推迟 P7 — `ReadHistory` metadata 保真问题仍在；Claude 路径仍只回 `Role/Content/Timestamp`，Codex 本地 rollout 路径仍不抽 metadata，只在 RPC fallback 时保留 `Message.Metadata`，见 `internal/provider/claudecli/session_history.go:35-43`, `internal/provider/claudecli/history.go:103-134`, `internal/provider/codexapp/history_rollout.go:52-73`, `internal/provider/codexapp/session_history.go:28-36`, `go-agent-v2/legacy-agentsdk/claude/history_backend.go:164-180`, `go-agent-v2/legacy-agentsdk/codex/rollout_reader.go:216-239`。
- ⏳ 推迟 P7 — `codexapp/recovery.go` 仍未接线；`session` 仍只持有 `recovery` 字段，没有任何 `CheckHealth/Connect` 调用点，见 `internal/provider/codexapp/session.go:82-87`, `internal/provider/codexapp/recovery.go:18-44`, `internal/provider/codexapp/session.go:18-32`。

### review-store

- 🔴 仍 Blocker — `sqlc` 生成层与 SQL 源的漂移仍未修；SQL 源仍只有 5 个 agent binding query，而生成层与 `binding.Store.SetArchived` 仍依赖 `UpdateAgentProviderBindingArchived`，见 `sql/queries/agent_provider_binding.sql:1-31`, `internal/store/sqlc/query_agent_binding.go:5-38`, `internal/store/binding/store.go:66-72`。
- ⏳ 推迟 P7 — V2 的独立 `AgentThreadBindingStore` 仍未以独立 repo 落地；当前顶层 store 聚合仍只有 `binding` 与 `thread`，没有 `threadbinding`/`AgentThreadBinding` 面，见 `internal/store/module.go:28-49`。
- ⏳ 推迟 P7 — `dbquery` 仍是 placeholder，不是 V2 `DBQueryStore.Query` 等价迁移；当前 contract 仍只有 `Placeholder`，SQL 仍写明“typed placeholder”，见 `internal/store/dbquery/contract.go:5-11`, `internal/store/dbquery/store.go:15-24`, `sql/queries/db_query.sql:1-8`。

### review-contract-dto-app

- ✅ 已修复 — B5 的 3 个悬空接口已清理；当前 `internal/contract` 只剩 `Driver/Session/TurnHandle`、`ApprovalResponder`、`SessionResolver` 这 5 个接口面，见 `internal/contract/provider.go:10-45`, `internal/contract/approval.go:7-16`, `internal/contract/session_resolver.go:5-7`。
- ⏳ 推迟 P7 — `EventHeader` 体系仍不是“9 层零重复”；当前仍是 12 个 header struct，且 `ThreadID`/`DagKey` 等字段仍跨分支重复，见 `internal/dto/shared/event.go:42-114`。
- ⏳ 推迟 P7 — JSON tag 仍以 lowerCamelCase 为主，不是 snake_case；这类 DTO 已广泛扩散到 provider/turn/task/workspace 面，宜渐进迁移而不是一次性全量改 wire contract，见 `internal/dto/provider/session.go:5-18`, `internal/dto/provider/turn.go:9-49`, `internal/dto/turn/model.go:11-19`, `internal/dto/task/event.go:6-35`, `internal/dto/workspace/event.go:6-45`。
- ✅ 已修复 — desktop/headless 双入口已经落地；当前 `RunDesktop()` 与 Wails app 已接线，终端入口也已切到 `app.RunDesktop()`，见 `internal/app/app.go:30-124`, `cmd/agent-terminal/main.go:10-14`。

#### 覆盖率补充（P5 RPC 同名覆盖率）

- 当前 V3 的 80 个 handler key 来自 `cmd/mcp-orch/orchestration/rpc.go:15-76`, `internal/module/skill/rpc.go:42-87`, `internal/module/thread/rpc.go:18-83`, `internal/module/turn/rpc.go:32-92`, `internal/module/workspace/rpc.go:13-23`。
- V2 当前快照仍是 `go-agent-v2/internal/guards/rpc_registry_snapshot.json:1-156` 的 154 个 method。
- 逐项比对后，同名命中仍是 `64/154 = 41.56%`，缺失仍有 90 个。主要缺口如下：
- `ui`（14）— `ui/code/locate`[L134], `ui/code/open`[L135], `ui/code/save`[L136], `ui/dashboard/get`[L137], `ui/log`[L138], `ui/preferences/get`[L139], `ui/preferences/getAll`[L140], `ui/preferences/set`[L141], `ui/projects/add`[L142], `ui/projects/get`[L143], `ui/projects/remove`[L144], `ui/projects/setActive`[L145], `ui/sidebar/get`[L146], `ui/state/get`[L147]。
- `dashboard`（12）— `dashboard/agentStatus`[L33], `dashboard/aiLogs`[L34], `dashboard/auditLogs`[L35], `dashboard/busLogs`[L36], `dashboard/commandCards`[L37], `dashboard/dagDetail`[L38], `dashboard/dags`[L39], `dashboard/prompts`[L40], `dashboard/sharedFiles`[L41], `dashboard/skills`[L42], `dashboard/taskAcks`[L43], `dashboard/taskTraces`[L44]。
- `config`（7）— `config/batchWrite`[L25], `config/lspPromptHint/read`[L26], `config/lspPromptHint/write`[L27], `config/mcpServer/reload`[L28], `config/read`[L29], `config/read-all`[L30], `config/value/write`[L31]。
- `account`（5）— `account/login/cancel`[L2], `account/login/start`[L3], `account/logout`[L4], `account/rateLimits/read`[L5], `account/read`[L6]。
- `lsp`（5）— `lsp/gui_file`[L65], `lsp/gui_grep`[L66], `lsp/gui_inspect`[L67], `lsp/gui_structure`[L68], `lsp/gui_xref`[L69]。
- `fuzzyFileSearch`（4）— `fuzzyFileSearch`[L52], `fuzzyFileSearch/sessionStart`[L53], `fuzzyFileSearch/sessionStop`[L54], `fuzzyFileSearch/sessionUpdate`[L55]。
- `agent`（3）— `agent.deleteSubAgent`[L9], `agent.persistSubAgentBinding`[L14], `agent.saveSubAgent`[L17]。
- `log`（3）— `log/filters`[L62], `log/list`[L63], `log/relay`[L64]。
- `prompts`（3）— `prompts/delete`[L81], `prompts/list`[L82], `prompts/write`[L83]。
- `debug`（2）— `debug/gc`[L45], `debug/runtime`[L46]。
- `externalAgentConfig`（2）— `externalAgentConfig/detect`[L49], `externalAgentConfig/import`[L50]。
- `inbox-items`（2）— `inbox-items`[L57], `inbox-items/get`[L58]。
- `tasks`（2）— `tasks/get`[L99], `tasks/list`[L100]。
- 单项缺失（各 1）— `agent-agents-md`[L7], `agent-home`[L8], `app/list`[L21], `collaborationMode/list`[L23], `configRequirements/read`[L32], `diff/get`[L47], `experimentalFeature/list`[L48], `feedback/upload`[L51], `git-origins`[L56], `initialize`[L59], `initialized`[L60], `local-environments/list`[L61], `lsp_diagnostics_query`[L70], `mcp-servers`[L71], `mcp/status`[L72], `mcpServer/oauth/login`[L73], `mcpServerStatus/list`[L74], `ml-interceptor/status`[L75], `mock/experimentalMethod`[L76], `model/list`[L77], `open-in-targets`[L78], `pending-automation-runs`[L79], `platform-info`[L80], `windowsSandbox/setupStart`[L148], `workspace-root-options`[L149], `worktrees/list`[L155]。

## 裁定汇总

| 总问题数 | ✅已修 | ⏳推迟P7 | 🔧当场修 | 🔴仍Blocker |
| ---: | ---: | ---: | ---: | ---: |
| 46 | 7 | 26 | 1 | 12 |

## 最终判断

- 当前仓已经达到“可编译、可跑基础 archtest、部分旧报告已被后续提交修掉”的状态，但离“10 份报告全部清零”还有明显距离。
- 真正仍会卡迁移 gate 的主阻断集中在 7 个面：`module/thread` 的 stub handler 与 wire 不兼容、`module/turn` 的 `turn/start|steer|review/start` 缺口、`module/orchestration` 的 V2 method 缺失与 stall recover 风险、`module/skill` 的 `command/exec` 协议漂移、`module/workspace` 的真实 merge I/O 缺失与 bootstrap 守卫薄弱、`provider/claudecli.Configure` 不生效、以及 `store/sqlc` 与 SQL 源漂移。
- 旧报告里已明确过期、可从清单中剔除的结论至少有 7 条：`RunDesktop` 缺失、3 个悬空 contract 接口、turn manifest `BinaryDir` 根路径问题、workspace `runKey` 路径逃逸、workspace 缺少 `merging/failed` 门闩、以及 orchestration “submission 在 queue 后丢失”的旧结论。

## 互审

### 对 final-verdict-1 的批判

1. `B1 submit 执行链：✅修复完成` 的口径偏满。[docs/plans/迁移/final-verdict-1.md:5-10] 只证明了 queue 到 `turnStarter` 的执行链打通，但当前 RPC 入口 `submitParams` 仍只接 `agent_id/prompt/images/files`，`submissionFromParams` 也仍只填 `AgentID/ThreadID/Inputs`，没有把 `SelectedSkills`、`ManualSkillSelection`、`OutputSchema` 从 `agent.submit*` 带进执行链；这些字段虽然在 `TurnSubmission` 和 `orchestrationTurnStarter` 中会被保留，但 RPC 根本不生产它们。证据：`docs/plans/迁移/final-verdict-1.md:5-10`，`cmd/mcp-orch/orchestration/rpc_types.go:70-77`，`cmd/mcp-orch/orchestration/rpc.go:90-100`，`internal/dto/turn/model.go:11-19`，`internal/module/turn/orchestration_starter.go:54-60`。

2. 它漏掉了 `review-platform-rpc` 里更具体也更危险的 approval dedupe 问题。[docs/plans/迁移/final-verdict-1.md:21-32] 列了 10 条 rpc 结论，但没有提原总审已明确指出的“pending approval 只按 `callID` 去重”。当前 `registerPending` 仍在 `m.pending[req.CallID]` 命中时直接复用旧 pending，这会在 `callID` 复用时把后续请求并到旧审批上。证据：`docs/plans/迁移/review-platform-rpc.md:181`，`internal/platform/rpc/approval.go:125-146`。

3. `turn/interrupt` 被下沉到 `⏳ 推迟 P7` 的理由站不住。[docs/plans/迁移/final-verdict-1.md:64-66] 已承认当前 handler 成功仍返回 `nil`；而 V2 schema 合同明确期望 `{"ok": true}`，这不是内部实现差异，而是外部 wire contract 回归。把这种 API shape 断裂放到 P7，尺度明显偏松。证据：`docs/plans/迁移/final-verdict-1.md:64-66`，`internal/module/turn/rpc.go:60-65`，`docs/plans/迁移/review-module-turn.md:81-89`，`go-agent-v2/internal/apiserver/methods_schema_contract_n_z_test.go:304`。

4. 它把 runner 提前 `nil` 返回的风险标成 `🔧 当场修复`，级别偏轻。[docs/plans/迁移/final-verdict-1.md:41-42] 自己已经点到了问题，但当前实现里任一 runner 返回都会 cancel 全组，而 app 侧只在 `err != nil` 时才触发 shutdown；也就是说 runner 静默返回 `nil` 会拖停 runtime，却不触发进程级退出。这更像可靠性 blocker，而不是简单代码整理。证据：`docs/plans/迁移/final-verdict-1.md:41-42`，`internal/platform/runner/group.go:22-37`，`internal/platform/runner/group.go:66-74`，`internal/app/runner.go:37-50`。

### 对 final-verdict-2 的批判

1. `agent.submit*` 的 `✅ 已修复` 仍然高估了现状。[docs/plans/迁移/final-verdict-2.md:29-30] 甚至写到 `SelectedSkills` 和 `OutputSchema` 会继续传入 turn 准备阶段；但当前 `submitParams` 根本没有这几个字段，`submissionFromParams` 也只填 `AgentID/ThreadID/Inputs`。所以修好的只是“排队后的 turn 能真正启动”，不是“V2 提交负载被完整保留”。证据：`docs/plans/迁移/final-verdict-2.md:29-30`，`cmd/mcp-orch/orchestration/rpc_types.go:70-77`，`cmd/mcp-orch/orchestration/rpc.go:90-100`，`internal/module/turn/orchestration_starter.go:54-60`。

2. `B5 悬空接口：✅` 的证据链不够严谨。[docs/plans/迁移/final-verdict-2.md:21-23] 声称 `ToolCallResponder`、`ThreadRepository`、`HandlerProvider` 零命中，却引用了 `cmd/mcp-orch/orchestration/contract.go` 和 `internal/module/workspace/contract.go`；这些文件只能说明两个模块各自还有 contract，不能直接证明那 3 个 `internal/contract` 接口已经删除或清空。真正的直接证据应落在当前 `internal/contract` 包只剩 `Driver/Session/TurnHandle`、`ApprovalResponder`、`SessionResolver`。证据：`docs/plans/迁移/final-verdict-2.md:21-23`，`internal/contract/provider.go:10-45`，`internal/contract/approval.go:7-16`，`internal/contract/session_resolver.go:5-7`。

3. `MergeRun(dryRun)` 被直接归为 `⏳ 推迟 P7`，理由偏弱。[docs/plans/迁移/final-verdict-2.md:87-88] 已承认 dry-run 会在状态迁移和事件发送前直接返回；当前代码也确实如此，`MergeRun` 在 `req.DryRun` 时直接走 `dryRunMerge`，而 `dryRunMerge` 只算结果并原样返回 `run.Status`，既不迁移状态，也不发 typed event。对外观察者来说，这不是“小尾巴”，而是“调用发生了但系统完全无感知”的行为缺口。证据：`docs/plans/迁移/final-verdict-2.md:87-88`，`internal/module/workspace/service.go:220-227`，`internal/module/workspace/service.go:325-339`。

4. 它对 `module/skill` 覆盖率的裁定没有做当前验证。[docs/plans/迁移/final-verdict-2.md:67-68] 明确写了“没有重新跑覆盖率；因此沿用报告结论”。对一份“最终裁定”来说，这意味着该项不是基于当前仓状态得出的结论，而是直接复述旧报告；至少在程序性上，这一段证据强度明显弱于同文件其他基于当前代码的裁定。证据：`docs/plans/迁移/final-verdict-2.md:67-68`。
