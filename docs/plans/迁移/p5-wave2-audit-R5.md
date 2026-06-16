# P5 波次 2 审查 R5（orchestration rpc）

## 1. 编译+守卫

- 题面已声明“编译+守卫已通过”；本次仅补代码层闭合性证据。`NewOrchestrationHandlers` 调用的参数类型 `launchParams`、`agentIDParams`、`createDAGParams`、`dagKeyParams`、`updateNodeParams` 均定义在 `internal/sidecar/orch/orchestration/rpc_types.go:5-48`，调用的 `Service` 方法 `LaunchAgent`、`ListAgents`、`StopAgent`、`Snapshot` 均声明在 `internal/sidecar/orch/orchestration/contract.go:10-18`，调用点位于 `internal/sidecar/orch/orchestration/rpc.go:11-35`。
- 未实现路径不是空实现；`task/dag/*`、`task/node/update`、`orchestration/report` 全部统一落到 `newNotImplementedHandler`，最终返回 `rpc.ErrNotImplemented(...)`，见 `internal/sidecar/orch/orchestration/rpc.go:30-41`。

## 2. 方法完整性

- 当前 V3 `handler.Map` 内确实有 9 个 key：`agent.launch`、`agent.stop`、`agent.list`、`agent.snapshot`、`task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update`、`orchestration/report`，见 `internal/sidecar/orch/orchestration/rpc.go:13-34`。
- V2 `registerOrchestrationMethods` 注册的是 12 个 key：`agent.launch`、`agent.submit`、`agent.submitPrompt`、`agent.stop`、`agent.list`、`agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`、`agent.getState`、`agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`。
- 逐一核对后，V2/V3 直接同名重合只有 3 个：`agent.launch`、`agent.stop`、`agent.list`，见 `internal/sidecar/orch/orchestration/rpc.go:13-26` 与 `go-agent-v2/internal/apiserver/methods_orchestration.go:15-19`。
- `agent.snapshot` 不是 V2 同名方法；V2 最接近的是 `agent.getState`，见 `internal/sidecar/orch/orchestration/rpc.go:27-29` 与 `go-agent-v2/internal/apiserver/methods_orchestration.go:23,166-177`。
- `orchestration/report` 不是 V2 同名方法；V2 对应读取报告的是 `agent.getReport`，见 `internal/sidecar/orch/orchestration/rpc.go:34` 与 `go-agent-v2/internal/apiserver/methods_orchestration.go:20,122-135`。
- 结论：`rpc.go` 内部“9 个 key 齐全”成立，但这不等于“V2 orchestration 方法齐全”；V2 中的 `agent.submit`、`agent.submitPrompt`、`agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`、`agent.getState`、`agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding` 均未在当前 V3 `handler.Map` 中出现，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:16-26` 对照 `internal/sidecar/orch/orchestration/rpc.go:13-34`。

## 3. import 方向

- `internal/sidecar/orch/orchestration/rpc.go` 的 import 只有 `context`、`github.com/creachadair/jrpc2/handler`、`internal/platform/rpc`，见 `internal/sidecar/orch/orchestration/rpc.go:3-9`。
- `NewOrchestrationHandlers` 只注入 `Service`，函数签名未暴露任何 store/provider 依赖，见 `internal/sidecar/orch/orchestration/rpc.go:11`。
- 结论：当前 import 方向满足“rpc glue 只依赖 orchestration.Service，不直接 import store/provider”的要求，证据见 `internal/sidecar/orch/orchestration/rpc.go:3-11`。

## 4. 行数

- `internal/sidecar/orch/orchestration/rpc.go` 共 42 行；最长函数是 `NewOrchestrationHandlers`，范围 `internal/sidecar/orch/orchestration/rpc.go:11-36`，共 26 行。次长函数 `newNotImplementedHandler` 位于 `internal/sidecar/orch/orchestration/rpc.go:38-42`，共 5 行。
- `internal/sidecar/orch/orchestration/rpc_types.go` 共 48 行；该文件只有参数 struct，无函数，见 `internal/sidecar/orch/orchestration/rpc_types.go:5-48`。
- `internal/sidecar/orch/orchestration/module.go` 共 12 行；该文件只有 `Module` 变量，无函数，见 `internal/sidecar/orch/orchestration/module.go:5-12`。
- `internal/sidecar/orch/orchestration/contract.go` 共 57 行；该文件只有 interface/type 定义，无函数，见 `internal/sidecar/orch/orchestration/contract.go:10-57`。

## 5. Service 方法映射

- `agent.launch` 映射到 `svc.LaunchAgent(ctx, LaunchRequest{...})`，handler 在 `internal/sidecar/orch/orchestration/rpc.go:13-20`，contract 声明在 `internal/sidecar/orch/orchestration/contract.go:11`，实现位于 `internal/sidecar/orch/orchestration/service.go:86-101`。
- `agent.stop` 映射到 `svc.StopAgent(ctx, p.AgentID)`，handler 在 `internal/sidecar/orch/orchestration/rpc.go:21-23`，contract 声明在 `internal/sidecar/orch/orchestration/contract.go:13`，实现位于 `internal/sidecar/orch/orchestration/service.go:103-117`。
- `agent.list` 映射到 `svc.ListAgents(ctx)`，handler 在 `internal/sidecar/orch/orchestration/rpc.go:24-26`，contract 声明在 `internal/sidecar/orch/orchestration/contract.go:12`，实现位于 `internal/sidecar/orch/orchestration/service.go:161-170`。
- `agent.snapshot` 映射到 `svc.Snapshot(ctx, p.AgentID)`，handler 在 `internal/sidecar/orch/orchestration/rpc.go:27-29`，contract 声明在 `internal/sidecar/orch/orchestration/contract.go:17`，实现位于 `internal/sidecar/orch/orchestration/service.go:172-181`。
- `task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update`、`orchestration/report` 没有映射到任何 `Service` 方法，而是全部落到 `newNotImplementedHandler`，见 `internal/sidecar/orch/orchestration/rpc.go:30-41`；`contract.go` 也只对 DAG 方法保留 TODO 注释，没有真实接口项，见 `internal/sidecar/orch/orchestration/contract.go:20-24`。

## 6. DAG 方法状态

- `task/dag/create` 使用 `newNotImplementedHandler[createDAGParams]("task/dag/create")`，见 `internal/sidecar/orch/orchestration/rpc.go:30`。
- `task/dag/get` 使用 `newNotImplementedHandler[dagKeyParams]("task/dag/get")`，见 `internal/sidecar/orch/orchestration/rpc.go:31`。
- `task/dag/list` 使用 `newNotImplementedHandler[struct{}]("task/dag/list")`，见 `internal/sidecar/orch/orchestration/rpc.go:32`。
- `task/node/update` 使用 `newNotImplementedHandler[updateNodeParams]("task/node/update")`，见 `internal/sidecar/orch/orchestration/rpc.go:33`。
- `contract.go` 对应只有 TODO 骨架注释：`CreateDAG`、`GetDAG`、`ListDAGs`、`UpdateNode`，见 `internal/sidecar/orch/orchestration/contract.go:20-24`。
- 结论：DAG 相关方法当前全部是 TODO 骨架，没有 service contract，也没有实现，见 `internal/sidecar/orch/orchestration/rpc.go:30-41` 与 `internal/sidecar/orch/orchestration/contract.go:20-24`。

## 7. 参数类型

- `launchParams` 当前字段为 `AgentID/Name/CWD/Command`，JSON tag 为 `agentId/name/cwd/command`，见 `internal/sidecar/orch/orchestration/rpc_types.go:5-10`；handler 将其转换为 `LaunchRequest{AgentID, Name, Cwd, Command}`，并对 `Command` 做一次切片拷贝，见 `internal/sidecar/orch/orchestration/rpc.go:13-19`；对应 contract 类型为 `LaunchRequest`，字段为 `AgentID/Name/ParentID/Cwd/Command/Env`，见 `internal/sidecar/orch/orchestration/contract.go:32-39`。对当前 V3 `LaunchAgent` contract 而言，类型是对齐的。
- `agentIDParams` 只有 `AgentID string`，复用于 `agent.stop`、`agent.snapshot`、`orchestration/report`，见 `internal/sidecar/orch/orchestration/rpc_types.go:12-14` 与 `internal/sidecar/orch/orchestration/rpc.go:21-29,34`。对当前 V3 handler 来说足够小且可复用。
- `dagKeyParams` 只有 `DagKey string`，供 `task/dag/get` 使用，见 `internal/sidecar/orch/orchestration/rpc_types.go:16-18` 与 `internal/sidecar/orch/orchestration/rpc.go:31`。对单 key 查询场景是合理的最小 DTO。
- 但如果目标是 V2 兼容，参数 tag 已经不兼容：V2 `agent.launch` 使用 `json:"id"`、`json:"prompt"`、`json:"instructions"`、`json:"dynamic_tools"`、`json:"config"`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:29-37`；V2 其他 agent 参数广泛使用 `json:"agent_id"`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:74-78,93-95,118-120,162-164,179-183,231-234`。当前 V3 `launchParams/agentIDParams` 使用的是 `agentId`，见 `internal/sidecar/orch/orchestration/rpc_types.go:6,13`。

## 8. HandlerMapResult

- `rpc.HandlerMapResult` 内嵌 `fx.Out`，并把 `Handlers handler.Map` 打到 `group:"rpc_handlers"`，见 `internal/platform/rpc/module.go:31-35`。
- `orchestration.Module` 通过 `fx.Provide(NewOrchestrationHandlers)` 注册 handler provider，见 `internal/sidecar/orch/orchestration/module.go:5-10`。
- `platform/rpc` 侧在 `registerAllHandlers` 中收集 `[]handler.Map \`group:"rpc_handlers"\`` 并统一注册到 server，见 `internal/platform/rpc/module.go:37-47`。
- 结论：`HandlerMapResult` 已正确输出到 fx group，`module.go` 也确实注册了 `NewOrchestrationHandlers`，证据见 `internal/platform/rpc/module.go:31-47` 与 `internal/sidecar/orch/orchestration/module.go:5-10`。

