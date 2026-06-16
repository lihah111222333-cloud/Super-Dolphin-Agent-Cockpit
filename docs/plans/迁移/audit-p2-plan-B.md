# P2 计划审查 — Agent B

## 1. 批次 B 可行性（B13/B14/B15 逐项验证+行号）

### B13 缺失方法补齐

- 计划对缺失面的描述偏窄。`docs/plans/迁移/p2-execution-plan.md:36` 把 B13 写成“V2 12 个 agent.* 方法面只有 4 个可调用”，并只打算补 `agent.getState`、`agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`。但 V2 实际注册的是 12 个方法：`agent.launch`、`agent.submit`、`agent.submitPrompt`、`agent.stop`、`agent.list`、`agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`、`agent.getState`、`agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:15-26`。
- “只有 4 个可调用”这个口径不准确。当前 V3 `internal/sidecar/orch/orchestration/rpc.go` 中，和 V2 同名且可调用的至少有 5 个：`agent.launch`、`agent.submit`、`agent.submitPrompt`、`agent.stop`、`agent.list`，见 `internal/sidecar/orch/orchestration/rpc.go:17-45`；其中 `agent.submit` / `agent.submitPrompt` 已确实接线到 `svc.SubmitTurn(...)`，见 `internal/sidecar/orch/orchestration/rpc.go:20-38`，并由 `submitParams.UnmarshalJSON` 兼容旧 `agentId` + `input` 形状，见 `internal/sidecar/orch/orchestration/rpc_types.go:51-81`。此外当前还多了一个非 V2 同名入口 `agent.snapshot`，见 `internal/sidecar/orch/orchestration/rpc.go:46-48`。
- 如果按“V2 缺失面”逐一列，计划遗漏了 3 个方法。当前 V3 完全缺失的 V2 方法是 `agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`、`agent.getState`、`agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding`，证据是 V2 注册面 `go-agent-v2/internal/apiserver/methods_orchestration.go:20-26` 对照当前 V3 注册面 `internal/sidecar/orch/orchestration/rpc.go:17-58`，并且在 orchestration 包内检索 `rememberReportRequest`、`reportEvent`、`saveSubAgent`、`deleteSubAgent`、`persistSubAgentBinding` 全无命中，见 `internal/sidecar/orch/orchestration/*.go` 的 LSP text_search 结果为空。
- `agent.getState` 可以用 `Snapshot` 派生，但这不等于“已具备 V2 同名方法”。计划把 `agent.getState` 写成 `→Snapshot`，见 `docs/plans/迁移/p2-execution-plan.md:36`；当前 `Service` 也只有 `Snapshot`，没有 `GetState`，见 `internal/sidecar/orch/orchestration/contract.go:9-18`。V2 `agentGetStateTyped` 返回 `{agent_id, state}`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:162-177`；而 V3 `agent.snapshot` 返回的是 `AgentSnapshot{ID,Name,ParentID,Port,ThreadID,Cwd,State,Provider,LastReport}`，见 `internal/sidecar/orch/orchestration/rpc.go:46-48`、`internal/sidecar/orch/orchestration/contract.go:41-51`。因此“可复用底层字段”成立，“方法已补齐”不成立。
- `agent.getReport` 同样只能部分复用 `LastReport`，还缺 getter 入口。当前 `Service` 只有 `SetReport`，见 `internal/sidecar/orch/orchestration/contract.go:17`；`SetReport` 的唯一入流就是 `orchestration/report` handler，见 `internal/sidecar/orch/orchestration/rpc.go:53-57` 与 `internal/sidecar/orch/orchestration/service.go:207-218` 的 call hierarchy。V2 `agentGetReportTyped` 则直接返回 `{agent_id, report, state}`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:122-135`。计划把 `agent.getReport` 简化成 “`→lastReport`” 可做底层映射，但不能证明方法面已补齐，见 `docs/plans/迁移/p2-execution-plan.md:36`、`internal/sidecar/orch/orchestration/contract.go:41-51`。

### B14 DAG store 对齐

