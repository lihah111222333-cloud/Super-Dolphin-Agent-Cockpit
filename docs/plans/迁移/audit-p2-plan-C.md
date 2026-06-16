# P2 计划审查 — Agent C

## 1. 批次 C 可行性

### I1 `command/exec` timeout + cwd + env

- **Blocker**：计划把 I1 收敛为 `skill/exec.go` 单文件修改（`docs/plans/迁移/p2-execution-plan.md:50`），但当前 V3 的请求与服务签名都承载不了 V2 语义。`execParams` 只有 `command/args/cwd`，没有 `env`（`internal/module/skill/rpc_types.go:26-30`）；`Service.ExecCommand` 也只有 `command, args, cwd`（`internal/module/skill/contract.go:13`）；`runExecCommand` 仅调用 `exec.CommandContext(ctx, ...)` 并直接设置 `cmd.Dir = strings.TrimSpace(cwd)`，没有超时包裹、没有环境变量注入（`internal/module/skill/exec.go:70-76`）。按当前代码，I1 至少还要改 `skill/rpc_types.go`、`skill/contract.go`，并新增 project-root / env-policy 依赖，不是只改 `skill/exec.go`。
- **V2 对照**：V2 不是在 `context.WithTimeout` 和 `exec.CommandContext` 之间二选一，而是先 `context.WithTimeout(ctx, 30*time.Second)`，再把 `execCtx` 传给 `exec.CommandContext`（`go-agent-v2/internal/apiserver/methods_command.go:55-57`）。cwd fallback 先看请求 `cwd`，为空时回退到 `CurrentProjectCwd(s)`（`go-agent-v2/internal/apiserver/methods_command.go:58-62`），而 `CurrentProjectCwd(s)` 依赖 server 的 `activeProjectCwd` 并做 canonicalize（`go-agent-v2/internal/apiserver/methods_ui_projects.go:89-100`）。env 白名单则在 `os.Environ()` 基础上追加请求里的允许项（`go-agent-v2/internal/apiserver/methods_command.go:63-67`），允许前缀定义在 `configEnvAllowPrefixes`（`go-agent-v2/internal/apiserver/methods_config.go:81-103`）。
- **当前 V3 依赖缺口**：`skill.service` 当前只持有 `cards/root/http/readConfigState`（`internal/module/skill/service.go:18-23`），构造器也只注入 `commandcard.Store`（`internal/module/skill/service.go:27-33`）；`skill.Module` 只 `Provide(NewService/NewSkillHandlers)`，没有 project/config resolver 注入（`internal/module/skill/module.go:5-8`）。结论：I1 可做，但计划低估了改动面。

### I2 `http.Client` timeout

- **OK**：计划将 `NewService` 改成 `http.Client{Timeout: 15*time.Second}`（`docs/plans/迁移/p2-execution-plan.md:51`）是准确且低风险的。当前构造器确实使用无 timeout 的 `&http.Client{}`（`internal/module/skill/service.go:27-32`），`ReadRemote` 直接走 `s.http.Do(req)`（`internal/module/skill/skills_fs.go:111-117`）。
- **V2 对照**：V2 `SkillsRemoteRead` 使用 `&http.Client{Timeout: 15 * time.Second}`（`go-agent-v2/internal/skills/methods.go:437-459`）。当前 V3 的远端读路径只是单次 GET 且正文仍受 1 MiB 上限保护（`internal/module/skill/skills_fs.go:121-129`），因此 15 秒足够作为 parity 方案。

### I3 card 工厂扩展

- **Warning**：计划说“扩展 `cardByKey` helper 覆盖剩余 4 个 card handler”（`docs/plans/迁移/p2-execution-plan.md:52`），但当前 `cardByKey` 只适用于 `key -> svcFn` 这一类 handler（`internal/module/skill/rpc.go:13-15`）。7 个 card handler 中，它只覆盖了 `command/card/get`、`command/card/delete`、`command/card/versions` 三个（`internal/module/skill/rpc.go:21`、`internal/module/skill/rpc.go:28`、`internal/module/skill/rpc.go:30`）。
- **剩余 4 个并非同构**：`command/card/list` 是零参数 handler（`internal/module/skill/rpc.go:20`）；`command/card/create` / `command/card/update` 需要把 params 转成 `Card`，并依赖 `buildCard(cardPayload)`（`internal/module/skill/rpc.go:22-27`、`internal/module/skill/rpc.go:68-80`）；`command/card/run` 需要 `key + args` 组合参数（`internal/module/skill/rpc.go:29`，`internal/module/skill/rpc_types.go:22-25`）。结论：I3 可以做，但不是“把 `cardByKey` 再套 4 次”这么简单；至少需要额外 helper，或接受这 4 个继续异构。

