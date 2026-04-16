# 11 Memory / Prompt / Thread 一体化代码地图

> 定位：本卷是第 07 卷（module 职责切面）与第 10 卷（store / sql 持久化）之间的跨模块语义链路图；重点解释 memory / prompt / thread / provider bridge 在 `start / resume / fork` 中如何串联，不重复第 02 卷的 sidecar/tool 暴露、第 07 卷的 UI 消费面和第 10 卷的落库细节。

## 1. 模块概述

### 1.1 这卷覆盖什么
这卷不是第 02 / 07 / 10 的替代卷，而是把四条原本分散在不同目录里的链路放到同一张图里讲清楚：

- **memory 模块**：负责 durable memory 的磁盘布局、索引、读写、检索、抽取、规则生成，以及 turn 完成后的显式记忆落盘。
- **prompt / system prompt 模块**：负责把静态规则、动态 section、环境信息、memory 规则、MCP 信息等组装成 provider-neutral 的 `StartAssembly / TurnAssembly`。
- **thread 模块**：负责 `start / resume / fork / recover` 生命周期，把 prompt assembly 变成 provider 会话启动参数，并把 thread / binding 持久化回 store。
- **prompt snapshot / provider bridge**：负责在 `thread/start` 与 `thread/resume` 之间，把 prompt 组装结果转成 `dto.StartSessionRequest / dto.ResumeSessionRequest`，再分别落到 `codexapp` 和 `claudecli` 的具体启动实现上。

### 1.2 一句话关系图

```text
memory
  ├─ DiskStore / MEMORY.md index / retrieval / extractor
  ├─ MemoryRulesProvider ───────→ prompt assembler(dynamic section: memory)
  └─ MemoryLifecycleHooks ─────→ turn-completed 事件 → durable memory 落盘

prompt
  ├─ static sections(identity / constraints / engineering / ...)
  ├─ dynamic sections(session_guidance / memory / env_info_simple / language / mcp_instructions)
  ├─ AssembleStart / AssembleTurn
  └─ PromptAssemblySnapshot

thread
  ├─ Start: prompt assemble → launch agent → StartSession → persist thread/binding
  ├─ Resume: resolve thread/binding → forward PromptSnapshot → ResumeSession
  └─ Fork: provider-side fork → launch child agent → resume child thread

provider bridge
  ├─ codexapp: thread/start + thread/resume RPC 参数化
  └─ claudecli: CLI manifest + --system-prompt 物化
```

### 1.3 当前源码的 6 个结论
1. **start 路径已经真正串起来了**：`thread.Service.Start` 会在内部调用 prompt assembly，再把结果送进 provider `StartSession`。
2. **memory 进入 system prompt 的主路径不是“读具体 memory 内容”，而是“注入 memory 使用规则”**：`memory.MemoryRulesProvider` 把 memory taxonomy / save rules / access rules 作为动态 section 注入 `AssembleStart`。
3. **resume 路径的 prompt snapshot 目前是“调用方传入、thread 透传”模式**：`ResumeRequest.PromptSnapshot` 会被送到 `starter.ResumeSession`，但 thread service 本身不会从 `threadstore` 自动装载 snapshot。
4. **fork 路径不会补发 prompt snapshot**：它依赖 provider 已经 fork 出来的远端 thread 保留上下文，本地只复制 display name / model / cwd 等 thread 元数据。
5. **持久化 snapshot 与运行时 snapshot 是两套 schema**：`threadstore.PromptSnapshot` 与 `contract.PromptAssemblySnapshot` 字段并不一致，当前源码里没有生产级桥接器把它们互转。
6. **prompt cache invalidation 能力已具备，但 thread 生命周期没有接入该能力**：`InvalidateClear / Compact / Worktree / ResumeRestore / ProviderSwitch` 目前在生产调用链里基本没有落点，主要停留在 prompt 模块内部与测试中。

### 1.4 边界划分

- **memory 负责“长期知识与规则”**，不负责 thread 生命周期。
- **prompt 负责“组装系统提示词”**，不负责启动 agent 进程。
- **thread 负责“编排生命周期和持久化 thread identity”**，不负责 memory 文件格式。
- **provider 驱动负责“最后一跳物化”**：Codex 走 RPC 参数，Claude 走 CLI `--system-prompt`。
- `dashboard/prompts` / dashboard `memory` 页面仍归第 07 卷：它们分别查询 prompt template store 与 sharedfile store，不等于本卷的 system prompt assembly / durable memory 主链。
- `uistate` 的 `config/lspPromptHint/*` 也归第 07 卷：它只是 shared file 默认值 + scoped preference override，不参与 prompt registry / `StartAssembly`。
- **`type`、`scope`、`mode` 是三条正交维度**：
  - `type=user/feedback/project/reference` 表示 memory 内容语义分类。
  - `scope=user/project/local` 表示可见性 / 存储边界；它是 ACL 维度，不是第五种 memory type。
  - `mode=standard/kairos/team` 只影响 memory 规则提示词的组织方式；P18.3 J/K 已把 `standard` / `kairos` / `team` 接进真实 dynamic section 产物，`LoadMemoryPrompt()` 会分发 Standard / KAIROS daily-log / Team Combined prompt。
- **disk/index 与 contract/tool 是两层模型**：
  - `internal/module/memory` 维护 rich disk schema（frontmatter、`aliases/search_keys/lang`、topic file、`MEMORY.md` index）与写入链路。
  - `internal/contract/memory.go` + `cmd/mcp-orch/memory` 只暴露 flattened、只读的 `Read` contract，不暴露 write/delete/rule injection。
- **prompt template store ≠ runtime system prompt assembly**：
  - 第 10 卷里的 `internal/store/prompt` / `prompt_templates` 是“模板资产存储”。
  - 本卷里的 `internal/module/prompt` 是“运行时 system prompt 装配器”。
  - 两者名字相近，但一个回答“模板怎么存”，另一个回答“本次 thread/start 时 system prompt 怎么拼”。

### 1.5 与 02 / 07 / 10 的交叉引用

- **第 02 卷 `02-mcp-orch.md`** 解决的是“system 外围入口、runtime 与工具暴露”的问题：
  - 它已经说明 `cmd/mcp-orch/memory/` 是 memory 的只读能力与 root/path 授权层；
  - 也已经说明 `remoteLauncher` 会通过主控 RPC 调 `thread/start`。
  - 本卷是在此基础上继续往内追：`thread/start` 进入 thread 模块后，如何调用 prompt assembly，再如何通过 provider bridge 把 prompt/snapshot 物化到 `codexapp` 与 `claudecli`。
- **第 07 卷 `07-module.md`** 解决的是“业务模块职责切面”的问题：
  - 它把 `thread` 定义为 lifecycle / binding / history / config 中心；
  - 把 `uistate`、`dashboard/prompts`、`dashboard memory` 等 UI / 页面语义留在第 07 卷。
  - 它还负责说明当前 `thread/resume` 的 RPC surface 只到 `threadId/path/cwd/model/provider` 这一层；service 侧额外存在的 `Effort / PromptSnapshot / ConfigOverride` 与 provider resume 语义差异，则继续由本卷展开。
  - 本卷则只保留 thread 的整合语义：`memory -> prompt -> thread -> provider`，不覆盖 UI 投影。
- **第 10 卷 `10-store.md`** 解决的是“持久化 contract 与 sqlc/store 语义”的问题：
  - 它已经说明 `thread store` 里有 `SavePromptSnapshot / LoadPromptSnapshot`，且当前 xref 主要还停留在测试；
  - 也已经明确本卷才负责解释 prompt snapshot 在运行态上的语义。
  - 因而第 10 卷回答“有没有落库、落了什么”，本卷回答“这些 snapshot 在 start/resume/fork 里是否真的被生产链路消费”。
- **推荐阅读顺序**：
  1. 先看第 07 卷明确 memory / prompt / thread 的模块边界；
  2. 再看本卷理解三者在 start/resume/fork 中如何串联；
  3. 然后回看第 10 卷确认哪些数据真正持久化；
  4. 若关注 sidecar / remote launcher / tool 暴露，再补第 02 卷。
- **反查入口建议**：
  - 从 `memory_read`、registry、bootstrap capability、`cmd/mcp-orch/memory/*` 追起时，先读第 02 卷，再回本卷看它如何接到 memory / prompt / thread 主链。
  - 从 `thread/start`、`thread/resume`、dashboard `prompts/memory`、`config/lspPromptHint/*` 这些模块入口追起时，先读第 07 卷，再回本卷看 system prompt assembly / durable memory 的内部语义。
  - 从 `prompt_snapshot` 列、`SavePromptSnapshot/LoadPromptSnapshot`、sqlc/schema 追起时，先读第 10 卷，再回本卷判断这些 snapshot 是否真的被生产恢复链路消费。

---

## 2. 目录结构

### 2.1 `internal/module/memory/`

```text
internal/module/memory/
├── agent.go            # AgentMemoryManager：按 scope 读取 agent 专属 MEMORY.md prompt
├── config.go           # memory 开关、root、feature flags、skipIndex 等配置
├── index.go            # MEMORY.md 索引读写、entry frontmatter 解析、全盘扫描
├── manifest.go         # 仅扫描 header 的 manifest builder
├── module.go           # fx.Module；注册 lifecycle / prompt provider / event hooks
├── path.go             # root/path 校验、auto-mem 路径推导、原子写文件
├── retrieval.go        # RelevantMemoryFinder：按 query 做 ranking + budget 选取
├── root.go             # MemoryExtractor：把 transcript 蒸馏为 durable memory 候选
├── rules.go            # memory taxonomy / access/save/trust rules 的系统提示词模板
├── rules_provider.go   # 把 rules 作为 prompt dynamic section 注入 prompt assembler
├── service.go          # root 准备、显式 save intent 检测、turn-completed hook
├── store.go            # DiskStore：Create/Read/Update/Delete/RebuildIndex
└── types.go            # MemoryType/Scope/Entry/ExtractedMemory 等领域类型
```

