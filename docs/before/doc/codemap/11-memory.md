# 11A Memory 代码地图

> 拆卷说明：本卷只覆盖 `internal/module/memory/` 主链；prompt / thread / provider 启停细节拆到 `11-prompt-thread.md`。
> 当前口径：以 2026-04-20 HEAD 为准；用于收口 `session-summary.md` 所述 **P18.3 / P18.4 / P19 B-1 / B-1 Follow-up** 的 memory 真值。
> 边界提醒：prompt 卷自有的 skill catalog / start-date hook 继续记在 `11-prompt-thread.md`；本卷只写它们与 memory 的接口边界。

## 1. 这卷回答什么

- durable memory **如何写入磁盘、更新 `MEMORY.md`、并触发 prompt 失效**。
- runtime **如何检索 memory**：`MEMORY.md` fallback、manifest + ranking + prefetch、`search past context` transcript fallback。
- **Claude parity 机制** 在 memory 侧接到哪里：`ClaudeMdSourceProvider` 如何把 auto/team memory 注入 prompt。
- 子包拆分后，**root memory 包还负责什么**，哪些能力已经下沉到 `agent / nested / retrieval / team / shared`。
- 模型如何通过 **host-direct `memory_read` / `memory_write` 工具**在 turn 中主动读写 durable memory，以及该路径与 onTurnEnd 隐式写侧的关系。

## 2. 当前源码结论

1. **memory 已不是单体包**：真实子包已有 `agent / nested / retrieval / shared / team` 五个；root 包当前主要保留装配、写侧、规则组装和 compat bridge。
2. **start 注入的是规则，不是 topic body**：`MemoryRulesProvider.Resolve()` 产出 `## memory` 动态 section（`internal/module/memory/rules_provider.go:40-62`），告诉模型怎么用 memory；不是把所有 memory 文件直接拼进去。
3. **turn 注入才带 runtime 数据**：`MemoryContextProvider.PrepareTurnContext()` 负责 relevant attachments + transcript fallback（`internal/module/memory/rules_provider.go:191-216`）。
4. **相关记忆检索仍是 runtime retrieval，不是 Claude 原生 memory tool**：P18.4 C-4 已在 ADR-001 定案为架构性偏离；当前实现是 `ManifestBuilder + RelevantMemoryFinder + PrefetchManager`。
5. **TeamSyncService 已接上线，不是空架子**：线程 started/stopped 事件通过 `StartSessionFromThreadEvent/StopSessionFromThreadEvent` 驱动 team sync；remote pull 后会主动 invalidate prompt cache（`internal/module/memory/module.go:449-460`，`internal/module/memory/team/thread_metadata.go:63-84`，`internal/module/memory/team/team_sync_pull.go:96-108`）。
6. **root retrieval compat bridge 还在，但 root constructor 已基本退役**：`memory.NewRelevantMemoryFinder()` 只是兼容壳；生产检索真正走 `memory/retrieval` 子包，root 版本的 xref 只剩测试 caller。
7. **模型可通过 host-direct `memory_read` / `memory_write` 工具主动读写 memory**：该路径由 `toolbridge.Handler.routeToolCall()` 顶层 switch 拦截，走 `routeHostOnlyToolCall()` → `callHostTool()`，绝不 fallback 到 peer MCP；依赖的 `contract.AgentMemoryReader` / `AgentMemoryWriter` 由 memory 包的 `provideAgentMemoryReader` / `provideAgentMemoryWriter` 提供（`internal/platform/toolbridge/memory_read_tool.go`、`memory_write_tool.go`，`internal/module/memory/module.go:413-419`）。

