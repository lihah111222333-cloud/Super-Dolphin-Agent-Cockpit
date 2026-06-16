# P5 波次 2 审查 R4（workspace）

## 1. 编译+守卫

- 题设已声明“编译+守卫已通过”，本轮不复验测试结果。
- 仅确认当前模块关键编译入口齐备：`Module` 提供 `NewService` 与 `NewWorkspaceHandlers`，见 `internal/module/workspace/module.go:5-8`；`NewService` 返回 `Service` 实现，见 `internal/module/workspace/service.go:22`；`NewWorkspaceHandlers` 返回 `rpc.HandlerMapResult`，见 `internal/module/workspace/rpc.go:13-54`。

## 2. 方法完整性

- V3 `handler.Map` 共 8 个 key，分别是：
  `workspace/run/create` `internal/module/workspace/rpc.go:15`
  `workspace/run/get` `internal/module/workspace/rpc.go:19`
  `workspace/run/list` `internal/module/workspace/rpc.go:23`
  `workspace/run/status/update` `internal/module/workspace/rpc.go:27`
  `workspace/run/merge` `internal/module/workspace/rpc.go:31`
  `workspace/run/abort` `internal/module/workspace/rpc.go:35`
  `workspace/run/files/list` `internal/module/workspace/rpc.go:45`
  `workspace/run/file/get` `internal/module/workspace/rpc.go:49`
- V2 `go-agent-v2/internal/apiserver/workspace_methods.go` 中只有 5 个 workspace RPC 方法：
  `workspaceRunCreate` `go-agent-v2/internal/apiserver/workspace_methods.go:41-62`
  `workspaceRunGet` `go-agent-v2/internal/apiserver/workspace_methods.go:64-83`
  `workspaceRunList` `go-agent-v2/internal/apiserver/workspace_methods.go:85-113`
  `workspaceRunMerge` `go-agent-v2/internal/apiserver/workspace_methods.go:115-136`
  `workspaceRunAbort` `go-agent-v2/internal/apiserver/workspace_methods.go:138-167`
- 逐一核对后，V3 当前 8 个 key 本身是齐全的；其中 `create/get/list/merge/abort` 可与 V2 逐项对应，`status/update`、`files/list`、`file/get` 是 V3 新增面，不在 V2 `workspace_methods.go` 内。

## 3. import 方向

- `internal/module/workspace/contract.go` import 列表为 `context`、`encoding/json`、`time`、`internal/store/workspace`，见 `internal/module/workspace/contract.go:3-9`。
- `internal/module/workspace/module.go` 仅 import `go.uber.org/fx`，见 `internal/module/workspace/module.go:3`。
- `internal/module/workspace/rpc.go` import 列表为 `context`、`errors`、`strings`、`github.com/creachadair/jrpc2/handler`、`internal/platform/rpc`，见 `internal/module/workspace/rpc.go:3-11`。
- `internal/module/workspace/rpc_types.go` 无 import block，文件范围见 `internal/module/workspace/rpc_types.go:1-31`。
- `internal/module/workspace/service.go` import 列表为 `context`、`errors`、`strconv`、`strings`、`time`、`internal/store/workspace`，见 `internal/module/workspace/service.go:3-11`。
- 结论：审查范围内未出现 `provider/*` import。

## 4. 行数

- `internal/module/workspace/contract.go` 共 35 行，范围 `internal/module/workspace/contract.go:1-35`；无函数。
- `internal/module/workspace/module.go` 共 8 行，范围 `internal/module/workspace/module.go:1-8`；无函数。
- `internal/module/workspace/rpc.go` 共 68 行，范围 `internal/module/workspace/rpc.go:1-68`；最长函数是 `NewWorkspaceHandlers`，范围 `internal/module/workspace/rpc.go:13-54`，42 行。
- `internal/module/workspace/rpc_types.go` 共 31 行，范围 `internal/module/workspace/rpc_types.go:1-31`；无函数。
- `internal/module/workspace/service.go` 共 92 行，范围 `internal/module/workspace/service.go:1-92`；最长函数是 `(*service).CreateRun`，范围 `internal/module/workspace/service.go:24-57`，34 行。
- 模块内最长函数是 `NewWorkspaceHandlers`，见 `internal/module/workspace/rpc.go:13-54`。