### 2.2 `cmd/mcp-orch/memory/` 与工具暴露

```text
cmd/mcp-orch/memory/
├── config.go           # tool/read 侧配置
├── entry_file.go       # 读 memory 文件的安全包装
├── index.go            # tool/read 侧 index 读取
├── path.go             # scope root / path sanitize / 授权校验
├── service.go          # contract.MemoryService 实现：Read(ctx, req)
└── store.go            # tool/read 侧扫描与 DTO 映射

cmd/mcp-orch/tools/memory_tools.go
└── memory_read         # 把 contract.MemoryService 暴露为 MCP 工具
```

补充：`cmd/mcp-orch/memory/path.go` 当前把 scope root 固定解析为三种目录形状：
- `user` → `<baseRoot>/user/memory`
- `project` → `<baseRoot>/projects/<git-root-slug>/memory`
- `local` → `<baseRoot>/local/<machine>/projects/<git-root-slug>/memory`
- 空 scope 会被规范化为 `project`；若 project/git root 不可用，则 project 返回 `deny`，local 返回 `local_unavailable`

### 2.3 `internal/module/prompt/`

```text
internal/module/prompt/
├── assembler.go        # AssembleStart / AssembleTurn / snapshot / cache invalidation
├── buildctx.go         # BuildCtx / MCPSnapshot 类型别名
├── cache.go            # sectionCache：generation + per-section cache
├── config.go           # ENABLE_PROMPT_* 配置
├── dynamic.go          # dynamic section 声明、注册、session guidance provider
├── env_provider.go     # 环境信息动态 section
├── language_provider.go# 语言偏好动态 section
├── mcp_provider.go     # MCP server instructions 动态 section
├── module.go           # fx.Module
├── registry.go         # SectionRegistry：注册/排序 section
├── section.go          # static system prompt sections 文本
├── service.go          # NewService；注册 static + dynamic providers
└── types.go            # PromptSection / SectionContext / snapshot / invalidate reason
```

### 2.4 `internal/module/thread/` + `internal/store/thread/`

```text
internal/module/thread/
├── contract.go              # Service 接口、Start/Resume/Fork 请求响应 DTO
├── factory.go               # threadState / offline config / binding chain 辅助构造
├── history.go               # ReadHistory / ReadMessages / ReadRuntimeConfig / Compact
├── lifecycle.go             # Start / Resume / persistThreadState
├── lifecycle_fork.go        # Fork / Recover
├── module.go                # fx.Module，注入 prompt assembly(optional)
├── rpc.go                   # thread/start|resume|fork|... RPC handlers
├── rpc_types.go             # start/resume 等 RPC 参数兼容层
├── service.go               # 线程查询、删除、binding/session 解析、事件发布
├── service_constructor.go   # NewService / NewServiceWithPromptAssembly
├── session_generation.go    # generation 绑定
├── start_session.go         # startSession / resumeSession / resume state hydration
└── start_session_helpers.go # prompt assembly → provider DTO 转换

internal/store/thread/
├── contract.go              # thread store 接口 + PromptSnapshot 持久化 schema
├── module.go                # store.fx.Module
└── store.go                 # sqlc-backed store 实现
```

### 2.5 provider / contract bridge

```text
internal/contract/
├── memory.go   # MemoryService / MemoryReadResult
├── prompt.go   # StartInput / TurnInput / PromptAssemblyService
└── provider.go # Driver / Session 接口

internal/dto/provider/session.go
└── StartSessionRequest / ResumeSessionRequest / StartAssembly / PromptAssemblySnapshot

internal/dto/provider/turn.go
└── TurnRequest / SteerRequest / TurnOverrides（引用 `session.go` 里的 `TurnAssembly`）

internal/provider/codexapp/
├── driver.go        # StartSession / ResumeSession / buildThreadResumeParams
├── support.go       # buildThreadStartParams / dynamic tools 启动
└── session_turn.go  # TurnAssembly.UserContextText → 额外 text input

internal/provider/claudecli/
├── config.go           # resolveStartAssembly / normalizePromptSnapshot
├── driver.go           # StartSession / ResumeSession / prepareSessionStart
├── transport_config.go # composeLaunchSystemPrompt / prompt snapshot helper
├── session.go          # runtimePromptSnapshotLocked / RuntimeConfigSnapshot
└── session_turn.go     # TurnAssembly.UserContextText → prepend 到 turn 文本
```

---

## 3. 核心类型 / 接口

### 3.1 Memory 侧核心类型

| 类型 / 接口 | 文件 | 作用 |
|---|---|---|
| `Config` | `internal/module/memory/config.go` | 控制 `Enabled`、`EnableTools`、`RootDir`、`SkipIndex`、`ExtraGuidelines`、feature flags。 |
| `Service` | `internal/module/memory/service.go` | 仅暴露 `Config()`、`RootDir()`、`EnsureRoot()`，是 memory root 生命周期抽象。 |
| `MemoryLifecycleHooks` | `internal/module/memory/service.go` | 订阅 `thread.Started` / `turn.Completed`，在回合完成后自动识别显式记忆并落盘。 |
| `DiskStore` | `internal/module/memory/store.go` | durable memory 的 CRUD 与索引更新器。 |
| `MemoryRuleEngine` | `internal/module/memory/rules.go` | 生成“如何保存/访问/信任 memory”的 system prompt 文本。 |
| `MemoryRulesProvider` | `internal/module/memory/rules_provider.go` | 实现 `prompt.DynamicSectionProvider`，把 memory rules 注入 prompt。 |
| `RelevantMemoryFinder` | `internal/module/memory/retrieval.go` | 基于 query 对 manifest entry 做 ranking + budget 裁剪。 |
| `MemoryExtractor` | `internal/module/memory/root.go` | 通过 LLM extractor 把 transcript 提炼为 durable memory 候选。 |
| `ManifestBuilder` | `internal/module/memory/manifest.go` | 只扫描 frontmatter/header，不加载全文。 |
| `AgentMemoryManager` | `internal/module/memory/agent.go` | 生成 agent-scope MEMORY prompt，包含截断与 scope 文案。 |
| `contract.MemoryService` | `internal/contract/memory.go` + `cmd/mcp-orch/memory/service.go` | tool/read 侧只读查询接口，供 `memory_read` 工具使用。 |

#### 补充：`type` / `scope` / `mode` 的语义边界

- `MemoryType` 在 `internal/module/memory/types.go` 与 `internal/contract/memory.go` 都定义了同样的枚举；前者服务于 rich disk model，后者服务于 tool/read DTO。
- `MemoryScope` 在 contract 与 mcp-orch 读侧是 runtime ACL 输入；在 `internal/module/memory` 当前生产路径里，scope 主要被 `AgentMemoryManager` 用来分 agent memory 目录，`DiskStore` 主写链路本身并不按 `MemoryScope` 分支。
- `MemoryMode` 仍位于 `internal/module/memory/rules.go` 的规则引擎层，但 P18.3 J/K 已通过 `MemoryRulesProvider.Resolve()` + `LoadMemoryPrompt()` 把 `standard / kairos / team` 接到真实生产链路。
- `MemoryModeKairos` / `MemoryModeTeam` 已在 P18.3 J/K 落地 —— `LoadMemoryPrompt()` 真实分发 KAIROS daily-log / Team Combined prompt，生产调用链已打通（参见 `kairos.go`、`team_manager.go`、`team_sync.go`、`auto_dream_task.go`）。

### 3.2 Prompt / system prompt 侧核心类型

| 类型 / 接口 | 文件 | 作用 |
|---|---|---|
| `PromptRegistry` / `Service` | `internal/module/prompt/service.go` | section 注册器 + `contract.PromptAssemblyService` 的统一实现。 |
| `PromptSection` | `internal/module/prompt/types.go` | 一个 section 的元信息：`Name / Order / Region / Volatile / StartOnly / Compute`。 |
| `SectionContext` | `internal/module/prompt/types.go` | 统一承载 `BuildCtx + StartInput + TurnInput`。 |
| `DynamicSectionProvider` | `internal/module/prompt/dynamic.go` | `session_guidance / memory / env_info_simple / language / mcp_instructions` 这 5 个动态 slot 的统一接口。 |
| `SectionRegistry` | `internal/module/prompt/registry.go` | 负责注册去重与按 `Order + Name` 排序。 |
| `sectionCache` | `internal/module/prompt/cache.go` | generation-based cache；与 `singleflight` 配合减少并发重复计算。 |
| `StartInput` / `TurnInput` | `internal/contract/prompt.go` | 两者共享 `BuildCtx` 维度字段，但只有 `StartInput` 含 `BaseInstructions / DeveloperInstructions / Name / Prompt`；`TurnInput` 改为 `UserText / SkillPrompt / Attachments / CurrentDate`，**没有 developer 通道**。 |
| `StartAssembly` / `TurnAssembly` | `internal/dto/provider/session.go` | prompt assembler 的 provider-neutral 输出；start 产出 `DisplayName/Base/Developer/ResolvedSections/Snapshot`，turn 只产出 `UserContextText/ResolvedSections`。 |
| `ResolvedPromptSection` | `internal/dto/provider/session.go` | 结构化 section 观测载荷：保留 `Name / Region / Volatile / Content`；DTO 会携带它，但 provider adapter 当前不按 section list 逐段消费。 |
| `PromptAssemblySnapshot` | `internal/dto/provider/session.go` | 运行时 prompt snapshot：`DisplayName / Base / Developer / Provider / Version / Hash / Generation`。 |

