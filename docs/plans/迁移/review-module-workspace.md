# 审查：module/workspace

## 范围

- 当前模块：`internal/module/workspace/rpc.go`
- 当前模块：`internal/module/workspace/rpc_types.go`
- 当前模块：`internal/module/workspace/contract.go`
- 当前模块：`internal/module/workspace/service.go`
- 当前模块：`internal/module/workspace/service_helpers.go`
- 当前模块：`internal/module/workspace/module.go`
- V2 对照：`go-agent-v2/internal/apiserver/methods.go`
- V2 对照：`go-agent-v2/internal/apiserver/workspace_methods.go`
- V2 对照：`go-agent-v2/internal/service/workspace.go`
- V2 对照：`go-agent-v2/internal/service/workspace_file_ops.go`
- store / event / bus：`internal/store/workspace/contract.go`、`internal/dto/workspace/event.go`、`internal/platform/bus/emitters.go`

## 主要 findings

### 1. ❌ `CreateRun` 存在 `runKey` 路径逃逸，默认 workspace 目录可被拼到预期根外

- 当前实现仅在 `buildRun` 里做 `strings.TrimSpace(req.RunKey)`，没有任何格式约束，见 `internal/module/workspace/service.go:75-108`。
- 默认路径由 `resolveWorkspacePath` 直接 `filepath.Join(base, ".workspace", runKey)` 生成，见 `internal/module/workspace/service.go:128-148`，其中关键拼接点在 `internal/module/workspace/service.go:138`。
- 现有保护只有 `workspacePath != sourceRoot`，见 `internal/module/workspace/service.go:144-146`；没有 “必须位于某个 workspace root 之下” 的检查。
- V2 明确用正则限制 `runKey`，见 `go-agent-v2/internal/service/workspace.go:37` 与 `go-agent-v2/internal/service/workspace.go:176-183`。
- 这意味着传入类似 `../../evil` 的 `runKey` 时，默认路径可以跳出 `base/.workspace/`。这不是理论风险，而是当前路径拼接逻辑直接允许的行为。

### 2. ❌ `MergeRun` 还不是 V2 的真实 merge，只是在做“评估 + UpsertFile”

- 当前 `MergeRun` 只读取 store 中已跟踪文件：`s.store.ListFiles(...)`，见 `internal/module/workspace/service.go:210`，没有像 V2 那样遍历 `run.WorkspacePath`。
- `planMerge` / `evaluateMergeFile` 只基于哈希计算结果并修改文件状态，见 `internal/module/workspace/service_helpers.go:73-132`。
- 最关键的真实写回缺失，代码里直接留了 TODO：`copy workspace content into sourceRoot once full V2 merge I/O is restored`，见 `internal/module/workspace/service_helpers.go:128-131`。
- `applyFileUpdates` 仅做 `UpsertFile`，不做任何 source tree 写入，见 `internal/module/workspace/service_helpers.go:141-148`。
- `DeleteRemoved` 也是显式 TODO/no-op，见 `internal/module/workspace/service.go:217-219`。
- V2 则先 `WalkDir(run.WorkspacePath)`，见 `go-agent-v2/internal/service/workspace.go:359-362`，再按候选文件实际写回 source root，见 `go-agent-v2/internal/service/workspace.go:507-557`，并补做删除语义，见 `go-agent-v2/internal/service/workspace.go:560-575` 与 `go-agent-v2/internal/service/workspace_file_ops.go:12-55`。

### 3. ❌ `MergeRun` 缺少 V2 的状态机门闩，既没有 `merging`，也没有 `failed`

- 当前实现只是先 `requireRun(..., statusActive)`，见 `internal/module/workspace/service.go:206`，直到全部文件状态写完后才尝试 `active -> merged`，见 `internal/module/workspace/service.go:233-246`。
- 冲突或错误路径直接把结果状态写回 `active` 并返回，见 `internal/module/workspace/service.go:228-231`；模块内也没有 `failed` / `merging` 状态常量。
- 这意味着并发 `MergeRun` 调用之间没有原子门闩，理论上可以同时读到 `active` 并并行执行。
- V2 明确先做 `TryTransitionRunStatus(active -> merging)`，见 `go-agent-v2/internal/service/workspace.go:283-304`，并在 defer 中统一收敛到 `merged` / `failed` / `active(dryRun)`，见 `go-agent-v2/internal/service/workspace.go:327-346`。

