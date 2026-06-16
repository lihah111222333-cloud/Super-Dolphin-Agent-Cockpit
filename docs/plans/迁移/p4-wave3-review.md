# P4 波次 3 方案审查

## 1. 前置依赖状态

- `internal/contract/provider.go` 已具备 `Driver`、`DriverFactory`、`Session`、`TurnHandle`、`ToolCallResponder` 五类核心契约。`Session` 已暴露 `Capabilities`、`StartTurn`、`Interrupt`、`ListThreads`、`ForkThread`、`Configure`、`Close`、`ForceStop`。
- `internal/dto/provider/*.go` 文件齐全：`capability.go`、`event.go`、`manifest.go`、`session.go`、`thread.go`、`thread_config.go`、`turn.go` 均已就位；LSP 诊断当前为 clean。
- `internal/provider/unified/event_map.go` 中 `EventDispatcher` 已就位，具备 translator 注册与 raw event 分发能力。
- `internal/provider/claudecli/` 与 `internal/provider/codexapp/` 两个 driver 包已具备 `driver.go`、`session.go`、`event_map.go`、`module.go` 等主体文件；LSP 诊断当前为 clean。
- `internal/provider/unified/module.go` 目前仅 `fx.Provide(NewEventDispatcher)`，尚无 registry、client、session facade。
- 现有契约仍有两个前置缺口：
  - `contract.ToolCallResponder` 已定义，但在 `internal/provider/*` 下没有实现搜索结果，说明 DynamicTools 结果回填链路尚未落地。
  - `dto/provider.InputItem` 只有 `Type/Content/Path`，而上层 `internal/dto/turn/InputItem` 已有 `Type/Text/URL/Path/Name/Content`；provider DTO 目前不足以无损承接 V2 turn 输入语义。

## 2. T5 依赖方向

- `provider/unified` 做 driver registry 的方向可行。
  - 现仓已有 `fx` value-group 使用先例：`internal/app/runner.go` 用 `group:"runners"` 收集 runner，`internal/sidecar/orch/orchestration/module.go` 用 `fx.Annotate(..., fx.ResultTags(\`group:"runners"\`))` 输出 runner。
  - 因此可采用：
    - `claudecli` / `codexapp` 各自输出一个 `contract.DriverFactory` 到 `group:"drivers"`
    - `provider/unified` 通过 `fx.In` 收集 `[]contract.DriverFactory`，构建只读 registry
- 方向约束可保持成立。
  - 当前 concrete driver 只 import `provider/unified` 获取 `EventDispatcher`。
  - `provider/unified` 当前没有 import `claudecli` / `codexapp`；后续应继续保持仅依赖 `contract` / `dto`。
- `provider/unified/session.go` 不应创建 concrete session。
  - 当前 `claudecli/driver.go` 与 `codexapp/driver.go` 已各自实现 `StartSession` / `ResumeSession` 并返回 `contract.Session`。
  - 当前 `claudecli/session.go` 与 `codexapp/session.go` 已分别内聚 transport、事件循环、capabilities、turn handle 管理。
  - 因此 unified 最合理的实现是 facade：选 driver，调用 `driver.StartSession` / `driver.ResumeSession`，必要时仅包一层 bookkeeping。
- Blocker：registry 当前没有稳定的 driver 选择输入。
  - `internal/dto/provider/StartSessionRequest` 与 `ResumeSessionRequest` 都没有 `Provider` / `Driver` 字段。
  - V2 的 provider 选择是显式契约：`go-agent-v2/legacy-agentsdk/agentcore/types.go` 中 `LaunchConfig.Provider`，以及 `go-agent-v2/internal/runner/provider_registry.go` 的 `ResolveProviderFactory(...)`。
  - 如果不补 typed provider 字段，unified registry 只能依赖隐式默认值或 `Config` map，和 V2 的显式 provider 选择相比不稳定。

## 3. T6 依赖方向

- `module/turn` 不 import `provider/*` 的方向可行，但必须严格依赖以下边界：
  - `contract.Session`
  - `dto/provider.*`
  - `dto/turn.*`
  - `module/orchestration.Service` 或更窄的 turn queue / complete 接口
- provider 能力获取可通过 `contract.Session.Capabilities()` 完成。
  - 当前 `contract.Session` 已返回 `dto.CapabilitySet`。
  - `claudecli/session.go` 与 `codexapp/session.go` 已分别实现 `Capabilities()`。
