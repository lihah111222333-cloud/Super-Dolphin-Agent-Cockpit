# V2↔V3 1:1 对齐：workspace/run/create + workspace/run/get

审查范围：

- V2 入口：`go-agent-v2/internal/apiserver/workspace_methods.go:41-83`
- V2 核心：`go-agent-v2/internal/service/workspace.go:176-247,253-275`、`go-agent-v2/internal/service/workspace_file_ops.go:92-153`、`go-agent-v2/internal/store/workspace_run.go:15-41,82-101`
- V3 入口：`internal/module/workspace/rpc.go:25-48`、`internal/module/workspace/rpc_types.go:38-40`
- V3 核心：`internal/module/workspace/contract.go:24-36`、`internal/module/workspace/service.go:61-195`、`internal/module/workspace/service_helpers.go:20-30,53-70,166-218,296-307`、`internal/store/workspace/store.go:16-96`
- 本次只用 LSP 读取代码；未使用 `grep/find/cat/sed/awk`。

## 总结

- `workspace/run/create`：`❌`。V3 没做到 V2 的 1:1，对齐缺口集中在 `cwd` 引入、workspace 目录策略、bootstrap 细节、`runKey` 规则和路径防护。
- `workspace/run/get`：`⚠️`。请求和返回 shape 基本对齐，但它仍只是裸 `GetRun`；V2 另有 `ResolveRunWorkspace` 做路径安全兜底，V3 在当前模块内没看到等价层。

## workspace/run/create

| 对比项 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| 参数：`cwd` | 无 `cwd`。workspace 位置由 `WorkspaceManager.rootDir` 固定控制。`go-agent-v2/internal/service/workspace.go:39-45,103-139` | 新增 `CWD`；`resolveSourceRoot` 和 `resolveWorkspacePath` 都会用它解析相对路径。`internal/module/workspace/contract.go:24-36`、`internal/module/workspace/service.go:81-116,129-167` | ❌ |
| 参数：`files` | 有 `Files []string`，`CreateRun` 直接把它传给 `bootstrapFiles`。`go-agent-v2/internal/service/workspace.go:225-226` | 也有 `Files []string`，`CreateRun` 同样先跑 `bootstrapFiles`。`internal/module/workspace/contract.go:33-35`、`internal/module/workspace/service.go:69-73` | ✅ |
| 目录创建 | 固定创建 `<rootDir>/<runKey>/workspace`，先校验 `runBase` 落在 `rootDir` 内，再 `os.MkdirAll(..., 0o750)`。`go-agent-v2/internal/service/workspace.go:201-208,637-651` | 默认创建 `<cwd or sourceRoot>/.workspace/<runKey>`，也允许调用方直接注入 `workspacePath`；只做 `workspacePath != sourceRoot`，然后 `os.MkdirAll(..., 0o755)`。`internal/module/workspace/service.go:66-68,147-167` | ❌ |
| file bootstrap | 有完整 bootstrap 流水线：去重、`maxFiles/maxFileBytes/maxTotalBytes`、source/target root 校验、拒绝 symlink、原子复制、记录 `bootstrap_files/bootstrap_bytes`，失败时把 run 置为 `failed`。`go-agent-v2/internal/service/workspace.go:225-241,577-621`、`go-agent-v2/internal/service/workspace_file_ops.go:98-153` | 只有去重和相对路径规范化，然后直接 `copyFile` 到 workspace；没有大小上限、symlink 防护、target containment 校验，也不回写 bootstrap 计数元数据或失败状态。`internal/module/workspace/service.go:69-78,180-190`、`internal/module/workspace/service_helpers.go:20-30,32-50,53-70,296-307` | ❌ |
| `UpsertFile` | 在 bootstrap 过程中逐文件 `SaveFile`，每复制一个文件就立即落一条 file 记录，初始 state 固定为 `synced`。`go-agent-v2/internal/service/workspace_file_ops.go:129-152`、`go-agent-v2/internal/store/workspace_run.go:82-101` | 先复制文件，再在 `persistRun` 里通过 `buildRunFile` 计算 hash 后 `UpsertFile`；最终也会有 file 记录，但入库时序和失败窗口不同。`internal/module/workspace/service_helpers.go:166-193,195-218`、`internal/store/workspace/store.go:80-96` | ⚠️ |
| store 事务 | 没有事务。流程是 `SaveRun -> bootstrap/copy + SaveFile -> SaveRun(metadata)`。`go-agent-v2/internal/service/workspace.go:210-241`、`go-agent-v2/internal/store/workspace_run.go:15-35,82-101` | `persistRun` 用 `WithTx` 包住 `UpsertRun + UpsertFile`，但目录创建和文件复制发生在事务之前，所以只是 DB 原子，不是端到端原子。`internal/module/workspace/service.go:66-74`、`internal/module/workspace/service_helpers.go:166-193`、`internal/store/workspace/store.go:16-20` | ⚠️ |
| `runKey` 校验 | 默认 `run-<millis>`；正则是 `^[A-Za-z0-9][A-Za-z0-9._-]{2,127}$`，允许 `.`，最短 3 位，首字符必须是字母或数字。`go-agent-v2/internal/service/workspace.go:37,177-183` | 默认 `run-<millis>`；正则是 `^[A-Za-z0-9_-]+$`，不允许 `.`, 但允许 1-2 位、也允许首字符直接是 `_` 或 `-`。`internal/module/workspace/service.go:38,82-87,119-127` | ❌ |
| 路径逃逸防护 | 有三层显式防护：run 目录必须在 `rootDir` 内，bootstrap source 必须在 `sourceRoot` 内，bootstrap target 必须在 `workspacePath` 内；同时拒绝 symlink。`go-agent-v2/internal/service/workspace.go:201-208,637-651`、`go-agent-v2/internal/service/workspace_file_ops.go:110-125,129-152` | 只对 `files` 做 `filepath.Clean` + 拒绝绝对路径/`..`；没有 `rootDir` 模型，也没有 source/target `isPathWithinRoot` 检查，`workspacePath` 只校验“不能与 sourceRoot 完全相等”。`internal/module/workspace/service.go:147-167`、`internal/module/workspace/service_helpers.go:20-30,296-307` | ❌ |
| 返回值 | 入口返回 `{"run": run}`；成功返回的 run 已经过一次 bootstrap 后补写元数据，`Metadata` 会带 `bootstrap_files/bootstrap_bytes`，`UpdatedBy` 固定回填为 `CreatedBy`。`go-agent-v2/internal/apiserver/workspace_methods.go:53-61`、`go-agent-v2/internal/service/workspace.go:233-241` | 入口同样返回 `{"run": ...}`，但返回 run 不会补写 bootstrap 统计；而且调用方还能直接注入 `WorkspacePath/Status/UpdatedBy/FinishedAt`，payload 语义不再是 V2 那个受控模型。`internal/module/workspace/rpc.go:25-35`、`internal/module/workspace/rpc_types.go:38-40`、`internal/module/workspace/contract.go:24-36` | ⚠️ |

