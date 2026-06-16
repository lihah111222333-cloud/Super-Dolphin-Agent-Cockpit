# P2 计划审查 — Agent A

## 1. 批次 A 可行性（B6/B7/B8/B12/I7 逐项验证+行号）

### B6 merge

- `Blocker`：计划把 B6 的目标文件写成仅 `service.go`，但当前 V3 `MergeRun` 对外签名是 `MergeRun(ctx, runKey string) (*Run, error)`，RPC 也只接收 `runKeyParams` 并返回 `runResult`，根本没有 V2 所需的 `WorkspaceMergeRequest/WorkspaceMergeResult` 入口与返回面。证据：`docs/plans/迁移/p2-execution-plan.md:20`，`internal/module/workspace/contract.go:11-19`，`internal/module/workspace/rpc.go:75-85`，`internal/module/workspace/service.go:102-105`，`go-agent-v2/internal/service/workspace.go:306-373`。
- `Warning`：store 原语大体够用，但不是 V2 的一一对应。V2 merge 依赖 `TryTransitionRunStatus/GetRun/ListFiles/SaveFile`，V3 只有 `TransitionRunStatus/GetRun/ListFiles/UpsertFile`，缺少布尔型 CAS helper `TryTransitionRunStatus(...)(run, transitioned, error)`；当前只能靠 `TransitionRunStatus + GetRun` 手动恢复 CAS miss。证据：`internal/store/workspace/contract.go:9-18`，`internal/module/workspace/service.go:123-147`，`go-agent-v2/internal/service/workspace.go:283-304`，`go-agent-v2/internal/service/workspace.go:348-372`。
- `Blocker`：当前 V3 运行态与文件态不足以承载 V2 merge 主路径。V2 依赖 `merging/failed` 运行状态和 `changed/merged/conflict/error/unchanged` 文件状态；V3 当前只声明 `active/aborted/merged` 与 `tracked/synced`。证据：`internal/module/workspace/service.go:20-26`，`internal/module/workspace/service.go:184-196`，`go-agent-v2/internal/service/workspace.go:23-34`，`go-agent-v2/internal/service/workspace.go:318-345`，`go-agent-v2/internal/service/workspace.go:442-558`。

### B7 abort

- `OK`：store contract 已有计划要求的落点字段，`TransitionRunStatusInput` 包含 `UpdatedBy string` 和 `Metadata json.RawMessage`，类型上可承接 `updatedBy/reason`。证据：`docs/plans/迁移/p2-execution-plan.md:21`，`internal/store/workspace/contract.go:33-39`。
- `Blocker`：当前模块层不存在 `abortRunParams`。`rpc_types.go` 只有 `runKeyParams/listRunsParams/updateRunStatusParams/runFileParams`，abort handler 直接复用 `runKeyParams`。证据：`internal/module/workspace/rpc_types.go:5-23`，`internal/module/workspace/rpc.go:88-100`。
- `Blocker`：`AbortRun` 的 service/contract 签名仍只有 `runKey`，`transitionRunStatus` 也没有把 `UpdatedBy/Metadata` 下传到 store，因此 B7 不只是“补字段”，还必须改接口面。证据：`internal/module/workspace/contract.go:16-19`，`internal/module/workspace/service.go:107-110`，`internal/module/workspace/service.go:128-132`。

### B8 CreateRun

- `Warning`：V3 在语言层面当然可以调用 `os.MkdirAll`，因为 `service.go` 已引入 `os/filepath`；但当前实现没有任何 workspace root 约定或注入点。service 结构只有 `store`，`module.go` 也只注入 `NewService/NewWorkspaceHandlers`。证据：`internal/module/workspace/service.go:3-18`，`internal/module/workspace/service.go:29-31`，`internal/module/workspace/module.go:5-8`。
- `Blocker`：当前 `CreateRun` 不创建目录，且在未传 `workspacePath` 时直接把 `workspacePath` 退回 `sourceRoot`；这与计划中“基于 CWD + runKey 创建 workspace 目录”不一致。证据：`docs/plans/迁移/p2-execution-plan.md:22`，`internal/module/workspace/service.go:33-45`，`internal/module/workspace/service.go:62-66`。
- `Blocker`：V2 CreateRun 依赖明确的 `rootDir`、`filepath.Join(m.rootDir, runKey, "workspace")`、`os.MkdirAll` 和 `bootstrapFiles`；V3 当前没有 `rootDir` 字段、没有 bootstrap copy 流程，也没有 sourceRoot 绝对化和目录校验。证据：`go-agent-v2/internal/service/workspace.go:176-243`，`internal/module/workspace/service.go:33-45`，`internal/module/workspace/service.go:149-172`。

