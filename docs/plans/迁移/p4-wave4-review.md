# P4 波次 4 方案审查

## 1. 前置依赖

- `internal/module/thread/` 当前只有 3 个文件，且是最小骨架：
  - `module.go` 仅 `fx.Provide(NewService)`。
  - `contract.go` 当前 `Service` 只有 `List(ctx)` / `Get(ctx, id)`，`Ref` 只有 `ID` / `Name`。
  - `service.go` 当前只有 `logger` 字段，`List` / `Get` 均返回空值。
  - 结论：可以扩充，但当前 contract 与构造参数都远不足以承载 T7。

- `internal/contract/provider.go` 的 `Session` 已具备 thread 相关能力：
  - 已有 `ListThreads(ctx)` / `ForkThread(ctx, req)`。
  - 另外还有 `Configure(ctx, patch)`、`Close(ctx)`、`ForceStop()`。
  - `TurnHandle` 当前是 `LocalID()` / `ProviderID()` / `Done()` / `Err()`。
  - 结论：T7/T8 所需的基础 contract 已在代码中落地。

- `internal/store/thread/` 已就位，但只覆盖运行态线程元数据：
  - 有 `GetByPort`、`ListRunning`、`ListRecoverable`、`ListRunningAgents`、`Upsert`、`UpdateStatus`、`RunningExists`、`ListCwds*`。
  - 没有 `GetByThreadID`、归档切换、provider thread 绑定、rollout path、消息历史读取、线程别名。
  - 结论：`store/thread` 只能支撑运行态扫描，单独不足以完成 T7。

- `internal/provider/unified/client.go` 与 `registry.go` 已就位：
  - `Client.StartSession` / `ResumeSession` 已通过 `Registry.Resolve` 分发到具体 driver。
  - `Registry` 已通过 `fx` driver group 构造冻结映射，并做 `trim + lower` 归一化。
  - `SessionManager` 已存在，可按 `agentID` 注册/获取活跃 session。
  - 结论：T8 的 registry/client 测试基座已存在。

- `internal/provider/claudecli/driver.go` 与 `internal/provider/codexapp/driver.go` 当前可编译：
  - 两个 driver 都有 `var _ contract.Driver = (*driver)(nil)`。
  - 两个 session 都有 `var _ contract.Session = (*session)(nil)`。
  - LSP diagnostics 未发现阻断级错误；仅有 `maps.Copy` 优化提示和 `client.go` 中 `ctx` 未使用提示。
  - 结论：T8 可以在当前代码面上补 contract 测试。

- 与 T7 直接相关的额外依赖已存在，但未接入 `module/thread`：
  - `internal/store/binding/` 已持有 `Provider`、`ProviderThreadID`、`CodexThreadID`、`RolloutPath`、`Archived`、`Cwd`。
  - `internal/store/agentstatus/` 已持有 `SessionID` / `Status`。
  - `internal/store/uipreference/` 已持有任意 key/value UI 偏好，可承接 V2 的 thread alias。
  - 结论：T7 真正需要的 store 面不是只有 `store/thread`。

## 2. T7 依赖方向

- `module/thread` 不应 import `internal/provider/*`。这一点与当前迁移方向一致。

- 按当前代码面，`module/thread` 的可行依赖集合应至少是：
  - `internal/contract`
  - `internal/dto/provider`
  - `internal/store/thread`
  - `internal/store/binding`
  - 可选：`internal/store/agentstatus`
  - 可选：`internal/store/uipreference`
  - 可选：`internal/platform/shared/*`

- 仅依赖 `contract + dto + store/thread + platform/*` 不够：
  - `store/thread` 没有 provider thread binding。
  - `store/thread` 没有 rollout path。
  - `store/thread` 没有 archived 状态。
  - `store/thread` 没有 alias/name 持久化。
  - 结论：若坚持只接 `store/thread`，T7 的 listing/history/messages 会直接缺数据源。

- `history/messages` 当前不能通过现有 `contract.Session` 拿到 provider session history backend：
  - `contract.Session` 没有 `ReadHistory` / `ReadMessages` / `ReadThread` 一类方法。
  - `claudecli/history.go` 的 `historyBackend.ReadHistory(...)` 是包私有实现。
  - `codexapp/history.go` 的 `rolloutReader.ReadHistory(...)` 也是包私有实现。
  - 两个 provider session 内部虽然持有 `history` 字段，但当前外部不可见，且仓内没有调用点。
  - 结论：现状下不能从 `module/thread` 直接“通过 contract 调 session history backend”。