**改错补充**：
- root 装配不是抽象概念：`module.go:218-245` 明确逐个挂载 `memagent` / `nestedpkg` / `retrievalpkg` / `teampkg`，而不是继续按旧单体包理解。
- auto-dream 订阅已进 root lifecycle：`registerMemoryHooks()` 的 `OnStart` 会调 `registerLifecycleSubscriptions()`，后者再串 `registerAutoDreamSubscriptions()`（`internal/module/memory/module.go:369-400`）。触发后的 dispatcher / provider / 子进程 / failover 真实现拆到 [`12-dream-pipeline.md`](12-dream-pipeline.md)，extractFn 是两卷的边界点。
- root compat bridge 当前收敛为 `domain_bridges.go + retrieval_bridge.go`；`domain_bridges.go:5` 已明确说明它把 earlier agent/team bridge 合并到单文件，本卷不再按旧桥接文件树记载。

## 3. P18.3 / P18.4 / P19 B-1 收口口径

| 历史阶段 | 当前在代码里的落点 | 本卷结论 |
|---|---|---|
| P18.3（standard / combined / kairos / team / nested 落地） | `rules.go`、`rules_provider.go`、`kairos.go`、`nested/claudemd_*`、`team/*` | 已进入真实 provider / runtime 链，不再是文档占位 |
| P18.4（Claude parity gap closure） | `nested/claudemd_candidates.go:50-63`、`nested/claudemd_sources.go:123-145`、`prompt/dynamic.go:54-58`、`thread/start_session_helpers.go:82-103`、provider adapters | memory 相关 parity 已靠 **ClaudeMd sources + provider-neutral prompt snapshot** 收口 |
| P19 B-1（子包拆分） | `internal/module/memory/{agent,nested,retrieval,shared,team}` + root bridges | 首波拆分已完成；root 只保留 orchestration / storage / compat |
| P19 B-1 Follow-up | `domain_bridges.go`、`retrieval_bridge.go`、`contract.TurnContextProvider` / `contract.DynamicSectionRegistrar` 接口化 | follow-up 的实质是 **桥接收口 + 依赖回切到 contract**，不是继续把所有文件都拆成新子包 |

> caveat：若对照 ADR / P19 旧 plan 仍看到 “kairos / extract 第三波” 说法，以本卷当前代码口径为准；旧 plan 在这里仅保留历史阶段语义，不再代表 2026-04-20 HEAD 的真实文件落点。

## 4. 真实包图与职责切面

### 4.1 实际子包

| 路径 | 真实包名 | 主要职责 | 核心锚点 |
|---|---|---|---|
| `internal/module/memory` | `memory` | fx 装配、写侧、规则组装、compat bridge | `module.go:218-245` |
| `internal/module/memory/agent` | `agent` | agent-memory path / prompt provider / scope sanitize | `agent/module.go:5-10`, `agent/agent.go:101-147` |
| `internal/module/memory/nested` | `nested` | ClaudeMd 多层来源、nested runtime、filter/cache | `nested/module.go:8-16`, `nested/claudemd_sources.go:111-158` |
| `internal/module/memory/retrieval` | `retrieval` | manifest / ranking / hydrate / prefetch | `retrieval/module.go:5-9`, `retrieval/finder.go:25-56`, `retrieval/prefetch.go:51-66` |
| `internal/module/memory/team` | `team` | team root / guard / sync / watcher / lifecycle helper | `team/module.go:9-16`, `team/team_sync.go:75-189`, `team/thread_metadata.go:63-84` |
| `internal/module/memory/shared` | `memshared` | path-safe / type aliases / canonical helpers | `shared/types.go:1-109`, `shared/pathsafe.go:17-46` |

### 4.2 root 包内部仍保留的 3 类角色

| 角色 | 代表文件 | 为什么还在 root |
|---|---|---|
| storage / write orchestration | `service.go`、`explicit_intent_helpers.go`、`store.go`、`index.go`、`extract_runtime.go` | 还要统一处理显式 remember/forget、extract、index refresh、invalidator |
| assembler / gate | `rules.go`、`rules_provider.go`、`gate.go`、`config.go` | 这是 memory 对 prompt / turn 暴露的公共 contract 面 |
| compat bridge | `domain_bridges.go`、`retrieval_bridge.go`、`types.go` | 负责把老 root API 过渡到新子包，避免一次性切爆 import 面 |