## 5. Service 接口

- `Service` 定义了 8 个方法，见 `internal/module/workspace/contract.go:11-20`。
- `rpc.go` 中 8 个 handler 对 `Service` 的调用如下：
  `CreateRun` `internal/module/workspace/rpc.go:15-18`
  `GetRun` `internal/module/workspace/rpc.go:19-22`
  `ListRuns` `internal/module/workspace/rpc.go:23-26`
  `UpdateRunStatus` `internal/module/workspace/rpc.go:27-30`
  `MergeRun` `internal/module/workspace/rpc.go:31-34`
  `AbortRun` 和后续 `GetRun` `internal/module/workspace/rpc.go:35-43`
  `ListRunFiles` `internal/module/workspace/rpc.go:45-48`
  `GetRunFile` `internal/module/workspace/rpc.go:49-52`
- 结论：`contract.go` 对“当前 V3 的 rpc.go”是全覆盖的。
- 但接口形状已明显收窄：`ListRuns(ctx)` 无任何过滤参数，见 `internal/module/workspace/contract.go:14`；`AbortRun(ctx, runKey)` 无 `updatedBy/reason`，见 `internal/module/workspace/contract.go:17`。这与 V2 `workspaceRunList`/`workspaceRunAbort` 暴露的请求面不一致，见 `go-agent-v2/internal/apiserver/workspace_methods.go:90-101` 与 `go-agent-v2/internal/apiserver/workspace_methods.go:143-154`。

## 6. service 实现

- `service` 明确持有 `storeworkspace.Store` 并由 `NewService` 注入，见 `internal/module/workspace/service.go:20-22`。
- 下列方法是直接委托 store：
  `CreateRun` 最终调用 `s.store.UpsertRun(...)`，见 `internal/module/workspace/service.go:46-56`
  `GetRun` 调用 `s.store.GetRun(...)`，见 `internal/module/workspace/service.go:59-61`
  `ListRuns` 调用 `s.store.ListRuns(...)`，见 `internal/module/workspace/service.go:63-65`
  `UpdateRunStatus` 调用 `s.store.UpdateRunStatus(...)`，见 `internal/module/workspace/service.go:67-72`
  `ListRunFiles` 调用 `s.store.ListFiles(...)`，见 `internal/module/workspace/service.go:83-88`
  `GetRunFile` 调用 `s.store.GetFile(...)`，见 `internal/module/workspace/service.go:90-92`
- `CreateRun` 只有轻量归一化逻辑：校验 `sourceRoot` 非空、生成 `runKey`、补默认 `status`，并在 `workspacePath` 为空时直接回落为 `sourceRoot`，见 `internal/module/workspace/service.go:25-45`。随后直接写库，见 `internal/module/workspace/service.go:46-56`。
- `MergeRun` 只是 `UpdateRunStatus(..., "merged")` 的薄封装，见 `internal/module/workspace/service.go:74-76`。
- `AbortRun` 只是 `UpdateRunStatus(..., "aborted")` 的薄封装，见 `internal/module/workspace/service.go:78-80`。
- 对照 V2：
  `CreateRun` 会做绝对路径化、目录存在性检查、创建独立 `workspacePath` 目录并 bootstrap 文件，见 `go-agent-v2/internal/service/workspace.go:185-240`
  `AbortRun` 会写入 `reason` metadata，见 `go-agent-v2/internal/service/workspace.go:277-280`
  `MergeRun` 会先转状态，再遍历 workspace、统计结果、处理删除文件，见 `go-agent-v2/internal/service/workspace.go:306-373`