### B12 事件

- `Blocker`：计划要求发布 `RunCreated/RunMerged/RunAborted/RunStatusChanged`，但当前 DTO 只定义了 `WorkspaceRunCreated/WorkspaceRunStatusChanged/WorkspaceRunMerged`，没有 `WorkspaceRunAborted`。证据：`docs/plans/迁移/p2-execution-plan.md:23`，`internal/dto/workspace/event.go:5-32`。
- `Blocker`：计划把 B12 的目标文件写成 `service.go, module.go`，但如果要补 `RunAborted` typed event，至少还要改 `internal/dto/workspace/event.go`，否则连类型都不存在。证据：`docs/plans/迁移/p2-execution-plan.md:23`，`internal/dto/workspace/event.go:5-32`。
- `Warning`：workspace 模块目前没有事件总线注入面。service 仅依赖 `storeworkspace.Store`，`module.go` 只注册 `NewService/NewWorkspaceHandlers`；B12 虽可做，但计划低估了依赖扩展量。证据：`internal/module/workspace/service.go:29-31`，`internal/module/workspace/module.go:5-8`。

### I7 ListFilesFilter.State

- `OK`：`ListFilesFilter` 的 `State` 字段已存在，store 确实有承接位。证据：`docs/plans/迁移/p2-execution-plan.md:24`，`internal/store/workspace/contract.go:41-45`。
- `Blocker`：计划把 I7 的目标文件写成 `rpc_types.go, service.go` 不够。当前 `Service.ListRunFiles` 仍是 `ListRunFiles(ctx, runKey string)`，handler 也只接 `runKeyParams`；如果要让 RPC 真能传 `state`，还必须修改 `contract.go` 与 `rpc.go`。证据：`docs/plans/迁移/p2-execution-plan.md:24`，`internal/module/workspace/contract.go:18`，`internal/module/workspace/rpc.go:104-114`，`internal/module/workspace/service.go:112-116`。

## 2. 代码量预估

- `OK`：按文件总行数看，`workspace/service.go` 当前约 235 行，若只增加 `+120`，文件级总量约为 `355`，仍低于代码守卫的 `400` 行上限。证据：`internal/module/workspace/service.go:226-235`，`docs/plans/迁移/p2-execution-plan.md:71-75`。
- `Warning`：但计划对改动面的估计偏乐观。B6/B7/I7 至少还要扩到 `contract.go/rpc.go/rpc_types.go`，B12 至少还要扩到 `internal/dto/workspace/event.go`，不只是 `service.go + module.go`。证据：`docs/plans/迁移/p2-execution-plan.md:20-24`，`internal/module/workspace/contract.go:11-19`，`internal/module/workspace/rpc.go:75-114`，`internal/module/workspace/rpc_types.go:5-23`，`internal/dto/workspace/event.go:5-32`。
- `Warning`：函数级守卫也有风险。V2 `CreateRun` 单段逻辑就覆盖 `176-243`，V2 `MergeRun` 单段逻辑覆盖 `306-373`；如果直接把 V2 逻辑平移进现有 V3 `CreateRun/MergeRun`，虽未必超 80 行，但已经逼近上限，必须拆 helper。证据：`go-agent-v2/internal/service/workspace.go:176-243`，`go-agent-v2/internal/service/workspace.go:306-373`，`docs/plans/迁移/p2-execution-plan.md:71-75`。

## 3. 并行冲突

- `OK`：当前计划文本内部一致。批次 A 标题已显式包含 `I7`，I7 行也列在批次 A 表格中；批次 C 标题已不再包含 I7；并行策略段再次声明 `I7 已归入 Agent A`。证据：`docs/plans/迁移/p2-execution-plan.md:16-24`，`docs/plans/迁移/p2-execution-plan.md:46-56`，`docs/plans/迁移/p2-execution-plan.md:63-65`。
- `Warning`：虽然计划文本一致，但代码落点上 I7 仍与 B7/B12 共用 workspace 的 `contract.go/rpc.go/rpc_types.go/service.go` 编辑面，因此 Agent A 内部需要一次性统一设计接口，不适合再拆子任务并行。证据：`internal/module/workspace/contract.go:16-19`，`internal/module/workspace/rpc.go:88-114`，`internal/module/workspace/rpc_types.go:5-23`，`internal/module/workspace/service.go:107-116`。