## 5. Memory 主链

### 5.1 fx 装配入口

- `memory.Module` 在 root 包装配全部 memory 能力：`fx.Provide(...) + fx.Options(memagent.Module, nestedpkg.Module, retrievalpkg.Module, teampkg.Module)`，再统一 `fx.Invoke(registerTeamMemoryRuntime, registerLifecycle, registerPromptProviders, registerMemoryHooks)`（`internal/module/memory/module.go:218-245`）。
- `registerLifecycle()` 只负责 root dir 初始化（`module.go:345-354`）。
- `registerTeamMemoryRuntime()` 在 app 生命周期内切 `teamMemoryRuntimeReady`（`module.go:356-367`）；因此 combined/team prompt 不会在 runtime 未就绪时误报可用。
- `registerMemoryHooks()` 的 `OnStart` 会统一注册 thread hook / background extraction / context provider / auto-dream 四类订阅；`registerAutoDreamSubscriptions()` 已在 root wiring 主链内（`module.go:369-400`）。
- `provideAgentMemoryReader(hooks)` / `provideAgentMemoryWriter(hooks)` 把 `MemoryLifecycleHooks` 暴露为 `contract.AgentMemoryReader` / `contract.AgentMemoryWriter`（`module.go:345-346,413-419`）；这两个 contract 是 host-direct memory tool 的依赖注入点（见 §5.7）。
- `registerPromptProviders()` 把 memory 侧 provider 注入 prompt service：
  - `MemoryRulesProvider`
  - `AgentMemoryPromptProvider`
  - `MemoryContextProvider`
  - `ClaudeMdSourcesProvider`
  （`internal/module/memory/rules_provider.go:465-487`）

### 5.2 写侧：turn-completed → durable memory

#### A. 显式 remember / forget / Kairos 写入

1. `MemoryLifecycleHooks.onTurnEnd()` 在 turn completed 上运行（`internal/module/memory/service.go:201-209`）。
2. 显式输入先走 `handleTrackedTurnIntent()`；识别逻辑在 `DetectSaveIntent()` / `DetectForgetIntent()`，Kairos 额外走 `DetectKairosWriteIntent()`（`service.go:218-340`，`kairos.go:100-122`）。
3. `intentDiskStores()` 决定 private/team 双写路由：
   - team 不可用时只写 private；
   - `project/reference` 默认优先 team；
   - 其余优先 private。
   （`internal/module/memory/explicit_intent_helpers.go:9-25,92-99`）
4. `upsertStructuredMemory()` / `deleteMemoryAcrossStores()` 真正执行 create/update/delete（`explicit_intent_helpers.go:42-90`）。
5. `diskStore.write()` / `Delete()` 结束后统一 `updateIndexAfterMutation()`；若 `SkipIndex=false`，就刷新 `MEMORY.md`（`internal/module/memory/store.go:120-165,474-481`）。
6. `UpdateMemoryIndex()` 重新扫描 topic files，再生成 pointer-only `MEMORY.md`（`internal/module/memory/index.go:118-168`）。

#### B. extract / consolidation 写入

- `buildManifest()` 读当前 memory manifest（`internal/module/memory/extract_runtime.go:328-337`）。
- `saveExtractedMemories()` 对抽取结果做 create-or-update（`extract_runtime.go:339-356`）。
- 写完后 `invalidateMemorySections()` 统一失效 `memory` 与 `memory_context` 两个 section（`extract_runtime.go:358-367`）。

**结论**：写侧的闭环不是“只落盘”；必须同时完成 **文件写入 → `MEMORY.md` 刷新 → prompt invalidation**，否则 prompt 层仍会读到旧缓存。

### 5.3 start 注入：memory rules / mode 选择