- 结论：当前 service 对“run 行记录”的 CRUD 不是空壳，但对 workspace 生命周期核心行为是明显骨架化/退化。

## 7. store 对齐

- `internal/store/workspace/contract.go` 的 `Store` 一共 8 个方法，见 `internal/store/workspace/contract.go:9-18`。
- `service` 实际只调用了其中 6 个：`UpsertRun`、`GetRun`、`ListRuns`、`UpdateRunStatus`、`ListFiles`、`GetFile`，见 `internal/module/workspace/service.go:46`、`internal/module/workspace/service.go:60`、`internal/module/workspace/service.go:64`、`internal/module/workspace/service.go:68`、`internal/module/workspace/service.go:84`、`internal/module/workspace/service.go:91`。
- `TransitionRunStatus` 与 `UpsertFile` 在当前 service 层无对应调用，定义见 `internal/store/workspace/contract.go:14-15`。
- `ListRunsFilter` 支持 `Status`、`DagKey`、`Limit`，见 `internal/store/workspace/contract.go:20-24`；但 service 只传 `Limit: defaultListLimit`，见 `internal/module/workspace/service.go:63-65`。
- `UpdateRunStatusInput` 支持 `UpdatedBy` 与 `Metadata`，见 `internal/store/workspace/contract.go:26-31`；但 service 只填了 `RunKey` 与 `Status`，见 `internal/module/workspace/service.go:67-71`。
- 结论：service 到 store 不是一一对应；当前存在“store 能力更宽，service/RPC 面收窄”的情况。

## 8. rpc_types.go

- `runKeyParams`、`updateRunStatusParams`、`runFileParams` 这三类输入包装一致，见 `internal/module/workspace/rpc_types.go:3-15`。
- `runResult`、`runsResult`、`runFileResult`、`runFilesResult` 的输出包装也一致，见 `internal/module/workspace/rpc_types.go:17-31`。
- 主要问题是“参数建模不足”：
  `workspace/run/list` 没有独立 params struct，handler 直接接受空结构体，见 `internal/module/workspace/rpc.go:23-25`
  `workspace/run/merge` 复用 `runKeyParams`，见 `internal/module/workspace/rpc.go:31-33` 与 `internal/module/workspace/rpc_types.go:3-5`
  `workspace/run/abort` 同样只接受 `runKeyParams`，见 `internal/module/workspace/rpc.go:35-43` 与 `internal/module/workspace/rpc_types.go:3-5`
- 对照 V2：
  `WorkspaceCreateRequest` 包含 `Files`，见 `go-agent-v2/internal/service/workspace.go:57-64`
  `WorkspaceMergeRequest` 包含 `UpdatedBy`、`DryRun`、`DeleteRemoved`，见 `go-agent-v2/internal/service/workspace.go:66-70`
  `workspaceRunAbort` 解析 `RunKey`、`UpdatedBy`、`Reason`，见 `go-agent-v2/internal/apiserver/workspace_methods.go:143-147`
- 结论：`rpc_types.go` 的共用模式本身整齐，但请求建模明显低配，无法承载 V2 的 merge/list/abort 语义。

## 9. HandlerMapResult

- `rpc.HandlerMapResult` 是 `fx.Out`，并把 `Handlers` 放入 `group:"rpc_handlers"`，见 `internal/platform/rpc/module.go:31-35`。
- `NewWorkspaceHandlers` 的返回类型就是 `rpc.HandlerMapResult`，见 `internal/module/workspace/rpc.go:13-54`。
- `workspace.Module` 已提供 `NewWorkspaceHandlers`，见 `internal/module/workspace/module.go:5-8`。
- `app.Module` 已注册 `workspace.Module`，见 `internal/app/modules.go:23-39`，其中 `workspace.Module` 在 `internal/app/modules.go:36`。
- `rpc.Module` 通过 `registerAllHandlers` 消费 `group:"rpc_handlers"`，见 `internal/platform/rpc/module.go:13-22` 与 `internal/platform/rpc/module.go:45-47`。
- 结论：`HandlerMapResult` 输出到 fx group，且 workspace 模块已被主应用注册。