### 4. ⚠️ `CreateRun` 缺少 V2 的 bootstrap 守卫，未拒绝 symlink / 体积超限 / 总量超限

- 当前 bootstrap 仅做相对路径去重和 `copyRunFile`，见 `internal/module/workspace/service.go:161-172` 与 `internal/module/workspace/service_helpers.go:20-71`。
- 当前模块内没有 `Lstat`、`ModeSymlink`、`maxFileBytes`、`maxTotalBytes` 之类的检查。
- `copyFile` 使用 `os.Open` + `Stat`，见 `internal/module/workspace/service_helpers.go:32-50`，会跟随 symlink。
- V2 在 bootstrap 前显式拒绝 symlink、目录、单文件过大、总量过大，见 `go-agent-v2/internal/service/workspace.go:577-597` 与 `go-agent-v2/internal/service/workspace_file_ops.go:98-120`。
- 这使得当前版本在 `CreateRun` 阶段比 V2 更弱，尤其是 sourceRoot 内部存在 symlink 时，复制范围可能超出“仅复制 sourceRoot 普通文件”的预期。

### 5. ⚠️ `ListRuns` 参数虽然透传，但丢失了 V2 的上限钳制

- 当前 handler 直接把 `status/dagKey/limit` 传给 service，见 `internal/module/workspace/rpc.go:52-59`。
- 当前 service 只在 `limit <= 0` 时改回默认值 200，见 `internal/module/workspace/service.go:178-187`。
- V2 RPC 层在 `limit <= 0 || limit > 5000` 时都会回落到 200，见 `go-agent-v2/internal/apiserver/workspace_methods.go:90-101`。
- 这不是功能阻断，但确实是 V2 行为回归。

## 15 项核验