- `MemoryRulesProvider.Resolve()` 只在 start 阶段生效，turn 阶段直接返回 nil；child-agent start 也会跳过 root memory rules（`internal/module/memory/rules_provider.go:40-49`）。
- `promptMode()` 根据 gate 选择：
  - `standard`
  - `combined`（需要 team runtime ready + team path）
  - `kairos`
  （`rules_provider.go:75-125`）
- `LoadMemoryPrompt()` 已是实分派，而不是空枚举：
  - standard → `BuildMemoryLines()`
  - combined → `buildCombinedMemoryPrompt()`
  - kairos → `BuildDailyLogPrompt()`
  （`internal/module/memory/rules.go:185-239`）
- `combined` 规则明确 private/team 两级目录、两套 `MEMORY.md` index 和 scope 选择规则（`rules.go:265-369`）。
- `kairos` 规则明确 append-only daily log、`MEMORY.md` 只读 orientation、后续 consolidation 回写 topic files（`internal/module/memory/kairos.go:20-79`）。

### 5.4 turn 检索：fallback index → relevant prefetch → transcript fallback

#### A. `memory_context` provider

- `MemoryContextProvider.Resolve()` 只在 turn 阶段工作；当 `InjectMemoryIndex=true` 时，会把当前 `MEMORY.md` 作为 fallback 文本注入（`internal/module/memory/rules_provider.go:165-180,427-443`）。
- `PrepareTurnContext()` 产出 `Attachments` + `Inputs`（`rules_provider.go:191-216`）：
  - attachments 来自 relevant memory prefetch；
  - inputs 来自 `search past context` transcript snippets。

#### B. relevant retrieval 真正实现

- `ManifestBuilder.BuildManifest()` 扫 header，限制最大候选数（`internal/module/memory/retrieval/manifest.go:18-27`）。
- `RelevantMemoryFinder.FindRelevantMemoriesWithAlreadySurfaced()` 负责 ranking → hydrate → budget/select（`internal/module/memory/retrieval/finder.go:38-56`）。
- `PrefetchManager.StartRelevantMemoryPrefetch()` 启动异步预取；`ConsumeIfReady()` 在下一轮读取 ready 结果（`internal/module/memory/retrieval/prefetch.go:86-129,176-225`）。
- `MemoryContextProvider.startRelevantPrefetch()` 维护每 thread 的 manager/handle（`internal/module/memory/rules_provider.go:333-366`）。
- gate 上 relevant prefetch 当前只在 `SkipIndex=true` 时打开（`internal/module/memory/gate.go:115-120`）。
- 真实 caller 链是 `turn/service_memory.go:33 syntheticMemoryContext()` → `MemoryContextProvider.PrepareTurnContext()` → `startRelevantPrefetch()` → `PrefetchManager.StartRelevantMemoryPrefetch()` → `retrieval.NewRelevantMemoryFinder()`（`rules_provider.go:191-216,333-366`；`retrieval/prefetch.go:51-66,86-114,220-225`）；这条链证明三者都不是孤儿。

#### C. search past context

- V3 不是 Claude 的“模型自主搜 log”；而是 runtime 先检索 durable memory，**只有 memory miss/低置信时** 才从 transcript 历史里截 snippets（`internal/module/memory/rules.go:154-161`，`rules_provider.go:240-258`）。
- 这个策略就是 ADR-001 的结论：保留 runtime retrieval，不向模型暴露独立 `memory_search` tool。

### 5.5 Claude parity / provider parity 接入点

#### A. ClaudeMd parity：把 memory 伪装成 Claude 层级来源

