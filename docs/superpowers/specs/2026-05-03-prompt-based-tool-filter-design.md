# 提示词软过滤替代 nativefilter 设计

> 日期: 2026-05-03
> 状态: 待实现
> 作者: 头脑风暴会话

## 1. 背景与动机

### 1.1 现状

P5 nativefilter 在 spawn Claude CLI 前，把 `SkillMeta.ReplacesNative["claude"]` 聚合结果写入 `<workspace>/.claude/settings.json` 的 `permissions.deny`，令 Claude CLI 隐藏被替代的原生工具。

**问题**：该机制仅对 Claude CLI 生效。Codex CLI 无等价 API（stub 未实现），未来的 Gemini / Copilot 等模型更不会有。工具过滤无法跨模型统一。

### 1.2 目标

- **去重**：项目已通过 MCP 提供 file / grep / patch_edit 等能力，不希望模型同时看到功能重叠的原生工具
- **跨模型统一**：一处声明，所有底层 LLM 都遵守
- **可扩展**：未来加新 MCP 工具时，只需在 skill meta 声明，无需改代码

## 2. 方案选型

| 方案 | 描述 | 优点 | 缺点 |
|------|------|------|------|
| A 硬编码 prompt | 在 tool_preferences 段写死替代映射 | 最简单 | 加工具要改 Go 代码 |
| **B 动态 prompt（选定）** | 从 SkillMeta.ReplacesNative 动态生成 prompt section | 声明式，可扩展 | 需注入 skilllibrary 依赖 |
| C 配置文件驱动 | 从 native-cli-filter.json 渲染 prompt | 纯配置 | 与 SkillMeta 数据重复 |

选定方案 B：复用现有数据模型，渲染目标从文件改为 prompt。

## 3. 设计

### 3.1 数据流

```
SkillMeta.ReplacesNative["*"]           ← 跨模型 key
  → AggregateReplacesNative(entries)    ← 复用 + 扩展现有函数
  → BuildCtx.SuppressedTools            ← 新字段
  → resolveToolPreferencesSection()     ← 现有渲染入口
  → system prompt tool_preferences 段
  → 所有模型统一看到
```

### 3.2 代码改动

#### 3.2.1 BuildCtx 加字段

文件: `internal/contract/prompt.go`

```go
type BuildCtx struct {
    // ... 现有字段 ...
    SuppressedTools []string // 被 MCP 等价工具替代的原生工具名
}
```

#### 3.2.2 聚合函数扩展

迁移后放置: `internal/module/skilllibrary/aggregate.go`（聚合的是 skill 数据，归属 skilllibrary 包）

新增 `AggregateAllReplacements` 函数：收集所有 key（`"*"` + `"claude"` + `"codex"` + ...）的值，去重排序。向后兼容旧 skill 的 `"claude"` key。

```go
func AggregateAllReplacements(entries []SkillEntry) []string {
    seen := make(map[string]struct{})
    for _, e := range entries {
        if e.Meta == nil || e.Meta.Disabled {
            continue
        }
        for _, tools := range e.Meta.ReplacesNative { // 遍历所有 key
            for _, name := range tools {
                if name != "" {
                    seen[name] = struct{}{}
                }
            }
        }
    }
    return sortedSeen(seen)
}
```

#### 3.2.3 Prompt 组装注入

文件: `internal/module/prompt/assembler.go`

prompt service（`service` struct）已通过 fx 注入各种依赖。新增 `skilllibrary.Store` 字段。

在 `buildStartCtx` 层不改（它只接收 `StartInput`），改在上层 `AssembleStart` 中：调用 `store.List()` → `AggregateAllReplacements()` → 赋值 `buildCtx.SuppressedTools`。

> **注意**：`AssembleTurn` 不需要处理——`tool_preferences` 是 static section（order=50, `PromptRegionStatic`），只在 `AssembleStart` 时 resolve，`AssembleTurn` 只 resolve dynamic sections。

#### 3.2.4 Prompt 渲染

文件: `internal/module/prompt/section.go`

在 `renderToolPreferencesSectionText` 中追加：

```go
if len(build.SuppressedTools) > 0 {
    bullets = append(bullets,
        "Do NOT use these native tools — they have been replaced by "+
        "project MCP equivalents: "+strings.Join(build.SuppressedTools, ", ")+".")
    // 中文版本同步输出（按 feedback 要求中英双版本）
    // "以下原生工具已被项目 MCP 等价工具替代，禁止使用：" + ...
}
```

渲染效果：
```
Tool preferences:
- Prefer repository-aware tools first: use file for reading, patch_edit for edits, and grep for search.
- Use exec_command for shell execution only when a dedicated tool cannot do the job.
- Do NOT use these native tools — they have been replaced by project MCP equivalents: Read, Write, Edit, Grep, Bash.
- Break larger tasks into explicit steps and keep tool usage stable instead of churning approaches.
- Batch independent tool calls in parallel and run dependent calls sequentially.
```

#### 3.2.5 Skill 声明方式

各 skill 的 `.skill-meta.json`：

```json
{
  "name": "lsp提示词",
  "replaces_native": {
    "*": ["Read", "Write", "Edit", "Grep", "Bash"]
  }
}
```

`"*"` = 跨模型通用。旧 `"claude"` key 继续兼容，聚合时自动合并。

### 3.3 删除清单

| 文件/包 | 操作 |
|---------|------|
| `internal/module/nativefilter/` | 整包删除（`AggregateAllReplacements` + `sortedSeen` 迁移到 `skilllibrary` 包后） |
| `internal/app/modules.go` | 移除 `nativefilter.Module` |
| `internal/provider/claudecli/driver.go` | 移除 `nativeFilter` 字段和 `Apply()` 调用 |
| `internal/provider/claudecli/module.go` | 移除 `NativeFilter` 依赖注入 |