- 因此，T7 只有两条可行路径：
  - 路径 A：把历史读取能力下沉到 provider-neutral 层。
    - 例如迁到 `internal/platform/...` 或 `internal/module/thread` 包内私有 helper。
    - 由 `binding.Store` 提供 `provider/provider_thread_id/rollout_path`，再选择 Claude/Codex 的 reader。
  - 路径 B：新增可选 contract。
    - 例如 `type ThreadHistoryReader interface { ReadHistory(...) }`。
    - 再由 `module/thread` 依赖一个 provider-neutral 的 session lookup 抽象，而不是直接依赖 `provider/unified.SessionManager`。

- 当前更稳妥的结论：
  - `history/messages` 的主数据源应优先走 `store/binding + provider-neutral rollout/history reader`。
  - 若要读取“活跃 session 的内存态/transport 态历史”，必须先扩 contract；否则不可行。

## 3. T7 V2 对照

### `thread_history_core.go`

- 前 50 行可见的核心签名：
  - `HistoryBackend`
  - `ResolveHistoryBackend`
  - `EnsureContext`
  - `NormalizeHistoryTimeout`
- LSP 文档符号显示的核心方法：
  - `ResolveProviderThreadCandidates`
  - `ThreadExistsInHistory`
- 对 V3 的含义：
  - `context/timeout` 归一化可直接合并进 `module/thread` helper。
  - 真正关键的是 provider thread candidate 解析与历史存在性检查。
  - 当前 V3 缺少 provider-neutral 的 candidate/source 解析层。
- 结论：部分可覆盖，但“provider thread 归一化 + 历史源解析”当前缺抽象。

### `thread_archive_core.go`

- 前 50 行可见的核心签名：
  - `ThreadArchiveFile`
  - `ThreadArchiveManifest`
  - `ThreadArchiveFileState`
  - `InferThreadArtifactKind`
  - `BuildThreadArchiveRestoreDeps`
- LSP 文档符号显示的核心方法：
  - `InspectThreadArchiveForRestore`
  - `RestoreThreadArchiveSources`
  - `PruneArchivedProviderSourceFiles`
- 对 V3 的含义：
  - 若波次 4 只做 archive/unarchive，V2 中的 restore/prune/checksum/manifest 校验不必全量保留。
  - 若还承诺 `rollback` / source restore，则 archive 逻辑不会只是一层状态翻转。
- 结论：可覆盖，但必须先冻结 archive 的精确语义；否则很容易把 restore/rollback 误删。

### `thread_listing_core.go`

- 前 50 行可见的核心签名：
  - `PaginateLoadedThreadIDs`
- LSP 文档符号显示的核心方法：
  - `BuildThreadList`
  - `AppendThreadHistoryFromStores`
  - `LoadBindings`
  - `LoadStatuses`
  - `LoadArchiveMap`
  - `AppendArchivedThreads`
  - `NormalizeThreadAliases`
  - `ApplyThreadAliases`
  - `PersistThreadAlias`
- 对 V3 的含义：
  - V2 listing 不是单一 runtime list；它聚合了 runtime、binding、status、archive、alias。
  - 当前 V3 只有 `store/thread`，没有把 binding/status/uipreference 接进 `module/thread`。
  - 当前 `thread.Ref.Name` 也没有可靠来源。
- 结论：当前骨架无法覆盖 V2 listing 语义；至少要补 `binding`，若保留名称/别名，还要补 `uipreference`。

### `slash_command_logic.go`

- 前 50 行可见的核心签名：
  - `SlashCommandWithArgsParams`
  - `RunSendSlashCommand`
- LSP 文档符号显示的核心方法：
  - `ResolveThreadForSlashCommandLogic`
  - `ParseSlashCommandArgParams`
  - `ThreadSkillsListResult`
- 对 V3 的含义：
  - `ResolveThreadForSlashCommandLogic` 与 thread 解析仍有保留价值。
  - `RunSendSlashCommand` 的“字符串命令透传”不应继续作为主路径。
  - `review/start` 已明确推迟 P5，可不纳入 T7。
