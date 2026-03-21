# Workspace 操作层能力 + 容错审查

审查范围：

- V3：`internal/module/workspace/*`、`internal/store/workspace/*`、`sql/queries/workspace_run.sql`
- V2 对照：`go-agent-v2/internal/service/workspace*.go`、`go-agent-v2/internal/store/workspace_run.go`、`go-agent-v2/pkg/toolsdk/tools/resource.go`
- 本次只用 LSP 做 `text_search / workspace_symbol / references(compact) / call_hierarchy / read_file`

## 总评

结论不是“能力闭合”，而是“骨架已搭，但仍明显低于 V2”。

最关键的 6 点：

1. `CreateRun` 的 DB 事务只保护 `UpsertRun + UpsertFile`，不保护目录创建和 bootstrap 文件复制；文件系统副作用发生在事务之前，失败后会留下工作目录和部分文件：`internal/module/workspace/service.go:57-74`、`internal/module/workspace/service_helpers.go:166-193`、`internal/store/sqlc/db.go:51-58`、`internal/platform/db/tx.go:11-20`。
2. `MergeRun` 的 B3 门闩只修到了正式 merge 路径；`dryRun` 直接旁路 `merging` 状态，不参与 CAS：`internal/module/workspace/service.go:220-240`、`internal/module/workspace/service.go:325-340`。
3. V3 `MergeRun` 仍未恢复 V2 的真实文件回写。`evaluateMergeFile` 在“成功”分支只改 DB 状态，不把 workspace 内容写回 source tree：`internal/module/workspace/service_helpers.go:100-132`。这意味着 `merged` 状态和 `WorkspaceRunMerged` 事件目前都可能是“逻辑已合并、文件未落盘”。
4. `DeleteRemoved` 在 V3 两条路径里都还是 TODO，且 V3 不 walk workspace 树，只看已跟踪文件；语义与 V2 明显不等价：`internal/module/workspace/service.go:335-337`、`internal/module/workspace/service_merge.go:26-28`，对照 `go-agent-v2/internal/service/workspace.go:359-370`、`go-agent-v2/internal/service/workspace_file_ops.go:12-55`。
5. V3 默认 workspace 在 `sourceRoot/.workspace/<runKey>`，而 V2 默认在独立 root 下的 `<rootDir>/<runKey>/workspace`；隔离性、清理性和路径防护都弱于 V2：`internal/module/workspace/service.go:143-163`，对照 `go-agent-v2/internal/service/workspace.go:201-208`、`go-agent-v2/internal/service/workspace.go:637-650`。
6. V3 没有 V2 的 `maxFiles / maxFileBytes / maxTotalBytes` 限流，也没有 bootstrap symlink 防护；大文件和大批量文件场景明显退化：V3 `internal/module/workspace/service.go:176-186`、`internal/module/workspace/service_helpers.go:20-50`；V2 `go-agent-v2/internal/service/workspace.go:123-131`、`go-agent-v2/internal/service/workspace.go:577-620`、`go-agent-v2/internal/service/workspace_file_ops.go:98-152`。

## 12 个维度

### 1. CreateRun

结论：部分通过。

