# workspace 1:1 对齐修复验证（create/merge/abort）

参考基线：

- `docs/plans/迁移/align-workspace-create.md:22-26`
- `docs/plans/迁移/align-workspace-merge.md:13-25`

## 1. CreateRun 独立目录 + runKey 校验：⚠️

目标依据：

- `docs/plans/迁移/align-workspace-create.md:22,26`

当前代码：

- 默认 `workspacePath` 仍会落到独立目录 `<cwd or sourceRoot>/.workspace/<runKey>`：`internal/module/workspace/service.go:153-159`
- 但调用方仍可直接传入 `workspace_path`，当前只拒绝 `workspacePath == sourceRoot`，没有固定 rootDir/containment 约束：`internal/module/workspace/contract.go:25-37`、`internal/module/workspace/service.go:148-167`
- `runKey` 校验已经存在，但规则是“非空 + 长度不超过 128 + 字符集 `^[A-Za-z0-9_-]+$`”：`internal/module/workspace/service.go:39,120-127`

判断：

- “独立目录”默认路径已具备，但不是 V2 那种固定受控目录模型。
- `runKey` 只做了部分长度/字符集校验；缺少最短长度和首字符规则，同时不接受 `.`。

## 2. MergeRun 三阶段 active→merging→merged/failed + deleteRemoved：⚠️

目标依据：

- `docs/plans/迁移/align-workspace-merge.md:13-15`

当前代码：

- 正式 merge 先做 `active -> merging`，然后在成功路径转 `merged`，在冲突/错误路径转 `failed`：`internal/module/workspace/service.go:225-245`、`internal/module/workspace/service_merge.go:32-55,58-97`
- `deleteRemoved` 已有代码路径：`internal/module/workspace/service_delete_removed.go:16-25`
- 但当前“removed”判定是遍历 `workspacePath` 中仍然存在的常规文件，并要求 `sourceRoot` 中对应文件已经不存在：`internal/module/workspace/service_delete_removed.go:39-50,61-94`
- 真正落地时只把文件状态改成 `removed` / `would_remove`，没有删除 source 文件：`internal/module/workspace/service_helpers.go:107-122`

判断：

- 正式 merge 的三阶段状态机已经到位。
- `deleteRemoved` 不是 V2 的“workspace 缺失文件 -> 删除 source”语义，更像“source 已缺失 -> 标记 removed”；因此不是 1:1 对齐。

## 3. AbortRun updatedBy / reason：✅

目标依据：

- `docs/plans/迁移/align-workspace-merge.md:16`

当前代码：

- RPC 参数层接收并透传 `run_key` / `updated_by` / `reason`：`internal/module/workspace/rpc_types.go:62-89`、`internal/module/workspace/rpc.go:83-96`
- service 层把 `updatedBy` 写入状态迁移输入，把 `reason` 写入 metadata：`internal/module/workspace/service.go:248-255`
- abort 事件也会继续带上 `reason` 和最终 `UpdatedBy`：`internal/module/workspace/service.go:259-260`、`internal/module/workspace/service_helpers.go:326-335`

判断：

- `updatedBy` / `reason` 已经贯通到状态更新和事件层。
- 额外差异是当前实现要求 `active -> aborted`：`internal/module/workspace/service.go:249-255`。

## 4. 事件发布 merge / abort 路径：⚠️

目标依据：

- `docs/plans/迁移/align-workspace-merge.md:23-24`

当前代码：

- 旧路径名 `workspace/run/merged` / `workspace/run/aborted` 已在 event surface 层恢复：`internal/platform/eventsurface/bind.go:17-29,72-83`
- merge/abort 的旧 payload 也是在 event surface 中组装出来的：`internal/platform/eventsurface/bind.go:107-134`
- merge 成功时会发 `WorkspaceRunMerged`，失败时发的是 `WorkspaceRunMergeError`，dry-run 直接绕过 merge 事件：`internal/module/workspace/service.go:230-231,330-344`、`internal/module/workspace/service_merge.go:37-55,79-97`
- abort 成功时会发 `WorkspaceRunAborted`，再由 event surface 转成 `workspace/run/aborted`：`internal/module/workspace/service.go:248-260`、`internal/module/workspace/service_helpers.go:326-335`、`internal/platform/eventsurface/bind.go:80-82,125-134`

判断：

- abort 路径已经回到旧方法名。
- merge 路径只覆盖“成功 merged”场景；失败走 `WorkspaceRunMergeError`，dry-run 不发 `workspace/run/merged`，因此仍不是 V2 的单一路径模型。

## 5. 文件 UpsertFile 事务保护：⚠️

目标依据：

- `docs/plans/迁移/align-workspace-create.md:24-25`

当前代码：

- CreateRun 的 `persistRun` 会在 `WithTx` 中执行 `UpsertRun + UpsertFile`：`internal/module/workspace/service_helpers.go:206-232`
- workspace store 的 `WithTx` 确实下沉到 sqlc / pgx 事务：`internal/store/workspace/store.go:16-20`、`internal/store/sqlc/db.go:51-58`
- 但 merge 过程的 `applyFileUpdates` / `restoreRunFiles` 仍是逐条 `UpsertFile`，没有事务包裹：`internal/module/workspace/service_helpers.go:181-204`、`internal/module/workspace/service_merge.go:29-30,88-96`

判断：

- CreateRun 的数据库写入已有事务保护。
- Merge / rollback 的 file upsert 仍然不是事务性的；另外，文件系统复制/目录创建本身也不在数据库事务内：`internal/module/workspace/service.go:67-74`

## 总结

- ✅ 已明确对齐：`AbortRun` 的 `updatedBy` / `reason` 透传。
- ⚠️ 部分对齐：CreateRun 默认独立目录、正式 merge 三阶段、旧事件路径恢复、CreateRun 的 DB 事务保护。
- ⚠️ 仍未 1:1：`runKey` 校验规则、`deleteRemoved` 语义、merge 失败与 dry-run 的旧事件路径、merge 阶段的 `UpsertFile` 事务边界。