## 9. V2 对照

- 对照方法一，`agent.launch`：
  - V2 注册点在 `go-agent-v2/internal/apiserver/methods_orchestration.go:15`，参数定义在 `go-agent-v2/internal/apiserver/methods_orchestration.go:29-37`，处理逻辑在 `go-agent-v2/internal/apiserver/methods_orchestration.go:39-70`，核心语义是把 `id/name/prompt/cwd/instructions/dynamic_tools/config` 交给 launcher。
  - V3 注册点在 `internal/sidecar/orch/orchestration/rpc.go:13`，参数定义在 `internal/sidecar/orch/orchestration/rpc_types.go:5-10`，处理逻辑在 `internal/sidecar/orch/orchestration/rpc.go:13-20`，核心语义是把 `agentId/name/cwd/command` 组装成 `LaunchRequest` 后调用 `LaunchAgent`。
  - 结论：方法名保留，但 payload 结构和服务语义都已经变化；这是“重实现”，不是“同 schema 迁移”，证据见 `go-agent-v2/internal/apiserver/methods_orchestration.go:29-60` 对照 `internal/sidecar/orch/orchestration/rpc_types.go:5-10`、`internal/sidecar/orch/orchestration/rpc.go:13-20`、`internal/sidecar/orch/orchestration/contract.go:32-39`。
