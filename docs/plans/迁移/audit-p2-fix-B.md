# P2 修复审查 — Agent B
## B13/B14/B15 逐项验证+行号
### B13
- `OK` `handler.Map` 已补齐 `agent.getState`、`agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`，且分别调用 `svc.GetState`、`svc.GetReport`、`svc.RememberReportRequest`、`svc.HandleReportEvent`。证据：`internal/sidecar/orch/orchestration/rpc.go:49-59`。
- `OK` 已新建独立 DTO，未复用 `AgentSnapshot`：`AgentStateResult` 定义为 `{agent_id,state}`，`AgentReportResult` 定义为 `{agent_id,report,state,...}`；`GetState` / `GetReport` 返回这两个 DTO。证据：`internal/sidecar/orch/orchestration/contract.go:57-71`，`internal/sidecar/orch/orchestration/report.go:44-64`。
- `Warning` 请求解码层能兼容 V2 snake_case，但声明的 request DTO 仍是 camelCase：`agentIDParams` 的 tag 为 `agentId`，`rememberReportRequestParams` 为 `agentId/requesterId`，`reportEventParams` 为 `agentId/eventType/eventData`；兼容 V2 依赖 `UnmarshalJSON` 回填 `agent_id/sender_id/worker_id/event_type/event_data`。这意味着运行时可接 V2，但声明 wire/schema 不是 V2 原样。证据：`internal/sidecar/orch/orchestration/rpc_types.go:19-39,126-154,156-188`；V2 对照：`go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:259-276`。
- `Blocker` `agent.rememberReportRequest` 的返回 shape 不对齐 V2。当前返回 DTO 为 `{success,agent_id,requester_id}`，V2 返回 `{success,sender_id,worker_id}`。证据：`internal/sidecar/orch/orchestration/contract.go:78-82`，`internal/sidecar/orch/orchestration/report.go:87-88`；V2 对照：`go-agent-v2/internal/apiserver/methods_orchestration.go:154-159`，`go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:269-271`。
- `Warning` `agent.getReport` 返回 shape 比 V2 多了可选 `metadata.requester_ids`；V2 只要求 `agent_id/report/state`。证据：`internal/sidecar/orch/orchestration/contract.go:62-70`，`internal/sidecar/orch/orchestration/report.go:129-140`；V2 对照：`go-agent-v2/internal/apiserver/methods_orchestration.go:130-134`，`go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:259-261`。
- `Warning` `agent.reportEvent` 的校验比 V2 宽松。V2 显式要求 `event_type` 非空；当前 `HandleReportEvent` 只校验 `agent id`，空 `eventType` 仍可返回成功。证据：`internal/sidecar/orch/orchestration/report.go:91-96,115-121`；V2 对照：`go-agent-v2/internal/apiserver/methods_orchestration.go:185-193`。

### B14
- `OK` `dag.go` 已存在并提供真实实现，不再是占位：`CreateDAG` 走 `WithTx -> UpsertDAG -> UpsertNode -> loadDAGDetail`，`GetDAG` 走 `GetDAG/ListNodes`，`ListDAGs` 走 `ListDAGs`，`UpdateNodeStatus` 走 `UpdateNodeStatus`。证据：`internal/sidecar/orch/orchestration/dag.go:14-79`。
- `OK` `taskdag.Store` 已注入到 service，并通过模块层构造进入 `NewService`。证据：`internal/sidecar/orch/orchestration/service.go:29-38,78-93`，`internal/sidecar/orch/orchestration/module.go:15-20`，`internal/store/module.go:28-49`，`internal/store/taskdag/module.go:5-7`，`internal/store/taskdag/store.go:13-18`。
- `OK` DAG RPC handler 已从“不实现”切到真实 service 调用：`task/dag/create|get|list` 与 `task/node/update` 分别直连 `svc.CreateDAG`、`svc.GetDAG`、`svc.ListDAGs`、`svc.UpdateNodeStatus`。证据：`internal/sidecar/orch/orchestration/rpc.go:61-71`。
- `OK` `task/node/update` 调用的是 `taskdag.Store.UpdateNodeStatus`，与 store contract 对齐；当前代码树无 `UpdatePhaseNodeStatus`。证据：`internal/sidecar/orch/orchestration/dag.go:65-78,151-167`，`internal/store/taskdag/contract.go:9-25,47-52`。
- `OK` DAG 返回 DTO 都有显式 JSON tag，避免返回 shape 漂移。证据：`internal/sidecar/orch/orchestration/contract.go:131-168`。

