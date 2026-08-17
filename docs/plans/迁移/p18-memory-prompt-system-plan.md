# P18 执行计划：记忆系统 + 系统提示词架构集成

> 基于 Claude Code 官方源码逆向文档
> 创建时间：2026-04-14
> 状态：**Historical Background / Synced Summary**
> **本文件保留总览与背景推演，但关键口径已同步到当前实现计划。authoritative source 仍以 `docs/plans/迁移/p18/README.md`、`phase-0`~`phase-8`、`review-summary.md` 为准。**

---

## 一、目标

将 Claude Code 的**记忆系统**和**系统提示词三层注入架构**移植到 Super-Dolphin V3，实现：

1. **持久化记忆**：跨会话保留用户偏好、工作反馈、项目上下文
2. **结构化提示词**：静态/动态分层 + 缓存友好的提示词注入
3. **Agent 记忆隔离**：主线程与子 Agent 各自维护独立记忆
4. **记忆检索**：基于相关性的异步记忆召回

### 当前实施边界与推荐顺序

- P18 默认落地路径是：**单用户 + Standard 主链**
- Team Memory / KAIROS / nested_memory 不在本轮实施范围
- 推荐执行顺序：`0 → 1 → 2 → 3 → 4.5 → 4 → 5/6/7 → 8`

### 当前对齐度与剩余 13% 差距

- 当前 P18 规划口径对 Claude Standard 主链约 **87%** 对齐
- 剩余约 **13%** 差距主要集中在：**KAIROS daily log、Team Memory + sync service、nested_memory、background extract / auto-dream、compact 邻接边缘行为**
> 2026-04-17 更新：KAIROS / Team Memory / nested_memory 已在 P18.3 J/K/L 落地；剩余 parity gap 归 P18.4 处理。
- 这些能力已在 README / 各 Phase 文档中显式标记为 deferred；P18 本轮验收只以 **单用户 + Standard 主链** 为目标

### Phase 依赖图（文本）

```text
0 → 1 → 5/6/7
0 → 2 → 3 → 4.5 → 4
2 → 7
3 → 4
0/1/2/3/4/4.5/5/6/7 → 8
```

### 延后风险

- **Team Memory**：只有在**完全不暴露 team scope**时才安全延后；若未来开放 team scope，必须连同 sync service / secret guard / team entrypoint 注入一起上线
- **KAIROS daily log**：在非长寿命 autonomous session 前提下可延后；若未来引入长期后台助手，应优先补 `/dream` + daily log 闭环
- **nested_memory**：可延后，但这不是 retrieval 的小补丁，而是 target-path 条件规则体系；复杂多目录项目命中率会弱于 Claude
- **compact 邻接行为**：P18 以 `PromptAssembly` invalidate + retrieval generation/cancel + attachment replay roundtrip 覆盖主链，但不追求 Claude 所有边缘 compact 行为一比一同构

---

## 二、Claude Code 核心架构摘要

### 2.1 记忆系统三种运行模式

| 模式 | 函数 | 特征 |
|------|------|------|
| **Standard** | `buildMemoryLines()` | 单目录 auto memory，四种类型，MEMORY.md 索引 |
| **KAIROS** | `buildAssistantDailyLogPrompt()` | append-only 日志，夜间蒸馏回 MEMORY.md |
| **Team Memory** | `buildCombinedMemoryPrompt()` | private + shared 双目录，scope 规则 |

### 2.2 四种记忆类型

| 类型 | 存什么 | Scope |
|------|--------|-------|
| `user` | 用户角色/目标/偏好/知识背景 | always private |
| `feedback` | 工作方式纠正/确认的非显然做法 | 默认 private，项目约定升 team |
| `project` | 项目背景/决策动机/截止日期 | 偏向 team |
| `reference` | 外部系统指针（Slack/Grafana/Linear） | usually team |

### 2.3 系统提示词三层注入

| 层 | API 位置 | V3 对应 |
|----|---------|---------|
| System Prompt | `system` 参数 | codex `thread/start` instructions |
| User Context | 前置 synthetic user message | `turn/start` 前置 synthetic 输入 |
| System Context | 追加到 system 尾部 | `thread/start` DeveloperInstructions / system tail |

### 2.4 提示词静态/动态分层