- 对照方法二，`agent.list`：
  - V2 注册点在 `go-agent-v2/internal/apiserver/methods_orchestration.go:19`，实现位于 `go-agent-v2/internal/apiserver/methods_orchestration.go:110-116`，直接返回 `s.mgr.List()`。
  - V3 注册点在 `internal/sidecar/orch/orchestration/rpc.go:24`，实现位于 `internal/sidecar/orch/orchestration/rpc.go:24-26`，通过 `svc.ListAgents(ctx)` 返回 `[]AgentSnapshot`；`ListAgents` contract 在 `internal/sidecar/orch/orchestration/contract.go:12`，返回体形状固定在 `internal/sidecar/orch/orchestration/contract.go:41-57`，实现位于 `internal/sidecar/orch/orchestration/service.go:161-170`。
  - 结论：方法名保留，V3 的分层更干净，但返回 shape 已从 V2 的“manager 原始列表”转成明确 contract；若外部依赖 V2 原始字段集合，需要单独核对兼容性，证据见 `go-agent-v2/internal/apiserver/methods_orchestration.go:110-116` 对照 `internal/sidecar/orch/orchestration/rpc.go:24-26`、`internal/sidecar/orch/orchestration/contract.go:12,41-57`。
- 补充：V2 `agent.getState` 返回 `{agent_id, state}`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:162-177`；V3 最接近的是 `agent.snapshot -> Snapshot`，见 `internal/sidecar/orch/orchestration/rpc.go:27-29` 与 `internal/sidecar/orch/orchestration/contract.go:17,41-57`。这属于重命名并扩展返回体，不是同名保留。

## 10. 与已有 service 的一致性

- `agent.launch` 的调用签名与 `Service.LaunchAgent(ctx context.Context, req LaunchRequest) error` 一致：handler 构造 `LaunchRequest` 后调用 `svc.LaunchAgent(...)`，见 `internal/sidecar/orch/orchestration/rpc.go:13-20` 与 `internal/sidecar/orch/orchestration/contract.go:11,32-39`。
- `agent.stop` 的调用签名与 `Service.StopAgent(ctx context.Context, agentID string) error` 一致，见 `internal/sidecar/orch/orchestration/rpc.go:21-23` 与 `internal/sidecar/orch/orchestration/contract.go:13`。
- `agent.list` 的调用签名与 `Service.ListAgents(ctx context.Context) ([]AgentSnapshot, error)` 一致，见 `internal/sidecar/orch/orchestration/rpc.go:24-26` 与 `internal/sidecar/orch/orchestration/contract.go:12`。
- `agent.snapshot` 的调用签名与 `Service.Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error)` 一致，见 `internal/sidecar/orch/orchestration/rpc.go:27-29` 与 `internal/sidecar/orch/orchestration/contract.go:17`。
- 对已接线的 4 个 handler，service 实现也存在，分别见 `internal/sidecar/orch/orchestration/service.go:86-117,161-181`。
- 对未接线的 `task/dag/*`、`task/node/update`、`orchestration/report`，当前没有可对齐的 service 方法签名；DAG 只剩 TODO 注释，report 连 TODO 都没有，见 `internal/sidecar/orch/orchestration/rpc.go:30-34` 与 `internal/sidecar/orch/orchestration/contract.go:20-24`、`internal/sidecar/orch/orchestration/contract.go:10-18`。

## 结论（Blocker / Improvement，附行号）

### Blocker

- V2 orchestration surface 没有迁平。V2 注册 12 个 `agent.*` 方法，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`；当前 V3 只有 9 个 key，且直接同名保留只有 `agent.launch`、`agent.stop`、`agent.list`，见 `internal/sidecar/orch/orchestration/rpc.go:13-34`。若波次 2 目标包含 V2 兼容面，这一项是 blocker。
- DAG 写面只是占位。`task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update` 全部直接返回 `ErrNotImplemented`，见 `internal/sidecar/orch/orchestration/rpc.go:30-41`；`contract.go` 只有 TODO 注释，没有 interface 方法，见 `internal/sidecar/orch/orchestration/contract.go:20-24`。若这些 key 已对外暴露，这些调用会稳定失败。
- `orchestration/report` 已注册为 RPC key，见 `internal/sidecar/orch/orchestration/rpc.go:34`，但 `Service` interface 只声明了 `LaunchAgent/ListAgents/StopAgent/SubmitTurn/CompleteTurn/Recover/Snapshot`，没有任何 report 方法，见 `internal/sidecar/orch/orchestration/contract.go:10-18`。这意味着该 key 当前没有 contract 落点，只能停留在 `ErrNotImplemented`。
- `agent.launch` 和通用 `agentID` 参数的 wire schema 不兼容 V2。V2 `agent.launch` 需要 `id/prompt/instructions/dynamic_tools/config`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:29-37`；V3 改为 `agentId/name/cwd/command`，见 `internal/sidecar/orch/orchestration/rpc_types.go:5-10`。V2 多个方法使用 `agent_id`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:74-78,93-95,118-120,162-164,179-183,231-234`；V3 统一使用 `agentId`，见 `internal/sidecar/orch/orchestration/rpc_types.go:6,13`。若调用方沿用 V2 payload，将直接解码失败或语义错位。

### Improvement

- 依赖方向是干净的。`rpc.go` 只注入 `Service`，import 只包含 `context`、`handler`、`platform/rpc`，没有 store/provider 泄漏，见 `internal/sidecar/orch/orchestration/rpc.go:3-11`。
- 已接线的 4 个 handler 与 service contract/impl 一致，链路完整：`agent.launch`、`agent.stop`、`agent.list`、`agent.snapshot` 分别落到 `LaunchAgent`、`StopAgent`、`ListAgents`、`Snapshot`，见 `internal/sidecar/orch/orchestration/rpc.go:13-29`、`internal/sidecar/orch/orchestration/contract.go:11-18`、`internal/sidecar/orch/orchestration/service.go:86-117,161-181`。
- fx 注册链路正确。`HandlerMapResult` 通过 `fx.Out` 写入 `group:"rpc_handlers"`，见 `internal/platform/rpc/module.go:31-35`；`module.go` 已注册 `NewOrchestrationHandlers`，见 `internal/sidecar/orch/orchestration/module.go:5-10`；`registerAllHandlers` 会统一收集并注册，见 `internal/platform/rpc/module.go:45-47`。
- 文件规模受控。`rpc.go` 42 行、`rpc_types.go` 48 行、`module.go` 12 行、`contract.go` 57 行，最长函数仅 `NewOrchestrationHandlers` 26 行，见 `internal/sidecar/orch/orchestration/rpc.go:11-42`、`internal/sidecar/orch/orchestration/rpc_types.go:5-48`、`internal/sidecar/orch/orchestration/module.go:5-12`、`internal/sidecar/orch/orchestration/contract.go:10-57`。

## 互辩：批判 R3 + R4

### 对 R3 审查报告的批判

- R3 报告把“配置与摘要也都是真实实现”说得过满，见 `docs/plans/迁移/p5-wave2-audit-R3.md:122-126`。代码里 `ReadConfig` 只是校验 `agentID` 后返回固定的 `{"skills":[],"session_bound":false}`，没有读取任何 store 或 agent 绑定，见 `internal/module/skill/skills_fs.go:143-149`；`WriteConfig` 只是转调 `WriteRemote`，见 `internal/module/skill/skills_fs.go:151-153`，而 `WriteRemote` 最终写的是 skills 根目录下的 `SKILL.md`，见 `internal/module/skill/skills_fs.go:132-141` 与 `internal/module/skill/skills_meta.go:259-270`。这不是“真实 config store”，更像占位语义。
- R3 对 store 注入的验证不够深。它只停在“`prompt.Store` 是死依赖”，见 `docs/plans/迁移/p5-wave2-audit-R3.md:134-145`；但更严重的问题是，公开 RPC 仍暴露 `skills/config/read` 与 `skills/config/write`，见 `internal/module/skill/rpc.go:54-57`，而 `service` 虽然注入了 `prompts promptstore.Store`，见 `internal/module/skill/service.go:18-23,27-33`，模块内却没有任何 `s.prompts` 调用。结论不应只写“死依赖”，而应写“公开 config 面未接 prompt store，且实际落到文件写入/固定返回”。
- R3 漏检了 skill 模块与 thread 模块的双重“skills list”表面。报告在方法完整性与接口评估里完全没讨论这组重叠，见 `docs/plans/迁移/p5-wave2-audit-R3.md:8-46,102-111`。代码上它们没有 registry 冲突，因为 key 分别是 `skills/list` 与 `thread/skills/list`，见 `internal/module/skill/rpc.go:34-40` 与 `internal/module/thread/rpc.go:68`；但语义明显不同，一个走 `svc.ListSkills` 扫描本地 skill 目录，见 `internal/module/skill/skills_fs.go:15-25`，另一个走 `SendCommand("/skills")` 进入 thread 命令通道，见 `internal/module/thread/rpc.go:68,99-103`。R3 应明确判定“无 key 冲突，但存在用户面语义重叠”。
- R3 的行数评估过轻。它只给了逐文件行数和 top 3 最长函数，见 `docs/plans/迁移/p5-wave2-audit-R3.md:69-90`，但没有把真实复杂度聚合起来：`rpc.go` 一口气挂了 22 个 handler，见 `internal/module/skill/rpc.go:19-61`；`Service` 暴露 20 个方法，见 `internal/module/skill/contract.go:5-25`；仅 `cards.go` 就有 7 个 service 方法，见 `internal/module/skill/cards.go:18-145`；`skills_fs.go` 再叠 11 个 service 方法与多个 helper，见 `internal/module/skill/skills_fs.go:15-164,201-268`；`skills_match.go` 还单独承载 auto-match 逻辑，见 `internal/module/skill/skills_match.go:14-48`。如果互辩目标是评估维护面，R3 的“Top 3 函数”统计不够。
- R3 第 7 节只证明了“RPC 调用都能在 interface 上找到签名”，见 `docs/plans/迁移/p5-wave2-audit-R3.md:102-111`，但没有把 20 个 interface 方法逐个落到实现文件。实际实现散在 `cards.go`、`exec.go`、`skills_fs.go`、`skills_match.go`：card 7 个方法见 `internal/module/skill/cards.go:18-145`，`ExecCommand` 见 `internal/module/skill/exec.go:27-60`，FS/remote/config/summary 11 个方法见 `internal/module/skill/skills_fs.go:15-164`，`MatchPreview` 见 `internal/module/skill/skills_match.go:14-25`。这次补查后可以确认“都有实现”，但原报告的证据链没有做到这个粒度。

### 对 R4 审查报告的批判

- R4 对 workspace service 的定性仍然偏宽松。它写“对 run 行记录的 CRUD 不是空壳”，见 `docs/plans/迁移/p5-wave2-audit-R4.md:73-77`，但代码里 `Service` 直接 alias store 类型 `Run`/`RunFile`，见 `internal/module/workspace/contract.go:22-23`；`service` 自身只持有 `storeworkspace.Store`，见 `internal/module/workspace/service.go:20-22`；`GetRun`、`ListRuns`、`UpdateRunStatus`、`ListRunFiles`、`GetRunFile` 基本都是单跳透传，见 `internal/module/workspace/service.go:59-72,83-91`；`MergeRun`、`AbortRun` 也只是 `UpdateRunStatus` 的薄封装，见 `internal/module/workspace/service.go:74-80`。这更像 store façade，不像有独立业务边界的 service。
- R4 把 `TransitionRunStatus` 与 `UpsertFile` 下降为 Improvement，严重程度判低了。报告把这两项放在 `docs/plans/迁移/p5-wave2-audit-R4.md:79-86,134`，但 V3 store contract 明明已经提供了这两个能力，见 `internal/store/workspace/contract.go:13-17`，而 module/workspace 内没有任何调用点。V2 merge 语义正是靠状态跃迁和文件级持久化完成的：`transitionToMerging` 用 `TryTransitionRunStatus`，见 `go-agent-v2/internal/service/workspace.go:283-304`；`saveFileAndRecord` 会持久化每个文件状态，见 `go-agent-v2/internal/service/workspace.go:158-174`，随后在 merge 主路径中被反复调用，见 `go-agent-v2/internal/service/workspace.go:348-373,417-557`。这不是“可选增强”，而是 `workspace/run/merge` 成立与否的 blocker。
- R4 漏掉了 `workspace/run/files/list` 与 `workspace/run/file/get` 的“无生产者”问题。报告第 2 节只说这两个 key 是 V3 新增面，见 `docs/plans/迁移/p5-wave2-audit-R4.md:10-25`，第 7 节只泛泛提到 `UpsertFile` 未使用，见 `docs/plans/迁移/p5-wave2-audit-R4.md:81-86`。但代码上问题更尖锐：RPC 明确暴露了 `workspace/run/files/list` 和 `workspace/run/file/get`，见 `internal/module/workspace/rpc.go:45-52`；service 只会读 `ListFiles/GetFile`，见 `internal/module/workspace/service.go:83-91`；模块内没有任何 `UpsertFile` 调用点，而 store contract 需要靠它写入文件记录，见 `internal/store/workspace/contract.go:15-17`。对照 V2，文件记录是在 merge 中通过 `saveFileAndRecord` 持续写入的，见 `go-agent-v2/internal/service/workspace.go:158-174,474-557`。因此这两个新读接口当前几乎没有数据源，R4 应单独点名。
- R4 只盯了 RPC 返回值和 store 写面，漏掉了事件/通知平面回退。V2 `workspaceRunCreate`、`workspaceRunMerge`、`workspaceRunAbort` 都会 `notify(...)`，见 `go-agent-v2/internal/apiserver/workspace_methods.go:60,134,161`；这些通知随后被 `syncUIRuntimeFromNotifyPayload` 消费并回灌 UI runtime，见 `go-agent-v2/internal/apiserver/server_payload.go:211-225`。当前 V3 `workspace/rpc.go` 的依赖只有 `context/errors/strings/handler/platform/rpc`，见 `internal/module/workspace/rpc.go:3-11`，`service.go` 也只有 store，见 `internal/module/workspace/service.go:3-11,20-22`，整个模块没有任何 bus/notify 依赖或事件发布点。即使先不谈 DAG，光是 workspace 状态变更的 side-effect plane 就已经断了，R4 没把这一层写出来。