### B15
- `OK` `report.go` 已存在，`lastReport` / `reportRequesters` 也已经进入 agent runtime。证据：`internal/sidecar/orch/orchestration/service.go:40-64`，`internal/sidecar/orch/orchestration/report.go:32-64`。
- `Warning` `orchestration/report` 仍是写接口，不是“真实读 lastReport”。handler 仍调用 `svc.SetReport(ctx, p.AgentID, p.Report)`。证据：`internal/sidecar/orch/orchestration/rpc.go:73-77`，`internal/sidecar/orch/orchestration/report.go:32-41`。
- `OK` `rememberReportRequest` 已把 requester 记录到 runtime `reportRequesters`。证据：`internal/sidecar/orch/orchestration/service.go:52-53`，`internal/sidecar/orch/orchestration/report.go:66-88,143-152`。
- `Warning` `rememberReportRequest` 只把当前 `requesterID` 直接写入 worker runtime，没有复现 V2 的“sender 向上归并到最上层 requester”语义。V2 先 `resolveOrchestrationReportRequesterID`，再写 waiter 状态；当前只有 append/dedup。证据：`internal/sidecar/orch/orchestration/report.go:66-88,143-152`；V2 对照：`go-agent-v2/internal/apiserver/orchestration_report.go:23-37,40-77`。
- `Blocker` `reportEvent` 没有真正触发完成通知。当前实现只在 terminal/report 事件时 drain `reportRequesters`，把结果塞回 `NotifiedRequesterIDs` 返回；`NotifiedRequesterIDs` 的 LSP references 只有 `internal/sidecar/orch/orchestration/contract.go:96` 和 `internal/sidecar/orch/orchestration/report.go:120`，不存在实际投递消费者。V2 则在事件流尾部调用 `maybeAutoReportOrchestrationCompletion`，并向 requester 追加 UI 内部消息。证据：`internal/sidecar/orch/orchestration/report.go:90-121`，`internal/sidecar/orch/orchestration/contract.go:91-97`；V2 对照：`go-agent-v2/internal/apiserver/server_event_handler.go:186-193`，`go-agent-v2/internal/apiserver/orchestration_report.go:99-136`。
- `Warning` `agent.reportEvent` 返回 shape 比 V2 多了可选 `report` / `notified_requester_ids`；V2 只返回 `success/agent_id/event_type`。证据：`internal/sidecar/orch/orchestration/contract.go:91-97`，`internal/sidecar/orch/orchestration/report.go:115-121`；V2 对照：`go-agent-v2/internal/apiserver/methods_orchestration.go:204-208`，`go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:274-276`。

## 拆分验证
- `OK` `service.go` 当前 382 行，满足 `<= 400`。证据：`internal/sidecar/orch/orchestration/service.go:380-382`。
- `OK` `dag.go` 当前 257 行。证据：`internal/sidecar/orch/orchestration/dag.go:250-257`。
- `OK` `report.go` 当前 229 行。证据：`internal/sidecar/orch/orchestration/report.go:220-229`。
- `OK` 新拆分方法未发现重复定义。LSP `text_search` 仅命中单一定义：`GetState` `internal/sidecar/orch/orchestration/report.go:44`，`GetReport` `internal/sidecar/orch/orchestration/report.go:55`，`RememberReportRequest` `internal/sidecar/orch/orchestration/report.go:66`，`HandleReportEvent` `internal/sidecar/orch/orchestration/report.go:91`，`CreateDAG` `internal/sidecar/orch/orchestration/dag.go:14`，`GetDAG` `internal/sidecar/orch/orchestration/dag.go:41`，`ListDAGs` `internal/sidecar/orch/orchestration/dag.go:49`，`UpdateNodeStatus` `internal/sidecar/orch/orchestration/dag.go:65`。