## 4. 批次 B/C 快扫

- `Blocker`：批次 B 的超限风险是真实存在的，而且比计划表述更接近硬超限。`orchestration/service.go` 当前已到 `390` 行结束，若再按计划增加约 `80` 行，会到约 `470` 行，明显超过 `400`。证据：`docs/plans/迁移/p2-execution-plan.md:40-42`，`docs/plans/迁移/p2-execution-plan.md:71-75`，`internal/sidecar/orch/orchestration/service.go:378-390`。
- `OK`：taskdag store 已具备计划 B14 所需的核心方法：`UpsertDAG/GetDAG/ListDAGs/UpdateNodeStatus` 全部存在。证据：`internal/store/taskdag/contract.go:9-15`。

## 结论（Blocker / Warning / OK）

- `Blocker`：批次 A 计划最大的技术问题不是“做不到”，而是**目标文件集明显低估**。B6 至少还要改 `contract.go/rpc.go`，B12 至少还要改 `internal/dto/workspace/event.go`，I7 至少还要改 `contract.go/rpc.go`。证据：`docs/plans/迁移/p2-execution-plan.md:20-24`，`internal/module/workspace/contract.go:11-19`，`internal/module/workspace/rpc.go:75-114`，`internal/module/workspace/rpc_types.go:5-23`，`internal/dto/workspace/event.go:5-32`。
- `Blocker`：B6/B8 的真正缺口在于 V3 当前没有 V2 那套 workspace 生命周期模型：缺 `rootDir`、缺真实 merge 请求/结果对象、缺 `merging/failed` 状态和多种文件状态。证据：`internal/module/workspace/service.go:20-26`，`internal/module/workspace/service.go:29-31`，`internal/module/workspace/service.go:33-45`，`internal/module/workspace/contract.go:11-19`，`go-agent-v2/internal/service/workspace.go:176-243`，`go-agent-v2/internal/service/workspace.go:283-373`。
- `Warning`：B7 本身可做，因为 store 字段已齐；但它不是“只改 rpc_types.go + service.go”级别，必须连同 `contract.go/rpc.go` 一起收口。证据：`internal/store/workspace/contract.go:33-39`，`internal/module/workspace/rpc_types.go:5-23`，`internal/module/workspace/rpc.go:88-100`，`internal/module/workspace/contract.go:16-19`。
- `OK`：文件级代码量上，批次 A 的 `workspace/service.go +120` 仍在安全区；批次 B 的 `orchestration/service.go +80` 不在安全区，拆 `dag.go/report.go` 的方向正确。证据：`internal/module/workspace/service.go:226-235`，`internal/sidecar/orch/orchestration/service.go:378-390`，`docs/plans/迁移/p2-execution-plan.md:40-42`，`docs/plans/迁移/p2-execution-plan.md:71-75`。

## 互辩

### 对 audit-p2-plan-B 的批判

