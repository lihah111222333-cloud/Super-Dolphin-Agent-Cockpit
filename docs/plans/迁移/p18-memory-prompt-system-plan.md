# P18 执行计划：记忆系统 + 系统提示词架构集成

> 基于 Claude Code 官方源码逆向文档
> 参考：`claude_memory_system_mapping.md` / `claude_system_prompts_mapping.md` + 对应 source_refs
> 创建时间：2026-04-14
> 状态：**Historical Only / Superseded**
> **本文件仅保留历史背景与早期推演，不再作为当前实施口径。当前 authoritative source 为 `docs/plans/迁移/p18/README.md`、`phase-0`~`phase-8`、`review-summary.md`。**

---

## 一、目标

将 Claude Code 的**记忆系统**和**系统提示词三层注入架构**移植到 Super-Dolphin V3，实现：

1. **持久化记忆**：跨会话保留用户偏好、工作反馈、项目上下文
2. **结构化提示词**：静态/动态分层 + 缓存友好的提示词注入
3. **Agent 记忆隔离**：主线程与子 Agent 各自维护独立记忆
4. **记忆检索**：基于相关性的异步记忆召回

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
| User Context | 前置 synthetic user message | 可注入到首轮 user message |
| System Context | 追加到 system 尾部 | 追加到 instructions 尾部 |

### 2.4 提示词静态/动态分层

- **Static（7 slots）**：身份声明、安全底线、工程规范、高危拦截、工具偏好、风格、输出效率
- **Boundary Marker**：缓存分界
- **Dynamic（13 slots）**：会话策略、记忆规则、环境信息、语言、输出风格、MCP 指令等

---

## 三、V3 集成架构设计

### 3.1 记忆存储层

```
~/.multi-agent/memory/
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

type MemoryEntry struct {
    Name        string     `yaml:"name"`
    Description string     `yaml:"description"`
    Type        MemoryType `yaml:"type"`
    Content     string     `yaml:"-"`
    FilePath    string     `yaml:"-"`
    UpdatedAt   time.Time  `yaml:"-"`
}
```

### 3.3 系统提示词注入架构

```
internal/module/prompt/
├── module.go           # fx 模块注册
├── registry.go         # Section 注册表（name → compute 函数 + 缓存）
├── sections_static.go  # 7 个静态 section
├── sections_dynamic.go # 动态 section（记忆、环境、MCP 等）
├── builder.go          # 组装最终 system prompt 数组
├── context.go          # User Context + System Context 构建
└── prompt_test.go
```

**Section 注册模型：**

```go
type PromptSection struct {
    Name     string
    Order    int
    Static   bool                    // boundary 前/后
    Compute  func(ctx *BuildCtx) *string
    Cached   bool                    // 是否已缓存
    Value    *string                 // 缓存值
}

type PromptRegistry struct {
    sections []PromptSection
    mu       sync.RWMutex
}
```

### 3.4 注入链路（V3 适配）

```
thread/start
  → PromptRegistry.Build()
    → Static sections (identity, security, engineering, actions, tools, style, efficiency)
    → BOUNDARY MARKER
    → Dynamic sections (session_guidance, memory_rules, env_info, language, mcp_instructions...)
  → MemoryModule.BuildUserContext()
    → 读 MEMORY.md 索引 + 包装
    → 读 CLAUDE.md 等指令文件（如果存在）
  → 组装 instructions + userContext → codex thread/start
```

---

## 四、分 Phase 实施

### Phase 0：基础设施（预计 1 天）
- [ ] 创建 `internal/module/memory/` 模块骨架
- [ ] 创建 `internal/module/prompt/` 模块骨架
- [ ] 定义 `MemoryType` / `MemoryEntry` / `PromptSection` 核心类型
- [ ] fx 注册到应用生命周期
- [ ] 创建 `~/.multi-agent/memory/` 目录结构管理

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
- [ ] 实现 `prompt/builder.go`：组装 static → boundary → dynamic → final string[]
- [ ] 实现 `prompt/context.go`：User Context（claudeMd + currentDate）+ System Context（gitStatus）

### Phase 4：注入到 Provider 链路（预计 1 天）
- [ ] 修改 `internal/provider/codexapp/session.go`：
  - `thread/start` 的 `instructions` 改为从 PromptRegistry.Build() 获取
  - `turn/start` 前注入 User Context
- [ ] 修改 `internal/provider/claudecli/session.go`：
  - Claude provider 同样接入 PromptRegistry
- [ ] 缓存失效时机：`/clear`、`/compact`、worktree 切换
- [ ] Section cache 清理 API

### Phase 5：Agent 记忆（预计 1 天）
- [ ] 实现 `memory/agent_memory.go`：
  - 三种 scope：`user`（全局）、`project`（项目级）、`local`（会话级）
  - 目录结构参考 Claude Code agent memory 路径
  - Agent 启动时读取 MEMORY.md 并内联到 prompt