- 目录创建、bootstrap、`UpsertRun + UpsertFile`、事务包装都存在：`internal/module/workspace/service.go:57-74`、`internal/module/workspace/service_helpers.go:166-193`、`internal/store/workspace/store.go:16-20`。
- B2 文档里定义的那类 `runKey` traversal 已修：`validateRunKey` 明确拒绝 `..`、`/`、`\`，而且发生在 `resolveWorkspacePath` 之前：`internal/module/workspace/service.go:77-92`、`internal/module/workspace/service.go:115-123`。
- 但这不是 V2 级别的路径防护。`workspacePath` 只校验“不能与 `sourceRoot` 完全相等”，不校验“是否落在受控 root 内”，默认还会落到 `sourceRoot/.workspace/<runKey>`：`internal/module/workspace/service.go:143-163`。
- 推断：`runKey="."` 仍会通过 `validateRunKey`，默认路径会折叠到 `<base>/.workspace`；因此 B2 修掉的是显式 traversal，不是严格 key 约束。证据同上，推断基于 `filepath.Join(base, ".workspace", runKey)` 的代码路径：`internal/module/workspace/service.go:153`。
- `CreateRunRequest` 还允许调用方直接注入 `WorkspacePath / Status / FinishedAt / UpdatedBy`，这也与 V2 固定 `active` + 受控 root 的建模不同：`internal/module/workspace/contract.go:25-37`。

### 2. MergeRun

结论：部分通过。

- 非 `dryRun` 路径已经具备 `active -> merging -> merged/failed` 三阶段，B3 对“正式 merge 门闩”来说已修：`internal/module/workspace/service.go:220-240`、`internal/module/workspace/service.go:289-312`、`internal/module/workspace/service_merge.go:32-55`、`internal/module/workspace/service_merge.go:58-97`。
- `dryRun` 直接在 `active` 上做 `planMerge`，不进 `merging`，所以不参与 CAS，也不具备 V2 的 dry-run 门闩语义：`internal/module/workspace/service.go:225-227`、对照 `go-agent-v2/internal/service/workspace.go:313-346`。
- `DeleteRemoved` 在 `dryRun` 和正式 merge 都还是 TODO：`internal/module/workspace/service.go:335-337`、`internal/module/workspace/service_merge.go:26-28`。
- 更严重的是，V3 merge 只读取 store 里的 tracked files，不 walk workspace tree，也不执行真正的文件落盘。`evaluateMergeFile` 的成功分支只把状态标成 `merged`：`internal/module/workspace/service_merge.go:17-31`、`internal/module/workspace/service_helpers.go:100-132`。
- 这与 V2 差异很大。V2 会 `WalkDir(run.WorkspacePath)`、构造 merge candidate、`copyFileAtomic` 写回 source root，并在 `DeleteRemoved` 时处理删除：`go-agent-v2/internal/service/workspace.go:348-372`、`go-agent-v2/internal/service/workspace.go:455-558`、`go-agent-v2/internal/service/workspace_file_ops.go:12-55`。

### 3. AbortRun

结论：按本轮目标通过，但与 V2 语义不等价。

- `updatedBy` 和 `reason` 都透传到 `TransitionRunStatusInput`；`reason` 放在 metadata，`updatedBy` 进入行更新与事件：`internal/module/workspace/service.go:243-256`。
- 已切到 `TransitionRunStatus`，不再走裸 `UpdateRunStatus`：`internal/module/workspace/service.go:244-250`、`internal/store/workspace/store.go:71-78`。
- 但 V3 只允许 `active -> aborted`。一旦 run 已进入 `merging / merged / failed`，`AbortRun` 会报状态不匹配：`internal/module/workspace/service.go:244-256`、`internal/module/workspace/service.go:289-312`。
- V2 不是这个语义。V2 `AbortRun` 直接 `UpdateRunStatus(..., aborted, ...)`，测试里甚至允许 merged 后再 abort：`go-agent-v2/internal/service/workspace.go:277-281`、`go-agent-v2/internal/service/workspace_run_state_guard_test.go:137-163`。

### 4. ListRuns

结论：通过，但不是“原样透传”。

- RPC 把 `status / dagKey / limit` 传给 service：`internal/module/workspace/rpc.go:52-59`。
- service 会 trim，并在 `limit <= 0` 时改写成默认 `200`：`internal/module/workspace/service.go:193-202`。
- store 和 SQL 层把三参数完整传到底：`internal/store/workspace/store.go:50-60`、`sql/queries/workspace_run.sql:22-28`。
- 与 V2 相比，V3 没有 `>5000` 的上限钳制；V2 resource layer 会把非法/过大 limit 收敛回 `200`：`go-agent-v2/pkg/toolsdk/tools/resource.go:413-429`。

### 5. ListRunFiles

结论：`state` 透传通过，但 surface 与 store 不完全对齐。

- RPC 把 `runKey / state` 传给 service：`internal/module/workspace/rpc.go:113-123`。
- service 把 `state` 透传到 store：`internal/module/workspace/service.go:259-265`。
- store 和 SQL 层也确实使用了 `runKey / state / limit`：`internal/store/workspace/store.go:107-117`、`sql/queries/workspace_run.sql:78-84`。
- 但 service 把 limit 固定成 `200`，也强制要求 `runKey` 非空；store/SQL 实际支持“跨 run 列表 + 自定义 limit”，这部分能力没有被 service 暴露：`internal/module/workspace/service.go:259-265`、`sql/queries/workspace_run.sql:81-84`。

### 6. 文件 I/O 容错

结论：部分通过，CreateRun 明显弱于 V2。

- `os.MkdirAll / os.Open / os.OpenFile / io.Copy / hashFile` 的错误都会直接上抛，磁盘满、权限不足、共享冲突都会表现为原始 OS 错误：`internal/module/workspace/service.go:62-67`、`internal/module/workspace/service_helpers.go:20-50`、`internal/module/workspace/service_helpers.go:310-330`。
- V3 bootstrap 用的是非原子 `copyFile`：目标文件先 `O_TRUNC`，失败后不会删除半写文件，也没有目录清理：`internal/module/workspace/service_helpers.go:32-50`。
- 如果 bootstrap 在 DB 持久化前失败，run 记录可能根本不存在，但目录和已复制文件已经留下：`internal/module/workspace/service.go:62-70`、`internal/module/workspace/service_helpers.go:166-180`。
- V3 merge 的容错相对好一些：list/apply/transition 失败会回滚 file states、把 run 置为 `failed`、并发 `merge error` 事件：`internal/module/workspace/service_merge.go:17-31`、`internal/module/workspace/service_merge.go:79-97`。
- 对照 V2，bootstrap 和 merge 写回都使用了 `copyFileAtomic`，写目标前有临时文件、`Sync`、`Rename`；bootstrap 还拒绝 symlink：`go-agent-v2/internal/service/workspace.go:653-695`、`go-agent-v2/internal/service/workspace.go:577-597`、`go-agent-v2/internal/service/workspace_file_ops.go:114-152`。

### 7. 并发操作

结论：CAS 门闩对“正式 merge / active abort”生效，但仍有旁路。

- 正式 merge 的门闩点是 `TransitionRunStatus(run_key, from_status)`；service 先读当前 run，再在 DB 层做 CAS，测试也覆盖了 concurrent merge 被拒绝：`internal/module/workspace/service.go:220-240`、`internal/module/workspace/service.go:289-312`、`internal/module/workspace/service_test.go:110-132`。
- `AbortRun` 也走 `active -> aborted` 的 CAS，所以同一 run 同时 merge 和 abort 时，只会有一个先拿到 `active`：`internal/module/workspace/service.go:243-256`。
- 但 abort 只支持 `active -> aborted`。如果 merge 已先切到 `merging`，abort 会失败，而不会“中止正在 merge 的 run”：`internal/module/workspace/service.go:244-250`。
- `dryRun` 是旁路，不进 `merging`；因此它不具备与正式 merge 相同的并发门闩：`internal/module/workspace/service.go:225-227`。
- 还有一个显式旁路是 `workspace/run/status/update`。它走的是非 CAS `UpdateRunStatus`，可以直接破坏状态机：`internal/module/workspace/rpc.go:18`、`internal/module/workspace/rpc.go:62-73`、`internal/module/workspace/service.go:204-218`。

### 8. 事件发布

结论：成功路径和 merge error 路径都已发布事件，且事件面比 V2 更丰富。

- V3 service 在构造时挂了 5 类 emitter：`created / merged / aborted / mergeError / statusChanged`：`internal/module/workspace/service.go:36-55`。
- `CreateRun` 成功后会发 `WorkspaceRunCreated`：`internal/module/workspace/service.go:69-74`、`internal/module/workspace/service_helpers.go:220-227`。
- 正式 `MergeRun` 会先发 `active -> merging` 的 `WorkspaceRunStatusChanged`，成功时再发 `merging -> merged` + `WorkspaceRunMerged`，冲突/错误时发 `merging -> failed` + `WorkspaceRunMergeError`：`internal/module/workspace/service.go:229-240`、`internal/module/workspace/service_merge.go:32-55`、`internal/module/workspace/service_merge.go:79-97`。
- `AbortRun` 会发 `WorkspaceRunStatusChanged` 和 `WorkspaceRunAborted`：`internal/module/workspace/service.go:243-256`、`internal/module/workspace/service_helpers.go:278-284`。
- 事件类型在 DTO 和 bus sink 都已注册：`internal/dto/workspace/event.go:5-52`、`internal/platform/bus/sink.go:75-80`。
- 但 `dryRun` 不发 merge 相关事件，`CreateRun` 失败也没有 failure 事件。
- V2 tool layer 只看到 `workspace/run/created|merged|aborted` 3 类 NotifyEvent，没有 merge-error 事件：`go-agent-v2/pkg/toolsdk/tools/resource.go:359-390`、`go-agent-v2/pkg/toolsdk/tools/resource.go:436-479`。

### 9. store 对齐

结论：大体一一对应，但有两个明显偏差。

- 逐个看引用，V3 service 确实把 `WithTx / UpsertRun / GetRun / ListRuns / UpdateRunStatus / TransitionRunStatus / UpsertFile / GetFile / ListFiles` 全部用到了，没有完全悬空的 store 方法：`internal/module/workspace/service.go:189-312`、`internal/module/workspace/service_helpers.go:141-193`。
- 偏差 1：`ListFiles` 的 store contract 支持“跨 run 列表 + 自定义 limit”，service surface 只暴露“指定 runKey + 固定 200 条”：`internal/store/workspace/contract.go:42-46`、`internal/module/workspace/service.go:259-265`。
- 偏差 2：`UpdateRunStatus` 作为公开 RPC 存在，属于绕开主状态机的 escape hatch；这不影响 store 合约闭合，但会削弱 `TransitionRunStatus` 的门闩价值：`internal/module/workspace/rpc.go:18`、`internal/module/workspace/service.go:204-218`。

### 10. workspace 目录清理

结论：不通过。

- 在 V3 `internal/module/workspace` 内没有看到任何 `os.Remove` / `os.RemoveAll` 用于 run 目录清理：对该目录的 `text_search` 为 0 命中。
- `AbortRun` 只改 DB 状态并发事件，不清理目录：`internal/module/workspace/service.go:243-256`。
- `CreateRun` 失败、`MergeRun` 失败、`AbortRun` 成功后，workspace 目录都会继续留在磁盘上。
- 另外，V3 也没有 run delete API；V2 同样没有 workspace 目录回收能力，只在 deleteRemoved 场景删除 source 文件，而不是清理 workspace 根：`go-agent-v2/internal/service/workspace_file_ops.go:49-55`。

### 11. 大文件处理

结论：不通过，性能与保护都弱于 V2。

- V3 没有 `maxFiles / maxFileBytes / maxTotalBytes`；bootstrap 只做 dedupe，不做限流：`internal/module/workspace/service.go:176-186`。
- 内存方面，V3 hash/copy 都是流式 `io.Copy`，不会把单个大文件整块读入内存：`internal/module/workspace/service_helpers.go:32-50`、`internal/module/workspace/service_helpers.go:310-320`。
- 但性能方面很差。每个 bootstrap 文件至少经历一次 copy、一次 source hash、一次 workspace hash，而且 `buildRunFile` 发生在 DB transaction 内，意味着事务持有时间会随文件数量/体积线性拉长：`internal/module/workspace/service_helpers.go:166-218`、`internal/platform/db/tx.go:11-20`。
- merge 也有隐藏上限。V3 用常量 `mergeListLimit = 5000` 读取 tracked files，但不是“超限报错”，而是“静默只看前 5000 条”：`internal/module/workspace/service.go:20-34`、`internal/module/workspace/service.go:325-329`、`internal/module/workspace/service_merge.go:17-20`。
- V2 明确设置默认阈值并在超限时返回错误：`go-agent-v2/internal/service/workspace.go:123-131`、`go-agent-v2/internal/service/workspace.go:397-400`、`go-agent-v2/internal/service/workspace.go:577-597`。

### 12. V2 等价性

结论：不等价。

主要差异：

1. V2 有 `ResolveRunWorkspace`；V3 没有对应 service/RPC 能力：`go-agent-v2/pkg/toolsdk/tools/providers.go:217-224`、`go-agent-v2/internal/service/workspace.go:253-275`，对照 `internal/module/workspace/contract.go:11-20`。
2. V2 `MergeRun` 真正 walk workspace 并写回 source；V3 只更新状态，不写文件：`go-agent-v2/internal/service/workspace.go:348-372`、`go-agent-v2/internal/service/workspace.go:455-558`，对照 `internal/module/workspace/service_merge.go:17-31`、`internal/module/workspace/service_helpers.go:100-132`。
3. V2 `DeleteRemoved` 已实现；V3 还是 TODO：`go-agent-v2/internal/service/workspace.go:368-370`、`go-agent-v2/internal/service/workspace_file_ops.go:12-55`，对照 `internal/module/workspace/service.go:335-337`、`internal/module/workspace/service_merge.go:26-28`。
4. V2 `dryRun` 也会经过 `merging` 门闩并最终回到 `active`；V3 `dryRun` 完全旁路状态机：`go-agent-v2/internal/service/workspace.go:313-346`，对照 `internal/module/workspace/service.go:225-227`。
5. V2 默认 workspace 在受控 root 下，带 root guard；V3 默认落在 source tree 内：`go-agent-v2/internal/service/workspace.go:201-208`、`go-agent-v2/internal/service/workspace.go:268-270`，对照 `internal/module/workspace/service.go:149-160`。
6. V2 有大文件/大批量保护、atomic copy、source symlink 拒绝；V3 都没有：见第 6、11 条。
7. V2 abort 语义更宽，merged 后也能 abort；V3 只能 `active -> aborted`：`go-agent-v2/internal/service/workspace.go:277-281`、`go-agent-v2/internal/service/workspace_run_state_guard_test.go:155-163`，对照 `internal/module/workspace/service.go:243-256`。
8. V3 额外暴露了 `UpdateRunStatus / ListRunFiles / GetRunFile`，同时 `CreateRun` 允许调用方传 `WorkspacePath / Status / FinishedAt`；这也不是 V2 原能力面：`internal/module/workspace/contract.go:11-37`、`internal/module/workspace/rpc.go:15-22`。

## 最终判断

- B2：已修掉文档中那类 `runKey` traversal，但路径隔离和 key 约束仍弱于 V2，不能算“V2 级完备修复”。
- B3：正式 merge 门闩已修；`dryRun` 旁路、`UpdateRunStatus` 旁路、以及 merge 未真实落盘，使其仍然只是“状态机修复”，不是“能力闭合”。
- 以“Workspace 操作层能力 + 容错”衡量，V3 当前更接近“最小可观测骨架”，还不能视为 V2 workspace 能力的等价替代。

## 互审

以下批判点均按 LSP 回查目标报告中的结论，再回到实现代码做交叉验证。

### 1. `cap-store-resilience.md`

- `docs/plans/迁移/cap-store-resilience.md:45-67` 对“事务使用”的判断偏浅。它提到了 `persistRun` 事务，却没有点出 `CreateRun` 的目录创建和 bootstrap 文件复制都发生在事务之前；也就是说 run 记录可以回滚，但磁盘目录和部分文件不会回滚。证据：`internal/module/workspace/service.go:57-70` 先 `MkdirAll + bootstrapFiles`，之后才进 `persistRun`；`internal/module/workspace/service_helpers.go:166-179` 的事务只包 `UpsertRun + UpsertFile*`。
- `docs/plans/迁移/cap-store-resilience.md:136-149` 把“sqlc 生成层漂移”直接判成“通过”，证据却只覆盖 `UpdateAgentProviderBindingArchived` 单一方法。这个结论口径过大，因为 `internal/store/sqlc/querier.go:8-110` 实际暴露的是一个很大的 generated surface，单点 spot-check 不能推出“生成层漂移已通过”。
- `docs/plans/迁移/cap-store-resilience.md:192-218` 把“大数据量”问题主要收束成 OOM/limit 语义，但漏掉了 workspace merge 的 correctness truncation。`internal/module/workspace/service.go:22` 定义 `mergeListLimit = 5000`，`internal/module/workspace/service.go:325-329` 与 `internal/module/workspace/service_merge.go:17-20` 都按这个上限取 tracked files；超出部分不是报错，而是静默不参与 merge 计划。这不是纯内存问题，而是结果不完整问题。
- `docs/plans/迁移/cap-store-resilience.md:45-67` 还把 thread+binding 无事务主要描述成“当前只有 workspace/taskdag 暴露事务能力”，但没有指出基础设施其实已经有通用事务入口，缺的是 store contract / service 组合没有把它暴露到 thread+binding。证据：`internal/platform/db/tx.go:11-20`、`internal/store/sqlc/db.go:51-58` 都已有通用 tx 支撑；相对地，`internal/store/thread/contract.go:7-22` 与 `internal/store/binding/contract.go:7-14` 没有 `WithTx`。

### 2. `cap-provider-session.md`

- `docs/plans/迁移/cap-provider-session.md:111-126` 把 `Archive/Delete` 并列为“关闭后 session 仍留在 manager，Recover 会误复用”的同一类风险，表述过宽。这个复用风险对 `Archive` 是实锤，但对 `Delete` 不成立得这么直接，因为 `Delete` 会删除 binding 和 thread：`internal/module/thread/service.go:107-118`；而 `Recover` 一开始先跑 `resolveBinding`：`internal/module/thread/lifecycle.go:137-145`。也就是说 deleted thread 通常会在 binding 解析阶段先失败，而不是走到“命中 stale session 跳过 resume”。
- `docs/plans/迁移/cap-provider-session.md:111-115` 把 `Archive/Delete` 总结成“只 Close 不 Remove”，这个说法还偏乐观了。真实代码里 `closeSessionIfActive` 会吞掉 `resolveBinding/GetSession` 错误并直接返回 `nil`：`internal/module/thread/service.go:228-240`。所以有些路径不是“只 Close 不 Remove”，而是“既不 Remove，Close 也可能静默没做”。
- `docs/plans/迁移/cap-provider-session.md:87-103` 把 `ctx` 失效主要归因到 driver `Close(ctx)` 不 honor deadline，但这没有覆盖完整根因。`SessionManager.Remove` 自己就硬编码 `session.Close(context.Background())`：`internal/provider/unified/session.go:59-82`。因此显式 `Remove` 路径的 ctx 在进入 driver 之前已经丢了，不是纯 driver 问题。
- `docs/plans/迁移/cap-provider-session.md:10-18` 的总结开头把“`StopAgent/进程退出/fx OnStop` 也能走到 `Remove/CloseAll`”放在正向面里，容易让人误判回滚是普遍成立的。实际上 `thread.Start/Resume` 后续失败只会调用 `stopAgent(...)`：`internal/module/thread/lifecycle.go:50-74`、`77-106`；而 `stopAgent` 在 `s.orchestration == nil` 时是 no-op：`internal/module/thread/lifecycle.go:309-313`。也就是说非 orchestration 场景下，provider rollback 并不闭合。

### 3. `cap-event-push.md`

- `docs/plans/迁移/cap-event-push.md:7-18` 与 `24-29` 把“没有硬孤儿”说得过满。现有代码只能证明 `LogSink` 订阅了固定六组已知 DTO：`internal/platform/bus/sink.go:21-32`、`43-87`；但 bus API 本身是泛型开放面的，`bus.NewEmitter` 与 `TypedEmitter.On` 都可以面对任意 `event.Event`：`internal/platform/bus/emitters.go:32-40`、`internal/platform/bus/typed.go:14-30`。仓内没有“发布后至少一个订阅者”的全局断言，所以更准确的结论应当是“对已接入 LogSink 的 DTO 不存在硬孤儿”，而不是 bus 运行时整体没有硬孤儿。
- `docs/plans/迁移/cap-event-push.md:253-287` 对“plain `event.Subscribe` panic 风险”的总结需要再收紧一点。LSP 回查显示，live 的 push/Wails wiring 实际都走 `ResilientSubscribe`：`internal/platform/rpc/push.go:75-92`、`internal/ui/wails/bridge.go:53-63`、`internal/platform/bus/resilient.go:10-23`；而 `BindEventToNotify` 这个 plain-subscribe helper 没有任何引用：`internal/platform/rpc/push.go:60-73` 的 `references(compact)` 为 0。也就是说，当前真实 bridge 的 panic 暴露面主要在 `LogSink`，不是 push/Wails 主链。
- `docs/plans/迁移/cap-event-push.md:18` 用“V2 63 个命名事件 vs V3 typed bus 28 个”来做迁移面判断，口径并不对齐。V3 不只有 typed bus，一层更前面的 raw provider 事件面仍然存在：`internal/dto/provider/event.go:3-10` 定义 `RawProviderEvent`，`internal/provider/unified/event_map.go:12-66` 先收 raw event 再翻译成 typed event。把 V2 的“外显命名事件总数”和 V3 的“typed DTO 数量”直接对比，会低估 V3 的实际事件面，也会把“raw→typed 翻译层”完全排除在口径之外。