1. `audit-p2-plan-B.md:62-63` 把“workspace event 类型已经定义，不是当前 blocker”定成 OK，证据不足且与计划冲突。计划 B12 明确要求 `CreateRun/MergeRun/AbortRun/UpdateRunStatus` 发布对应 typed event（`docs/plans/迁移/p2-execution-plan.md:23`），但当前 DTO 只有 `WorkspaceRunCreated/WorkspaceRunStatusChanged/WorkspaceRunMerged`（`internal/dto/workspace/event.go:5-32`），`WorkspaceRunAborted` 在 `internal/dto/workspace` 中无命中；workspace 模块还没有 bus 注入面（`internal/module/workspace/service.go:29-31`，`internal/module/workspace/module.go:5-8`）。这个点在我的 workspace 审查里是 `Blocker`，B 报告的快扫结论失真。
2. `audit-p2-plan-B.md:7-11,70-72` 过度把 B13 收敛成“缺几个方法”，遗漏了更严重的语义回退：当前 `agent.launch` 虽已注册（`internal/sidecar/orch/orchestration/rpc.go:17-19`），但 `launchParams`/`LaunchRequest` 只有 `AgentID/Name/CWD/Command/ParentID/Env`（`internal/sidecar/orch/orchestration/rpc_types.go:8-17`，`internal/sidecar/orch/orchestration/contract.go:32-39`），缺 V2 `Prompt/Instructions/DynamicTools/Config`（`go-agent-v2/internal/apiserver/methods_orchestration.go:29-37`）。按计划目标“达到 V2 功能等价的最低可用标准”（`docs/plans/迁移/p2-execution-plan.md:10`），这同样是 `Blocker`，但 B 报告没有把它抬到结论层。
3. `audit-p2-plan-B.md:18-21,77` 对 B14 create 路径仍然估轻。它只点到 DAG `Status` 和节点拆写，但 `createDAGNodeParams` 还缺 node `Status`，`DependsOn` 也是 `[]string`（`internal/sidecar/orch/orchestration/rpc_types.go:41-49`），而 store `Node` 要 `Status` 和 `DependsOn json.RawMessage`（`internal/store/taskdag/contract.go:166-176`）。不补默认节点状态和序列化策略，`task/dag/create` 依然不能落地；B 报告遗漏了这个更硬的适配缺口。
4. `audit-p2-plan-B.md:20` 把 `list -> ListDAGs` 写成“同形可行”不够严谨。RPC `listDAGsParams.Limit` 是 `int`（`internal/sidecar/orch/orchestration/rpc_types.go:88-92`），store `ListDAGsFilter.Limit` 是 `int32`（`internal/store/taskdag/contract.go:41-45`）；这不是 blocker，但说明 B 报告在适配复杂度判断上偏乐观。

### 对 audit-p2-plan-C 的批判

1. `audit-p2-plan-C.md:43-45,72` 把 workspace event DTO 缺口压成 `Warning`，与计划和代码都不符。B12 明确要求 `AbortRun` 也发布 typed event（`docs/plans/迁移/p2-execution-plan.md:23`），当前只有 `WorkspaceRunCreated/WorkspaceRunStatusChanged/WorkspaceRunMerged`（`internal/dto/workspace/event.go:5-32`），`WorkspaceRunAborted` 无命中；workspace service/module 也无 bus 依赖（`internal/module/workspace/service.go:29-31`，`internal/module/workspace/module.go:5-8`）。这与我的 A 报告 `Blocker` 结论直接矛盾。
2. `audit-p2-plan-C.md:53` 的 `234 + 150 = 384` 算法没有代码支撑。计划里的 `~150 行` 是批次 A 总量，不是 `workspace/service.go` 单文件增量（`docs/plans/迁移/p2-execution-plan.md:20-26`）；而当前代码显示 B7/I7 至少还要动 `contract.go/rpc.go/rpc_types.go`，B12 还要动 `event.go`（`internal/module/workspace/contract.go:16-19`，`internal/module/workspace/rpc.go:88-114`，`internal/module/workspace/rpc_types.go:5-23`，`internal/dto/workspace/event.go:5-32`）。因此它的守卫判断既不能证明 `service.go` 安全，也掩盖了计划目标文件集低估问题。
3. `audit-p2-plan-C.md:29-32,68` 虽然把 I5 判成 `Blocker`，但仍没把“计划目标文件写错”上升成结论。计划只列 `skill/rpc.go, thread/rpc.go`（`docs/plans/迁移/p2-execution-plan.md:54`），实际 `unsupported command: /skills` 出在 `thread/command.go:12-40`，`thread/rpc.go` 只是转发（`internal/module/thread/rpc.go:67-68`，`internal/module/thread/rpc.go:99-103`）。如果不明确指出 `thread/command.go` 也必须改，修复范围仍会被低估。
4. `audit-p2-plan-C.md:46-49` 统计 handler 总 key 数 `76 -> 80`，但这段没有对应的守卫或验收口径。现有代码守卫只检查文件/函数/包复杂度（`internal/archtest/guardlib.go:17-24`），计划验证标准也只有 build/vet/test/diagnostics（`docs/plans/迁移/p2-execution-plan.md:67-74`）。这段分析对 blocker/feasibility 没有直接证明力，反而挤占了对真实 blocker 的篇幅。