## 10. V2 对照

- 对照方法 1，`workspaceRunList`：
  V2 解析 `status`、`dagKey`、`limit`，并把它们传给 manager，见 `go-agent-v2/internal/apiserver/workspace_methods.go:90-101`；manager 继续把这三个参数传给 store，见 `go-agent-v2/internal/service/workspace.go:249-250`。
  V3 handler 完全不接收列表参数，见 `internal/module/workspace/rpc.go:23-25`；service 也硬编码 `Limit: 200`，见 `internal/module/workspace/service.go:63-65`。
- 对照方法 2，`workspaceRunMerge`：
  V2 解析 `WorkspaceMergeRequest`，见 `go-agent-v2/internal/apiserver/workspace_methods.go:120-127`；返回值是 `{"result": result}`，见 `go-agent-v2/internal/apiserver/workspace_methods.go:134-135`；manager 会实际执行 merge，见 `go-agent-v2/internal/service/workspace.go:306-373`。
  V3 只接收 `runKey`，见 `internal/module/workspace/rpc.go:31-33` 与 `internal/module/workspace/rpc_types.go:3-5`；返回值是 `runResult`，见 `internal/module/workspace/rpc.go:31-34`；service 只把状态改成 `merged`，见 `internal/module/workspace/service.go:74-76`。
- 补充对照，`workspaceRunAbort`：
  V2 解析 `runKey`、`updatedBy`、`reason`，见 `go-agent-v2/internal/apiserver/workspace_methods.go:143-154`；manager 会把 `reason` 写入 metadata，见 `go-agent-v2/internal/service/workspace.go:277-280`。
  V3 只接收 `runKey`，见 `internal/module/workspace/rpc.go:35-43` 与 `internal/module/workspace/rpc_types.go:3-5`；service 只做状态更新，见 `internal/module/workspace/service.go:78-80`。

## 结论

### Blocker

- `workspace/run/list` 在 V3 中丢失了 `status/dagKey/limit` 输入面，且 service 硬编码 `Limit: 200`；这不是实现细节差异，而是 API 能力回退。证据：`internal/module/workspace/rpc.go:23-25`、`internal/module/workspace/service.go:63-65`、`go-agent-v2/internal/apiserver/workspace_methods.go:90-101`、`go-agent-v2/internal/service/workspace.go:249-250`。
- `workspace/run/merge` 在 V3 中不是“merge”，只是把 run 状态更新成 `merged`，同时返回 `runResult` 而非 merge 结果对象；相对 V2 的真实 merge 语义是功能级回退。证据：`internal/module/workspace/rpc.go:31-34`、`internal/module/workspace/service.go:74-76`、`go-agent-v2/internal/apiserver/workspace_methods.go:120-135`、`go-agent-v2/internal/service/workspace.go:306-373`。
- `workspace/run/abort` 在 V3 中丢失 `updatedBy` 与 `reason`，导致 abort 的审计信息无法表达；相对 V2 的请求面和 metadata 写入都是回退。证据：`internal/module/workspace/rpc.go:35-43`、`internal/module/workspace/rpc_types.go:3-5`、`go-agent-v2/internal/apiserver/workspace_methods.go:143-154`、`go-agent-v2/internal/service/workspace.go:277-280`。
- `CreateRun` 在 V3 中只做轻量字段归一化后写库，不再创建独立 workspace 目录，也不再 bootstrap `Files`；如果按 V2 workspace manager 语义衡量，这是核心行为缺失。证据：`internal/module/workspace/service.go:24-56`、`internal/store/workspace/store.go:15-32`、`go-agent-v2/internal/service/workspace.go:176-243`、`go-agent-v2/internal/service/workspace.go:57-64`。

### Improvement