### I4 auto-match TODO

- **OK，但仅是文档准确，不是能力闭合**：计划把 I4 定义为“在 `skill/module.go` 加 TODO，明确当前不是运行时接线”（`docs/plans/迁移/p2-execution-plan.md:53`），这与现状一致。`skill.Module` 只有 `NewService` 和 `NewSkillHandlers`，没有 `fx.Invoke`、没有 event/bus 接线（`internal/module/skill/module.go:5-8`）。
- **当前调用面**：`skills/match/preview` 是 `MatchPreview` 的唯一业务入口（`internal/module/skill/rpc.go:62-63`），`MatchPreview` 内部才创建 auto-match collector（`internal/module/skill/skills_match.go:14-17`），collector 的实现也局限在 `skills_match.go`（`internal/module/skill/skills_match.go:43-57`）。LSP `call_hierarchy` 只看到来自 `NewSkillHandlers` 和测试的入边；没有运行时 event 入口。
- **运行时风险仍在**：`collectChangedSkillNames` 只定义未消费（`internal/module/skill/skills_match.go:189-204`），`lsp_file(diagnostics)` 也直接报它是 unused func（`internal/module/skill/skills_match.go:189`）。结论：I4 的 TODO 表述本身成立，但它只是标注缺口，不会缩小任何运行时差距。

### I5 `skills/list` 语义

- **Blocker**：计划把 I5 定义为“加注释说明 `skills/list` 与 `thread/skills/list` 的语义差异”（`docs/plans/迁移/p2-execution-plan.md:54`），但当前问题不只是语义重叠，而是 `thread/skills/list` 在 V3 里本身不闭合。
- **语义差异确实存在**：`skills/list` 会直接调用 `svc.ListSkills` 并返回本地 skill 目录扫描结果（`internal/module/skill/rpc.go:34-39`），底层实现就是 `scanSkills()`（`internal/module/skill/skills_fs.go:15-24`）。而 `thread/skills/list` 则通过 `newThreadCommandHandler(..., "/skills")` 走 thread 命令通道（`internal/module/thread/rpc.go:68`、`internal/module/thread/rpc.go:99-103`）。
- **但当前 thread 路径是坏的**：`thread.Service.SendCommand` 只支持 `/model`、`/personality`、`/approvals`、`/interrupt`，其余命令全部落到 `unsupported command`（`internal/module/thread/command.go:17-35`）。也就是说，单加注释并不能把 I5 闭合到“最低可用标准”。
- **V2 对照**：V2 `skills/list` 是本地技能目录视图（`go-agent-v2/internal/apiserver/methods_command.go:217-239`，`go-agent-v2/internal/skills/methods.go:91-109`）；V2 `thread/skills/list` 则是 provider 命令路径，直接调用 `ThreadSkillsList()`（`go-agent-v2/internal/apiserver/methods_thread_turn.go:110`），并由 adapter 发 `/skills` slash command（`go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:437-439`）。当前计划把它降成“只加注释”，定性过轻。

## 2. 跨批次风险

### 2.1 `orchestration/service.go` 行数与拆分压力

- **Blocker**：计划只写“`orchestration/service.go` 超限风险，拆 `dag.go + report.go`”（`docs/plans/迁移/p2-execution-plan.md:74`），但当前文件真实长度已经是 **391 行**（`internal/sidecar/orch/orchestration/service.go:1-391`），不是“约 350 行”。`SetReport` 也已经在 service 中存在（`internal/sidecar/orch/orchestration/service.go:207-218`，`internal/sidecar/orch/orchestration/contract.go:17`），所以 B15 不是从 0 到 1，而是在已接近上限的文件里继续堆 report requester / event 逻辑。
- **B14 还会扩大构造器面**：当前 `NewService` 只注入 `logger/eventBus/sessionCleaner`（`internal/sidecar/orch/orchestration/service.go:75-89`），而计划的 B14 需要再引入 `taskdag.Store`（`docs/plans/迁移/p2-execution-plan.md:37`）。`taskdag.Store` 的确已经具备 `UpsertDAG/GetDAG/ListDAGs/UpdateNodeStatus` 级别的基础能力（`internal/store/taskdag/contract.go:9-18`、`internal/store/taskdag/contract.go:41-52`），但如果仍把 DAG 方法塞回 `service.go`，文件一定越过 400 行。