补充差异：

- `sourceRoot` 缺省值也不一致。V2 入口会把空 `sourceRoot` 回填为 `"."`：`go-agent-v2/internal/apiserver/workspace_methods.go:46-53`；V3 `workspace/run/create` 直接要求 `sourceRoot` 必填：`internal/module/workspace/rpc.go:25-35`。

## workspace/run/get

| 对比项 | V2 | V3 | 结论 |
| --- | --- | --- | --- |
| 参数：`cwd` | 不涉及；入口只接收 `runKey`。`go-agent-v2/internal/apiserver/workspace_methods.go:64-77` | 不涉及；入口只接收 `runKey`。`internal/module/workspace/rpc.go:38-46` | ✅ |
| 参数：`files` | 不涉及。`go-agent-v2/internal/apiserver/workspace_methods.go:64-77` | 不涉及。`internal/module/workspace/rpc.go:38-46` | ✅ |
| 目录创建 | 不涉及；`GetRun` 只是读 store。`go-agent-v2/internal/service/workspace.go:245-247` | 不涉及；`GetRun` 只是读 store。`internal/module/workspace/service.go:193-195` | ✅ |
| file bootstrap | 不涉及。`go-agent-v2/internal/service/workspace.go:245-247` | 不涉及。`internal/module/workspace/service.go:193-195` | ✅ |
| `UpsertFile` | 不涉及。`go-agent-v2/internal/service/workspace.go:245-247` | 不涉及。`internal/module/workspace/service.go:193-195` | ✅ |
| store 事务 | 只是 `strings.TrimSpace(runKey)` 后调用 store `GetRun`，没有事务。`go-agent-v2/internal/service/workspace.go:245-247`、`go-agent-v2/internal/store/workspace_run.go:37-41` | 同样只是 trim 后调用 store `GetRun`，没有事务。`internal/module/workspace/service.go:193-195`、`internal/store/workspace/store.go:41-48` | ✅ |
| `runKey` 校验 | 入口只做非空校验，service 只 trim，不复用 create 的 regex。`go-agent-v2/internal/apiserver/workspace_methods.go:69-80`、`go-agent-v2/internal/service/workspace.go:245-247` | 入口只做非空校验，service 只 trim，也不复用 create 的 regex。`internal/module/workspace/rpc.go:39-47`、`internal/module/workspace/service.go:193-195` | ✅ |
| 路径逃逸防护 | 原始 `GetRun` 不校验 `WorkspacePath`；V2 真正解析可用 workspace 路径时另有 `ResolveRunWorkspace`，会做 `isPathWithinRoot` 和 `os.Stat` 检查。`go-agent-v2/internal/service/workspace.go:245-247,253-275` | 原始 `GetRun` 也只是返回 store 记录；在本次读取的 `internal/module/workspace/*` 中未见等价 `ResolveRunWorkspace` 层。`internal/module/workspace/service.go:193-195` | ⚠️ |
| 返回值 | 入口返回 `{"run": run}`。`go-agent-v2/internal/apiserver/workspace_methods.go:78-82` | 入口返回 `runResult{Run: run}`，JSON 也是 `{"run": ...}`。`internal/module/workspace/rpc.go:43-47`、`internal/module/workspace/rpc_types.go:38-40` | ✅ |

## 结论

- `workspace/run/create`：1:1 对齐结论是 `❌`。`cwd`、目录根模型、bootstrap、防护和 `runKey` 规则都与 V2 不同，不能算等价迁移。
- `workspace/run/get`：surface 基本对齐，结论是 `⚠️`。如果只看 `runKey -> store.GetRun -> {"run": ...}`，它和 V2 基本一致；但如果把“后续安全使用 `WorkspacePath`”也算进这条链路，V3 少了 V2 `ResolveRunWorkspace` 那层显式兜底。