### 3.3 Thread / provider bridge 侧核心类型

| 类型 / 接口 | 文件 | 作用 |
|---|---|---|
| `thread.Service` | `internal/module/thread/contract.go` | thread 生命周期总接口：`Start/Resume/Fork/Recover/...`。 |
| `StartRequest` | `internal/module/thread/contract.go` | 启动参数，包含 legacy `Prompt`、新 `Name`、`BaseInstructions`、`PromptAssemblyRef` 等；其中 `PromptAssemblyRef` 是 service/DI 侧能力，不是当前 public RPC 输入字段。 |
| `ResumeRequest` | `internal/module/thread/contract.go` | 恢复参数，核心增量是 `PromptSnapshot` 与 `ConfigOverride`；它的能力面比当前 `thread/resume` RPC 更大。 |
| `SessionStarter` | `internal/module/thread/lifecycle.go` | provider-neutral 的会话启动抽象。 |
| `threadState` | `internal/module/thread/lifecycle.go` + `factory.go` | thread/binding 持久化与事件发布的中心状态。 |
| `threadstore.Store` | `internal/store/thread/contract.go` | thread 持久化接口；除 thread CRUD 外，还暴露 `SavePromptSnapshot / LoadPromptSnapshot`。 |
| `contract.Driver` | `internal/contract/provider.go` | provider factory contract：`StartSession / ResumeSession`。 |
| `contract.Session` | `internal/contract/provider.go` | provider-neutral 会话抽象：`ForkThread / ReadHistory / Configure / Close ...` |
| `dto.StartSessionRequest / ResumeSessionRequest` | `internal/dto/provider/session.go` | thread → provider 的 start/resume 桥接 DTO。 |
| `dto.TurnRequest / SteerRequest` | `internal/dto/provider/turn.go` | turn → provider 的回合桥接 DTO；schema 已预留 `TurnAssembly`，所以 turn 侧缺口在“默认不填充”，不是“线协议缺字段”。 |

### 3.4 两种 snapshot schema 的差异

这是这卷最容易误判、也是最值得单独拎出来的地方。

#### A. 运行时 prompt snapshot：`contract/dto.PromptAssemblySnapshot`
来源：`internal/contract/prompt.go`、`internal/dto/provider/session.go`

- `DisplayName`
- `BaseInstructions`
- `DeveloperInstructions`
- `Provider`
- `Version`
- `Hash`
- `Generation`

用途：
- thread `ResumeRequest.PromptSnapshot`
- provider `ResumeSessionRequest.PromptSnapshot`
- codex/claude driver 在 runtime config / system prompt 里恢复 base/dev instructions

#### B. store 层 prompt snapshot：`threadstore.PromptSnapshot`
来源：`internal/store/thread/contract.go`

- `BaseInstructions`
- `DeveloperInstructions`
- `SectionSnapshot map[string]string`
- `Generation`

用途：
- 只在 `internal/store/thread/store.go` 与对应测试里读写

#### B.1 字段对照表：为什么它们不能直接互换

| 维度 | runtime snapshot：`PromptAssemblySnapshot` | store snapshot：`threadstore.PromptSnapshot` | 当前生产用途 |
|---|---|---|---|
| display name | `DisplayName` | 无 | 只在 runtime snapshot 里参与 start/resume 恢复与 provider runtime config |
| base/dev instructions | 有 | 有 | 两边都持有，但 schema 名称与宿主对象不同 |
| provider | `Provider` | 无 | 仅 runtime snapshot 记录 provider 视角 |
| hash/version | `Hash` / `Version` | 无 | 仅 runtime snapshot 用于 prompt 代次与一致性观测 |
| section-level data | `ResolvedSections` 不进 snapshot；snapshot 本体不带 section map | `SectionSnapshot map[string]string` | 仅 store snapshot 保存 section 级扁平快照 |
| generation 类型 | `uint64` | `int64` | 即使都叫 generation，也不是同一 schema 的直接镜像 |

#### C. 当前源码结论
- 两个 snapshot **不是同一个 DTO**。
- 运行时 snapshot 才是 thread/service → provider 的恢复载体；store snapshot 目前仍停留在 store API 与测试侧使用。
- 当前源码里**没有生产级 mapping** 把 store snapshot 转成 runtime snapshot。
- `lookupResumeState` / `hydrateResumeSessionRequest` 不会调用 `threadstore.LoadPromptSnapshot` 来补 runtime snapshot。
- 因此 thread `Resume()` 现在不会自动从 `threadstore` 恢复 prompt snapshot，只会透传调用方显式传入的 `ResumeRequest.PromptSnapshot`。
- 语义上可以概括为：**Start 负责生成 runtime prompt snapshot；Resume 只会复用已有 snapshot，不会在本地重建它。**

### 3.5 两种 generation 的区别

#### A. prompt assembly generation：`PromptAssemblySnapshot.Generation`
来源：`internal/module/prompt/assembler.go:newSnapshot`

- 值来自 `s.cache.Generation()`。
- 它代表的是 prompt section cache / invalidate epoch。
- `PromptAssemblyService.Invalidate()` bump generation 后，后续 `AssembleStart` 会产生新的 runtime snapshot generation。
- 这个 generation 描述的是“prompt 内容所在的缓存代次”，不是 provider session 或 agent 进程的代次。

#### B. session generation：`bindSessionGeneration`
来源：`internal/module/thread/session_generation.go`

- `SessionGeneration(agentID)` 从运行时 session provider 中读取当前 session 实例代次。
- `orchestration.BindSessionGeneration(...)` 用它把 agent 与当前 session 实例绑定起来。
- `Start / Resume / Fork` 在 session ready 后都会调用这条链路。
- 这个 generation 描述的是“运行时 session 实例代次”，与 prompt hash / snapshot 内容无关。

#### C. 阅读源码时不要混淆

- 两者都叫 generation，但一个属于 prompt/cache 体系，一个属于 session/orchestration 体系。
- `Start` 同时会产生新的 prompt snapshot generation，并在 provider session ready 后绑定 session generation。
- `Resume / Fork / Recover` 只会绑定 session generation；本地不会重新 assemble prompt，也不会新生成 runtime prompt snapshot。

---

## 4. 关键函数 / 方法

### 4.1 Memory：durable memory 主链路

#### `(*service).EnsureRoot` — `internal/module/memory/service.go`
- 调 `resolvedStoreRoot` 解析 root。
- 处理 `MULTI_AGENT_MEMORY_DIR`、project root、auto-mem path override。
- 最终 `os.MkdirAll(root, 0o755)`，确保 durable memory 根目录存在。

#### `(*MemoryLifecycleHooks).onTurnEnd` — `internal/module/memory/service.go`
- 订阅 `turn.Completed`。
- 仅在 `evt.Success=true` 时工作。
- 对 `evt.Message / evt.Result / evt.Summary` 里的 **assistant 确认文本**做 `DetectSaveIntent`；当前识别的是“`I'll remember` / `saved to memory` / `记住了` / `已保存到记忆`”这一类确认语句，而不是任意用户请求。
- 成功后进入 `writeIntent`，最终落盘 durable memory。

#### `(*DiskStore).write` — `internal/module/memory/store.go`
- 统一承载 `Create / Update` 的核心逻辑。
- `normalizeWritableEntry` 校验 name/description/content/type。
- `withDiskStoreLock(root, ...)` 保障同一 memory root 的串行写。
- `WriteMemoryFile` 写 markdown entry。
- `updateIndexAfterMutation` 同步更新 `MEMORY.md`。

#### `UpdateMemoryIndex` / `scanMemoryEntries` — `internal/module/memory/index.go`
- 全盘扫描 `root/**/*.md`（排除 `MEMORY.md`）。
- 解析 frontmatter，构建 `MemoryIndexEntry{Title, Path, Hook}`。
- 最终重写 `MEMORY.md`，把 topic file 变成扁平入口索引。

#### `(*ManifestBuilder).BuildManifest` — `internal/module/memory/manifest.go`
- 只扫描 header，不读取正文内容。
- 输出 header-only 的 `[]MemoryEntry`，供 retrieval/ranking 使用。
- 默认上限 `defaultManifestFileLimit=200`。

#### `(*RelevantMemoryFinder).FindRelevantMemories` — `internal/module/memory/retrieval.go`
- `rankEntries(query, manifest)` 先做基于名字/别名/description/path 的轻量 ranking。
- `hydrateEntries` 只对 top candidates 读正文，避免全量 IO。
- `SelectRelevantMemories` 再按 budget / 去重 / 最大条数裁剪。

#### `(*MemoryExtractor).Extract` — `internal/module/memory/root.go`
- 把 transcript 包装成严格 JSON 输出格式的 extractor prompt。
- 允许 envelope/list/single 三种 JSON 返回结构。
- `normalizeExtractedMemories` 做去重、type 规范化、tags 截断。

