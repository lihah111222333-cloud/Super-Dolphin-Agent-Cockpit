# V2↔V3 `workspace/run/{merge,abort,list}` 1:1 对齐

## 范围

- V2 对照：`go-agent-v2/internal/service/workspace.go`、`go-agent-v2/internal/service/workspace_file_ops.go`、`go-agent-v2/internal/apiserver/workspace_methods.go`
- V3 对照：`internal/module/workspace/contract.go`、`internal/module/workspace/rpc.go`、`internal/module/workspace/rpc_types.go`、`internal/module/workspace/service.go`、`internal/module/workspace/service_merge.go`、`internal/module/workspace/service_helpers.go`、`internal/store/workspace/store.go`、`internal/store/sqlc/query_workspace.go`
- 本次只使用 LSP `text_search` + `read_file`

## 逐项对比

| 项目 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| merge 三阶段（正式 merge） | `transitionToMerging` 先做 `active -> merging` CAS，`defer` 末尾按结果收敛到 `merged/failed`：`go-agent-v2/internal/service/workspace.go:283-346` | `requireRun(active)` 后执行 `transitionRunStatus(active -> merging)`，`executeMerge` 再落到 `merged/failed`：`internal/module/workspace/service.go:224-245`、`internal/module/workspace/service_merge.go:32-55`、`internal/module/workspace/service_merge.go:58-97` | ✅ 正式 merge 的三阶段已对齐。 |
| merge `dryRun` | `MergeRun` 仍先进入 `merging`，`defer` 中把最终状态改回 `active`：`go-agent-v2/internal/service/workspace.go:313-346` | `req.DryRun` 直接走 `dryRunMerge`，完全绕过 `merging` 和 CAS：`internal/module/workspace/service.go:229-230`、`internal/module/workspace/service.go:329-344` | ❌ 生命周期不 1:1。V3 缺少 V2 的 `active -> merging -> active` 门闩。 |
| merge `deleteRemoved` | `DeleteRemoved` 会真正处理删除、删除冲突和 `would_delete`：`go-agent-v2/internal/service/workspace.go:368-370`、`go-agent-v2/internal/service/workspace.go:560-575`、`go-agent-v2/internal/service/workspace_file_ops.go:12-55` | 正式 merge 和 `dryRun` 都只有 TODO，未恢复 V2 语义：`internal/module/workspace/service_merge.go:26-28`、`internal/module/workspace/service.go:339-341` | ❌ 未对齐。 |
| abort `updatedBy/reason` | RPC 接收 `runKey/updatedBy/reason`，service 透传到 `UpdateRunStatus(..., aborted, updatedBy, {"reason": ...})`：`go-agent-v2/internal/apiserver/workspace_methods.go:143-165`、`go-agent-v2/internal/service/workspace.go:277-281` | RPC 接收同名字段，service 透传到 `TransitionRunStatusInput` 的 `UpdatedBy/Metadata`：`internal/module/workspace/rpc_types.go:16-26`、`internal/module/workspace/rpc.go:83-96`、`internal/module/workspace/service.go:247-260` | ✅ 字段透传已对齐。 |
| list `status/dagKey/limit` 过滤 | RPC 透传 `status/dagKey/limit`，并把 `limit <= 0 || limit > 5000` 收敛到 `200`：`go-agent-v2/internal/apiserver/workspace_methods.go:90-101` | RPC 透传 `status/dagKey/limit`，service 仅在 `limit <= 0` 时回退到 `200`；SQL 直接使用传入的 `LIMIT $3`：`internal/module/workspace/rpc.go:51-58`、`internal/module/workspace/service.go:197-205`、`internal/store/sqlc/query_workspace.go:8`、`internal/store/sqlc/query_workspace.go:36-37` | ⚠️ `status/dagKey` 已对齐，但缺少 V2 的 `limit > 5000 -> 200` 钳制。 |

## 事件发布

| 项目 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| merge 事件 | RPC 成功返回后发 `workspace/run/merged`，payload 是 `runKey + result`：`go-agent-v2/internal/apiserver/workspace_methods.go:127-135`。而 `result.Status` 在 service 内可能被收敛成 `merged`、`failed` 或 `active(dryRun)`：`go-agent-v2/internal/service/workspace.go:327-346`。 | 正式 merge 先发 `WorkspaceRunStatusChanged(active->merging)`，然后按结果发 `WorkspaceRunMerged` 或 `WorkspaceRunMergeError`；`dryRun` 不发事件：`internal/module/workspace/service.go:233-244`、`internal/module/workspace/service.go:329-344`、`internal/module/workspace/service_merge.go:32-55`、`internal/module/workspace/service_merge.go:79-97`、`internal/module/workspace/service_helpers.go:229-258` | ❌ 事件名、事件时机、失败分支和 `dryRun` 行为都不 1:1。 |
| abort 事件 | RPC 成功后发 `workspace/run/aborted`，payload 是 `runKey + run + reason`：`go-agent-v2/internal/apiserver/workspace_methods.go:154-165` | service 先发 `WorkspaceRunStatusChanged(active->aborted)`，再发 `WorkspaceRunAborted(reason, updatedBy)`：`internal/module/workspace/service.go:248-260`、`internal/module/workspace/service_helpers.go:229-236`、`internal/module/workspace/service_helpers.go:278-284` | ⚠️ 有 abort 事件，但不是 V2 的同名、同 payload、单事件模型。 |
| list 事件 | `workspaceRunList` 只返回 `runs`，不 `notify`：`go-agent-v2/internal/apiserver/workspace_methods.go:85-113` | `handleListRuns` 只返回 `runs`，不发事件：`internal/module/workspace/rpc.go:51-58` | ✅ 一致，均无事件。 |

## 附带发现

- `AbortRun` 的字段面已对齐，但状态守卫不等价。V2 直接 `UpdateRunStatus`，不要求原状态为 `active`：`go-agent-v2/internal/service/workspace.go:277-281`。V3 强制 `active -> aborted`：`internal/module/workspace/service.go:247-260`、`internal/module/workspace/service.go:293-316`。如果目标是严格 1:1，这一点仍应记为 `⚠️`。
- 基于仓内 LSP `text_search("workspace/run/merged")` / `text_search("workspace/run/aborted")` 结果，V3 仓内未见与 V2 同名的字符串通知实现；当前明确可见的是 typed bus 事件 `WorkspaceRunStatusChanged / WorkspaceRunMerged / WorkspaceRunMergeError / WorkspaceRunAborted`：`internal/module/workspace/service.go:40-59`、`internal/dto/workspace/event.go:13-52`。这条是基于仓内检索的推断，不是运行时抓包结论。

## 汇总

- ✅ 已对齐：正式 merge 三阶段、abort `updatedBy/reason` 字段透传、list 无事件。
- ⚠️ 部分对齐：list 缺少 `limit > 5000` 钳制，abort 事件模型与状态守卫不完全等价。
- ❌ 未对齐：merge `dryRun` 生命周期、`deleteRemoved` 语义、merge 事件发布模型。