- `nested.NewClaudeMdSourcesProvider()` 是 memory 侧 parity 入口（`internal/module/memory/nested/module.go:8-16`，`nested/claudemd_sources.go:111-121`）。
- `resolveClaudeMdCandidates()` 不只枚举 managed/user/project `.md`；还会把 auto memory 与 team memory 的 `MEMORY.md` 作为 `automem` / `teammem` candidate 追加进去（`internal/module/memory/nested/claudemd_candidates.go:50-63`）。
- `ResolveClaudeMdSources()` 会按 gate 过滤、缓存，并在必要时跳过 injected memory files / project-local claude md（`nested/claudemd_sources.go:123-145,173-230`）。
- `prompt.BuildBaseUserContext()` 把这些 sources 渲染成统一 `claudeMd` payload；`teammem` 会额外包上 `<team-memory-content source="shared">` 标记（`internal/module/prompt/user_context_builder.go:75-81,192-214`）。
- 这条 parity 注册链是：`nested/claudemd_candidates.go:61-62` 追加 `automem` / `teammem` candidate，`registerPromptProviders()` 在 `rules_provider.go:483` 调 `RegisterClaudeMdSourceProvider(...)`，`prompt/service.go:240-246` 挂到 prompt service，再由 `user_context_builder.go:159-163` 在组装 user context 时读取。

#### B. provider-neutral prompt snapshot：Claude/Codex 两端共吃一份 memory prompt

- prompt 动态 section 顺序里，`memory` 是 start-only `order=120`，`memory_context` 是 turn `order=125`（`internal/module/prompt/dynamic.go:54-58`）。
- `prompt.AssembleStart()` / `AssembleTurn()` 会把 memory sections、ClaudeMd user context、runtime extras 一起组装成 provider-neutral assembly（`internal/module/prompt/assembler.go:28-57,91-117`）。
- `thread.resolveStartPromptAssembly()` 再把 assembly 转成 provider DTO，并把 snapshot 一起带下去（`internal/module/thread/start_session_helpers.go:82-103`）。
- Claude 侧：`internal/provider/claudecli/config.go:52-68` 优先吃 `StartAssembly.BaseInstructions`，再 fallback snapshot。
- Codex 侧：`internal/provider/codexapp/driver.go:225-237` 用同样的 `StartAssembly / Snapshot` 取 base+developer instructions。
- 具体到字段：`claudecli/config.go:52-67` 的 `resolveStartAssembly()` 会先取 `StartAssembly.BaseInstructions` / `StartAssembly.DeveloperInstructions`，再 normalize 回 snapshot。
- `codexapp/driver.go:225-237` 的 `startAssemblyInstructions()` 则按 `StartAssembly.{BaseInstructions,DeveloperInstructions}` → `Snapshot.*` → legacy config 的顺序消费 provider 输入。

**结论**：memory parity 不是在 provider 里各写一份特化逻辑，而是通过 **prompt-neutral assembly + shared snapshot**，让 Claude/Codex 两边看到同一份 memory 注入结果。

#### C. host-direct memory tool 统一路由

- 模型发起的 `memory_read` / `memory_write` tool call 在两个 provider 下走同一条 `routeHostOnlyToolCall` 路径：
  - **Codex**：`codexapp.ServerManager.SetToolHandler(h.HandleToolCall)` → `Handler.routeToolCall()` switch → `routeHostOnlyToolCall()` → `callHostTool()`（`internal/platform/toolbridge/module.go:277`，`handler.go:83-89,107-112`）。
  - **Claude CLI**：通过 proxy HTTP endpoint 进入同一个 `Handler.routeToolCall()` → `routeHostOnlyToolCall()`。
- 该路径绝不 fallback 到 peer MCP 工具（`cmd/mcp-orch` 不再注册 memory_read/memory_write，测试强制保证）。

### 5.6 Team / Agent memory 的当前位置

- `agent` 子包已是真实 provider，不再只是空目录：`PromptProvider.Resolve()` 用 agent type + scope 读取对应 `MEMORY.md`，只在 child-agent start 时挂入（`internal/module/memory/agent/agent.go:109-147,206-220`）。
- `team` 子包负责 shared root、guard、sync、watcher：
  - runtime readiness：`team/team_manager.go:47-55,94-107`
  - thread event adapter：`team/thread_metadata.go:63-84`
  - remote pull invalidate：`team/team_sync_pull.go:96-108`