#### `(*MemoryRuleEngine).BuildMemoryLines` / `LoadMemoryPrompt` — `internal/module/memory/rules.go`
- 生成 memory taxonomy、save rules、access rules、trust rules、plan 与 memory 的边界、search past context 说明。
- `LoadMemoryPrompt` 会根据 `autoEnabled` 与 mode 决定是否返回动态 section 内容。

#### `(*MemoryRulesProvider).Resolve` — `internal/module/memory/rules_provider.go`
- 实现 prompt dynamic section `memory`。
- 仅在 `input.Start != nil` 且不是 turn 组装时工作。
- 输出内容统一包装为：
  - `## memory`
  - 后跟由 `MemoryRuleEngine` 生成的多级标题文本。

#### `(*AgentMemoryManager).LoadAgentMemoryPrompt` — `internal/module/memory/agent.go`
- 按 `MemoryScopeUser / Project / Local` 选择 agent memory 目录：
  - `user` → `<validatedRoot>/agents/<agentType>/MEMORY.md`
  - `project` → `<projectRoot>/memory/agents/<agentType>/MEMORY.md`
  - `local` → `<projectRoot>/memory/local/<agentType>/MEMORY.md`
- 它读取的是 agent 自己目录下的 `MEMORY.md` entrypoint，而不是普通 durable memory topic file。
- 普通 durable memory 写链路走的是 `resolvedStoreRoot()` / `GetAutoMemPath()` 解析出的 root，并在该 root 下按 `MemoryType` 写 `user|feedback|project|reference` 子目录；这与 agent memory 的 scope 目录是两套布局。
- 限制 200 行 / 25,000 UTF-16 code units，超限时自动截断并追加 warning。
- 当前生产源码里它还没有直接接入 thread start 链路，但已形成独立 prompt 生成面。

#### `cmd/mcp-orch/memory.(*service).Read` — `cmd/mcp-orch/memory/service.go`
- `prepareRead`：参数清洗，要求 `name/path` 至少提供一个。
- `prepareRoot`：根据 scope 解析 authorized root，并检查 `Enabled && EnableTools`。
- `loadIndex` 先尝试读取 `MEMORY.md`，失败时降级成 `rebuilt_view`；它主要提供 `IndexHit / Degraded / Source` 这类元信息。
- `lookupEntry` 并不是“只靠 index 定位”：`path` 模式会直接校验后读取文件，`name` 模式会调用 `scanEntries/findEntryByCanonical` 扫描 topic files。
- 返回 `contract.MemoryReadResult`，包含 `IndexHit / DenyReason / Degraded / Source` 等元信息。

#### disk/index 与 contract/tool 的只读边界
- `internal/module/memory/index.go` 会把 topic file 写成 rich frontmatter（`name/description/type/lang/aliases/search_keys`）并维护 `MEMORY.md` pointer index。
- `cmd/mcp-orch/memory/entry_file.go` 读取同一套磁盘约定，但向外只折叠成 `contract.MemoryEntry{Name, Description, Type, Content, SourcePath, UpdatedAt}`。
- 因而 `memory_read` 读到的是“可授权的扁平结果”，不是内部 `MemoryEntry{Frontmatter, CanonicalName, FilePath, ...}` 全量对象。
- 这也是为什么 `internal/contract/memory.go` 只定义 `Read(ctx, req)`，而没有 `Write/Delete/RebuildIndex`。

#### 显式 save vs delete 的当前状态
- 生产写入主链路已经接通：`turn.Completed` → `DetectSaveIntent` → `buildExplicitMemoryEntry` → `DiskStore.Create/Update` → `UpdateMemoryIndex`。
- 删除原语已经存在，但分两层：
  - `DeleteMemory(root, name)` 只负责删除 topic file。
  - `(*DiskStore).Delete(name)` 在删除成功后再调用 `updateIndexAfterMutation()` 重建 `MEMORY.md`。
- 当前源码里还没有与 save 对应的 `DetectForgetIntent` / `MemoryLifecycleHooks` delete 分支，也没有 `memory_delete` 之类的 tool/contract 入口。
- 因此规则文本里提到的 explicit `forget` 目前仍停留在“规则先行、入口未接”的状态；实际生产链路只有 explicit save 被接通。

### 4.2 Prompt：system prompt 组装链路

#### `NewService` — `internal/module/prompt/service.go`
启动时会做两件事：

1. `registerBuiltInSections()` 注册所有静态 section 与动态 slot section。
2. 自动注册 4 个内建 dynamic provider：
   - `SessionGuidanceProvider`
   - `EnvInfoProvider`
   - `LanguageProvider`
   - `MCPInstructionsProvider`

memory 模块自己的 `MemoryRulesProvider` 并不是这里硬编码注册，而是通过 `memory.Module -> registerPromptProvider` 在 fx 生命周期里额外接入。

#### `(*service).AssembleStart` — `internal/module/prompt/assembler.go`
- 取 `startSections = staticSections + dynamicSections`。
- `resolveSections()` 逐段求值。
- 成功时：
  - `BaseInstructions = renderResolvedSections(resolved) + 原始 BaseInstructions tail`
  - `DeveloperInstructions = strings.TrimSpace(in.DeveloperInstructions)`；**不参与 static/dynamic section system**，也不会新增 `ResolvedSections`
  - `DisplayName = Name > Prompt`
  - `Snapshot = newSnapshot(...)`
- `ResolvedSections` 记录的是已求值 section 的结构化观测结果；它会通过 `dto.StartAssembly / dto.TurnAssembly` 被携带出去，但当前 provider adapter 真正消费的是 `BaseInstructions / DeveloperInstructions / UserContextText / PromptSnapshot`，而不是逐段读取 `ResolvedSections`。
- 失败时：
  - 记录 warning
  - 回退到 `fallbackStartAssembly`，保证 thread/start 不因 prompt provider 失败而完全不可用。

#### `(*service).AssembleTurn` — `internal/module/prompt/assembler.go`
- 只解析 dynamic sections。
- 输出 `TurnAssembly{UserContextText, ResolvedSections}`。
- 不会给 dynamic section 人工再套一层 section heading；`UserContextText` 直接拼接 resolved content。
- **当前接线状态**：provider adapter 两侧都已经支持消费 `TurnAssembly.UserContextText`（Codex 会把它 prepend 成额外 text input，Claude 会把它 prepend 到 turn 文本前），但 `internal/module/turn/service.go:55-75` 的 `PrepareTurn` 当前不会主动调用 `PromptAssemblyService.AssembleTurn`，也不会默认填充 `dto.TurnRequest.TurnAssembly`。因此 `TurnAssembly` 目前属于“能力已就绪、默认主链路未自动接线”。

#### `(*service).computeSection` — `internal/module/prompt/assembler.go`
- `Volatile=false`：走 `singleflight + sectionCache`。
- `Volatile=true`：每次重新求值，但仍把结果写进当前 generation cache。
- cache key 由 `generation + section.Name` 组成。

#### `(*service).Invalidate` — `internal/module/prompt/assembler.go`
- 本质是 `cache.InvalidateAll(reason)`。
- generation 递增，老缓存全部作废。
- 当前主要在 prompt 测试里被显式调用；生产 thread 生命周期暂未接线。

#### `(*SectionRegistry).Sections` — `internal/module/prompt/registry.go`
- 把 section 按 `Order` 排序。
- 同序号时按 `Name` 稳定排序。
- 这保证最终 system prompt 的 section 顺序可预测。

#### `StaticSections` — `internal/module/prompt/section.go`
静态 system prompt 由 7 段固定文本组成，准确顺序与属性如下：

| order | section | region | startOnly | volatile |
|---|---|---|---|---|
| 10 | `identity` | `static` | `false` | `false` |
| 20 | `system_constraints` | `static` | `false` | `false` |
| 30 | `engineering` | `static` | `false` | `false` |
| 40 | `actions` | `static` | `false` | `false` |
| 50 | `tool_preferences` | `static` | `false` | `false` |
| 60 | `style` | `static` | `false` | `false` |
| 70 | `output_efficiency` | `static` | `false` | `false` |

这些内容就是 prompt 体系里的“system prompt 基底”。

#### dynamic slots 与 provider 分工
`dynamicSectionSpecs` 当前固定了 5 个 slot，语义边界如下：

| order | slot | region | 属性 | provider / 接入点 | 作用 |
|---|---|---|---|---|---|
| 110 | `session_guidance` | `dynamic` | `startOnly=false` `volatile=false` | `SessionGuidanceProvider`（prompt 内建） | 基于 `EnabledTools + SessionFlags` 注入会话级行为指导。 |
| 120 | `memory` | `dynamic` | `startOnly=true` `volatile=false` | `memory.MemoryRulesProvider`（memory.Module 外接） | 注入 memory taxonomy / save/access/trust 规则；默认只进 start，不进 turn。 |
| 130 | `env_info_simple` | `dynamic` | `startOnly=false` `volatile=false` | `EnvInfoProvider`（prompt 内建） | 注入 `CWD / GitRoot / CurrentDate / OS / Shell / LSP / worktree / provider / model` 等环境快照。 |
| 140 | `language` | `dynamic` | `startOnly=false` `volatile=false` | `LanguageProvider`（prompt 内建） | 注入“始终用某语言回复”的输出语言约束。 |
| 150 | `mcp_instructions` | `dynamic` | `startOnly=false` `volatile=true` | `MCPInstructionsProvider`（prompt 内建） | 把 MCP server/tool/instructions 快照规整成按 server 分块的说明文本。 |