- **Static（7 slots）**：身份声明、安全底线、工程规范、高危拦截、工具偏好、风格、输出效率
- **Boundary Marker**：缓存分界
- **Dynamic（13 slots）**：会话策略、记忆规则、环境信息、语言、输出风格、MCP 指令等

---

## 三、V3 集成架构设计

### 3.1 记忆存储层

```
~/.super-dolphin/memory/
├── projects/
│   └── <sanitized-project-root>/
│       └── memory/
│           ├── MEMORY.md                  # 索引文件
│           ├── user-profile.md            # user 类型
│           ├── session-habits.md          # feedback 类型
│           ├── project-context.md         # project 类型
│           └── references.md              # reference 类型
├── agent-memory/
│   └── <agent-type>/
│       ├── MEMORY.md
│       └── <topic-files>.md
└── team/                                  # 未来 team memory
    ├── MEMORY.md
    └── <shared-topic-files>.md
```

**同时保留 DB 共享文件**：`shared_files` 表用于 agent 间实时共享，磁盘 memory 用于跨会话持久化。

### 3.2 记忆 Go 模块设计

```
internal/module/memory/
├── module.go           # fx 模块注册
├── types.go            # MemoryType enum + MemoryEntry struct
├── store.go            # 磁盘读写（MEMORY.md 索引 + topic files）
├── prompt_builder.go   # 生成记忆行为规则 prompt
├── retrieval.go        # 相关记忆异步检索
├── agent_memory.go     # Agent 专用记忆（user/project/local scope）
└── memory_test.go
```

**核心类型：**

```go
type MemoryType string

const (
    MemoryTypeUser      MemoryType = "user"
    MemoryTypeFeedback  MemoryType = "feedback"
    MemoryTypeProject   MemoryType = "project"
    MemoryTypeReference MemoryType = "reference"
)

// MemoryType 表示语义分类，不表示 scope；scope/namespace 需独立建模。
type MemoryScope string

// 持久化 frontmatter
type MemoryFrontmatter struct {
    Name        string      `yaml:"name"`
    Description string      `yaml:"description"`
    Type        *MemoryType `yaml:"type,omitempty"`
}

// MemoryEntry 是运行时表示，不是磁盘格式
// 磁盘格式是 YAML frontmatter (name/description/type) + markdown body
type MemoryEntry struct {
    Frontmatter MemoryFrontmatter `yaml:",inline"`
    Content     string            `yaml:"-"`
    FilePath    string            `yaml:"-"`
    UpdatedAt   time.Time         `yaml:"-"`
}
```

### 3.3 系统提示词注入架构

```
internal/module/prompt/
├── module.go           # fx 模块注册
├── registry.go         # Section 注册表（name → compute 函数 + 缓存）
├── sections_static.go  # 7 个静态 section
├── sections_dynamic.go # 动态 section（记忆、环境、MCP 等）
├── builder.go          # 组装最终 system prompt parts
├── context.go          # User Context + System Context 构建
├── assembly.go         # PromptAssemblyService / StartAssembly / TurnAssembly
└── prompt_test.go
```

**Section 注册模型：**

```go
type PromptSection struct {
    Name     string       // 唯一缓存键
    Order    int
    Region   PromptRegion // Static | Dynamic
    Volatile bool         // true = 每轮重算（DANGEROUS）
    Compute  func(ctx context.Context, b BuildCtx) (*string, error)
}

type PromptRegion int
const (
    PromptRegionStatic PromptRegion = iota
    PromptRegionDynamic
)

type PromptRegistry struct {
    sections []PromptSection
    cache    map[string]*string
    mu       sync.RWMutex
}

type StartAssembly struct {
    DisplayName           string
    BaseInstructions      string
    DeveloperInstructions string
    Snapshot              PromptAssemblySnapshot
}

type TurnAssembly struct {
    UserContextText string
}

type PromptAssemblySnapshot struct {
    DisplayName           string
    BaseInstructions      string
    DeveloperInstructions string
    Provider              string
    Version               int
    Hash                  string
}

type PromptAssemblyService interface {
    AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error)
    AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error)
    Invalidate(ctx context.Context, reason InvalidateReason) error
}
```

### 3.4 注入链路（V3 适配）