- `combined` 模式是否可见，不只看 feature flag；还取决于 team runtime 是否 ready、path 是否存在、Kairos 是否激活。

### 5.7 Host-direct memory tool 路径（模型主动读写）

> 与 5.2 写侧（`onTurnEnd` 触发）不同，本节描述模型在 turn 中主动调用 `memory_read` / `memory_write` 工具的链路。

#### A. 工具定义与注册

| 工具 | 实现 | 依赖 contract |
|---|---|---|
| `memory_read` | `MemoryReadHostToolRegistry`（`internal/platform/toolbridge/memory_read_tool.go`） | `contract.AgentMemoryReader` |
| `memory_write` | `MemoryWriteHostToolRegistry`（`internal/platform/toolbridge/memory_write_tool.go`） | `contract.AgentMemoryWriter` |

两者由 `CompositeHostToolRegistry` 组合后注入 `Handler.hostTools`；`ListHostTools()` 通过 gate（`Enabled + ToolsEnabled`）决定是否向模型暴露工具 schema。

#### B. 调用链路

1. 模型返回 tool_use → provider 层解码→ `Handler.routeToolCall()`。
2. `routeToolCall()` 顶层 switch 拦截 `ToolNameMemoryRead` / `ToolNameMemoryWrite` → `routeHostOnlyToolCall()`（`handler.go:83-89`）。
3. `routeHostOnlyToolCall()` 确认 `hostTools.HasTool()` 后调 `callHostTool()` → `MemoryReadHostToolRegistry.CallHostTool()` / `MemoryWriteHostToolRegistry.CallHostTool()`。
4. 最终分别调用 `contract.AgentMemoryReader.ReadAgentMemory()` / `contract.AgentMemoryWriter.WriteAgentMemory()`，实体即 `MemoryLifecycleHooks`。

#### C. 与 onTurnEnd 写侧的关系

- `memory_write` 是模型在 turn 中的显式写入入口，绕过 `onTurnEnd` 的隐式 intent 检测。
- 写完后同样触发 `MEMORY.md` 刷新 + prompt invalidation，与 5.2 写侧闭环一致。
- `memory_read` 是纯只读操作，不触发任何写侧副作用。

#### D. fx 装配来源

- `provideAgentMemoryReader` / `provideAgentMemoryWriter` 在 `memory.Module` 内提供 contract（§5.1）。
- `toolbridge.Module` 消费这两个 contract 构造 `MemoryReadHostToolRegistry` / `MemoryWriteHostToolRegistry`，组合为 `CompositeHostToolRegistry` 注入 `Handler`。

## 6. 依赖树（真实子包 + root 角色）

> 说明：右侧是**真实子包**；左侧是 root `memory` 包内部仍保留的文件簇角色，不是新子包名。

```mermaid
flowchart LR
  subgraph root[internal/module/memory root package]
    module[module.go<br/>fx assembly]
    assembler[rules.go + rules_provider.go + gate.go<br/>prompt/turn injection]
    storage[service.go + explicit_intent_helpers.go<br/>store.go + index.go + extract_runtime.go<br/>write / index / invalidate]
    bridge[domain_bridges.go + retrieval_bridge.go + types.go<br/>compat bridge / aliases]
  end

  agent[agent/]
  nested[nested/]
  retrieval[retrieval/]
  team[team/]
  shared[shared/<br/>(package memshared)]

  module --> assembler
  module --> storage
  module --> bridge
  module --> agent
  module --> nested
  module --> retrieval
  module --> team

  assembler --> team
  assembler --> nested
  assembler --> retrieval
  storage --> team
  storage --> retrieval
  storage --> shared
  bridge --> agent
  bridge --> retrieval
  bridge --> team

  agent --> shared
  nested --> shared
  retrieval --> shared
  team --> shared
```

## 7. 符号状态：接线了还是仍是孤儿