这里要特别区分两层“provider”：
- 上表这些是 **dynamic section provider**，职责只是生成 section 文本。
- 真正把 `BaseInstructions / DeveloperInstructions / UserContextText / PromptSnapshot` 物化到 Codex/Claude 启动参数里的，是 `internal/provider/codexapp/*` 与 `internal/provider/claudecli/*` 这层 provider adapter。

#### `SessionGuidanceProvider.Resolve` — `internal/module/prompt/dynamic.go`
- 根据 `EnabledTools` 和 `SessionFlags` 决定是否注入：
  - `request_user_input` 的追问提示
  - `spawn_agent` 的并行委派规则
  - `verification_required` 的独立验证提醒

#### `EnvInfoProvider.Resolve` — `internal/module/prompt/env_provider.go`
- 注入当前 `CWD / GitRoot / CurrentDate / OS / Shell / LSP status / worktree / provider / model`。
- 这是 prompt 体系里“运行环境快照”的核心来源。

#### `LanguageProvider.Resolve` — `internal/module/prompt/language_provider.go`
- 当 `BuildCtx.Language` 非空时，注入“始终用某语言回复”的规范段落。

#### `MCPInstructionsProvider.Resolve` — `internal/module/prompt/mcp_provider.go`
- 把 `MCPSnapshot.Servers / Tools / Instructions` 规整成按 server 分块的指令文本。
- 该 section 标记为 `volatile`，因为 MCP 连接状态与 tool snapshot 可能频繁变化。

### 4.3 Thread：生命周期编排链路

#### `NewServiceWithPromptAssembly` — `internal/module/thread/service_constructor.go`
- `NewService` 创建的是 plain thread service，`promptAssembly=nil`。
- `NewServiceWithPromptAssembly` 则显式注入 `contract.PromptAssemblyService`。
- `thread.Module` 默认走 `NewServiceWithPromptAssembly`，所以生产 `thread/start` 是 prompt-aware 的。
- 这个接入边界也决定了：**只有 Start 路径会主动调用 prompt assembler；Resume / Fork / Recover 都不会重新 assemble prompt。**

#### `buildLaunchRequest` — `internal/module/thread/launch_request.go`
- orchestration bootstrap 只承载 `AgentID / Name / Cwd / Command / Env`。
- `Env` 只注入 `AGENT_PROVIDER` 与 `AGENT_MODEL`。
- 这里**不携带** `PromptSnapshot / StartAssembly / Fork lineage`；prompt 语义真正生效是在 agent 拉起后调用 `StartSession / ResumeSession` 的阶段。

#### `(*service).Start` — `internal/module/thread/lifecycle.go`
主顺序与源码完全一致：

1. `normalizeStartRequest`
2. 默认注入 `s.promptAssembly`
3. `resolveStartPromptAssembly`
4. `launchAgent`
5. `startSession`
6. `bindSessionGeneration`
7. `lookupSession`
8. `enrichFromSessionConfig`
9. `newThreadState(threadStateStartKind, ...)`
10. `persistThreadState`

关键语义：
- `Start` 是三条链路里**唯一会重新 assemble prompt** 的入口。
- `Prompt` 已退化成 legacy display-name fallback；真正持久化到 thread store 的是 `DisplayName/Name`。
- provider session 的 base/dev instructions 通过 `StartSessionRequest` 传给 driver；thread store 只持久化 display name、model、cwd、status 等 runtime 元信息。
- 也是唯一会重新跑 `session_guidance / memory / env_info_simple / language / mcp_instructions` 这 5 个 dynamic slots 的 thread 生命周期入口。

#### `resolveStartPromptAssembly` — `internal/module/thread/start_session_helpers.go`
- 若 `PromptAssemblyRef == nil`，退回 `buildStartAssembly`，只保留最基础的 name/base/dev。
- 否则构造 `contract.StartInput`，把 `agentID` 填进 `input.ThreadID`，再调用 `PromptAssemblyService.AssembleStart`。
- `DisplayName` 最终按 `assembly.DisplayName > req.Name > req.Prompt` 归一化，避免 legacy prompt 污染真正的 display-name 语义。
- 输出同时带 `ResolvedSections` 与 `Snapshot`，形成 typed prompt 载体；但当前 provider 主要读取 `DisplayName / Base / Developer / Snapshot`，`ResolvedSections` 更多用于观测与断言。

#### `(*service).startSession` — `internal/module/thread/start_session.go`
- 把组装结果转成 `dto.StartSessionRequest`：
  - `Instructions = assembly.BaseInstructions`
  - `StartAssembly = toProviderStartAssembly(assembly)`
  - `Config = buildStartSessionConfig(req, assembly)`
- 这一步是 prompt → provider bridge 的关键节点。
- 语义上是 **typed + legacy 双轨并存**：
  - typed 轨：`StartAssembly.DisplayName/Base/Dev/ResolvedSections/Snapshot`
  - legacy 轨：`Instructions` 和 `Config` 里的别名字段（如 `developerInstructions`、`approvalPolicy`）

#### `(*service).Resume` — `internal/module/thread/lifecycle.go`
主顺序：

1. `resolveResumeRequest`
2. 清空旧 session cache（`sessions.RemoveSession`）
3. `launchAgent`
4. `resumeSession`
5. `bindSessionGeneration`
6. `lookupSession`
7. `newThreadState(threadStateResumeKind, ...)`
8. `persistThreadState`

关键语义：
- `Resume` 不会调用 `PromptAssemblyService`，所以它不是“重新求值 prompt”，而是“恢复 metadata + 可选透传已有 snapshot”。
- `resolveResumeRequest` 会从 persisted thread + binding chain 补齐 `AgentID / Provider / ProviderThreadID / CWD / Model / Effort`。
- `PromptSnapshot` 不来自 thread store，而来自调用方显式传参。
- binding upsert 失败时会写 warning，但仍显式发 `thread.Started`，避免 UI 卡死。

#### `(*service).lookupResumeState` / `hydrateResumeSessionRequest` — `internal/module/thread/start_session.go`
- `lookupResumeState` 同时看：
  - `threadStore.GetByThreadID`
  - `resolveBinding`
  - binding chain fallback
- 会用 `SessionUUID` 修正 `ProviderThreadID`，但仅当该 UUID 看起来像真实 UUID。
- `hydrateResumeSessionRequest` 会把 `ConfigOverride.Model/Effort` 与线程离线配置合并。
- 这里恢复的是 runtime metadata；`state.Prompt` 只作为 display name / launch name 来源，不会被重新解释为 base instructions。
- 整条链路里也没有 `threadstore.LoadPromptSnapshot`，说明 resume 并不会从 store 自动合成 runtime prompt snapshot。

#### `thread/start` / `thread/resume` 的 RPC 暴露差 — `internal/module/thread/rpc_types.go` + `rpc.go`
- `startParams` 暴露 `Name`、legacy `Prompt`、`BaseInstructions`、`DeveloperInstructions`；其中：
  - `prompt` 被兼容解码成 display-name alias
  - `instructions` 被兼容解码成 base-instructions alias
- `resumeParams` 只暴露 `ThreadID / Path / CWD / Model / Provider`。
- `newResumeHandler` 也只把这些字段映射进 `ResumeRequest`；它**不会接收** `PromptSnapshot / Effort / ConfigOverride`。
- 因此 `thread.Service.Resume` 的能力面明显大于当前 `thread/resume` RPC surface：
  - service 层支持 snapshot-based resume
  - 当前公开 RPC 还不能把这项能力完整表达出来

#### `(*service).Fork` — `internal/module/thread/lifecycle_fork.go`
主顺序：

1. `resolveSession`
2. `session.ForkThread(historyTargetID(binding, threadID))`
3. `lookupThreadMeta`
4. 用 `newThreadID` 同时作为 child `agentID` 与 `publicThreadID`
5. `launchAgent`
6. `resumeSession(ResumeRequest{Provider, AgentID, ThreadID, CWD, Model})`
7. `bindSessionGeneration`
8. `persistThreadState(threadStateForkKind)`

关键点：
- child fork **没有显式传 `PromptSnapshot`**。
- `launchAgent` bootstrap 也只带 `displayName / provider / model`，不会承载 prompt assembly 结果。
- 它依赖 provider 远端 `ForkThread` 已把对话上下文 / system prompt 一起继承出去。
- 本地 thread store 只继承 `displayName / model / cwd` 等元数据。
- 因此 Fork 的语义更接近：**provider 原生上下文复制 + 本地 thread 身份重建**。

#### `(*service).Recover` — `internal/module/thread/lifecycle_fork.go`
- 先恢复 agent 进程；如果 session 不存在，再走 `resumeSession`。
- `relaunch_resume` 分支里构造的 `ResumeRequest` 只有 `Provider / AgentID / ThreadID / ProviderThreadID`，不会补传 `PromptSnapshot`。
- 然后重新 `persistThreadState`。
- 与 `Fork` 一样，这里不会重新 assemble prompt，也不会在本地补建新的 runtime prompt snapshot。
- 语义上偏“会话恢复/重新附着”，而不是重新生成 prompt。

#### `(*service).persistThreadState` — `internal/module/thread/lifecycle.go`
- `ensurePublicThreadAvailable`
- `maybeRegisterThreadBinding`
- `persistStartedThread`

它是 thread state / binding state / UI 事件的共同汇聚点。