- 结论：可覆盖，但应重写成显式 service 方法，不应回流 generic slash tunnel。

### `thread_messages_logic.go`

- 前 50 行可见的核心签名：
  - `BuildThreadMessagesResponse`
  - `BuildThreadMessagesPagePayload`
  - `ResolveHistoryBackendForProvider`
  - `messagesReaderForBackend`
- LSP 文档符号显示的核心方法：
  - `LoadAllThreadMessagesFromProviderRolloutWithBackend`
  - `StreamRemainingHistory`
  - `HandleThreadMessagesHydration`
- 对 V3 的含义：
  - messages 不是单纯“读一份 rollout 文件”；它还包含 provider 选择、reader 选择、分页 hydration。
  - 当前 V3 的 Claude/Codex reader 仍在 `provider/*` 包内，`module/thread` 无法合法复用。
- 结论：messages 可覆盖，但前提仍是先把 history reader/provider 选择机制 provider-neutral 化。

### 对 14 文件口径的补充判断

- 仓内文档目前并不只有一个 T7 口径：
  - `docs/plans/迁移/p4-execution-plan.md` 仍是窄口径 `10 文件 / 2.7k 行`。
  - `docs/plans/迁移/v3-migration-plan.md` 额外把这些文件也归到 `module/thread`：
    - `service/lifecycle/thread_lifecycle_logic.go`
    - `service/lifecycle/turn_resume_core.go`
    - `internal/apiserver/codexadapter/thread_messages.go`
    - `internal/apiserver/codexadapter/adapter_thread_listing.go`
    - 以及 `methods_thread_turn.go` / `methods_thread_helpers.go` 等 RPC/adapter 包装层
- 结论：
  - `14 文件 / 3,016 行` 是“更宽口径”的说法，逻辑上成立。
  - 但仓内尚未形成统一的单一统计基线，实施前必须先冻结 retained source set。

## 4. T7 代码量

- `V2 14 文件 / 约 3,016 行 -> V3 <= 1,000 行` 不是天然不合理，但成立条件很严格。

- 可以明显删除或强合并的 V2 逻辑：
  - `internal/apiserver/*` 的 RPC 注册、request/response wrapper、adapter glue。
  - provider-specific 的 `AGENT_PROVIDER` / env 分支。
  - review、realtime、skills list、dynamic tool 相关旁支。
  - 运行时 messages 与 hydration 的双链路重复包装。
  - archive 的 checksum、prune、manifest 扫描、source copy/restore 工具层，前提是本波次不承诺 rollback/restore。
  - UI 日志、结果包装、debug guard。

- 必须保留的 V2 语义：
  - thread canonical ID / provider thread candidate 解析。
  - list 聚合：至少 runtime + binding + archived。
  - history/messages 读取与 rollout 合并。
  - hydration 分页和 load-limit。
  - capability fallback：例如 Claude 的 `ListThreads` / `ForkThread`。
  - 若 `thread.Ref.Name` 继续保留，则 alias/name 也必须有真实来源。

- 代码量判断：
  - 若 T7 只保留 `history + archive/unarchive + listing + command(parse/dispatch) + messages + hydration` 的 provider-neutral 核心，`<= 1,000` 有机会成立。
  - 若 T7 同时把 `thread_lifecycle_logic.go`、`turn_resume_core.go`、`thread/read`、`thread/name/set`、`thread/config/*`、rollback/restore 一并吸收，`<= 1,000` 过紧。

- 结构建议：
  - 不建议把全部逻辑继续塞回单个 `service.go`。
  - `docs/plans/迁移/v3-module-migration-details.md` 已给出更合理的包内拆分：`service.go` + `archive.go` + `config.go` + `helpers.go` + `rpc.go`。
  - 结论：应把“V3 <= 1,000”理解为 package 级目标，而不是单文件目标。

## 5. T8 可行性

