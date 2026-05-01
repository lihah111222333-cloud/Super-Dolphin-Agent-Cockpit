# 记忆中心 UI 改造 + Agent 记忆后端清理 — 实现计划

## 元信息

| 项 | 值 |
|---|---|
| 规格来源 | 头脑风暴讨论 2026-05-01 |
| 任务数 | 8 |
| 核心原则 | 不需要灰度测试与限制，直接全量上线 |
| 前置 | feature/memory-dedup-filter 分支已有去重过滤器代码 |

## 设计摘要

两项并行改动：

1. **前端 UI 改造**：记忆中心从按 scope（private/team）分组改为按 type（偏好/项目）分组；删除 Agent 记忆区域
2. **后端 Agent 记忆清理**：删除 `internal/module/memory/agent/` 子包及所有关联代码

## 文件地图

### 前端修改

| 文件 | 改动 |
|---|---|
| `cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js` | 删除 Agent 记忆整个区域；合并 private/team 为按 type 分组；新建类型从 4 项改为 2 项；加 scope 标签 |
| `cmd/agent-terminal/frontend/vue-app/composables/useMemoryEditors.js` | 删除 `useAgentMemoryEditor` 和 `useInlineDeleteConfirm` 中 agent 相关逻辑 |
| `cmd/agent-terminal/frontend/vue-app/app.js` | 从 `EMPTY_MEMORY_CENTER` 删除 `agentScopes`；`applyMemoryCenterSnapshot` 不再处理 agentScopes |

### 后端删除

| 文件/目录 | 操作 |
|---|---|
| `internal/module/memory/agent/` 整个目录 | **删除**（5 个文件：agent.go、agent_type.go、module.go、agent_test.go、sanitize_test.go） |
| `internal/module/memory/domain_bridges.go` | 删除 agent-memory bridge 区段（保留 team-memory bridge） |
| `internal/module/memory/module.go` | 移除 `memagent` import + `fx.Options(memagent.Module)` + `AgentProvider` 类型引用 + 4 个 provide 函数 |
| `internal/module/memory/ui_rpc.go` | 删除 `loadUIAgentMemoryScopes` + `UIMemorySnapshot.AgentScopes` 字段赋值 |
| `internal/module/memory/ui_rpc_mutations.go` | 删除 agent/get、agent/save、agent/delete 三个 RPC 注册 + handler 实现 |
| `internal/module/memory/config.go` | 删除 `IsAgentMemoryPath` 相关逻辑 |
| `internal/module/memory/rules_provider.go` | 删除 AgentProvider 注册为 DynamicSectionProvider 的代码 |
| `internal/archtest/freeze_registry.go` | 内联后检查 memory 包文件数，按需调整 freeze limit |

---

## 任务列表

### Phase A：后端 Agent 记忆清理（先做，因为前端依赖后端 API 变化）

---

#### Task A1 — 删除 agent RPC 端点

**文件**：`internal/module/memory/ui_rpc_mutations.go`

1. 搜索 `ui/memory/agent/get`、`ui/memory/agent/save`、`ui/memory/agent/delete` 的注册位置
2. 删除这 3 个 handler 的注册行
3. 删除对应的 handler 实现函数（`getUIAgentMemory`、`saveUIAgentMemory`、`deleteUIAgentMemory` 及其辅助函数）
4. 删除 `UIAgentMemoryDetail` 类型定义（如果只被 agent handler 使用）
5. 删除不再需要的 import

**文件**：`internal/module/memory/ui_rpc.go`

1. 删除 `UIAgentMemoryScope` 和 `UIAgentMemoryEntry` 类型定义
2. 删除 `loadUIAgentMemoryScopes` 函数
3. 在 `buildUIMemorySnapshot` 中删除 `AgentScopes` 字段赋值
4. 从 `UIMemorySnapshot` struct 中删除 `AgentScopes` 字段

**验证**：`go build ./internal/module/memory/...`

---

#### Task A2 — 删除 domain_bridges.go 中的 agent bridge

**文件**：`internal/module/memory/domain_bridges.go`