```
thread/start
  → PromptAssemblyService.AssembleStart()
    → PromptRegistry.BuildSystemPrompt()
      → Static sections (identity, security, engineering, actions, tools, style, efficiency)
      → Dynamic sections (session_guidance, memory_rules, env_info, language, mcp_instructions...)
      → （概念上保留 static/dynamic 分区；V3 不实现 literal boundary marker）
    → PromptContext.BuildSystemContext()
      → DeveloperInstructions / system tail
  → 写入 StartAssembly.Snapshot（供 resume/recover/fork 复用）
  → provider 只消费 BaseInstructions + DeveloperInstructions

turn/start
  → PromptAssemblyService.AssembleTurn()
    → BuildBaseUserContext()
      → 聚合 CLAUDE.md / AutoMem entrypoint / currentDate
    → MergeRuntimeUserContext()
  → `TurnAssembly.UserContextText` 交给 thread/turn adapter
    → codex: 前置 synthetic text input（在 skill prompt 之后）
    → claude: provider-local prepend block（不回写普通 user inputs）
  → relevant memories 走 attachment/hint 链，**不进入** `TurnAssembly.UserContextText`
```

---

## 四、分 Phase 实施

> 依赖顺序以 `0 → 1 → 2 → 3 → 4.5 → 4 → 5/6/7 → 8` 为主线。
> 其中 **Phase 4 必须等待 Phase 4.5 完成**；Phase 8 负责把 0-7 + 4.5 串成最终回归闭环。

### Phase 0：基础设施（预计 1 天）
- [ ] 创建 `internal/module/memory/` 模块骨架
- [ ] 创建 `internal/module/prompt/` 模块骨架
- [ ] 定义 `MemoryType` / `MemoryEntry` / `PromptSection` 核心类型
- [ ] fx 注册到应用生命周期
- [ ] 创建 `~/.super-dolphin/memory/` 目录结构管理

### Phase 1：记忆存储层（预计 2 天）
- [ ] 实现 `memory/store.go`：
  - `ReadMemoryIndex(projectRoot string) ([]IndexEntry, error)`
  - `WriteMemoryFile(entry MemoryEntry) error`
  - `UpdateMemoryIndex(entry MemoryEntry) error`
  - `DeleteMemory(name string) error`
  - `ScanMemoryHeaders(dir string) ([]MemoryHeader, error)`
- [ ] MEMORY.md 索引格式：`- [Title](file.md) — one-line hook`
- [ ] Topic file 格式：YAML frontmatter + markdown content
- [ ] 路径安全校验：拒绝 `..`、绝对路径、symlink 穿越
- [ ] 截断策略：200 行 / 25KB 上限

### Phase 2：记忆行为规则注入（预计 1 天）
- [ ] 实现 `memory/prompt_builder.go`：
  - `BuildMemoryPrompt(mode MemoryMode) string`
  - 输出四种记忆类型的 taxonomy + save/access/trust 规则
  - 明确哪些内容不能存（代码模式、架构、git history 等）
- [ ] 标准模式：告诉模型 MEMORY.md 是索引，topic file 存正文
- [ ] 保存协议：写 topic file → 更新 MEMORY.md 索引

### Phase 3：系统提示词 Section 注册表（预计 2 天）
- [ ] 实现 `prompt/registry.go`：Section 注册 + 按 name 缓存 + 失效
- [ ] 实现静态 sections（`sections_static.go`）：
  - `identity`：身份声明 + 安全底线（参考 Claude Code getSimpleIntroSection）
  - `system_constraints`：工具调用规则 + 注入防御（参考 getSimpleSystemSection）
  - `engineering`：工程规范 + 防过度设计三原则（参考 getSimpleDoingTasksSection）
  - `actions`：高危动作拦截规则（参考 getActionsSection）
  - `tool_preferences`：LSP 工具链偏好决策树（V3 定制，融合 lsp-mandatory-prefix.md）
  - `style`：风格去装饰 + 代码引用格式（参考 getSimpleToneAndStyleSection）
  - `output_efficiency`：输出效率（参考 getOutputEfficiencySection）
- [ ] 实现动态 sections（`sections_dynamic.go`）：
  - `session_guidance`：会话策略（Agent 使用指南、Skill 调用等）
  - `memory`：记忆行为规则（Phase 2 产出）
  - `env_info`：宿主环境（CWD、Git、平台、模型）
  - `language`：语言偏好
  - `mcp_instructions`：MCP 服务器指令（DANGEROUS，每轮重算）