- 两个 driver 当前都不能直接用“纯内存 mock transport”做端到端 contract 测试：
  - Claude:
    - `driver.start(...) -> launchCLI(...) -> newTransport(...) -> exec.Command(...)`
    - 没有 transport interface，也没有可注入的 factory。
    - 结论：只能用假 CLI 子进程或先做构造注入重构，不能纯内存。
  - Codex:
    - `newSession(...) -> newTransport(serverURL) -> websocket connect / spawn local codex app-server`
    - 同样没有 transport interface 或 dialer 注入面。
    - 结论：可以用进程内 websocket stub server 做测试，但这不是纯内存 transport。

- 因此 T8 的共享 contract suite 是可行的，但必须拆成：
  - 通用断言层：共享。
  - driver-specific harness：分别提供 Claude 假 CLI、Codex 假 websocket server。

- 当前 contract 在代码层面是稳定的，但在文档层面仍不稳定：
  - 代码当前的 `Session` 是：
    - `ThreadID`
    - `Capabilities`
    - `StartTurn`
    - `Interrupt`
    - `ListThreads`
    - `ForkThread`
    - `Configure`
    - `Close`
    - `ForceStop`
  - 旧文档仍出现这些过时形态：
    - `Events() <-chan ...` + 独立 `SessionControl`
    - `TurnHandle.TurnID`
  - 结论：T8 必须以 `internal/contract/provider.go` 当前代码为唯一基线，不能再按旧文档写测试。

- 当前没有现成测试可复用：
  - 在 `internal/provider`、`internal/dto/provider`、`internal/provider/unified` 下未找到现有 `func Test...`。
  - 结论：T8 基本是从零起测。

- `BuildManifest` 测试可以是纯单测，且范围应收窄：
  - 当前 `BuildManifest(ctx)` 实际只根据 capability 决定 binaries：
    - 默认输出 `go-agent-mcp-lsp`、`go-agent-mcp-orch`
    - 若 `ctx.ThreadCaps.Has("ida")` 再追加 `go-agent-mcp-ida`
  - 它当前并不使用 `AgentID`、`CWD`、`Env`。
  - 结论：测试只需锁定输出顺序、名称与 command path 形状，不必虚构更大的 schema 语义。

- `Registry.Resolve` 测试完全可行，且适合纯单测：
  - 验证 `trim + lower` 归一化。
  - 验证未知 provider 报错。
  - 验证 `Create == nil` 的 factory 会被跳过。
  - 可顺带验证 `Names()` 排序。

## 6. 结论与调整建议

### Blocker