- service 与 store 不一一对应，且 service 主动丢弃了 store 已具备的 richer input：`TransitionRunStatus`/`UpsertFile` 未暴露，`ListRunsFilter.Status/DagKey` 与 `UpdateRunStatusInput.UpdatedBy/Metadata` 未向上透出。证据：`internal/store/workspace/contract.go:14-31`、`internal/module/workspace/service.go:63-71`。
- import 方向与 fx 注册链路是干净的：workspace 模块内部未引入 `provider/*`，且 handler 已正确进入 `rpc_handlers` group。证据：`internal/module/workspace/contract.go:3-9`、`internal/module/workspace/module.go:3`、`internal/module/workspace/rpc.go:3-11`、`internal/module/workspace/service.go:3-11`、`internal/platform/rpc/module.go:31-35`、`internal/module/workspace/module.go:5-8`、`internal/app/modules.go:36`。

## 互辩：批判 R3（skill）

1. R3 的方法计数结论没有把基线拆清楚，导致“22 个齐全”这句话容易误导。R3 在 `docs/plans/迁移/p5-wave2-audit-R3.md:44-46` 收束为“15 个对齐 + 7 个新增”；但代码层实际应明确拆成 `7` 个 `command/card/*`（`internal/module/skill/rpc.go:20-30`）+ `1` 个 `command/exec`（`internal/module/skill/rpc.go:31-33`）+ `14` 个 `skills/*`（`internal/module/skill/rpc.go:34-61`）。V2 `registerSkillMethods` 本身只有 14 个 `skills/*`，见 `go-agent-v2/internal/apiserver/methods.go:229-237`；`command/exec` 来自 `registerCoreMethods`，见 `go-agent-v2/internal/apiserver/methods.go:157-160`。也就是说，旧基线是 `14 + 1`，不是“V2 自带 22”。R3 没把这层说死，计数争议没有彻底关门。

2. R3 对 `command/exec` 的依赖判断不够扎实。报告在 `docs/plans/迁移/p5-wave2-audit-R3.md:121-132` 只说明它“是真实实现”，但没有显式证明“它为什么不需要 SessionResolver 一类运行时依赖”。实际代码里，`ExecCommand` 直接走本地 `exec.CommandContext(ctx, name, args...)`，见 `internal/module/skill/exec.go:27-59`，而 `service` 结构体只注入 `commandcard.Store`、`prompt.Store` 和 `http.Client`，见 `internal/module/skill/service.go:18-33`。这说明它确实是本地执行路径；R3 的底层判断大体对，但证据链不完整。

3. R3 对 card handler 工厂的评估仍然偏定性。报告在 `docs/plans/迁移/p5-wave2-audit-R3.md:94-100` 说“部分消重”，但没有把覆盖率量化。实际 `rpc.go` 里唯一真正作用于 card 路由的 helper 只有 `cardByKey`，定义于 `internal/module/skill/rpc.go:13-15`，且只覆盖 `command/card/get`、`command/card/delete`、`command/card/versions` 三处，见 `internal/module/skill/rpc.go:21,28,30`；全部 card 路由一共 7 个，见 `internal/module/skill/rpc.go:20-30`。`namedContent` 虽然也复用 3 次，但它覆盖的是 `skills/remote/export`、`skills/remote/write`、`skills/config/write`，见 `internal/module/skill/rpc.go:47,51,55`，不属于 card CRUD 工厂。R3 应该明确写成“card 工厂只吃掉 3/7，另外 4/7 仍是内联样板”，否则结论过软。

4. R3 对 auto-match 的“下沉到 service”表述过于乐观。报告在 `docs/plans/迁移/p5-wave2-audit-R3.md:127,149-157` 反复强调 auto-match 已下沉，但代码显示它只被 `skills/match/preview` 这一条 RPC 调用：RPC 入口见 `internal/module/skill/rpc.go:61`，`MatchPreview` 内部才创建 collector，见 `internal/module/skill/skills_match.go:14-16`。`skill.Module` 只有 `NewService` 和 `NewSkillHandlers` 两个 provider，没有 `fx.Invoke` 或事件接线，见 `internal/module/skill/module.go:5-8`；同时 `collectChangedSkillNames` 只是孤立定义，见 `internal/module/skill/skills_match.go:105-120`。也就是说，当前 auto-match 不是“运行时已接线的系统能力”，而是“仅供 preview RPC 调用的局部逻辑”。R3 漏掉了这一层。