| 符号 | 当前状态 | 证据 | 结论 |
|---|---|---|---|
| `retrieval.NewRelevantMemoryFinder` | **已接线** | xref 指向 `retrieval/prefetch.go:52,224`；`MemoryContextProvider` 通过 `NewPrefetchManager()` 间接消费它 | relevant retrieval 主实现已在生产链上 |
| `(*PrefetchManager).StartRelevantMemoryPrefetch` | **已接线** | xref 指向 `rules_provider.go:362`；生产 turn 链由 `MemoryContextProvider.startRelevantPrefetch()` 直接调用 | async relevant prefetch 已进入生产 turn 主链 |
| `(*MemoryContextProvider).PrepareTurnContext` | **已接线** | xref 指向 `turn/service_memory.go:33` 与 `rules_provider.go:188`；turn service 的 `syntheticMemoryContext()` 直接消费它 | `memory_context` 不是测试壳，而是 turn 真实注入面 |
| `memory.NewRelevantMemoryFinder`（root compat） | **仍近似孤儿** | xref 仅见 `parser_test.go:139`；生产代码只剩 bridge 自身 | root compat constructor 可以继续留桥，但不应再被新代码使用 |
| `TeamSyncService.StartSession/StopSession` | **已接线** | `module.go:451,456 -> team/thread_metadata.go:63,79 -> team/team_sync.go:105,156` | Team sync 生产链真实存在，且 start/stop 都由线程事件驱动 |
| `NewTeamSyncService` 构造器 | **fx 提供 + 测试** | xref 主要是 `team/module.go:13` 与测试；生产启用靠事件驱动其方法，而非手写 caller | 这是正常的 DI 构造器，不算空架子 |
| `registerTeamMemoryRuntime` | **已接线** | xref 指向 `module.go:244` | team runtime ready gate 已进入 app lifecycle |

## 8. 新增 memory 子能力 how-to

1. **先决定落点**：
   - 只改提示规则 → 放 `rules.go` / `rules_provider.go`；
   - 要在 turn 带 runtime 数据 → 实现 `contract.TurnContextProvider` 或 `contract.DynamicSectionProvider`；
   - 要模拟 Claude 分层来源 → 实现 / 扩展 `contract.ClaudeMdSourceProvider`；
   - 要改真实落盘 → 进 `service.go` / `store.go` / `extract_runtime.go`。
2. **补 gate / config**：在 `config.go` 增 feature flag，在 `gate.go` 把 gate 收口到 `MemoryGateSnapshot`。
3. **决定放 root 还是子包**：
   - 通用 path/type/helper → `shared/`；
   - ranking/prefetch → `retrieval/`；
   - shared team sync/guard → `team/`；
   - ClaudeMd / nested runtime → `nested/`；
   - child-agent memory → `agent/`；
   - 只有 root orchestration 才需要的 glue，再留 root。
4. **在 `module.go` 注册**：
   - provider 能力走 `fx.Provide(...)` + `registerPromptProviders()`；
   - 事件驱动能力走 `registerMemoryHooks()` / `registerTeamSyncSubscriptions()`；
   - 生命周期 gate 走 `fx.Lifecycle`。
5. **任何会改变 memory 可见面的写入都要失效缓存**：
   - root 写侧走 `InvalidateSections(contract.InvalidateMemoryWrite, DynamicSectionMemory, DynamicSectionMemoryContext)`；
   - team sync 走 `PromptAssemblyService.Invalidate(ctx, contract.InvalidateMemoryWrite)`。
6. **验收至少做 3 件事**：
   - grep：生产代码没有重新引入 `memory -> prompt` / `turn -> memory concrete` 直耦；
   - xref：新 helper 至少有一条生产 caller，不是只在 `_test.go`；
   - prompt/provider：确认 Claude/Codex 都消费同一份 `StartAssembly/Snapshot`。

### 8.1 memory → prompt 快速挂接