#### `newThreadState` — `internal/module/thread/factory.go`
- 先收敛 `displayName := Name > Prompt`，然后同时写入 `state.Name` 与 `state.Prompt`。
- 这意味着 `threadState.Prompt` / `threadstore.Thread.Prompt` 在当前生命周期语义里实际上都是 display-name 槽位，而不是 base instructions 槽位。
- `start` 默认 `PublicThreadID = AgentID`
- `fork` 默认 `PublicThreadID = ProviderThreadID > AgentID`
- `resume/recover` 默认 `PublicThreadID = persisted public thread > requested thread > agentID`
- `ProviderThreadID` 保持原样，不在这里擅自补默认值

#### `ReadRuntimeConfig` — `internal/module/thread/history.go`
- 在线 session 存在时，用 session `RuntimeConfigSnapshot()` 覆盖 offline config。
- 这也是观察 prompt base/dev instructions 是否已进入 provider runtime config 的主要入口之一。
- 运行时若能看到 `baseInstructions / developerInstructions`，说明 prompt snapshot 或 start assembly 已被 provider 物化。 

### 4.4 Provider：prompt 如何被真正物化

#### Codex：`(*driver).StartSession` / `ResumeSession` — `internal/provider/codexapp/driver.go`
- `StartSession`：
  - `startAssemblyInstructions(req)` 取 `StartAssembly.BaseInstructions > StartAssembly.Snapshot.BaseInstructions > req.Instructions`
  - 把 base/dev instructions 写进 runtime config
  - `buildThreadStartParams(req)` 生成 `thread/start` RPC 参数
- `ResumeSession`：
  - `promptSnapshotInstructions(req.PromptSnapshot)` 直接从 snapshot 取 base/dev
  - `buildThreadResumeParams(req)` 把它们送进 `thread/resume`

**结论**：Codex 的 start/resume 都真的消费了 prompt assembly / prompt snapshot。

#### Claude：`resolveStartAssembly` + `prepareSessionStart` — `internal/provider/claudecli/config.go` / `driver.go`
- `resolveStartAssembly` 会把 base/dev instructions 与 snapshot 规范化。
- `prepareSessionStart` 把 `PromptSnapshot` 填进 `cliLaunchConfig`。
- `buildCLIArgs(..., --system-prompt composeLaunchSystemPrompt(...))` 最终把 base/dev/meta 合并成 CLI system prompt。

#### Claude system prompt 物化：`composeLaunchSystemPrompt` — `internal/provider/claudecli/transport_config.go`
它把三块内容拼成最终 CLI `--system-prompt`：

1. base instructions
2. developer instructions
3. 运行元信息：`approval_policy / sandbox / summary / effort / personality`

#### Claude 运行期 snapshot：`runtimePromptSnapshotLocked` — `internal/provider/claudecli/session.go`
- 优先 `transportConfig.PromptSnapshot`
- 若 transport snapshot 为空，再回退到 `config.PromptSnapshot`
- 供 `RuntimeConfigSnapshot()` 与 restart 逻辑继续使用

#### Claude resume 的一个重要现状
`internal/provider/claudecli/driver.go` 的 `ResumeSession` 当前只把 `ThreadID / publicThread / cwd / model / effort / configOverride` 送进 `startSpec`；**它没有直接读取 `req.PromptSnapshot`**。

因此：
- thread 层虽然会把 `ResumeRequest.PromptSnapshot` 透传给 `starter.ResumeSession`
- 但 Claude 驱动的当前 resume 实现并不会像 Codex 那样显式消费该 snapshot
- Claude 更依赖已有 transport/runtime state、历史 session 状态，以及 restart 时保留的 `PromptSnapshot`

这就是 prompt snapshot 在不同 provider 上最明显的行为差异。

#### provider bridge 对照表

| 维度 | Codex bridge | Claude bridge | 当前含义 |
|---|---|---|---|
| start 物化入口 | `buildThreadStartParams` → `thread/start` RPC | `resolveStartAssembly` + `composeLaunchSystemPrompt` → CLI `--system-prompt` | 两边都会消费 start assembly，但落点不同：一个是 RPC 参数，一个是 CLI prompt 字符串 |
| resume 是否显式读取 `req.PromptSnapshot` | **是**，`promptSnapshotInstructions(req.PromptSnapshot)` | **否**，`ResumeSession` 本身不直接读取该字段 | 同一份 `ResumeRequest` 在两个 provider 上恢复精度不同 |
| runtime prompt snapshot 驻留点 | runtime config + thread resume params | `transportConfig.PromptSnapshot` / `config.PromptSnapshot` | Claude 更偏“会话内部持有 snapshot”，Codex 更偏“resume 时显式重送 snapshot” |
| turn 对 `UserContextText` 的消费方式 | 变成额外 text input item | prepend 到最终 turn 文本字符串 | 两边都消费 turn 文本，但插入方式不同 |
| 对 `ResolvedSections` 的消费 | 不逐段消费 | 不逐段消费 | provider 真正吃的是 `BaseInstructions / DeveloperInstructions / PromptSnapshot / UserContextText`，而不是 section list 本身 |
| 对 restart 的依赖 | 较弱 | 较强 | Claude 的 prompt 恢复更依赖 transport/runtime snapshot 与 restart 逻辑延续 |

#### `ResolvedSections` 的当前语义
从 `internal/provider/*` 的 xref 可见，provider 驱动当前直接消费的是：

- start / resume：`BaseInstructions / DeveloperInstructions / PromptSnapshot`
- turn：`UserContextText`

而 `ResolvedSections` 当前没有作为 provider 输入被逐段解释。它更像：
- prompt 装配的结构化观测面
- DTO 已保留的 section 级 typed 载荷（`StartAssembly` / `TurnAssembly` 都能带它）
- golden / e2e 测试断言材料
- 后续 UI / debug / snapshot bridge 可扩展的 section 级元数据

---

## 5. 依赖关系

### 5.1 Memory 依赖什么

- `internal/platform/config`：解析 project root / env 开关。
- `os / filepath / exec.Command(git rev-parse)`：路径校验、auto-mem root 推导、原子写。
- `internal/module/prompt`：通过 `MemoryRulesProvider` 注入 dynamic section。
- `internal/platform/bus` + thread/turn DTO：通过事件 hook 做显式记忆落盘。
- `internal/contract/memory.go`：tool/read 侧的只读接口约束。

### 5.2 Prompt 依赖什么

- `internal/contract/prompt.go`：统一的 `StartInput / TurnInput / PromptAssemblyService / BuildCtx`。
- 各 dynamic provider：
  - memory rules
  - session guidance
  - env info
  - language
  - MCP instructions
- `singleflight` 与 `sectionCache`：控制并发与缓存代数。

### 5.3 Thread 依赖什么

- `threadstore.Store`：thread metadata 持久化。
- `bindingstore.Store`：public thread / provider thread / agent / session UUID 绑定。
- `SessionProvider`：运行时 session lookup。
- `SessionStarter`：provider-neutral 启动/恢复入口。
- `contract.PromptAssemblyService`：仅在 start 路径上使用；通过 `NewServiceWithPromptAssembly` 可选注入。
- `turn.Service`：stop/delete/archive 前做活动回合清理。
- `OrchestrationFacade`：拉起/停止 agent 进程。
- `event.Dispatcher`：发 `thread.Started / Stopped / MessagesPage / Compacted` 事件。

### 5.4 Provider bridge 依赖什么

- `dto.StartSessionRequest / ResumeSessionRequest`：thread 与 provider 的 start/resume 桥接协议。
- `dto.TurnRequest / SteerRequest`：turn 与 provider 的回合桥接协议，承载 `TurnAssembly` 的 transport surface。
- `contract.Driver / Session`：provider-neutral 抽象。
- `codexapp`：需要 transport RPC 与 dynamic tools list provider。
- `claudecli`：需要 CLI manifest、transport、history backend、system prompt 组装。

### 5.5 谁依赖它们

- `memory.Module` 反向依赖 `prompt.PromptRegistry`，把 memory section 接入 prompt。
- `thread.Module` 反向依赖 `prompt.AsPromptAssemblyService`，把 prompt assembler 接入 start path。
- `cmd/mcp-orch/tools/memory_tools.go` 依赖 `contract.MemoryService` 暴露 `memory_read`。
- provider runtime config / UI 读取链路通过 `thread.ReadRuntimeConfig` 观察 prompt/base/dev 等运行态值。

---

## 6. 数据流

### 6.1 启动链路：memory rules → prompt assembly → thread/start → provider session

1. RPC `thread/start` 进入 `internal/module/thread/rpc.go:newStartHandler`，由 `startParams` 兼容解码 `name/prompt/baseInstructions/instructions` 等字段。
2. `thread.Module` 默认经 `NewServiceWithPromptAssembly` 装配 thread service；因此 `thread.Service.Start` 在 `StartRequest.PromptAssemblyRef == nil` 时会自动注入 `s.promptAssembly`。
3. `resolveStartPromptAssembly` 构造 `contract.StartInput`，调用 `PromptAssemblyService.AssembleStart`。
4. `prompt.AssembleStart`：
   - 先拼 7 个 static sections
   - 再执行 dynamic providers：`session_guidance / memory / env_info_simple / language / mcp_instructions`
   - 生成 `StartAssembly + PromptAssemblySnapshot`
5. `launchAgent -> buildLaunchRequest` 先完成 agent 进程 bootstrap；这一层只携带 `Name/Cwd/AGENT_PROVIDER/AGENT_MODEL`，并不携带 prompt snapshot。
6. `thread.startSession` 把 assembly 转成 `dto.StartSessionRequest`：
   - `Instructions = assembly.BaseInstructions`
   - `StartAssembly = toProviderStartAssembly(assembly)`
   - `Config = buildStartSessionConfig(...)`