- MCP manifest 不需要 import `provider/unified`。
  - `dto/provider/manifest.go` 已提供 `BuildManifest(...)`。
  - `dto/provider/TurnRequest` 已带 `MCP MCPManifest`。
  - 当前 `claudecli/session.go` 已在 `applyTurnSettingsLocked` 中消费 `req.MCP`；`codexapp` 可忽略该字段。
- Blocker：`review.go` 当前无下层契约。
  - V2 `ReviewStart` 通过 `/review` 命令实现，见 `go-agent-v2/internal/apiserver/codexadapter/adapter_lifecycle.go:115-119`。
  - 当前 `contract.Session` 没有 `ReviewStart`、通用 slash-command、或 review 专用接口。
  - 若波次 3 保留 `module/turn/review.go`，必须先补 contract；否则应将 review 移出本波次。
- Blocker：turn ID 相关职责未统一。
  - `module/orchestration` 在 provider 接受前就为 turn 分配 ID：`internal/sidecar/orch/orchestration/helpers.go:147-153`。
  - `claudecli/session.go` 在本地生成 turn ID：`newTurnHandle(shared.NewID("turn"))`。
  - `codexapp/session.go` 则以远端 `turn/start` 返回值作为 turn ID。
  - 当前没有 local turn ID 与 provider turn ID 的相关性模型，`module/turn` 无法安全对齐 orchestration 状态、provider event、interrupt/tracker。
- Improvement：当前总线上 turn 事件存在职责重叠风险。
  - `module/orchestration/events.go` 会发布 `TurnStarted` / `TurnCompleted` / `TurnInterrupted`。
  - `claudecli/event_map.go` 与 `codexapp/event_map.go` 也会发布同名 `dto/turn` 事件。
  - 若 `module/turn` 再引入 tracker/stall，必须先定义 turn lifecycle 单一事实来源。

## 4. V2 对照

- `go-agent-v2/internal/apiserver/codexadapter/adapter.go` 的核心职责不是单一 provider facade，而是一个大杂糅对象：
  - 依赖装配 `Deps`
  - tracker state
  - deferred turn start
  - interrupt helper
  - dynamic tool inventory
  - UI runtime/store/binding/status 访问
  - lifecycle/runtime helper 分发
- 对 V3 `provider/unified` 来说，可继承的只有很小一部分：
  - driver/provider 选择
  - session 生命周期 facade
  - event dispatcher 接线
  - 可能的 active session registry
  - store/UI/dynamic tool/tracker 不应回流进 unified
- `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go` 的 submit 主链已经能映射到 V3，但不是 1:1 文件搬运：
  - V2：`SubmitWithSkillsAndOverrides` 根据 client 能力在 `SubmitWithSkillsAndOverrides` / `SubmitWithSkills` / `Submit` 之间分支。
  - V3：应统一为 `module/turn` 组装 `dto/provider.TurnRequest`，再交给 `contract.Session.StartTurn`。
  - 当前 `codexapp/session.go` 已能将 `TurnRequest.Overrides` 与 `OutputSchema` 映射到 `turn/start` RPC。
  - 当前 `claudecli/session.go` 已能消费 `Overrides` 并在 manifest/model/effort 变化时重启 CLI。
- `SubmitWithSkillsAndOverrides` 在 V3 的拆解建议：
  - `skills` 解析与 prompt 组装进入 `module/turn`
  - `model/effort/outputSchema` 进入 `dto/provider.TurnRequest`
  - provider-specific fallback 分支删除，由各 driver 的 `Session.StartTurn` 自己解释统一请求
- 关键对照缺口 1：skill prompt 还未真正覆盖。
  - V3 `dto/provider.SkillRef` 已带 `Name` 与 `Prompt`。
  - 但 `claudecli/session.go` 的 `buildTurnText` 只使用 `SkillRef.Name`，忽略 `SkillRef.Prompt`。
  - `codexapp/session.go` 的 `buildTurnStartParams` 也只发送 skill name。
  - 若 T6 只传 `SkillRef`，V2 的 `BuildSelectedSkillPrompt` / `RenderAutoMatchedSkillPrompt` 语义不会自动保留。