| 场景 | 步骤 | 锚点 | 验证 |
|---|---|---|---|
| memory 规则 / agent / team / context 进入 prompt | 1) 落 `MemoryRulesProvider` 或相关 provider；2) 经 `registerPromptProviders()` 注册到 prompt；3) durable write 仍留在 `hooks.onTurnEnd()` / 写侧，不把落盘塞进 prompt provider | `internal/module/memory/rules_provider.go:465-488` | `rules_test.go` / `hooks_test.go` |

## 9. 测试入口 + archtest freeze 映射

| 包 | 测试入口 | 核心 Test* | freeze |
|---|---|---|---|
| `memory` | `rules_test.go` / +21（root 共 22 个 `_test.go`） | `TestMemoryRuleEngineRulesForKnownTypes` | `27`（`internal/archtest/freeze_registry.go:21-26`） |
| `memory/agent` | `agent_test.go` / +1（共 2 个 `_test.go`） | `TestAgentMemoryManagerGetAgentMemoryDirScopeIsolation` | — |
| `memory/nested` | `claudemd_sources_test.go` / +4（共 5 个 `_test.go`） | `TestResolveClaudeMdSourcesOrdersLayersAndPreservesRuleMetadata` | — |
| `memory/retrieval` | `prefetch_test.go` / +4（共 5 个 `_test.go`） | `TestPrefetchManagerConsumeIfReady` | — |
| `memory/shared` | `pathsafe_test.go`（共 1 个 `_test.go`） | `TestValidateMemoryRootRejectsRelativePath` | — |
| `memory/team` | `team_path_test.go` / +7（共 8 个 `_test.go`） | `TestTeamSanitizePathKeyRejectsTraversalAttacks` | — |

## 10. 关键锚点索引

- 装配：`internal/module/memory/module.go:218-245`
- root lifecycle + auto-dream：`internal/module/memory/module.go:369-400`
- compat bridge 收口：`internal/module/memory/domain_bridges.go:1-7`、`internal/module/memory/retrieval_bridge.go:44-46`
- 写侧：`internal/module/memory/service.go:201-340`
- 双 store 路由：`internal/module/memory/explicit_intent_helpers.go:9-99`
- disk/index：`internal/module/memory/store.go:120-165,474-481`；`internal/module/memory/index.go:118-168`
- start 规则注入：`internal/module/memory/rules_provider.go:40-62`
- turn 检索注入：`internal/module/memory/rules_provider.go:191-216,333-443`
- turn caller 链：`internal/module/turn/service_memory.go:11-34`、`internal/module/memory/retrieval/prefetch.go:51-66,86-114,220-225`
- retrieval 子包：`internal/module/memory/retrieval/{manifest.go,finder.go,prefetch.go}`
- Claude parity：`internal/module/memory/nested/{claudemd_candidates.go,claudemd_sources.go}`
- Claude parity 注册链：`internal/module/memory/rules_provider.go:465-488`、`internal/module/prompt/service.go:240-246`、`internal/module/prompt/user_context_builder.go:159-163`
- Team sync 事件链：`internal/module/memory/module.go:449-460`、`internal/module/memory/team/thread_metadata.go:63-84`、`internal/module/memory/team/team_sync.go:105-173`
- provider-neutral 对齐：`internal/module/prompt/{dynamic.go,assembler.go,user_context_builder.go}`、`internal/module/thread/start_session_helpers.go`、`internal/provider/{claudecli/config.go,codexapp/driver.go}`
- provider 消费字段：`internal/provider/claudecli/config.go:52-67`、`internal/provider/codexapp/driver.go:225-237`
- host-direct memory tool：`internal/platform/toolbridge/memory_read_tool.go`、`internal/platform/toolbridge/memory_write_tool.go`
- memory contract 提供：`internal/module/memory/module.go:345-346,413-419`（`provideAgentMemoryReader` / `provideAgentMemoryWriter`）
- tool 路由：`internal/platform/toolbridge/handler.go:83-89,107-112`（`routeToolCall` switch + `routeHostOnlyToolCall`）
- freeze：`internal/archtest/freeze_registry.go:21-26`