### 2.2 workspace event 类型是否已定义

- **OK，但只定义了 3 类事件**：workspace DTO 已存在 `WorkspaceRunCreated`、`WorkspaceRunStatusChanged`、`WorkspaceRunMerged` 三类 typed event（`internal/dto/workspace/event.go:5-32`），bus log sink 也已经订阅这三类事件（`internal/platform/bus/sink.go:75-79`）。
- **Warning**：当前并没有专门的 “aborted” 事件类型；若批次 A 的 B12 想把 `AbortRun` 发成独立 typed event，就需要新增 DTO。否则只能把 abort 折叠进 `WorkspaceRunStatusChanged`。另外，workspace 模块本身目前还没有 bus 注入，`Module` 仍只有 `NewService/NewWorkspaceHandlers`（`internal/module/workspace/module.go:5-8`），所以 B12 至少会改 constructor/module 装配。

### 2.3 `handler.Map` 总 key 数预估

- **当前实际总数是 76**：thread 29 个（`internal/module/thread/rpc.go:20-80`），turn 6 个（`internal/module/turn/rpc.go:33-91`），skill 22 个（`internal/module/skill/rpc.go:20-63`），workspace 8 个（`internal/module/workspace/rpc.go:15-22`），orchestration 11 个（`internal/sidecar/orch/orchestration/rpc.go:17-58`）。
- **按计划完成后的预估总数是 80**：批次 A 与批次 C 都不新增 RPC key；批次 B 的 B13 会补 4 个当前缺失的 `agent.*` key，即 `agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`、`agent.getState`（计划见 `docs/plans/迁移/p2-execution-plan.md:36`，V2 注册见 `go-agent-v2/internal/apiserver/methods_orchestration.go:20-23`）。当前 orchestration handler 表并不包含这 4 个 key（`internal/sidecar/orch/orchestration/rpc.go:17-58`），因此预估值是 `76 + 4 = 80`。

## 3. 代码守卫预检

- `internal/module/workspace/service.go` 当前 **234 行**（`internal/module/workspace/service.go:1-234`）。按计划批次 A 约 `+150` 行（`docs/plans/迁移/p2-execution-plan.md:26`），理论上约到 `384` 行，仍在守卫内，但已经不宽裕。
- `internal/sidecar/orch/orchestration/service.go` 当前 **391 行**（`internal/sidecar/orch/orchestration/service.go:1-391`）。计划批次 B 约 `+170` 行（`docs/plans/迁移/p2-execution-plan.md:40`），若不先拆文件必然越过 `400` 行守卫。
- `internal/module/skill/exec.go` 当前 **116 行**（`internal/module/skill/exec.go:1-116`），安全。
- `internal/module/skill/rpc.go` 当前 **80 行**（`internal/module/skill/rpc.go:1-80`），文件本身安全；`NewSkillHandlers` 范围 `12-66`，函数体 55 行，也未踩 80 行函数守卫（`internal/module/skill/rpc.go:12-66`）。
- `internal/module/skill/service.go` 当前 **44 行**（`internal/module/skill/service.go:1-44`），安全。
- **诊断面**：当前相关文件只有一条 LSP diagnostics，`collectChangedSkillNames` 未使用（`internal/module/skill/skills_match.go:189`）。这与 I4 的“仅 preview 触发、无运行时接线”判断一致。

## 4. V2 参考可达性

- 计划列的批次 C 参考文件均可达：`go-agent-v2/internal/apiserver/methods_command.go` 存在，当前已验证的关键区间 `20-67`、`217-239` 都在文件内，且文件至少到 `318` 行（`go-agent-v2/internal/apiserver/methods_command.go:300-318`）；`go-agent-v2/internal/skills/methods.go` 存在，关键区间 `91-109`、`437-459` 都在文件内，且文件至少到 `476` 行（`go-agent-v2/internal/skills/methods.go:461-476`）。
- 计划列的批次 A/B 参考文件也可达：`go-agent-v2/internal/service/workspace.go` 的 `176-373` 区间存在，且文件至少到 `559` 行（`go-agent-v2/internal/service/workspace.go:176-373`、`go-agent-v2/internal/service/workspace.go:540-559`）；`go-agent-v2/internal/apiserver/methods_orchestration.go` 存在，注册表在 `14-27`（`go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`），文件至少到 `242` 行（`go-agent-v2/internal/apiserver/methods_orchestration.go:230-241`）；`go-agent-v2/internal/apiserver/orchestration_report.go` 存在，计划引用的 `23-137` 在文件内，且文件结束于 `137` 行（`go-agent-v2/internal/apiserver/orchestration_report.go:23-137`）；`go-agent-v2/internal/runner/manager.go` 存在，当前已验证到 `531` 行（`go-agent-v2/internal/runner/manager.go:520-531`）。