- 关键对照缺口 2：turn input 语义存在压缩损失。
  - V2 `agentcore.TurnInput` 字段为 `Type, Text, URL, Path, Name, Content`。
  - V3 `dto/provider.InputItem` 只有 `Type, Content, Path`。
  - 当前 `codexapp/session.go` 的 `mapTurnInput(...)` 主要读取 `item.Content`，对 `Path` 不敏感；这和 `claudecli/session.go` 的处理方式不一致。
  - 结论：若不扩展 provider DTO 或修正 provider mapper，T6 无法无损迁移 V2 的 image/file/mention/fileContent 输入。
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_prepare_core.go` 的核心保留面：
  - `PrepareTurnStartSubmission`
  - `PrepareTurnSteerSubmission`
  - `prepareTurnSubmissionCommon`
  - `ParseTurnInputs`
  - skill force/explicit match 组装
  - `BuildUserTimelineAttachments`
  - `ComposeUserTimelineTextForTurn`
- 其中可直接删除或移出 T6 的部分：
  - UI timeline append 相关
  - injected LSP prompt 展示偏好
  - DynamicTools/LSP availability warning
- `go-agent-v2/legacy-agentsdk/service/runtime/turn_runtime_logic.go` 中大段逻辑属于“准备可提交进程/恢复历史线程”，不应进入 T6：
  - `EnsureThreadReadyForTurn`
  - launch context / binding candidate / resume candidate
  - historical launch/recover
  - 这些职责在 V3 应前移到 T5 session bootstrap 或更外层 agent/session 管理服务
- `go-agent-v2/legacy-agentsdk/service/interrupt/turn_interrupt_core.go` 与 `service/tracker/*` 中需要保留的不是 API 形状，而是行为：
  - active turn terminal wait
  - interrupt settle
  - watchdog
  - stall grace period + auto interrupt
  - recovery callback
- `service/tracker/turn_tracker_summary_core.go` 可以删减。
  - V2 负责 turn summary cache/inject。
  - 当前 V3 `internal/dto/turn/TurnCompleted` 只有 `Success/Error`，没有 summary 字段；该块不再是 wave3 核心必需项。
- DynamicTools 主链可从 wave3 主路径移除。
  - 当前 `contract.ToolCallResponder` 无实现搜索结果。
  - 当前 provider 侧已具备 tool call / approval event 翻译，但没有 result-response 主链；说明 wave3 可只保留事件面，不迁移 V2 DynamicTools 回填链路。

## 5. 代码量

- T5 `<= 1,500` 行：合理。
  - V2 来源约 1.1k 行：`adapter.go` + `adapter_lifecycle.go` + `adapter_launch_config.go` + `provider_registry.go`。
  - V3 已有 concrete driver、event map、session 基本实现。
  - T5 只要收敛到 registry + facade + session bookkeeping，不需要再带上 UI/store/tracker，1.5k 充足。
  - 唯一会显著冲高代码量的是 provider 选择契约若继续放在 `Config map` 中做兼容分支。
- T6 `<= 1,300` 行：偏激进，但在严格裁剪范围后可达。
  - V2 来源表面约 3.5k 行，但其中大量是可删除或可外移逻辑。
  - 可以删除或外移的块：
    - DynamicTools schema / result-response
    - UI timeline append / injected prompt 显示偏好
    - process launch/resume/binding/history 恢复
    - tracker summary cache/inject
    - provider-specific submit fallback 分支
  - 必须保留的块：
    - input parsing / attachment normalization / guardrail
    - skill normalize + force/explicit match + prompt compose
    - TurnRequest 组装（skills/overrides/outputSchema/MCP）
    - active turn state + interrupt settle + stall/watchdog
  - 若 `review.go` 仍在 T6 范围内，且本波次还要补 contract，则 `1,300` 目标明显偏紧。

## 6. 工厂模式

- driver registry 用 `fx` 组装是正确方向。
  - 推荐 `claudecli` / `codexapp` 输出 `contract.DriverFactory` 到 `group:"drivers"`。
  - `provider/unified` 在构造期收集一次，建立不可变 map。
- `provider/unified` 更适合做“两级工厂”：
  - 一级：registry 解析 provider -> `contract.Driver`
  - 二级：driver 创建 concrete `contract.Session`
- T6 也需要显式“组装管道”工厂，否则会重演 V2 的大 Adapter。
  - 推荐最少拆为：
    - `InputAssembler`
    - `SkillResolver`
    - `ManifestBuilder`
    - `Tracker`
    - `InterruptCoordinator`
  - `service.go` 只保留 orchestration / session / event bus 编排。
- tracker/stall 检测不能直接复用 orchestration 现有 `StallDetector`。
  - 当前 `internal/sidecar/orch/orchestration/recover.go` 的 `StallDetector` 只按 agent `updatedAt` 判定整进程卡死。
  - V2 tracker 的 stall 检测是 turn 级别，含 heartbeat、grace period、auto interrupt、recovery callback。
  - 两者层级不同；最多共享阈值配置与 logger，不应共享实现。
- `module/turn` 与 `module/orchestration` 当前存在职责重叠风险：
  - orchestration 负责 turn queue、local turn id、agent state 迁移、turn event 发布
  - provider translator 负责 provider turn event 发布
  - 若 T6 再接入 tracker，则 turn started/completed 的归属必须先统一

## 7. 结论与调整建议

- T5 方向基本可做，但必须先补 provider 选择契约，否则 registry 只有收集价值，没有稳定选路能力。
- T6 方向理论上可做，但当前契约还不足以无损承接 V2 的 review、turn id 对齐、skill prompt、rich input 语义。
- 结论：不建议直接按现方案开工。建议先做一轮“波次 3 前置契约修补”，再进入 T5/T6 正式实现。

### Blocker

- `StartSessionRequest` / `ResumeSessionRequest` 缺少 typed provider 字段；`provider/unified` 无法稳定选择 `claude` / `codex`，与 V2 `LaunchConfig.Provider` 能力不等价。
- `module/turn/review.go` 当前无 provider-neutral 下层接口；`contract.Session` 无 `ReviewStart` 或通用 slash-command 能力。
- turn ID 归属未定义：orchestration 预分配 local turn ID，provider 再生成 runtime turn ID，当前没有相关性模型。
- `dto/provider.InputItem` 过瘦，且 provider mapper 行为不一致；`SkillRef.Prompt` 也尚未被 session 实现消费，V2 turn prepare 语义无法无损迁移。

### Improvement

- 立即把 driver 注册改成 `fx.Out` / `fx.In` `group:"drivers"`，停止在 driver module 中直接 provide raw `contract.Driver`。
- 保持 `provider/unified` 只做 facade/registry，不承接 store/UI/tracker/dynamic tool 逻辑。
- 为 `module/turn` 增加 archtest：禁止 import `internal/provider/`；同时补一条规则，禁止 `provider/unified` import concrete provider。
- 若保持 provider-neutral turn prepare，需要补 capability/contract 描述：
  - provider 选择
  - native skill handling
  - review support
  - turn id correlation
- T6 首版建议只保留 turn core：
  - input assemble
  - skill prompt merge
  - `StartTurn` / `Interrupt`
  - active turn tracker + stall/watchdog
  - 把 summary cache、UI timeline、DynamicTools 留给后续波次

## 8. 修补落地

- B1 已落地：`internal/dto/provider/session.go` 已为 `StartSessionRequest` 与 `ResumeSessionRequest` 增加 `Provider string \`json:"provider"\``。
- B2 已落地：本次未在 `contract.Session` 中引入 review/slash-command 契约；仓内 `internal` 范围内 `ReviewStart` 搜索结果为 0。`review` 明确推迟到 P5 RPC 层。
- B3 已落地：
  - `internal/contract/provider.go` 的 `TurnHandle` 已改为 `LocalID()` / `ProviderID()` / `Done()` / `Err()`。
  - `internal/dto/provider/turn.go` 已新增 `TurnResult`。
  - `internal/dto/provider/turn.go` 的 `TurnRequest` 已补 `LocalID`，用于后续本地 turn ID 透传。
  - `internal/provider/claudecli/session.go` 与 `internal/provider/codexapp/session.go` 已实现 `LocalID()` / `ProviderID()`。
- B4 已落地：
  - `internal/dto/shared/input.go` 新增统一 `shared.InputItem`。
  - `internal/dto/provider/turn.go` 与 `internal/dto/turn/model.go` 均已改为 `type InputItem = shareddto.InputItem`。
  - `internal/provider/claudecli/session.go` 与 `internal/provider/codexapp/session.go` 已消费 `Content/Path/Name/URL`。
  - `internal/provider/claudecli/session.go` 已通过 `buildSkillSection(req.Skills)` 消费 `SkillRef.Prompt`。
  - `internal/provider/codexapp/session.go` 已通过 `buildSkillPromptInput(req.Skills)` 将 `SkillRef.Prompt` 注入 turn input。
- I1 已落地：
  - `internal/provider/claudecli/module.go` 与 `internal/provider/codexapp/module.go` 已输出 `group:"drivers"` 的 `DriverFactory`。
  - `internal/provider/unified/module.go` 已通过 `RegistryParams` 收集 `[]contract.DriverFactory`。
  - `internal/provider/unified/registry.go` 已新增只读 `DriverRegistry`。
- I2 已落地：新增 `internal/archtest/dependency_direction_wave3_test.go`，补充两条方向规则：
  - `module/turn` 禁止 import `internal/provider/`
  - `provider/unified` 禁止 import concrete provider
- I3 已落地：
  - `internal/sidecar/orch/orchestration/events.go` 已移除 turn 级事件发布。
  - `internal/sidecar/orch/orchestration/runner_actor.go` 已不再发布 `TurnStarted`。
  - `internal/sidecar/orch/orchestration/service.go` 已不再发布 `TurnCompleted`。
  - 当前规则明确为：orchestration 仅发 agent 级事件；provider translator 独占 turn/tool 级事件；未来 `module/turn` 仅消费。

## 9. 自审（10 维度）

### 1. 编译+守卫

- `go build ./...`：通过。
- `go vet ./...`：通过。
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过。

### 2. B1-B4 逐条复核

- B1：通过。`Provider` 字段已同时存在于 start/resume 请求。
- B2：通过。`internal` 范围 `ReviewStart` 搜索结果为 0，review 已明确延期到 P5 RPC 层。
- B3：通过。`TurnHandle` 已具备 `LocalID/ProviderID`，两个 driver session 已实现；`TurnResult` 已存在。
- B4：通过。`InputItem` 已统一上提到 `dto/shared`；`SkillRef.Prompt` 已被两个 session 消费。

### 3. I1-I3 逐条复核

- I1：通过。`claudecli`/`codexapp` 均已改为 `group:"drivers"` 输出，`unified` 已收集。
- I2：通过。新增 wave3 archtest 文件，规则已纳入测试并通过。
- I3：通过。`internal/sidecar/orch/orchestration` 下 `TurnStarted{` / `TurnCompleted{` 搜索结果为 0；`internal/provider/*/event_map.go` 仍保留 turn 级 typed event 发布。

### 4. import 全面扫描

- `internal/dto/provider` 中搜索 `internal/dto/turn`：0 结果。
- `internal/provider/unified` 中搜索 `internal/provider/claudecli`：0 结果。
- `internal/provider/unified` 中搜索 `internal/provider/codexapp`：0 结果。

### 5. 行数

- 修改文件全部 `<= 400` 行；最大文件为 `internal/provider/claudecli/session.go`，`wc -l` 结果为 393。
- 其余关键文件：
  - `internal/provider/codexapp/session.go`：337
  - `internal/sidecar/orch/orchestration/service.go`：308
  - `internal/provider/unified/registry.go`：53

### 6. 接口完整性

- `contract.TurnHandle` 新方法已由 `claudecli` 与 `codexapp` 的 `turnHandle` 完整实现。
- `dto/provider/TurnRequest.LocalID` 已为 future `module/turn -> provider session` 的本地 turn ID 透传留出入口。

### 7. CapabilityError

- `internal/dto/provider/capability.go` 的 `NewCapabilityError` 保持不变。
- `internal/provider/claudecli/session.go` 仍对 `ListThreads` / `ForkThread` 返回 `dto.NewCapabilityError(...)`，行为未回退。

### 8. DynamicTools 零残留

- `internal` 范围搜索 `DynamicTools`：0 结果。
- `internal` 范围搜索 `DynamicTool`：0 结果。
- 仍保留 `ToolCallResponder` 泛化契约与 tool 事件 DTO；未发现 V2 `DynamicTools` 命名残留重新进入波次 3 主链。

### 9. kelindar/event 契约

- `internal/provider/claudecli/module.go` 与 `internal/provider/codexapp/module.go` 仍通过 `fx.Invoke(RegisterTranslators)` 注册 translator。
- `internal/provider/claudecli/session.go` 与 `internal/provider/codexapp/session.go` 仍调用 `EventDispatcher.Dispatch(raw)`。
- `internal/provider/unified/event_map.go` 的 `Dispatch` 仍将 typed event 通过 `event.Publish` 发送到总线，translator 链路保持通。

### 10. 波次 3 可开工判定

- 结论：可开工。
- 判定依据：
  - 4 个 Blocker 已全部在契约/装配/事件边界层消除。
  - `provider/unified` 现在已有 typed provider 输入 + fx registry 装配前提。
  - turn 本地 ID / provider ID 相关性契约已补齐。
  - `InputItem` 公共类型已统一，skill prompt 已被 session 消费。
- 仍需注意的非 blocker 事项：
  - `module/turn` 目录尚未创建，新增 archtest 规则当前会在目录缺失时跳过。
  - review 已明确后移到 P5 RPC 层，不属于 wave3 首版范围。