- [ ] 实现 `prompt/builder.go`：组装 static → dynamic（保留概念分区，不实现 literal boundary marker）→ provider-neutral parts / `[]ResolvedPromptSection`
- [ ] 实现 `prompt/context.go`：User Context（claudeMd + currentDate）+ System Context（gitStatus）
- [ ] 实现 `PromptAssemblyService`：统一封装 `StartAssembly / TurnAssembly / PromptAssemblySnapshot`

### Phase 4：注入到 Provider 链路（预计 1 天）
- [ ] `thread/start` 改为消费 `PromptAssemblyService.AssembleStart()` 返回的 `StartAssembly{BaseInstructions, DeveloperInstructions, Snapshot}`
- [ ] Codex `turn/start` / `turn/steer` 消费 `TurnAssembly.UserContextText`，映射为前置 synthetic text input（在 skill prompt 之后）
- [ ] Claude launch/turn/steer 复用同一 assembly 产物；turn 侧把 `TurnAssembly.UserContextText` 消费为 provider-local prepend block，不回写普通 user inputs
- [ ] 缓存失效时机：`/clear`、`/compact`、worktree、resume/restore、provider switch、auto-compact、partial compact
- [ ] Section cache 清理 API + `PromptAssemblySnapshot` 持久化契约

### Phase 4.5：Provider 归一前置解耦（预计 5-7 天）
- [ ] 解耦 `Name / Prompt / BaseInstructions / DeveloperInstructions` 语义：`Name` 只管 displayName，`BaseInstructions` / `DeveloperInstructions` 只给 provider
- [ ] 新增 `PromptAssemblyService` 契约：统一产出 `StartAssembly / TurnAssembly / PromptAssemblySnapshot`
- [ ] 收口 lifecycle / RPC / binding / orchestration launch contract，避免 legacy `prompt` 再污染 thread store、resume、fork、recover 与 UI
- [ ] 显式建模 `SessionIdentity` 与 provider capability 边界；Provider 切换/Resume/Recover 复用同一 snapshot 语义
- [ ] 子 Agent 改为透传 `DisplayName + BaseInstructions + DeveloperInstructions + Snapshot`

### Phase 5：Agent 记忆（预计 1 天）
- [ ] 实现 `memory/agent_memory.go`：
  - 三种 scope：`user`（全局）、`project`（项目级）、`local`（持久但不进版本控制）
  - 目录结构参考 Claude Code agent memory 路径
  - Agent 启动时读取 `MEMORY.md` 并内联到专用 builder，再经 `PromptAssemblyService.AssembleStart()` 并入 `StartAssembly.BaseInstructions`
- [ ] 子 Agent 提示词自动注入记忆规则 + `MEMORY.md` 内容
- [ ] Agent 类型隔离：不同 agent type 独立 memory 目录
- [ ] `@agent` 检索 / 预览 / Memory 工具共享同一可见性 + ACL 规则

### Phase 6：记忆检索（预计 2 天）
- [ ] 实现 `memory/retrieval.go`：
  - `StartRelevantMemoryPrefetch(userQuery string)`
  - 异步扫描 memory headers → 默认 `manifest.jsonl` sidecar 构建 / repair
  - 使用模型 side-query 选择相关记忆（最多 5 个）
  - 截断策略：200 行 / 4096 bytes
- [ ] 集成到 turn 执行链路：每轮 user message 触发预取；relevant memories 走 provider-neutral attachment/hint 链，**不进入** `TurnAssembly.UserContextText`
- [ ] 去重：已 surfaced 的记忆不重复注入
- [ ] `@agent` 检索复用 Phase 5 的 `sanitize + resolve + authorize` 规则
- [ ] Fail-soft：检索失败不阻塞主流程