7. provider 分流：
   - `codexapp`：`buildThreadStartParams` → `thread/start`
   - `claudecli`：`resolveStartAssembly` → `composeLaunchSystemPrompt` → CLI `--system-prompt`
8. `thread.bindSessionGeneration` 绑定 session generation（运行时 session 代次，不是 prompt snapshot generation）。
9. `thread.persistThreadState` 持久化 thread + binding，并发布 `thread.Started`。

**结论**：Start = **重新 assemble prompt + 生成 runtime prompt snapshot + 启动全新 provider session**。

### 6.2 回合链路现状：TurnAssembly 能产出，但 turn 主路径尚未默认接线

1. `prompt.AssembleTurn` 已能产出 `TurnAssembly{UserContextText, ResolvedSections}`。
2. `internal/dto/provider/turn.go` 的 `TurnRequest` 与 `SteerRequest` 都已经显式携带 `TurnAssembly`，说明线协议本身支持 turn 侧 prompt 注入。
3. `internal/provider/codexapp/session_turn.go` 会把 `TurnAssembly.UserContextText` 变成**额外 text input item**；`internal/provider/claudecli/session_turn.go` 则把它 **prepend 到最终 turn 文本字符串** 前。
4. 但 `internal/module/turn/service.go:55-75` 的 `PrepareTurn` 当前只负责组装 `Inputs / Skills / Overrides / MCP`，不会主动调用 `PromptAssemblyService.AssembleTurn`，也不会默认填 `dto.TurnRequest.TurnAssembly`。
5. 因而当前默认生产链路里，TurnAssembly 更像“provider-neutral capability + 测试覆盖到的集成点”，而不是像 start path 那样天然总会生效的注入链路。

### 6.3 恢复链路：thread/binding store → caller snapshot → provider resume

1. 当前 public RPC `thread/resume` 进入 `newResumeHandler`，但 `resumeParams` 只暴露 `threadId/path/cwd/model/provider`。
2. service 层 `ResumeRequest` 其实更丰富，支持 `PromptSnapshot / Effort / ConfigOverride`；只是当前 RPC surface 尚未把这些字段公开出去。
3. 这意味着 service 层的 snapshot-based resume 现在主要保留在**直接调用 `thread.Service.Resume(...)`** 的场景。
   - `Fork / Recover` 虽然也会内部调用 `resumeSession`
   - 但它们当前构造的 `ResumeRequest` 并不会填 `PromptSnapshot`
4. `thread.resolveResumeRequest` / `lookupResumeState` 从 `threadStore + bindingStore + remembered thread/agent relation` 还原：
   - `AgentID`
   - `Provider`
   - `ProviderThreadID`
   - `CWD`
   - `Model / Effort`
   - `SessionUUID`
5. thread 模块不会调用 `threadstore.LoadPromptSnapshot`；`ResumeRequest.PromptSnapshot` 只有在调用方显式传入时才会保留下来。
6. `thread.resumeSession` 组装 `dto.ResumeSessionRequest{PromptSnapshot: ...}`。
7. provider 分流：
   - **Codex**：`buildThreadResumeParams` 把 snapshot 的 base/dev instructions 送进 `thread/resume`。
   - **Claude**：当前 `ResumeSession` 没有直接读取 `req.PromptSnapshot`；更偏向使用既有 transport/runtime 状态。
8. session 恢复成功后，thread 重新 `bindSessionGeneration + persistThreadState`，必要时仍会强制发 `thread.Started` 给 UI。

**结论**：Resume = **恢复 thread/binding metadata + 可选复用调用方手里的 snapshot**，而不是重新 assemble prompt。

### 6.4 fork 链路：provider 先 fork，thread 后补本地状态

1. `thread.Fork` 先拿 parent session 与 binding。
2. 调 `session.ForkThread(historyTargetID(binding, threadID))`，让 provider 复制远端 thread。
3. `lookupThreadMeta` 从 parent thread store 取出 `displayName / model / cwd`。
4. `newThreadID` 同时变成 child `agentID` 与默认 `publicThreadID`。
5. thread 模块重新 `launchAgent`，但 bootstrap 仍只带 `displayName / provider / model`，不带 prompt snapshot。
6. 再走一次 `resumeSession`，但只传 `Provider / AgentID / ThreadID / CWD / Model`。
7. 因为这次 `ResumeRequest` 没有 `PromptSnapshot`，所以 fork 后的本地 thread 不会显式做 snapshot replay。
8. child thread `bindSessionGeneration + persistThreadState(threadStateForkKind)`，`OwnerThreadID` 指回 parent。

**结论**：Fork = **provider 远端上下文复制 + 本地 thread identity / lineage 重建**；本地不会重新 assemble prompt，也不会显式补发 `PromptSnapshot`。

### 6.5 memory 写入链路：turn-completed → durable memory

1. `memory.Module` 在 fx `OnStart` 时注册事件订阅。
2. `turn.Completed` 成功事件触发 `MemoryLifecycleHooks.onTurnEnd`。
3. `DetectSaveIntent` 从 assistant 输出里识别“`I'll remember` / `saved to memory` / `记住了` / `已保存到记忆`”这类确认语句。
4. `buildExplicitMemoryEntry` 根据内容推断 `user / feedback / project / reference`。
5. `DiskStore.Create/Update` 写 topic file。
6. `UpdateMemoryIndex` 重建 `MEMORY.md`。

### 6.6 memory 读取链路：memory_read 工具

1. `cmd/mcp-orch/tools/memory_tools.go` 暴露 `memory_read`，把 `name/path/scope/type` 转成 `contract.MemoryReadRequest`。
2. `cmd/mcp-orch/memory.(*service).prepareRead` 先做参数清洗；`prepareRoot` 再执行 `ensureEnabled`、scope root resolve 与授权判定。
3. scope root 解析的真实落点是：
   - `user` → `<baseRoot>/user/memory`
   - `project` → `<baseRoot>/projects/<git-root-slug>/memory`
   - `local` → `<baseRoot>/local/<machine>/projects/<git-root-slug>/memory`
   - 当 project root / git root 不可用时，project 返回 `deny`，local 返回 `local_unavailable`
4. `loadIndex` 优先读取 `MEMORY.md`；若 index 缺失或无法读取，则降级为扫描 topic file 构造 `rebuilt_view`，并在结果里打上 `Degraded/Source`。
5. `lookupEntry` 支持两条路径：
   - `path`：仍要经过 sanitize → resolve → authorize，不能绕过 root 校验。
   - `name`：按 canonical name 扫描并去重定位。
   - 也就是说，index 主要承担命中标记和 degraded source 说明，不是唯一 lookup 数据源。
6. `type` 只是只读筛选条件；entry 命中后若 type 不匹配，返回空结果而不是触发写入/修复。
7. 最终返回 `MemoryReadResult{Entry, SourcePath, IndexHit, DenyReason, Degraded, Source}`；整个 tool/read 面仍是只读 contract，不承担 write/delete/index rebuild API。
8. 但要结合第 02 卷一起看当前 wiring 现状：source tree 已有 `cmd/mcp-orch/memory/*` 与 `memory_read` tool definition，不过 `cmd/mcp-orch/run()` 的 Fx provider 目前还没有把 `memory.NewConfig / memory.NewService` 正式接进 registry 主装配；因此“源码实现已齐、默认运行路径未必已全量接线”是阅读这条链路时必须保留的前提。

### 6.7 prompt cache / snapshot / store 的现状

1. `prompt.AssembleStart` 每次生成 `PromptAssemblySnapshot{DisplayName, Base, Dev, Provider, Version, Hash, Generation}`。
2. `prompt.Invalidate` 会 bump generation 并清空 cache。
3. `threadstore.SavePromptSnapshot / LoadPromptSnapshot` 只会持久化另一套 `PromptSnapshot{Base, Dev, SectionSnapshot, Generation}`。
4. 通过 LSP xref 可见，这套 store snapshot API 在当前生产代码里没有调用点，引用基本只出现在 store 测试里。
5. 因而当前生产链路的“resume 时 prompt 恢复”主要依赖：
   - 调用方显式传入 `ResumeRequest.PromptSnapshot`
   - provider 自身的远端 thread / runtime snapshot 保留能力
   - 而不是依赖 `internal/store/thread` 的 snapshot 表

### 6.8 agent memory 的位置

- `AgentMemoryManager.LoadAgentMemoryPrompt` 已经能根据 `user/project/local` scope 读取 agent 专属 `MEMORY.md` 并拼出 prompt。
- 这条链路和 durable memory topic file CRUD / `memory_read` tool 是并列能力面，不共享同一个 DTO：前者返回 prompt 文本，后者返回 contract read result。
- 它也不共享同一套根目录语义：agent memory 直接挂在 `agents/` 或项目内 `memory/agents|local/` 下；普通 durable memory 则挂在 `resolvedStoreRoot()` 解析出的 root 下，再按 `MemoryType` 子目录落 topic file。
- 当前 xref 显示它还没有直接接进 `thread.Start -> prompt.AssembleStart` 主链路，因此更像“已实现但未接主流程”的独立入口。

### 6.9 retrieval / prefetch / extractor 的当前接入状态