## 互辩：批判 R5（orchestration）

1. R5 对 `module.go` 的验证不充分。报告在 `docs/plans/迁移/p5-wave2-audit-R5.md:56-59,95` 只证明了 `NewOrchestrationHandlers` 能进入 `rpc_handlers`，但没有核对同一个 `module.go` 里另外两条关键装配线：`func(s *service) Service { return s }` 负责把具体实现抬成接口，见 `internal/sidecar/orch/orchestration/module.go:8`；`fx.Annotate(NewRunnerActor, fx.ResultTags(\`group:"runners"\`))` 负责把 runner actor 注入运行时，见 `internal/sidecar/orch/orchestration/module.go:10`。`app` 侧运行时实际消费的是 `group:"runners"`，见 `internal/app/runner.go:13-26`，而 `orchestration.Module` 与 `platformrunner.Module` 都由主模块引入，见 `internal/app/modules.go:29,35`。不把这条 runner 链走完，不能算“module.go 已验证充分”。

2. R5 低估了未完成面的交付风险。报告在 `docs/plans/迁移/p5-wave2-audit-R5.md:40-45,87-89` 主要强调 DAG TODO，但当前 `rpc.go` 一共暴露 9 个 key，真正落到 service 的只有 4 个：`agent.launch`、`agent.stop`、`agent.list`、`agent.snapshot`，见 `internal/sidecar/orch/orchestration/rpc.go:13-29`；另外 5 个 key 全都直接走 `newNotImplementedHandler`，包括 `task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update`、`orchestration/report`，见 `internal/sidecar/orch/orchestration/rpc.go:30-40`。如果按“可实际调用成功的 RPC”算，当前完成度是 `4/9`；即便只按 contract 中 4 个 DAG TODO 算，也最多只是 `5/9`。R5 没把这个比例上提到交付结论层，风险表达偏轻。

3. R5 没指出 `agent.submit` / `agent.submitPrompt` 属于“已有 service 能力但未接 RPC”的近距缺口。报告在 `docs/plans/迁移/p5-wave2-audit-R5.md:11-15,86` 只把它们列为 V2 缺失方法，但 V2 这两个路由确实存在，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:16-17,73-91`；而 V3 `Service` 已经声明了 `SubmitTurn`，见 `internal/sidecar/orch/orchestration/contract.go:14`，并且 `service` 也有实现，见 `internal/sidecar/orch/orchestration/service.go:140-159`。当前问题不是“底层完全不存在”，而是 `rpc.go` 没有把现成 service 接成 `agent.submit` / `agent.submitPrompt`。这会影响后续修复优先级判断，R5 漏掉了。

4. R5 的注册面对照没有转化成清晰的“V2 可用面 vs V3 可用面”口径。它在 `docs/plans/迁移/p5-wave2-audit-R5.md:10-15` 正确指出 V2 注册了 12 个方法、V3 只有 9 个 key，但没有进一步收束出“V2 的 12 个方法里，V3 目前只有 3 个同名保留，另有 1 个近似替换（`agent.snapshot` vs `agent.getState`），同时还暴露了 5 个稳定失败的 stub 路由”。代码证据分别是 V2 注册表 `go-agent-v2/internal/apiserver/methods_orchestration.go:15-26`、V3 注册表 `internal/sidecar/orch/orchestration/rpc.go:13-34`、以及 stub 实现 `internal/sidecar/orch/orchestration/rpc.go:38-40`。R5 虽然逐点提到了这些事实，但没有把它们压成足够尖锐的迁移结论。