## 结论（Blocker / Warning / OK）

- **Blocker**：I1 不能只改 `skill/exec.go`；当前 contract / params / DI 都不够承载 V2 的 timeout + cwd fallback + env allowlist（`docs/plans/迁移/p2-execution-plan.md:50`，`internal/module/skill/rpc_types.go:26-30`，`internal/module/skill/service.go:18-33`，`go-agent-v2/internal/apiserver/methods_command.go:55-67`）。
- **Blocker**：I5 被错误降成“注释问题”；`thread/skills/list` 当前会掉进 `unsupported command: /skills`，不是仅有语义重叠（`docs/plans/迁移/p2-execution-plan.md:54`，`internal/module/thread/rpc.go:68`，`internal/module/thread/command.go:17-35`）。
- **Blocker**：批次 B 若不先拆文件，`orchestration/service.go` 会直接撞上 400 行守卫；当前已 391 行（`docs/plans/迁移/p2-execution-plan.md:74`，`internal/sidecar/orch/orchestration/service.go:1-391`）。
- **Warning**：I3 对工厂扩展的表述过于乐观；现有 `cardByKey` 只适配 3/7 的 key-only 形态（`docs/plans/迁移/p2-execution-plan.md:52`，`internal/module/skill/rpc.go:13-15`，`internal/module/skill/rpc.go:20-30`）。
- **Warning**：I4 的 TODO 描述准确，但它只是在文档层承认“无运行时接线”；并没有减少任何 runtime gap（`docs/plans/迁移/p2-execution-plan.md:53`，`internal/module/skill/module.go:5-8`，`internal/module/skill/skills_match.go:189-204`）。
- **Warning**：workspace event DTO 已经有 `Created/StatusChanged/Merged` 三类，但若 B12 需要专门的 abort 事件，还要补新 DTO（`internal/dto/workspace/event.go:5-32`）。
- **OK**：I2 与代码守卫上，skill 模块本身改动空间充足；15 秒 HTTP timeout 与 V2 保持一致（`docs/plans/迁移/p2-execution-plan.md:51`，`internal/module/skill/service.go:27-32`，`go-agent-v2/internal/skills/methods.go:437-459`）。

## 互辩

### 对 audit-p2-plan-A 的批判

1. `audit-p2-plan-A.md:25-26` 把“缺 `WorkspaceRunAborted` DTO”判成 B12 blocker，证据链不够严。当前仓库里的计划文本只要求 `CreateRun/MergeRun/AbortRun/UpdateRunStatus` 成功后“发布 typed event”（`docs/plans/迁移/p2-execution-plan.md:23`），并没有要求独立的 `RunAborted` 类型；现有 `WorkspaceRunStatusChanged{OldStatus, NewStatus}` 已能表达 `aborted` 状态（`internal/dto/workspace/event.go:13-19`），而状态常量本身也已有 `statusAborted`（`internal/module/workspace/service.go:24-26`）。A 把“可选的单独 abort event”写成“当前计划的硬要求”，判定过重。
2. `audit-p2-plan-A.md:23-27` 盯住 DTO 与 bus 注入，却漏掉了更重的 side-effect plane 缺口。V2 `workspaceRunCreate` / `workspaceRunMerge` / `workspaceRunAbort` 都会先更新 `uiRuntime`，再 `notify(...)`（`go-agent-v2/internal/apiserver/workspace_methods.go:57-60`, `go-agent-v2/internal/apiserver/workspace_methods.go:131-165`）；而当前 V3 `workspace/rpc.go` 只依赖 `context/errors/strings/handler/rpc`（`internal/module/workspace/rpc.go:1-11`），`service` 只持有 `storeworkspace.Store`（`internal/module/workspace/service.go:29-31`），`module.go` 也只注册 `NewService/NewWorkspaceHandlers`（`internal/module/workspace/module.go:5-8`）。如果只补 typed event，不补 notify/UI 回灌，B12 仍然不等价。这一点比“有没有独立 `RunAborted` DTO”更严重，A 没抓到。
3. `audit-p2-plan-A.md:19-21` 把“没有 workspace root 注入点”当成 B8 的关键 warning，但这条证据并不硬。当前 `CreateRunRequest` 已经有 `SourceRoot` 和 `WorkspacePath` 两个输入字段（`internal/module/workspace/contract.go:25-35`），因此 `CreateRun` 完全可以基于请求参数实现 `MkdirAll/bootstrap`，不一定要复制 V2 的 `rootDir` 注入模型。A 真正漏掉的更严重问题是：`buildRun` 现在会在 `workspacePath` 为空时直接回退到 `sourceRoot`（`internal/module/workspace/service.go:62-66`），而 `WorkspaceRunCreated` / `WorkspaceRunMerged` 都把 `WorkspacePath` 作为显式事件载荷（`internal/dto/workspace/event.go:8-10`, `internal/dto/workspace/event.go:24-26`）。这意味着 B8 与 B12 如果不一起收口，后续事件会发布错误语义的 workspace 路径；A 没把这条跨批次耦合提到 blocker 层。