1. 找到 `// ==== agent-memory bridge ====` 分隔注释
2. 删除从该注释到 `// ==== team-memory bridge ====` 之间的所有代码（AgentMemoryManager struct + 所有方法 + AgentMemoryPromptProvider struct + 所有方法 + NewAgentMemoryManager + NewAgentMemoryPromptProvider + 错误变量）
3. 删除 `memagent` import
4. 更新文件头注释：去掉 agent-memory 相关描述

**验证**：`go build ./internal/module/memory/...`（此时会有 module.go 编译错误，Task A3 修复）

---

#### Task A3 — 更新 module.go 移除 agent 依赖

**文件**：`internal/module/memory/module.go`

1. 删除 `memagent` import
2. 从 `promptProviderParams` struct 中删除 `AgentProvider` 字段
3. 删除 `fx.Options(memagent.Module)` 行
4. 删除以下 4 个 provide 函数的 fx.Provide 注册和函数定义：
   - `provideAgentMemoryConfig`
   - `provideAgentMemoryPathHelper`
   - `provideAgentMemoryPromptBuilder`
   - `provideAgentMemoryGateResolver`
5. 在 `registerPromptProviders` 中删除 AgentProvider 相关的注册逻辑

**文件**：`internal/module/memory/config.go`

1. 搜索 `IsAgentMemoryPath` 和 `AgentMemory` 相关配置
2. 删除不再需要的配置字段和函数

**文件**：`internal/module/memory/rules_provider.go`

1. 搜索 AgentProvider 的 DynamicSectionProvider 注册
2. 删除相关代码

**验证**：`go build ./internal/module/memory/...`

---

#### Task A4 — 删除 agent 子包目录

1. 确认 Task A1-A3 完成后，全项目无残余引用：
   ```bash
   grep -r "module/memory/agent" . --include="*.go" | grep -v _test.go
   ```
2. 删除 `internal/module/memory/agent/` 整个目录
3. 全项目构建验证：`go build ./...`
4. 检查 memory 包非测试文件数：
   ```bash
   ls internal/module/memory/*.go | grep -v _test.go | wc -l
   ```
5. 根据文件数更新 `internal/archtest/freeze_registry.go` 中 memory 的 freeze limit
6. 运行 archtest：`go test ./internal/archtest/...`

---

### Phase B：前端 UI 改造

---

#### Task B1 — 删除 Agent 记忆 UI 区域

**文件**：`cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js`

1. 删除 template 中 Agent 记忆的整个渲染区域（搜索 `agentScopes`、`agentEditor`、`agentDeleteTarget` 找到所有相关模板代码）
2. 删除 data/computed 中的 agent 相关状态：
   - `agentDeleteTarget`
   - `showAllScopes` / `expandedEmptyScopes`
   - `filterAgentScopes` computed
   - `nonEmptyAgentScopes` / `visibleAgentScopes` computed
3. 删除 methods 中的 agent 相关方法：
   - `askAgentDelete` / `cancelAgentDelete` / `confirmAgentDelete`
   - `toggleAllScopes` / `toggleEmptyScope` / `isScopeExpanded`
   - `scopeTitle`
4. 删除 `useAgentMemoryEditor` 的导入和调用

**文件**：`cmd/agent-terminal/frontend/vue-app/composables/useMemoryEditors.js`

1. 删除 `useAgentMemoryEditor` 函数导出
2. 删除 `resetAgentForm` 函数导出
3. 保留 `useDurableMemoryEditor` 和 `useInlineDeleteConfirm`（durable memory 仍在使用）

**验证**：启动 agent-terminal，记忆中心页面不再显示 Agent 记忆区域，无 JS 控制台报错

---

#### Task B2 — 按 type 分组显示（偏好 / 项目）

**文件**：`cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js`

将 private 和 team 两个 section 合并为按 type 分组的两个 section：