- `ManifestBuilder.BuildManifest`、`RelevantMemoryFinder.FindRelevantMemories`、`PrefetchManager.StartRelevantMemoryPrefetch`、`MemoryExtractor.Extract` 都已实现完整能力。
- 但当前 xref 显示它们的生产接线程度并不相同：
  - `ManifestBuilder` / `RelevantMemoryFinder` 已被 `PrefetchManager` 在包内组合使用。
  - `PrefetchManager` 自身的构造与启动入口目前仍主要停留在 `prefetch_test.go`，没有接进 `memory.Module`、prompt assembler、thread start/resume 或 `memory_read` tool。
  - `MemoryExtractor` 被 `memory.Module` 通过 `fx.Provide(NewMemoryExtractor)` 暴露出来，但除模块装配外，当前生产引用仍主要停留在测试；它尚未接入 turn-completed save、prompt assembly、thread lifecycle 或 mcp-orch read 路径。
- 因而这三块应视为“已实现的辅助能力面”，而不是当前 durable memory save 或 `memory_read` 的主调用链。

---

## 7. 测试与风险

### 7.1 当前测试覆盖点

#### Memory 侧
- `internal/module/memory/hooks_test.go`
  - 校验 turn-completed 后显式 memory 能落盘。
- `internal/module/memory/rules_test.go`
  - 校验 memory prompt 文本结构与 skip-index 行为。
- `internal/module/memory/retrieval_test.go`
  - 校验 ranking / budget / 去重逻辑。
- `internal/module/memory/index_test.go`、`manifest_test.go`、`store_test.go`
  - 校验 entry/frontmatter/index 的 round-trip 与磁盘行为。
- `internal/module/memory/agent_test.go`
  - 校验 agent MEMORY prompt 的 entrypoint 读取与截断/BOM 行为。

#### Prompt 侧
- `internal/module/prompt/e2e_test.go`
  - 覆盖 fx wiring：memory.Module + prompt.Module + thread.Module 的一体化链路。
- `internal/module/prompt/assembler_test.go`
  - 校验 `AssembleStart / AssembleTurn / snapshot` 行为。
- `internal/module/prompt/cache_invalidation_test.go`
  - 校验并发 invalidate 与 AssembleTurn 的安全性。
- `internal/module/prompt/golden_test.go`
  - 校验稳定输出。

#### Thread / provider bridge 侧
- `internal/module/thread/prompt_integration_test.go`
  - 校验 start path 真正使用 prompt assembly；resume 透传 snapshot；fork 保留 display name / model。
- `internal/module/thread/phase45_regression_test.go`
  - 校验 base instructions 不污染 prompt 存储、resume 透传 snapshot、name 优先于 legacy prompt。
- `internal/store/thread/snapshot_test.go`、`internal/store/thread/store_test.go`
  - 校验 store-level prompt snapshot 的 schema、并发与 nil map 归一化。
- `internal/provider/codexapp/driver_session_test.go`
  - 校验 Codex start/resume 参数真正消费 assembly / snapshot。
- `internal/provider/claudecli/transport_config_test.go`
  - 校验 Claude `composeLaunchSystemPrompt` 优先使用 prompt snapshot。
- `internal/provider/claudecli/session_restart_config_test.go`
  - 校验 Claude restart 继续使用 snapshot 的 base/dev instructions。

### 7.2 主要风险 / 待补位

#### 风险 1：snapshot schema 分裂，且 store snapshot 未进入生产恢复链路
- `threadstore.PromptSnapshot` 与 `contract.PromptAssemblySnapshot` 字段集不同。
- 生产代码没有 adapter。
- 结果是“数据库里虽然有 prompt snapshot store 能力，但 resume 主链路并不会自动用它”。

#### 风险 2：Claude 与 Codex 的 resume 语义不完全对齐
- Codex `ResumeSession` 明确消费 `req.PromptSnapshot`。
- Claude `ResumeSession` 当前没有显式读取 `req.PromptSnapshot`。
- 同一个 thread 层 `ResumeRequest`，在不同 provider 上的恢复精度不同。

#### 风险 3：fork 没有显式 snapshot 补发
- `thread.Fork` 只把 `Provider / AgentID / ThreadID / CWD / Model` 送进 child `resumeSession`。
- 如果某 provider 的 `ForkThread` 不能完整保留 system prompt，child 侧就没有 thread 层兜底。

#### 风险 4：prompt invalidate reason 已定义，但生命周期没有接线
- `InvalidateCompact / Worktree / ResumeRestore / ProviderSwitch` 都已有 reason 常量。
- 生产链路里暂时看不到 thread/turn/worktree 事件去主动调用 `PromptAssemblyService.Invalidate`。
- cache generation 能变，但缺少真实触发器。

#### 风险 5：memory 的若干能力还没有接进 start 主链路
- `RelevantMemoryFinder`、`ManifestBuilder`、`AgentMemoryManager` 都已存在。
- 但当前 start 主链路真正接进去的是 `MemoryRulesProvider`，不是“具体 memory 内容召回”。
- 这意味着“memory system 已提供规则层整合，但知识内容层整合仍不完整”。

#### 风险 6：thread store 持久化的是 display name，不是完整 system prompt
- `thread.Start`/`Resume` 持久化到 `threadStore.Upsert` 的核心字段是 `Prompt`（实际承载 display name）、`Model`、`Cwd` 等。
- 完整 base/dev instructions 并不在 thread 主表里。
- 一旦 provider 自身的 runtime/snapshot 信息不可恢复，就只能依赖外部显式传入 snapshot。

#### 风险 7：TurnAssembly 已有 provider 消费面，但 turn default path 未自动填充
- `prompt.AssembleTurn`、`internal/provider/codexapp/session_turn.go`、`internal/provider/claudecli/session_turn.go` 三端都已具备能力。
- 但 `turn.Service.PrepareTurn` 当前不调用 prompt assembly，也不会默认写入 `dto.TurnRequest.TurnAssembly`。
- 结果是 turn 侧 system/user-context 注入是否生效，取决于上游是否显式构造 `TurnAssembly`。

#### 风险 8：`memory_read` 的实现与默认装配存在落差
- 从源码与第 02 卷交叉核对可见：`cmd/mcp-orch/memory/service.go`、`cmd/mcp-orch/tools/memory_tools.go` 已经把只读 contract 与 tool 定义补齐。
- 但 `run()` 主装配路径当前仍可能缺少 `memory.NewConfig / memory.NewService` 的正式 provider 接线。
- 更具体地说，`cmd/mcp-orch/runtime.go:newRegistry()` 会**无条件**把 `memoryToolDefinitions(deps.Memory)` 加进 registry；真正执行时再由 `makeHandler(...).requireDependency(...)` 检查 `MemoryService` 是否为空。
- 结果是文档层面不能把 `memory_read` 直接视为“默认 runtime 必然可用”的稳定链路，而应标注为“tool definition 已存在，但默认 wiring 与运行时依赖仍需继续核对”的能力面。

#### 风险 9：public RPC surface 比 in-process service contract 更窄
- `thread.Service.Resume` 的 in-process contract 支持 `PromptSnapshot / Effort / ConfigOverride`。
- 但公开的 `thread/resume` RPC 入口当前只暴露 `resumeParams{threadId,path,cwd,model,provider}`。
- 这意味着外部通过公共 JSON-RPC 调用 `thread/resume` 时，拿不到与 in-process 调用相同的恢复语义；尤其无法直接携带 runtime snapshot 参与恢复。
- 如果后续要把“store snapshot → runtime snapshot”或“显式 resume snapshot 注入”能力开放给外部 caller，首先要补的不是 store，而是 RPC surface。

---

## 8. 关键源码文件索引

### 8.1 Memory 主文件
- `internal/module/memory/config.go`
- `internal/module/memory/service.go`
- `internal/module/memory/store.go`
- `internal/module/memory/index.go`
- `internal/module/memory/manifest.go`
- `internal/module/memory/retrieval.go`
- `internal/module/memory/root.go`
- `internal/module/memory/rules.go`
- `internal/module/memory/rules_provider.go`
- `internal/module/memory/agent.go`
- `cmd/mcp-orch/memory/service.go`
- `cmd/mcp-orch/tools/memory_tools.go`

### 8.2 Prompt 主文件
- `internal/module/prompt/module.go`
- `internal/module/prompt/service.go`
- `internal/module/prompt/registry.go`
- `internal/module/prompt/section.go`
- `internal/module/prompt/dynamic.go`
- `internal/module/prompt/assembler.go`
- `internal/module/prompt/cache.go`
- `internal/module/prompt/env_provider.go`
- `internal/module/prompt/language_provider.go`
- `internal/module/prompt/mcp_provider.go`
- `internal/module/prompt/types.go`
- `internal/contract/prompt.go`

### 8.3 Thread / snapshot / provider bridge 主文件
- `internal/module/thread/contract.go`
- `internal/module/thread/service.go`
- `internal/module/thread/service_constructor.go`
- `internal/module/thread/lifecycle.go`
- `internal/module/thread/lifecycle_fork.go`
- `internal/module/thread/start_session.go`
- `internal/module/thread/start_session_helpers.go`
- `internal/module/thread/history.go`
- `internal/module/thread/factory.go`
- `internal/module/thread/rpc.go`
- `internal/store/thread/contract.go`
- `internal/store/thread/store.go`
- `internal/dto/provider/session.go`
- `internal/contract/provider.go`
- `internal/provider/codexapp/driver.go`
- `internal/provider/codexapp/support.go`
- `internal/provider/claudecli/config.go`
- `internal/provider/claudecli/driver.go`
- `internal/provider/claudecli/transport_config.go`
- `internal/provider/claudecli/session.go`