### 对 audit-p2-plan-B 的批判

1. `audit-p2-plan-B.md:62-63,82-83` 说“workspace event 类型已经具备，不是当前 blocker”，这个结论与代码事实和跨批次风险分析相冲突。workspace 侧当前没有任何 event 发布或 notify/UI side-effect：`service` 只有 `store`（`internal/module/workspace/service.go:29-31`），`module.go` 只有 `NewService/NewWorkspaceHandlers`（`internal/module/workspace/module.go:5-8`），`rpc.go` 也没有 bus/ui 依赖（`internal/module/workspace/rpc.go:1-11`）。对照 V2，create/merge/abort 都会更新 `uiRuntime` 并 `notify(...)`（`go-agent-v2/internal/apiserver/workspace_methods.go:57-60`, `go-agent-v2/internal/apiserver/workspace_methods.go:131-165`）。只因为 DTO 已定义就把 B12 从 blocker 降掉，过于宽松。
2. `audit-p2-plan-B.md:45` 只在方法对照表里轻描淡写提到 `agent.launch` 缺 `prompt/instructions/dynamic_tools/config`，但没有把它上升为 blocker。这与计划目标“达到 V2 功能等价的最低可用标准”（`docs/plans/迁移/p2-execution-plan.md:10`）直接冲突。V2 `agentLaunchParams` 明确包含 `Prompt`、`Instructions`、`DynamicTools`、`Config`（`go-agent-v2/internal/apiserver/methods_orchestration.go:29-36`）；当前 V3 `launchParams` 只有 `agentId/name/cwd/command/parentId/env`（`internal/sidecar/orch/orchestration/rpc_types.go:10-17`），`LaunchRequest` 也只承接这几项（`internal/sidecar/orch/orchestration/contract.go:32-39`）。即便 B13 按计划补完 4 个 getter/report 方法，`agent.launch` 仍明显不等价；B 没把这条保留下来的 contract drift 提到结论层。
3. `audit-p2-plan-B.md:50,53,76` 把 `agent.getReport` / `agent.getState` 主要描述成“缺入口，可由 `LastReport` / `Snapshot.State` 派生”，但漏掉了更硬的 wire-shape 缺口。V2 `agentGetReportTyped` 返回 `{agent_id, report, state}`（`go-agent-v2/internal/apiserver/methods_orchestration.go:122-135`），V2 `agentGetStateTyped` 返回 `{agent_id, state}`（`go-agent-v2/internal/apiserver/methods_orchestration.go:166-177`）；当前 V3 只有 `AgentSnapshot{ID,Name,ParentID,Port,ThreadID,Cwd,State,Provider,LastReport}`（`internal/sidecar/orch/orchestration/contract.go:41-51`）和 setter-only 的 `SetReport`，而 `SetReport` 的唯一入边仍是 `orchestration/report` handler（`internal/sidecar/orch/orchestration/rpc.go:53-57`；`call_hierarchy` 指向 `internal/sidecar/orch/orchestration/service.go:207-218`）。因此真正的 blocker 不只是“再挂两个 route”，而是要新增 getter 响应投影/DTO；B 没把这层要求写清楚。