### Phase 7：Hook 注入 + 记忆读取工具 + 迁移兼容（预计 0.5-1 天）
- [ ] `thread/start` hook：加载 `MEMORY.md` + memory 文件 → `PromptAssemblyService.AssembleStart()`
- [ ] `turn/end` hook：保存意图检测 → `extractMemory()` → 写盘 → 更新索引
- [ ] `session/stop` hook：仅预留 `extractMemories / autoDream` 扩展点（P19）
- [ ] `memory_read` 作为唯一 Memory MCP 工具；统一 `sanitize + resolve + authorize`，只读无副作用
- [ ] `/memory` / `/forget` 走 slash command + skill 框架
- [ ] 现有 `shared_file_read/write` 保持兼容；保留 `shared_files → 磁盘记忆` 迁移脚本（幂等 + dry-run + rollback 报告）

### Phase 8：测试 + 守护（预计 1-2 天）
- [ ] **P0（blocking）**：PromptAssembly / provider 注入 / memory store / retrieval 去重并发 / ACL / rollback drill / roundtrip
- [ ] **P1（扩展）**：benchmark 基线、额外 golden、compat matrix、长期 arch guard
- [ ] 集成测试：`StartAssembly` / `TurnAssembly` / relevant memories attachment-hint / provider switch / snapshot roundtrip
- [ ] 架构测试：确保 memory 模块不依赖 provider 模块（单向依赖）
- [ ] 守护测试：确保 prompt sections 数量和关键内容不回退

---

## 五、关键设计决策

### 5.1 存储选型：磁盘 vs 数据库

| 维度 | 磁盘（选用） | 数据库 |
|------|-------------|--------|
| 跨会话持久化 | ✅ 天然支持 | ✅ 支持 |
| Git 版本控制 | ✅ 可选择性 commit | ❌ |
| 与 Claude Code 对齐 | ✅ 完全对齐 | ❌ 偏离 |
| Agent 间共享 | ❌ 需文件锁 | ✅ 原生支持 |
| 检索性能 | ⚠️ 扫描 | ✅ SQL |

**决策**：主记忆用磁盘（对齐 Claude Code），agent 间实时协作继续用 `shared_files` DB。

### 5.2 记忆规则注入位置

**决策**：记忆行为规则走 system prompt dynamic section；主线程 AutoMem / CLAUDE.md 类上下文走 UserContext 前置注入；agent memory / relevant memories 分别走专用 prompt builder 与 attachment/hint 链。

### 5.3 提示词缓存策略

**决策**：
- section cache 按 **`name + generation` / dependency invalidate** 设计；静态区是 V3 分区，不等于 Claude global cache scope
- `mcp_instructions` 等 volatile section 每轮重算，但只在值变化时真正打破 prompt cache
- invalidate 原因至少覆盖：`/clear`、`/compact`、auto-compact、partial compact、worktree、resume_restore、provider switch

### 5.4 不实现的部分（V3 暂不需要）

| 功能 | 原因 |
|------|------|
| KAIROS daily log 模式 | V3 当前不是 long-lived autonomous agent；若未来引入长寿命后台助手，需连同 `/dream` 一起补闭环 |
| Team Memory + sync service + secret guard | 只有在**完全不暴露 team scope**时才安全延后，不能只做目录壳 |
| Background extract / auto-dream | P18 不做 stop-hook 后台补漏提取与蒸馏 |
| nested_memory | 复杂度高，单独排期到 P19 |
| `tengu_moth_copse` 完整 parity | 只吸收必要语义，不复刻 Claude 同名 feature flag 模式 |
| Global cache scope | V3 不实现 provider 级 global cache / boundary 机制 |
| Token budget / Output Style / ant-only sections | 交互模型或 provider 能力不匹配，P18 不实现 |

### 5.5 当前对齐度与延后风险

- 当前规划约 **87%** 对齐 Claude Standard 主链；剩余约 **13%** 差距集中在 KAIROS / Team Memory / nested_memory / background services / compact 邻接行为
> 2026-04-17 更新：KAIROS / Team Memory / nested_memory 已在 P18.3 J/K/L 落地；剩余 parity gap 归 P18.4 处理。
- Team Memory 只有在**完全不暴露 team scope**时才安全延后；一旦未来开放 team scope，必须把双目录、sync service、secret guard、team entrypoint 注入一起打包上线
- P18 当前以 `PromptAssembly` invalidate + retrieval generation/cancel + attachment replay roundtrip 覆盖主链，但不追求 Claude 所有边缘 compact 行为一比一同构

---

## 六、依赖与风险