- 计划判断“task/dag/* 全部 ErrNotImplemented”是准确的。当前 `task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update` 全部还是 `newNotImplementedHandler(...)`，见 `internal/sidecar/orch/orchestration/rpc.go:49-52`；这与 `docs/plans/迁移/p2-execution-plan.md:37` 一致。
- store 能力面本身是够的。`internal/store/taskdag/contract.go` 明确存在 `UpsertDAG`、`GetDAG`、`ListDAGs`、`UpdateNodeStatus`，见 `internal/store/taskdag/contract.go:10-15`；计划对这四个方法名的引用没有写错，见 `docs/plans/迁移/p2-execution-plan.md:37`。
- 但 B14 的“最小实现”不能只在 `rpc.go` 落地，必须先扩 service contract。当前 `orchestration.Service` 只有 `LaunchAgent`、`ListAgents`、`StopAgent`、`SubmitTurn`、`CompleteTurn`、`Recover`、`Snapshot`、`SetReport`，见 `internal/sidecar/orch/orchestration/contract.go:9-18`；DAG 相关仍只是 TODO 注释，见 `internal/sidecar/orch/orchestration/contract.go:20-24`。因此计划写的“目标文件：contract.go, service.go, rpc.go”是对的，但风险比文字描述更高，见 `docs/plans/迁移/p2-execution-plan.md:37`。
- `create -> UpsertDAG` 不是一对一可落地映射。RPC `createDAGParams` 除了 DAG 头字段，还带 `Nodes`，见 `internal/sidecar/orch/orchestration/rpc_types.go:32-49`；而 store 的 `UpsertDAG` 只处理 DAG 头，节点要单独走 `UpsertNode`，见 `internal/store/taskdag/contract.go:10-16`。另外 `taskdag.DAG` 必带 `Status` 字段，RPC `createDAGParams` 没有这个字段，见 `internal/store/taskdag/contract.go:152-164`。所以计划里“create→UpsertDAG”如果按字面做，会漏节点写入和默认状态填充，见 `docs/plans/迁移/p2-execution-plan.md:37`。
- `get -> GetDAG` 也不是完整闭环。`GetDAG` 只返回 DAG 头，见 `internal/store/taskdag/contract.go:13`；节点要额外走 `ListNodes`，见 `internal/store/taskdag/contract.go:16`。而 orchestration contract 的 TODO 已经把目标返回形状写成 `DAGDetail`，见 `internal/sidecar/orch/orchestration/contract.go:20-24`。因此 B14 最小实现至少需要新 DTO，而不是直接把 store 返回值透出。
- `list -> ListDAGs` 和 `node/update -> UpdateNodeStatus` 这两项映射是可行的。RPC `listDAGsParams{Status,Keyword,Limit}` 与 store `ListDAGsFilter{Status,Keyword,Limit}` 同形，见 `internal/sidecar/orch/orchestration/rpc_types.go:88-92`、`internal/store/taskdag/contract.go:41-45`；RPC `updateNodeParams{dagKey,nodeKey,status,result}` 与 store `NodeStatusUpdate{DagKey,NodeKey,Status,Result}` 也同形，见 `internal/sidecar/orch/orchestration/rpc_types.go:94-98`、`internal/store/taskdag/contract.go:47-52`。
- 真正的 blocker 在依赖注入。当前 `service` 结构里没有任何 `taskdag.Store` 或等价依赖，见 `internal/sidecar/orch/orchestration/service.go:28-36`；`NewService` 也只注入 `logger`、`eventBus`、`sessionCleaner`，见 `internal/sidecar/orch/orchestration/service.go:75-89`；`Module` 里同样没有 taskdag provider，见 `internal/sidecar/orch/orchestration/module.go:15-23`。此外在 orchestration 包内检索 `taskdag` 无命中，说明 B14 还没开始接依赖，见 `internal/sidecar/orch/orchestration/*.go` 的 LSP text_search 结果为空。

### B15 report 实现范围

- 计划对问题大小的判断是准确的，但修复策略低估了缺口。`docs/plans/迁移/p2-execution-plan.md:38` 说“orchestration/report 缺失范围远大于 getter”，这点和 V2 实现一致：V2 注册了 `agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent` 三个方法，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:20-23`；对应 handler 在 `go-agent-v2/internal/apiserver/methods_orchestration.go:122-209`；核心逻辑还另有 `go-agent-v2/internal/apiserver/orchestration_report.go:23-137`。
- V2 `rememberReportRequest` 的职责不是简单“记个 worker_id”。它会把 `senderID` 归并到最上层 requester，再把 waiter 写入共享状态，见 `go-agent-v2/internal/apiserver/orchestration_report.go:23-38`、`go-agent-v2/internal/apiserver/orchestration_report.go:40-77`；底层状态入口在 `go-agent-v2/internal/apiserver/server_context.go:294-305`。
- V2 `reportEvent` 的职责也不是“写一份 report”。`agentReportEventTyped` 只是入口，它把 `agentcore.Event` 交给 `AgentEventHandler`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:185-203`；真正的完成态逻辑在 `maybeAutoReportOrchestrationCompletion`，其中会 drain requesters、提取 summary、构造 completion report、自动回传 UI，见 `go-agent-v2/internal/apiserver/orchestration_report.go:99-137`。
- 当前 V3 只有“最小版 lastReport 写链”，没有 requester / event 语义。`orchestration/report` 只调用 `svc.SetReport(ctx, p.AgentID, p.Report)`，见 `internal/sidecar/orch/orchestration/rpc.go:53-57`；`SetReport` 只写 `agent.lastReport` 和 `updatedAt`，见 `internal/sidecar/orch/orchestration/service.go:207-218`；在 orchestration 包内检索 `rememberReportRequest`、`reportEvent` 也完全无命中，见 `internal/sidecar/orch/orchestration/*.go` 的 LSP text_search 结果为空。
- 计划把多种 report 语义收敛到 `orchestration/report` 的写法，当前连参数层都对不上。现有 `reportParams` 只有 `agentId` 和 `report`，见 `internal/sidecar/orch/orchestration/rpc_types.go:83-86`；而 V2 `agent.rememberReportRequest` 需要 `sender_id/worker_id`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:137-160`，V2 `agent.reportEvent` 需要 `agent_id/event_type/event_data`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:179-209`。所以 `docs/plans/迁移/p2-execution-plan.md:38` 里“orchestration/report 改为真实读 lastReport、rememberReportRequest 注册请求者、reportEvent 处理报告事件”这句，按当前 DTO 结构无法直接成立。
- 因此，计划中“orchestration/report 改为真实读 lastReport、rememberReportRequest 注册请求者、reportEvent 处理报告事件。复杂归并逻辑先最小版+TODO”这句需要拆开看，见 `docs/plans/迁移/p2-execution-plan.md:38`。如果 B15 只想先补一个共享 getter/setter，那可行；但如果它同时声称要补 `rememberReportRequest` 和 `reportEvent`，那么 requester 归并和完成态自动回传不是可选 TODO，而是这两个 RPC 的核心语义，证据见 `go-agent-v2/internal/apiserver/methods_orchestration.go:142-159`、`go-agent-v2/internal/apiserver/methods_orchestration.go:185-203`、`go-agent-v2/internal/apiserver/orchestration_report.go:99-137`。

## 2. service.go 行数超限方案

- `service.go` 当前实际并未超守卫，但已逼近上限。`document_symbol` 最后一个函数 `(*service).handleProcessExitTransition` 结束在 `internal/sidecar/orch/orchestration/service.go:390`，说明文件当前原始行数约 391 行，见 `internal/sidecar/orch/orchestration/service.go:378-390`。计划把风险写成“可能超 400 行”，这个判断成立，见 `docs/plans/迁移/p2-execution-plan.md:42,74`。
- 代码守卫的单文件和包文件数上限分别是 400 行、15 个非测试 `.go` 文件，见 `internal/archtest/guardlib.go:17-24`、`internal/archtest/guardlib.go:300-317`。当前 orchestration 包共有 11 个 `.go` 文件，其中 10 个非测试文件、1 个测试文件，见 `internal/sidecar/orch/orchestration/{contract.go,events.go,helpers.go,module.go,recover.go,rpc.go,rpc_types.go,runner_actor.go,service.go,submission.go,submission_test.go}:1` 的 `package orchestration` 命中统计。按守卫口径，再拆出 `dag.go` + `report.go` 后仍不会撞上包文件数上限。
- 拆 `report.go` 是合理的。当前 report 相关逻辑集中在 `SetReport` 与 `snapshotLocked` 上，见 `internal/sidecar/orch/orchestration/service.go:207-232`；如果 B15 要再接 getter / requester / event，这块最容易把 `service.go` 顶过 400 行。
- 拆 `dag.go` 在结构上也合理，但它不是简单的“把现有代码搬出去”。因为当前 `service.go` 里根本没有 DAG 实现，见 `internal/sidecar/orch/orchestration/service.go:97-390` 的函数清单；B14 要先新增 `taskdag.Store` 依赖、补 service contract，再谈拆分，见 `internal/sidecar/orch/orchestration/contract.go:20-24`、`internal/sidecar/orch/orchestration/module.go:15-23`。所以计划写“超限风险 → 拆 dag.go + report.go”方向对，但不能把它当作纯代码整理。

## 3. V2 方法精确对照表

V2 注册 12 个方法，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:15-26`。当前 V3 `handler.Map` 注册 11 个 key，见 `internal/sidecar/orch/orchestration/rpc.go:17-58`。

| V2 方法 | V2 证据 | V3 当前状态 | V3 证据 |
| --- | --- | --- | --- |
| `agent.launch` | `go-agent-v2/internal/apiserver/methods_orchestration.go:15,29-71` | 已注册；但 contract 只覆盖 `AgentID/Name/ParentID/Cwd/Command/Env`，不含 V2 `prompt/instructions/dynamic_tools/config` | `internal/sidecar/orch/orchestration/rpc.go:17-19`、`internal/sidecar/orch/orchestration/contract.go:32-39`、`internal/sidecar/orch/orchestration/rpc_types.go:8-17` |
| `agent.submit` | `go-agent-v2/internal/apiserver/methods_orchestration.go:16,73-91` | 已注册并调用 `SubmitTurn` | `internal/sidecar/orch/orchestration/rpc.go:20-29` |
| `agent.submitPrompt` | `go-agent-v2/internal/apiserver/methods_orchestration.go:17,73-91` | 已注册并调用 `SubmitTurn` | `internal/sidecar/orch/orchestration/rpc.go:30-39` |
| `agent.stop` | `go-agent-v2/internal/apiserver/methods_orchestration.go:18,93-108` | 已注册 | `internal/sidecar/orch/orchestration/rpc.go:40-42` |
| `agent.list` | `go-agent-v2/internal/apiserver/methods_orchestration.go:19,110-116` | 已注册 | `internal/sidecar/orch/orchestration/rpc.go:43-45` |
| `agent.getReport` | `go-agent-v2/internal/apiserver/methods_orchestration.go:20,122-135` | 缺失；只能部分由 `AgentSnapshot.LastReport` 派生 | `internal/sidecar/orch/orchestration/rpc.go:46-58`、`internal/sidecar/orch/orchestration/contract.go:41-51` |
| `agent.rememberReportRequest` | `go-agent-v2/internal/apiserver/methods_orchestration.go:21,142-160` | 缺失 | `internal/sidecar/orch/orchestration/rpc.go:17-58` |
| `agent.reportEvent` | `go-agent-v2/internal/apiserver/methods_orchestration.go:22,185-209` | 缺失 | `internal/sidecar/orch/orchestration/rpc.go:17-58` |
| `agent.getState` | `go-agent-v2/internal/apiserver/methods_orchestration.go:23,166-177` | 缺失；只能部分由 `Snapshot.State` 派生 | `internal/sidecar/orch/orchestration/rpc.go:46-48`、`internal/sidecar/orch/orchestration/contract.go:41-51` |
| `agent.saveSubAgent` | `go-agent-v2/internal/apiserver/methods_orchestration.go:24,217-220` | 缺失 | `internal/sidecar/orch/orchestration/rpc.go:17-58` |
| `agent.deleteSubAgent` | `go-agent-v2/internal/apiserver/methods_orchestration.go:25,226-229` | 缺失 | `internal/sidecar/orch/orchestration/rpc.go:17-58` |
| `agent.persistSubAgentBinding` | `go-agent-v2/internal/apiserver/methods_orchestration.go:26,236-241` | 缺失 | `internal/sidecar/orch/orchestration/rpc.go:17-58` |

补充：当前 V3 额外暴露了 6 个不在 V2 12 方法中的 key：`agent.snapshot`、`task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update`、`orchestration/report`，见 `internal/sidecar/orch/orchestration/rpc.go:46-58`。

## 4. 批次 A/C 快扫

- 批次 A 关心的 workspace event 类型已经定义，不是当前 blocker。当前存在 `WorkspaceRunCreated`、`WorkspaceRunStatusChanged`、`WorkspaceRunMerged` 三个 typed event，见 `internal/dto/workspace/event.go:5-32`；共享头 `WorkspaceRunHeader` 也已定义，见 `internal/dto/shared/event.go:94-99`。
- 批次 C 的 `skill/exec.go` 体量远未超限。`document_symbol` 显示最后一个符号 `(*limitedBuffer).String` 位于 `internal/module/skill/exec.go:116`，说明文件当前只有 117 行左右，见 `internal/module/skill/exec.go:104-116`。对照守卫 `MaxFileLines = 400`、`MaxFuncLines = 80`，当前尺寸很安全，见 `internal/archtest/guardlib.go:17-24`。
- 批次 C 的 timeout 缺口确实存在。计划要求“加 30s timeout”，见 `docs/plans/迁移/p2-execution-plan.md:50`；但当前 `ExecCommand` 签名只有 `(ctx, command, args, cwd)`，见 `internal/module/skill/contract.go:13`，实现只是把上层 `ctx` 直接传给 `exec.CommandContext(...)`，并没有在本层创建固定超时，见 `internal/module/skill/exec.go:27-45`、`internal/module/skill/exec.go:70-90`。因此“加 timeout 后是否超限”的答案是：不会，但功能确实还没做。

## 结论（Blocker / Warning / OK）

### Blocker

- B13 计划口径不精确。`docs/plans/迁移/p2-execution-plan.md:36` 把现状写成“只有 4 个可调用”，但代码事实是 V2 同名可调用入口已有 5 个，缺失面也不止 4 个，还包括 `agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:15-26`、`internal/sidecar/orch/orchestration/rpc.go:17-58`。
- B14 的真正 blocker 不是 store，而是 orchestration service/module 还没有 DAG 落点。store 具备 `UpsertDAG/GetDAG/ListDAGs/UpdateNodeStatus`，见 `internal/store/taskdag/contract.go:10-15`；但 current `Service`、`service`、`Module` 都没有 DAG contract 或 `taskdag.Store` 注入，见 `internal/sidecar/orch/orchestration/contract.go:9-24`、`internal/sidecar/orch/orchestration/service.go:28-36,75-89`、`internal/sidecar/orch/orchestration/module.go:15-23`。
- B15 若同时宣称补 `rememberReportRequest` 与 `reportEvent`，那么“复杂归并逻辑先最小版+TODO”风险过高。V2 的 requester 归并与完成态自动回传是这两个 RPC 的核心语义，不是可随意降级的外围逻辑，见 `go-agent-v2/internal/apiserver/orchestration_report.go:23-38,40-77,99-137`、`go-agent-v2/internal/apiserver/methods_orchestration.go:142-159,185-203`。

### Warning

- `agent.getState -> Snapshot`、`agent.getReport -> lastReport` 只能说明底层字段可复用，不能说明方法面已经补齐。当前 `rpc.go` 仍没有这两个入口，见 `internal/sidecar/orch/orchestration/rpc.go:17-58`。
- `create -> UpsertDAG` 这个写法容易误导实现。因为 `createDAGParams` 含 `Nodes`，而 `UpsertDAG` 不处理节点且 `DAG` 还需要 `Status`，见 `internal/sidecar/orch/orchestration/rpc_types.go:32-49`、`internal/store/taskdag/contract.go:10-16,152-164`。
- `service.go` 当前虽未超 400 行，但只剩很小余量；若 B14/B15 直接都堆入同一文件，超限几乎是大概率事件，见 `internal/sidecar/orch/orchestration/service.go:378-390`、`internal/archtest/guardlib.go:17-24`。

### OK

- 计划对 `task/dag/*` 当前是 stub、`service.go` 有超限风险、`skill/exec.go` 需要 timeout 这三件事的方向判断是对的，见 `docs/plans/迁移/p2-execution-plan.md:37,42,50,74`。
- workspace event 类型已经具备，A/C 快扫没有发现新的尺寸级 blocker，见 `internal/dto/workspace/event.go:5-32`、`internal/dto/shared/event.go:94-99`、`internal/module/skill/exec.go:1-117`。

## 互辩

### 对 audit-p2-plan-A 的批判

1. `audit-p2-plan-A.md:23-27` 把 “没有 `WorkspaceRunAborted` DTO” 直接判成 B12 blocker，证据并不充分。`docs/plans/迁移/p2-execution-plan.md:23` 只要求在 `CreateRun/MergeRun/AbortRun/UpdateRunStatus` 成功后发布 typed event，并没有强制要求独立的 aborted 事件类型；当前 `WorkspaceRunStatusChanged` 已经有 `OldStatus/NewStatus`，完全可以表达 `active -> aborted`，而 bus sink 也已经订阅这一类事件，见 `internal/dto/workspace/event.go:13-19`、`internal/platform/bus/sink.go:75-78`。A 把“缺专用 DTO”直接上升为 blocker，定性过重。
2. `audit-p2-plan-A.md:19-21,52-53` 抓到了 `rootDir/bootstrap` 缺口，但遗漏了更直接的 merge 语义污染：当前 `buildRun` 在未传 `workspacePath` 时直接把它退回 `sourceRoot`，见 `internal/module/workspace/service.go:62-66`；随后 `buildRunFile` 会对 `filepath.Join(run.WorkspacePath, rel)` 取 hash，并在 `workspaceHash == sourceHash` 时把文件状态标成 `synced`，见 `internal/module/workspace/service.go:174-197`。这意味着默认路径下，run file 基线从一开始就可能与 source 树重合，后续 merge 判断会被直接污染。A 没把这个问题单列出来，遗漏了更严重的 blocker。
3. `audit-p2-plan-A.md:47` 的批次 B 代码量引用不严谨。它写的是“若再按计划增加约 `80` 行”，但 `docs/plans/迁移/p2-execution-plan.md:40-42` 明确给 B 批次的预估代码量是 `~170` 行，不是 `~80`。虽然它最终仍判断会超限，但引用计划数值本身已经失真，削弱了这一段的证据可靠性。
4. `audit-p2-plan-A.md:25-27` 对 B12 的落点批判仍然偏轻。它提到缺 `WorkspaceRunAborted` 和缺 bus 注入，但没有把“当前连已有 `Created/StatusChanged/Merged` 三类事件都没有任何发布路径”单列为更大的问题。workspace `service` 当前只有 `store` 依赖，见 `internal/module/workspace/service.go:29-31`；`Module` 只提供 `NewService/NewWorkspaceHandlers`，见 `internal/module/workspace/module.go:5-8`；而 bus sink 只是被动订阅，见 `internal/platform/bus/sink.go:75-78`。换言之，DTO 是否齐全还在第二位，真正的一阶缺口是发布链路根本不存在。

### 对 audit-p2-plan-C 的批判

1. `audit-p2-plan-C.md:46-49,67-69` 对 orchestration 方法面仍然低估，和我的 B13 审查矛盾。它把批次 B 完成后的总 key 预估成 `80`，只按计划补 `agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`、`agent.getState` 四个 key；但 V2 还额外有 `agent.saveSubAgent`、`agent.deleteSubAgent`、`agent.persistSubAgentBinding` 三个当前 V3 完全缺失的方法，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:24-26`，而当前 V3 `rpc.go` 里确实没有这些 key，见 `internal/sidecar/orch/orchestration/rpc.go:17-58`。因此它的 `76 + 4 = 80` 只是复述了计划口径，不是基于 V2 parity 的真实缺口。
2. `audit-p2-plan-C.md:38-40,69` 抓到了 `service.go` 行数压力，但遗漏了 B15 更硬的协议层 blocker：当前 `orchestration/report` 的 DTO 只有 `agentId` 和 `report`，见 `internal/sidecar/orch/orchestration/rpc_types.go:83-86`；而 V2 `agent.rememberReportRequest` 需要 `sender_id/worker_id`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:137-160`，V2 `agent.reportEvent` 需要 `agent_id/event_type/event_data`，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:179-209`。也就是说，B15 不是单纯“已接近 400 行还要继续堆代码”，而是当前计划想复用的 `orchestration/report` 入口在参数层就承载不了它宣称的语义。
3. `audit-p2-plan-C.md:29-32,68` 正确指出 `thread/skills/list` 现在会掉进 `unsupported command`，但仍遗漏了更底层的 schema 退化。V3 `thread/skills/list` 经过 `newThreadCommandHandler(svc, "/skills")`，所有 thread command 都统一压成 `commandParams.Args string`，见 `internal/module/thread/rpc.go:68,96-103`、`internal/module/thread/rpc_types.go:34-37`；而 V2 对应的是专门的 `ThreadSkillsList()` provider 路径，不走这个扁平化参数面，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:107-110`。所以即使未来补上 `/skills` 分支，当前 V3 的 RPC 合同也仍未恢复到 V2 等价。
4. `audit-p2-plan-C.md:43-44,72` 对 workspace event 的批判还停留在“有没有专门 aborted DTO”，这一点和 A 类似，也没有抓住发布链路缺失才是一阶问题。当前 workspace 模块没有事件总线依赖，见 `internal/module/workspace/module.go:5-8`、`internal/module/workspace/service.go:29-31`；bus sink 订阅 `WorkspaceRunCreated/WorkspaceRunStatusChanged/WorkspaceRunMerged` 只说明日志侧能消费，不说明 workspace 侧能发布，见 `internal/platform/bus/sink.go:75-78`。因此 C 的定性仍偏向 DTO 层，而没有把 runtime publish gap 提到更高优先级。