## 编译守卫
- `OK` `go build ./...` 通过。
- `OK` `go vet ./...` 通过。
- `OK` `go test ./internal/archtest/... -count=1 -timeout 120s` 通过，输出 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.164s`。

## 结论（Blocker / Warning / OK）
- `Blocker`
- 阻断项 1：`agent.rememberReportRequest` 返回 wire shape 仍与 V2 不一致，见 `internal/sidecar/orch/orchestration/contract.go:78-82` 对比 `go-agent-v2/internal/apiserver/methods_orchestration.go:154-159`。
- 阻断项 2：`agent.reportEvent` 仍未形成真正的完成通知投递链，见 `internal/sidecar/orch/orchestration/report.go:90-121` 对比 `go-agent-v2/internal/apiserver/server_event_handler.go:186-193` 与 `go-agent-v2/internal/apiserver/orchestration_report.go:99-136`。
- 非阻断但仍需收口：request DTO tag 仍非 V2 snake_case、`orchestration/report` 仍是写接口、`rememberReportRequest` 未做顶层 requester 归并、`agent.getReport` / `agent.reportEvent` 结果存在附加字段。

## 互辩
### 对 audit-p2-fix-A 的批判
1. `docs/plans/迁移/audit-p2-fix-A.md:20,33-34` 把 B12 只落成 `Warning`，但代码问题更重。`MergeRun` 在 conflict/error 分支 `internal/module/workspace/service.go:219-221` 直接返回，既不 `transitionRunStatus`，也不发任何 workspace event；而 sink 只订阅 `WorkspaceRunCreated/WorkspaceRunStatusChanged/WorkspaceRunMerged/WorkspaceRunAborted` 四类事件 `internal/platform/bus/sink.go:75-79`。这不是“merge 事件不完整”这么轻，而是失败终态对事件观察侧完全不可见。
2. `docs/plans/迁移/audit-p2-fix-A.md:15-20,34` 漏掉了更硬的事件链断点。`emitRunStatusChanged` 的 LSP incoming call hierarchy 只有 `UpdateRunStatus -> internal/module/workspace/service.go:202`；`MergeRun` 和 `AbortRun` 只分别调用 `emitRunMergedEvent` / `emitRunAbortedEvent`，见 `internal/module/workspace/service.go:239,254`，对应 helper 为 `internal/module/workspace/service_helpers.go:222-238`。也就是说即便 merge/abort 成功，通用 `WorkspaceRunStatusChanged` 事件仍不会发，报告没有指出这一点。
3. `docs/plans/迁移/audit-p2-fix-A.md:8,32` 正确指出 B6 缺 `dryRun/deleteRemoved` 语义，但证据停在 service/contract 层，没追到 RPC wire 面。实际 `MergeRunRequest` 只有 `runKey/updatedBy` `internal/module/workspace/contract.go:39-42`，`mergeRunParams` 直接等于该 request `internal/module/workspace/rpc_types.go:3-5`，handler 只是原样透传 `internal/module/workspace/rpc.go:75-85`。所以 parity 缺口不是“实现不完整”而已，而是方法签名本身已经缺字段。
4. `docs/plans/迁移/audit-p2-fix-A.md:9-11,35` 把 B7 判成 `OK` 过快。`AbortRun` 确实把 `reason` 写进 metadata `internal/module/workspace/service.go:243-250`，但 RPC 返回的是 `runResult{Run: run}` `internal/module/workspace/rpc.go:88-100`，而 `Run` 只是 `WorkspaceRun` alias，没有显式 `Reason` 字段，只有原始 `Metadata` `internal/module/workspace/contract.go:22-37`、`internal/store/workspace/contract.go:47-60`。报告证明了写侧，没有证明调用方可观测侧。

### 对 audit-p2-fix-C 的批判
1. `docs/plans/迁移/audit-p2-fix-C.md:17,30` 把 orchestration DAG/report 链路写成 `OK`，但它只验证了“有接口、有注册”，没有验证 V2 wire compatibility，和我的 orchestration blocker 直接冲突。当前 `RememberReportRequestResult` 返回的是 `{success,agent_id,requester_id}` `internal/sidecar/orch/orchestration/contract.go:78-82`，实现位于 `internal/sidecar/orch/orchestration/report.go:87-88`；V2 返回 `{success,sender_id,worker_id}` `go-agent-v2/internal/apiserver/methods_orchestration.go:154-159`。此外 request DTO 仍以 camelCase tag 为主，只靠 `UnmarshalJSON` 回填 V2 snake_case，见 `internal/sidecar/orch/orchestration/rpc_types.go:126-188`。
2. `docs/plans/迁移/audit-p2-fix-C.md:17` 所谓 “report.go 与 fx 注册完整” 不能推出“跨模块链路闭合”。`SetReport` 和 `HandleReportEvent` 的 LSP incoming call hierarchy 都只有 `NewOrchestrationHandlers` 一条入边，调用点分别是 `internal/sidecar/orch/orchestration/rpc.go:74,59`；也就是说它们只挂在 RPC 上，没有接入 provider/event 流。V2 的完成报告链路则是 `server_event_handler.go:186-193 -> orchestration_report.go:99-136`。C 报告把“可调用”误写成了“已接通”。
3. `docs/plans/迁移/audit-p2-fix-C.md:15` 的跨模块 key 统计有算术错误。LSP `text_search` 在 `internal/module/thread/rpc.go` 返回 29 个 `"thread/"` handler，分布在 `internal/module/thread/rpc.go:20,32-39,41,44,47-70,75,79-82`；报告写成 28 个。这会直接削弱它对 handler.Map 总量统计的可信度。
4. `docs/plans/迁移/audit-p2-fix-C.md:28` 对 I1 的 blocker 描述不够尖锐。它只抓到了 caller-supplied `env` overlay 丢失，但没指出 wire shape 也已经破了：V3 `command/exec` 请求是 `{command,args,cwd}` `internal/module/skill/rpc_types.go:26-30`，handler 只传这三项 `internal/module/skill/rpc.go:51-53`；V2 请求是 `{argv,cwd,env}` `go-agent-v2/internal/apiserver/methods_command.go:20-24,40-57`。即使把 `env` 加回来，`argv` 级兼容仍然未恢复。