| # | 维度 | 结论 | 证据 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | 文件清单与行数 | ✅ | `rpc.go` 151 行；`rpc_types.go` 61 行；`contract.go` 63 行；`service.go` 338 行；`service_helpers.go` 329 行；`module.go` 14 行 | `service.go <= 400`、`service_helpers.go <= 400` 均满足。 |
| 2 | handler 完整性 | ✅ | `internal/module/workspace/rpc.go:15-22` | 共 8 个 key：`workspace/run/create`、`get`、`list`、`status/update`、`merge`、`abort`、`files/list`、`file/get`。 |
| 3 | handler 验参顺序 | ✅ | `internal/module/workspace/rpc.go:26-137` | 7 个有必填参数的 handler 都是先 `required/required2` 再调 service；`ListRuns` 无必填参数，直接调用 `svc.ListRuns` 合理。 |
| 4 | V2 对照 | ⚠️ | V2 注册：`go-agent-v2/internal/apiserver/methods.go:255-259`；当前注册：`internal/module/workspace/rpc.go:15-22` | 重叠的 5 个 key 名称保持一致；当前新增 3 个 key：`status/update`、`files/list`、`file/get`。但 `CreateRun`、`MergeRun`、`ListRuns` 存在语义漂移。 |
| 5 | CreateRun | ⚠️ | `internal/module/workspace/service.go:55-73`、`internal/module/workspace/service.go:128-148`、`internal/module/workspace/service_helpers.go:166-193` | 独立目录、bootstrap files、`UpsertFile`、`WithTx` 都有；但默认路径受未校验 `runKey` 影响可逃逸，且文件系统副作用不在事务回滚面内。 |
| 6 | MergeRun | ❌ | `internal/module/workspace/service.go:205-255`、`internal/module/workspace/service_helpers.go:73-155` | 没有真实遍历 workspace tree，没有写回 sourceRoot，`deleteRemoved` 未实现，rollback 只恢复 run_file 行，不是完整 merge 事务。 |
| 7 | AbortRun | ✅ | `internal/module/workspace/service.go:257-270` | `updatedBy` 透传到 `TransitionRunStatus`，`reason` 进入 metadata，并发布 status changed + aborted typed event。 |
| 8 | ListRuns | ⚠️ | `internal/module/workspace/rpc.go:52-59`、`internal/module/workspace/service.go:178-187` | `status/dagKey/limit` 都已透传；但相较 V2 丢失 `limit > 5000` 的保护。 |
| 9 | ListRunFiles | ✅ | `internal/module/workspace/rpc.go:113-123`、`internal/module/workspace/service.go:273-279` | `state` 已从 RPC 透传到 `storeworkspace.ListFilesFilter.State`，I7 修复已在。 |
| 10 | event 发布 | ⚠️ | `internal/module/workspace/service.go:71`、`internal/module/workspace/service.go:201`、`internal/module/workspace/service.go:252-253`、`internal/module/workspace/service.go:268-269`、`internal/module/workspace/service_helpers.go:220-258` | `CreateRun`、`UpdateRunStatus`、`AbortRun`、`MergeRun(success/error)` 都会发 typed event；但 `MergeRun(dryRun)` 在 `internal/module/workspace/service.go:220-222` 直接返回，不发任何 event。 |
| 11 | MergeRun error event | ⚠️ | `internal/module/workspace/service.go:224-230`、`internal/module/workspace/service.go:247-249`、`internal/module/workspace/service_helpers.go:248-258` | 非 dryRun 的 apply error / conflict / error / status transition error 都会发 `WorkspaceRunMergeError`；dryRun 的 conflict/error 路径不会发。 |
| 12 | store 对齐 | ✅ | contract：`internal/store/workspace/contract.go:9-18`；调用点：`internal/module/workspace/service.go:175-182,190-195,210,274,282,311,318` 与 `internal/module/workspace/service_helpers.go:143,159,168-189` | `WithTx`、`UpsertRun`、`GetRun`、`ListRuns`、`UpdateRunStatus`、`TransitionRunStatus`、`UpsertFile`、`GetFile`、`ListFiles` 9 个 contract 方法都有实际调用。 |
| 13 | import 方向 | ✅ | `internal/module/workspace/*.go` 无 `provider/` 命中 | 模块依赖仅指向 DTO、store、bus、platform/rpc，不反向依赖 provider。 |
| 14 | fx 注册 | ✅ | `internal/module/workspace/module.go:9-14`、`internal/module/workspace/service.go:43-52` | `module.go` 正确注入 `storeworkspace.Store` 与 `*bus.WorkspaceEmitters`，`NewService` 再绑定 typed emitters。 |
| 15 | 函数复杂度 | 信息项 | 见下表 | 最长函数都还在可读范围内，但 `MergeRun` 已经是该模块最复杂入口。 |

## handler 核对

| Key | 结论 | 证据 |
| --- | --- | --- |
| `workspace/run/create` | ✅ | `internal/module/workspace/rpc.go:26-37` |
| `workspace/run/get` | ✅ | `internal/module/workspace/rpc.go:39-50` |
| `workspace/run/list` | ✅ | `internal/module/workspace/rpc.go:52-60` |
| `workspace/run/status/update` | ✅ | `internal/module/workspace/rpc.go:62-73` |
| `workspace/run/merge` | ✅ | `internal/module/workspace/rpc.go:75-86` |
| `workspace/run/abort` | ✅ | `internal/module/workspace/rpc.go:97-111` |
| `workspace/run/files/list` | ✅ | `internal/module/workspace/rpc.go:113-124` |
| `workspace/run/file/get` | ✅ | `internal/module/workspace/rpc.go:126-137` |

## 最长函数 Top 3

| 排名 | 函数 | 位置 | 行数 |
| --- | --- | --- | --- |
| 1 | `(*service).MergeRun` | `internal/module/workspace/service.go:205-255` | 51 |
| 2 | `buildRun` | `internal/module/workspace/service.go:75-108` | 34 |
| 3 | `evaluateMergeFile` | `internal/module/workspace/service_helpers.go:100-132` | 33 |

## 结论

- 结构层面，`module/workspace` 已经完成了 RPC、service、store、event、fx 装配的基本闭环。
- 但从 V2 workspace 语义看，`MergeRun` 仍未达标，是本模块的主阻断项。
- 其次，`CreateRun` 的 `runKey` 路径逃逸与 bootstrap 守卫缺失都属于需要尽快补上的安全/边界问题。
- 如果这份审查用于迁移 gate，当前状态不建议把 `module/workspace` 视为“已完成 V2 等价迁移”。