- `module/thread` 当前无法在“不 import provider/*”的前提下获取 provider history backend。
  - `contract.Session` 没有 history/messages 读取能力。
  - Claude/Codex history reader 都是 provider 包私有实现。
  - 这会直接阻断 T7 的 `history/messages` 落地。

- `store/thread` 单独不足以支撑 T7。
  - V2 listing/messages 依赖 binding/status/archive/alias。
  - 当前至少还需要 `store/binding`；若保留名称/别名，还需要 `store/uipreference`；若保留 status fallback，还需要 `store/agentstatus`。

- T8 不能按“纯内存 mock transport”假设开工。
  - Claude 需要假 CLI 子进程或 transport factory 注入。
  - Codex 需要 websocket stub server 或 transport factory 注入。

- T8 不能以旧波次文档为 contract 基线。
  - 当前代码 contract 与早期文档的 `Events()/SessionControl/TurnID` 版本不一致。
  - 若不先声明“以代码为准”，测试会在落地前就产生目标漂移。

### Improvement

- 先冻结 T7 retained source set。
  - 明确到底采用窄口径 `10 文件 / 2.7k 行`，还是宽口径 `14 文件 / 3.0k 行`。
  - 只有先冻结集合，`<= 1,000` 的目标才有可评估性。

- 给 `module/thread` 增加 provider-neutral 的 history source 抽象。
  - 推荐优先走 `binding.Store -> provider/provider_thread_id/rollout_path -> reader registry`。
  - 若需要读活跃 session 的 transport 态，再单独引入可选 contract。

- 不要把 T7 继续收束为单一 `service.go`。
  - 维持一个小的公开 `Service`。
  - 把 archive/messages/helpers/config/rpc 拆到包内私有文件。

- T8 采用“共享断言 + provider harness”方案。
  - 共享断言覆盖 `Driver.Name/StartSession/ResumeSession`、`Session` 方法集、`TurnHandle.LocalID/ProviderID`、capability error、manifest、registry。
  - Claude/Codex 各自提供启动 harness。

- 若波次 4 不承诺 rollback/restore：
  - 明确把 archive 语义限定为 `archive/unarchive + binding/archive state + 最小 manifest`。
  - 这样更接近 `<= 1,000` 的现实目标。

- 综合结论：
  - T7：方向成立，但在 history backend 抽象与 store 依赖面上存在实质阻断，不能按当前依赖描述直接开工。
  - T8：可以开工，但不能按“纯内存 transport”设计；必须先确定 driver-specific 测试 harness，并以当前代码 contract 为准。

## 7. 修补记录

### B1：Session.ReadHistory

- 已在 `internal/contract/provider.go` 的 `Session` 接口新增：
  - `ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error)`
- 已新增 `internal/dto/provider/message.go`：
  - `dto.Message` 定义为 `Role`、`Content`、`Timestamp time.Time`、`Metadata map[string]any`
- 已在两个 provider session 中完成实现：
  - `internal/provider/claudecli/session_history.go`
  - `internal/provider/codexapp/session_history.go`
- 实现方式：
  - Claude：委托 `historyBackend.ReadHistory(ctx, threadID)`，再包装为 `[]dto.Message`
  - Codex：委托 `rolloutReader.ReadHistory(ctx, threadID, limit)`，再包装为 `[]dto.Message`

### B2：T7 store 依赖面确认

- T7 的 `module/thread/service.go` 构造函数将注入以下接口：
  - `store/thread.Store`：线程元数据 CRUD
  - `store/binding.Store`：provider 绑定查询
  - `contract.Session`：通过 `unified.SessionManager` 获取，用于 history 读取
- `store/agentstatus` 和 `store/uipreference` 暂不注入；如 T7 实现时需要 status/alias 语义，再按需追加。

### B3：T8 contract test 设计决策

- T8 的 contract test 不 mock transport，而是 mock `contract.Session` 接口。
- 设计基线：

```go
 type mockSession struct {
     caps     dto.CapabilitySet
     threadID string
 }
```

- 测试范围：
  - 验证 `Session` 接口所有方法的行为契约
  - 不支持的能力方法返回 `CapabilityError`
  - MCP manifest 测试直接调用 `dto.BuildManifest(...)`
  - Registry 测试使用真实 `DriverFactory` + mock `Driver`

## 8. 自审

### 1. 编译+守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过

### 2. B1 复核

- `Session` 已新增 `ReadHistory`
- `dto.Message` 已定义
- `claudecli.session` 已实现 `ReadHistory`
- `codexapp.session` 已实现 `ReadHistory`

### 3. import 方向

- `internal/dto/provider/message.go` 仅 import `time`
- `internal/contract/provider.go` 仍只 import `context` 与 `dto/provider`
- 未引入 `dto -> contract` 或 `contract -> provider/*` 反向依赖

### 4. 行数

- 本次新增/修改文件均低于 400 行：
  - `internal/contract/provider.go`：52 行
  - `internal/dto/provider/message.go`：10 行
  - `internal/provider/claudecli/session_history.go`：58 行
  - `internal/provider/codexapp/session_history.go`：63 行
- 本次新增函数均低于 80 行

### 5. 接口一致性

- `internal/provider/claudecli/session.go` 仍保留 `var _ contract.Session = (*session)(nil)`
- `internal/provider/codexapp/session.go` 仍保留 `var _ contract.Session = (*session)(nil)`
- `go build ./...` 已证明新增方法后两个断言仍成立

### 6. B2/B3 决策记录

- 已写入本文件 `## 7. 修补记录`

### 7. 波次 4 可开工判定

- 本轮审查识别的 3 个 Blocker 已消除：
  - B1：已落代码
  - B2：已冻结依赖决策
  - B3：已冻结测试设计决策
- 结论：波次 4 可以开工
- 剩余事项属于实现期改进项，不再构成开工阻断

### 8. CapabilityError

- Claude 的 `ListThreads` / `ForkThread` 仍返回 `dto.NewCapabilityError(...)`
- Codex 的 `ListThreads` / `ForkThread` 行为未变
- `ReadHistory` 当前不走 capability-gated 设计，因为在当前双 provider 范围内 Claude/Codex 都已实现该方法