- [ ] 子 Agent 提示词自动注入记忆规则
- [ ] Agent 类型隔离：不同 agent type 独立 memory 目录

### Phase 6：记忆检索（预计 2 天）
- [ ] 实现 `memory/retrieval.go`：
  - `StartRelevantMemoryPrefetch(userQuery string)`
  - 异步扫描 memory headers → manifest 构建
  - 使用模型 side-query 选择相关记忆（最多 5 个）
  - 截断策略：200 行 / 4KB
- [ ] 集成到 turn 执行链路：每轮 user message 触发预取
- [ ] 去重：已 surfaced 的记忆不重复注入
- [ ] Fail-soft：检索失败不阻塞主流程

### Phase 7：迁移工具 + 兼容层（预计 1 天）
- [ ] Memory MCP 工具：
  - `memory_read`：读取指定记忆
  - `memory_write`：写入新记忆（带 type 校验）
  - `memory_search`：搜索记忆
  - `memory_list`：列出记忆索引
- [ ] 现有 `shared_file_read/write` 保持兼容
- [ ] 迁移脚本：将现有 `docs/plans/迁移/session-summary.md` 和 `会话习惯.md` 转为 memory 格式

### Phase 8：测试 + 守护（预计 1 天）
- [ ] 单元测试：memory store CRUD、prompt builder、section registry
- [ ] 集成测试：完整的 thread/start → memory 注入 → turn 执行 → memory 保存 链路
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

**决策**：记忆行为规则走 system prompt dynamic section（和 Claude Code 一致），MEMORY.md 内容走 User Context 前置注入。

### 5.3 提示词缓存策略

**决策**：
- 静态 sections 进程内缓存，直到 `/clear` 或 `/compact`
- 动态 sections 按 name 缓存，首次计算后复用
- MCP instructions 每轮重算（DANGEROUS section）

### 5.4 不实现的部分（V3 暂不需要）

| 功能 | 原因 |
|------|------|
| KAIROS daily log 模式 | V3 不是 long-lived autonomous agent |
| Team Memory 双目录 | V3 当前单用户，无 team 协作 |
| `tengu_moth_copse` 过滤 | V3 不走 Claude Code 的 feature gate 体系 |
| Global cache scope | V3 走 codex 的缓存策略，不需要自己管 API cache |
| Token budget section | V3 由 codex 管理 token |
| Output Style section | V3 用 CLAUDE.md / 系统提示词控制风格 |

---

## 六、依赖与风险

### 6.1 依赖
- Phase 0-2 无外部依赖，可立即开始
- Phase 4 依赖 codex app-server 的 instructions 注入机制（已有）
- Phase 6 依赖模型 side-query 能力（可用 codex 子会话实现）

### 6.2 风险
| 风险 | 影响 | 缓解 |
|------|------|------|
| instructions 过长导致 codex 超 token | 高 | 静态 sections 精简，设 hard limit |
| 记忆检索延迟影响 turn 响应 | 中 | 异步预取，不阻塞主流程 |
| 记忆文件并发写冲突 | 低 | 文件锁 + 最终一致 |

---

## 七、验收标准

1. ✅ 新会话启动时自动加载记忆（MEMORY.md 索引 ≤ 200 行）
2. ✅ Agent 可通过 memory_write 保存四种类型记忆
3. ✅ 系统提示词分静态/动态，静态部分进程内缓存
4. ✅ 子 Agent 有独立记忆空间（按 agent type 隔离）
5. ✅ 记忆检索异步执行，不阻塞 turn
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
| 4 | Provider 链路注入 | 1 天 |
| 5 | Agent 记忆 | 1 天 |
| 6 | 记忆检索 | 2 天 |
| 7 | 迁移工具 + 兼容层 | 1 天 |
| 8 | 测试 + 守护 | 1 天 |
| **合计** | | **12 天** |

---

## 九、参考文档

- `claude_memory_system_mapping.md`：记忆系统运行模式、四种类型、注入链路
- `claude_memory_system_source_refs.md`：记忆系统源码锚点
- `claude_system_prompts_mapping.md`：系统提示词三层架构、静态/动态分层
- `claude_system_prompts_source_refs.md`：提示词源码锚点
- `docs/plans/迁移/session-summary.md`：当前会话摘要（迁移种子数据）
- `docs/plans/迁移/会话习惯.md`：用户习惯画像（迁移种子数据）
- `docs/plans/迁移/lsp-mandatory-prefix.md`：LSP 工具链规范（融入 tool_preferences section）
- `docs/plans/迁移/lsp-advanced-guide.md`：LSP 高级指南（融入 tool_preferences section）