### 6.1 依赖
- 推荐顺序：`0 → 1 → 2 → 3 → 4.5 → 4 → 5/6/7 → 8`
- Phase 4 **必须**等待 Phase 4.5 完成；Phase 5/6/7 都复用 Phase 1 的 memory root / path / sanitize 主链
- Phase 6 依赖模型 side-query / selector 能力（可用 codex 子会话实现）
- Phase 8 依赖 0-7 + 4.5 收口完成，用于回归与守护闭环

### 6.2 风险
| 风险 | 影响 | 缓解 |
|------|------|------|
| instructions 过长导致 codex 超 token | 高 | 引入统一 token/byte 预算器，分别约束 `BaseInstructions / DeveloperInstructions / UserContext / relevant memories / MCP volatile`；静态 sections 精简只是第一层缓解 |
| 记忆检索延迟影响 turn 响应 | 中 | 异步预取，不阻塞主流程 |
| 记忆文件并发写冲突 | 低 | 文件锁 + 最终一致 |
| 剩余约 13% parity gap（KAIROS / Team Memory / nested_memory / 后台服务层 / compact 边缘行为） | 中高 | README / phase 文档显式登记 deferred boundary；P18 仅按**单用户 + Standard 主链**验收，不暴露 team scope 半成品 |

> 2026-04-17 更新：KAIROS / Team Memory / nested_memory 已在 P18.3 J/K/L 落地；剩余 parity gap 归 P18.4 处理。
| Phase 4 在 4.5 前提前开工导致 prompt 语义继续污染 lifecycle | 高 | 严格按依赖图执行：先收口 `PromptAssemblyService / StartAssembly / TurnAssembly / Snapshot`，再接 provider 链路 |

---

## 七、验收标准

1. ✅ 新会话启动时自动加载记忆（MEMORY.md 索引 ≤ 200 行）
2. ✅ Hook / slash command 可完成记忆保存、查看、删除；MCP 面仅保留 `memory_read`，且 ACL / degraded 语义闭合
3. ✅ 系统提示词分静态/动态，并通过 `PromptAssemblyService` 产出 `StartAssembly / TurnAssembly`
4. ✅ 子 Agent 有独立记忆空间（按 agent type 隔离），并经 `PromptAssemblyService.AssembleStart()` 注入
5. ✅ 记忆检索异步执行，不阻塞 turn；relevant memories 走 attachment/hint，不进入 `TurnAssembly.UserContextText`
6. ✅ `go build` / `go vet` / `go test` 全绿
7. ✅ 仓库契约：文件 ≤ 400 行，函数 ≤ 80 行，CC ≤ 10

---

## 八、工作量估算

| Phase | 内容 | 预计工时 |
|-------|------|---------|
| 0 | 基础设施 | 1 天 |
| 1 | 记忆存储层 | 2 天 |
| 2 | 记忆行为规则注入 | 1 天 |
| 3 | 提示词 Section 注册表 | 2 天 |
| 4.5 | Provider 归一前置解耦 | 5-7 天 |
| 4 | Provider 链路注入 | 1 天 |
| 5 | Agent 记忆 | 1 天 |
| 6 | 记忆检索 | 2 天 |
| 7 | Hook 注入 + 记忆读取工具 + 迁移兼容 | 0.5-1 天 |
| 8 | 测试 + 守护 | 1-2 天 |
| **合计** | | **16.5-20 天** |

---

## 九、参考文档

- `docs/plans/迁移/p18/README.md`：当前 authoritative README / Phase 索引 / 边界与延后风险
- `docs/plans/迁移/p18/review-summary.md`：第 13 轮收官统一复核与审查结论
- `docs/plans/迁移/p18/source-refs-appendix.md`：全量源码锚点附录
- `docs/plans/迁移/p18/phase-0-infrastructure.md` ~ `phase-8-testing.md`：各 Phase 详细实施口径
- `docs/plans/迁移/session-summary.md`：当前会话摘要（迁移种子数据）
- `docs/plans/迁移/会话习惯.md`：用户习惯画像（迁移种子数据）
- `docs/plans/迁移/lsp-mandatory-prefix.md`：LSP 工具链规范（融入 tool_preferences section）
- `docs/plans/迁移/lsp-advanced-guide.md`：LSP 高级指南（融入 tool_preferences section）