1. 新增 computed 属性 `preferenceEntries` 和 `projectEntries`：
   ```js
   preferenceEntries() {
     const priv = (this.model.private?.entries || [])
       .filter(e => e.type === 'user' || e.type === 'feedback')
       .map(e => ({ ...e, scope: 'private' }))
     const team = (this.model.team?.entries || [])
       .filter(e => e.type === 'user' || e.type === 'feedback')
       .map(e => ({ ...e, scope: 'team' }))
     return [...priv, ...team]
   },
   projectEntries() {
     const priv = (this.model.private?.entries || [])
       .filter(e => e.type === 'project' || e.type === 'reference')
       .map(e => ({ ...e, scope: 'private' }))
     const team = (this.model.team?.entries || [])
       .filter(e => e.type === 'project' || e.type === 'reference')
       .map(e => ({ ...e, scope: 'team' }))
     return [...priv, ...team]
   }
   ```

2. 替换 template 中 private 和 team 的两个 section 为两个新 section：
   - "偏好" section 显示 `preferenceEntries`
   - "项目" section 显示 `projectEntries`

3. 每个条目卡片增加 scope 标签：
   ```html
   <span class="memory-scope-badge">{{ entry.scope === 'team' ? 'Team' : 'Private' }}</span>
   ```

4. 搜索过滤逻辑（`filterEntries`）改为对新的 computed 列表过滤

5. 新建/编辑时需要知道 target（private/team），根据条目的 scope 决定：
   - 编辑已有条目：用条目自身的 scope（private 或 team）
   - 新建条目：默认 private

**验证**：记忆中心显示"偏好"和"项目"两个分组，各条目带 scope 标签

---

#### Task B3 — 简化新建类型选择

**文件**：`cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js`

1. 找到新建 durable memory 的类型选择 UI（搜索 memoryEditor 的 type 选择下拉框）
2. 将 4 个选项（user/feedback/project/reference）改为 2 个：
   - "偏好" → 后端写 `type: "feedback"`
   - "项目" → 后端写 `type: "project"`
3. 编辑已有条目时，类型显示为对应的中文名但不可修改（已有的 user/reference 条目保持原 type）

**文件**：`cmd/agent-terminal/frontend/vue-app/composables/useMemoryEditors.js`

1. 找到 `memoryTemplateForType` 函数
2. 将 4 种模板映射为 2 种（偏好→feedback 模板，项目→project 模板）

**验证**：新建按钮只显示"偏好"和"项目"两个选项

---

#### Task B4 — 更新 app.js 数据结构

**文件**：`cmd/agent-terminal/frontend/vue-app/app.js`

1. 从 `EMPTY_MEMORY_CENTER` 删除 `agentScopes: []`
2. 在 `applyMemoryCenterSnapshot` 中删除 agentScopes 的赋值
3. `resetMemoryCenterState` 中删除 agentScopes 的重置

**验证**：`refreshMemoryCenterState` 调用后 memoryCenter 不再包含 agentScopes 字段

---

## 执行顺序与依赖

```
Phase A（后端清理，严格顺序）
  Task A1 → A2 → A3 → A4

Phase B（前端改造，A4 完成后开始）
  Task B1 → B2 → B3
  Task B4（独立，可与 B1-B3 并行）
```

Phase A 必须先完成，因为：
- A1 删除 agent RPC 端点后，前端调用这些端点的代码才能安全删除
- A4 删除后端 agent 子包后，`ui/memory/get` 返回的 snapshot 不再包含 agentScopes

## 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| 删除 agent 子包后 memory 主包文件数变化 | archtest freeze 可能需要调整 | Task A4 步骤 5 显式检查并更新 |
| 前端删除 Agent 区域后留下孤立的 CSS | 页面样式异常 | 搜索 `memory-agent-` 前缀的 CSS class，确认全部移除 |
| 编辑已有 user/reference 条目时类型显示 | 可能显示不在新选项里的类型 | 编辑时类型字段只读，显示原始类型的中文映射（user→偏好, reference→项目） |
| domain_bridges.go 删除 agent bridge 后行数减少 | freeze 限额可能变为死键 | Task A4 中统一处理 |
| 新建时 target（private/team）的确定 | 用户可能想写到 team | 新建默认 private；如果 team 可用，提供 scope 选择（与已有的 private/team 新建按钮逻辑一致） |