### 3.4 残留清理

删除 nativefilter 后，旧运行写入的 `<workspace>/.claude/settings.json` 可能残留 `permissions.deny` 条目。

**策略**：简单处理——在 `claudecli/driver.go` 原来调用 `nativeFilter.Apply()` 的位置，改为写入一个空的 `{"permissions":{"deny":[]}}` 覆盖旧文件。运行一次后残留即清除。后续版本可以把这段清理代码也删掉。

## 4. 兼容与迁移

### 4.1 迁移顺序

1. **先加新**：BuildCtx 加字段 → 聚合函数 → prompt 渲染 → 测试通过
2. **再删旧**：移除 nativefilter 模块 → fx 注册 → driver 调用
3. **清残留**：清理旧 settings.json

### 4.2 Skill 迁移

- 旧 skill 的 `"claude": [...]` 声明自动生效（聚合函数遍历所有 key）
- 新 skill 统一用 `"*": [...]`
- 旧 skill 后续逐步迁移到 `"*"`，不急

### 4.3 回退方案

`ReplacesNative` 数据模型未变，如果软过滤穿透率不可接受，随时可恢复 nativefilter 模块或升级为混合模式。

## 5. 测试

### 5.1 单元测试

- `AggregateAllReplacements`：覆盖 `"*"` key、混合 key、空值、disabled skill
- `renderToolPreferencesSectionText`：`SuppressedTools` 非空时生成正确 bullet

### 5.2 集成验证

- 启动会话，确认 system prompt 包含抑制文本
- 确认模型实际使用 file 而非 Read

## 6. 前端设计

### 6.1 现状

当前 `BuiltinToolsSettings.ts` 是一个配置面板，用户手动勾选禁用哪些原生工具，写入 `uipreference.Store`（key: `config/builtinTools.disabled`）。

后端：`internal/module/uistate/builtin_tools.go` 提供 `config/builtinTools/read` 和 `config/builtinTools/write` 两个 RPC。

### 6.2 改造方向：混合模式（自动 + 手动）

过滤来源分两层：

1. **自动（技能声明）**：技能通过 `ReplacesNative` 声明替代关系，只读不可取消
2. **手动（用户勾选）**：用户对未被技能覆盖的工具自行勾选禁用，存入 `uipreference.Store`

两层合并后统一注入 `SuppressedTools`，渲染到 system prompt，对所有模型生效。

#### 数据流

```
技能声明（skilllibrary）─┐
                         ├→ SuppressedTools → tool_preferences prompt → 所有模型
用户手动勾选（uipreference）─┘
```

#### 前端组件改动

文件: `cmd/agent-terminal/frontend/vue-app/pages/settings/BuiltinToolsSettings.ts`

- 标题改为“原生工具过滤”
- 移除 Claude / Codex 分组（跸模型后不再按 provider 分）
- 分三组展示：
  - **自动替代**：技能声明的，只读，显示“🔄 ← 技能X”
  - **手动过滤**：用户勾选的，可 toggle
  - **未过滤**：剩余工具，可 toggle 勾选禁用
- 计数改为“已过滤 X / Y”

#### 后端 API 改动

文件: `internal/module/uistate/builtin_tools.go`

- `readBuiltinTools()` 数据源改为双源：`skilllibrary.Store`（自动）+ `uipreference.Store`（手动）
  - 返回结构增加 `replacedBy` 字段（自动替代来源）
  - 保留 `enabled` 字段（手动勾选状态）
- `writeBuiltinTool()` 写入接口 **保留**（用于手动勾选）
- `config/builtinTools/write` RPC **保留**

#### prompt assembler 改动

`aggregateSuppressedTools` 合并两个来源：

```go
suppressed = union(
    skilllibrary.AggregateAllReplacements(entries),  // 自动
    uipreference 手动禁用列表,                       // 手动
)
```

#### UI Mockup

```
┌──────────────────────────────────────────────────────┐
│ 原生工具过滤                         已过滤 10 / 14   │
│ 技能自动替代 + 用户手动勾选，统一对所有模型生效。  │
│                                                      │
│ ▸ 自动替代（8）—— 由技能声明，不可取消              │
│   🔄 Read     ← lsp提示词 (file)                │
│   🔄 Write    ← lsp提示词 (patch_edit)                │
│   🔄 Edit     ← lsp提示词 (patch_edit)                │
│   ...                                                │
│                                                      │
│ ▸ 手动过滤（2）—— 用户自行勾选                    │
│   ☑ WebFetch   （用户勾选禁用）                      │
│   ☑ WebSearch  （用户勾选禁用）                      │
│                                                      │
│ ▸ 未过滤（4）                                          │
│   ☐ TodoWrite                                        │
│   ☐ NotebookEdit                                     │
│   ☐ Task                                             │
│   ☐ ExitPlanMode                                     │
└──────────────────────────────────────────────────────┘
```

## 7. 风险与取舍

| 风险 | 评估 | 缓解 |
|------|------|------|
| 模型偶尔穿透使用原生工具 | 概率 <1%，现代 LLM 对 system prompt 遵从度高 | 可接受；需要时加回硬过滤 |
| 原生工具 schema 仍占 token | 不可避免，是软过滤的固有代价 | token 开销相对 system prompt 整体 <0.6% |
| prompt service 新增 skilllibrary 依赖 | 增加一个 fx 注入 | 依赖方向合规（外→内） |
