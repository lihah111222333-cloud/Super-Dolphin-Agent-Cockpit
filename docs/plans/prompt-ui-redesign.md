# 提示词管理页面 UI 改版实施计划

> 状态: 待执行
> 创建: 2026-05-01
> 范围: 仅前端 + 后端 RPC 结构体小幅扩展（添加 tags 字段）

---

## 目标

将 System 提示词管理页面从面向开发者的技术界面改为面向消费端用户的友好界面：
- Tab 分类从"主 Agent / 子 Agent"改为按用户自定义角色（程序员/产品经理/设计师/自定义）
- 编辑体验简化：普通用户只看到名称、描述、角色、场景标签、提示词内容
- 技术字段（match_when JSON / priority / sections / enable_when）藏进折叠的"高级设置"
- 保存时自动从场景标签生成 match_when.tags_has

## 不改的部分

- 智能分类器开关 + "设为启动"交互保持原样
- 后端三层匹配逻辑（pin → match_when → classifier → default）不动
- 后端 API 端点 URL 不变，仅在现有 struct 中添加 optional 字段

---

## 前置发现

`tags` 字段在后端 DB 中存在（`prompt_templates.tags JSONB`），store.Upsert 和 SQL 都支持读写，但 **RPC 层完全未暴露**：
- `promptWriteParams` 无 tags 字段 → 前端写不进
- `promptRPCItem` 无 tags 字段 → 前端读不出
- 当前 tags 仅内部用于 scope 标记（`scope://cwd:...`）

必须先做 Task 0 打通 tags，否则场景标签只能存在前端本地，分类器（Layer 3）无法消费。

---

## 文件结构

### 修改的文件

| 文件 | 改动量 | 职责 |
|------|--------|------|
| `internal/module/prompt/service_surface.go` | ~20 行 | RPC 结构体添加 tags 字段 |
| `cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js` | 重写 ~60% | 主页面：角色 Tab + 简化编辑器 + 新卡片 |
| `cmd/agent-terminal/frontend/vue-app/styles/system-prompt.css` | 重写 ~40% | 新增角色栏 + 标签 + 折叠区样式 |

### 新建的文件

| 文件 | 行数估算 | 职责 |
|------|----------|------|
| `vue-app/components/RoleBar.js` | ~180 行 | 角色横栏组件（展示 + 新建角色弹窗） |
| `vue-app/components/TagInput.js` | ~120 行 | 场景标签输入组件（chips + 输入框） |

---

## 任务列表

### Task 0: 后端 RPC 结构体添加 tags 字段

**文件**: `internal/module/prompt/service_surface.go`

**目标**: 在 `promptWriteParams` 和 `promptRPCItem` 中添加 `tags` 字段，使前端可以读写 tags。

**步骤**:

1. 在 `promptWriteParams` (行 62-71) 添加 Tags 字段:
```go
type promptWriteParams struct {
    ID          string          `json:"id,omitempty"`
    Name        string          `json:"name"`
    Content     string          `json:"content,omitempty"`
    Description string          `json:"description,omitempty"`
    AgentType   string          `json:"agentType,omitempty"`
    Cwd         string          `json:"cwd,omitempty"`
    MatchWhen   json.RawMessage `json:"match_when,omitempty"`
    Priority    int             `json:"priority,omitempty"`
    Tags        json.RawMessage `json:"tags,omitempty"`        // ← 新增
}
```

2. 在 `PromptWriteRequest` (行 31-38) 添加 Tags 字段:
```go
type PromptWriteRequest struct {
    ID, Name, Content, Description, AgentType string
    MatchWhen json.RawMessage
    Priority  int
    Tags      json.RawMessage  // ← 新增
}
```

3. 在 handler 映射处 (行 177-186) 传递 Tags:
```go
template, err := promptSvc.WritePrompt(ctx, p.Cwd, PromptWriteRequest{
    // ...existing fields...
    Tags: p.Tags,  // ← 新增
})
```

4. 在 `buildPromptTemplate` 函数 (service.go 行 378-408) 中合并客户端 tags 与 scope tags:
```go
// 新建时
clientTags := p.Tags
if len(clientTags) == 0 {
    clientTags = json.RawMessage("[]")
}
template.Tags = withPromptScopeTag(clientTags, promptScopeForWrite(current, cwd))

// 更新时：合并客户端 tags 和现有 scope tags
if len(p.Tags) > 0 {
    template.Tags = withPromptScopeTag(p.Tags, promptScopeForWrite(current, cwd))
} else {
    template.Tags = withPromptScopeTag(current.Tags, promptScopeForWrite(current, cwd))
}
```

5. 在 `promptRPCItem` (行 113-123) 添加 Tags 字段:
```go
type promptRPCItem struct {
    ID          string          `json:"id"`
    Name        string          `json:"name"`
    Content     string          `json:"content"`
    Description string          `json:"description"`
    AgentType   string          `json:"agentType"`
    CreatedAt   time.Time       `json:"createdAt"`
    UpdatedAt   time.Time       `json:"updatedAt"`
    MatchWhen   json.RawMessage `json:"match_when,omitempty"`
    Priority    int             `json:"priority,omitempty"`
    Tags        json.RawMessage `json:"tags,omitempty"`        // ← 新增
}
```

6. 在 `toRPCItem()` 转换函数中填充 Tags（从 store 读回的 template.Tags 中过滤掉 scope:// 前缀的内部 tag，只返回用户可见的 tag）。

**验证**: `go build ./internal/module/prompt/...` 编译通过 + 手动 RPC 调用 prompts/write 携带 tags 后 prompts/list 能读回。

---

### Task 1: TagInput 组件

**文件**: `cmd/agent-terminal/frontend/vue-app/components/TagInput.js`

**功能**: 标签输入组件，支持添加/删除标签 chips。

**Props**:
- `modelValue: Array` — 标签数组 `["代码审查", "bug"]`
- `placeholder: String` — 输入提示
- `disabled: Boolean` — 禁用状态

**Emit**: `update:modelValue`

**交互**:
- 输入框键入文本 → 按 Enter 或逗号添加标签
- 标签 chip 右侧 × 按钮删除
- 重复标签自动忽略
- 空白标签自动忽略

**渲染结构**:
```html
<div class="sp-tag-input" :class="{ disabled }">
  <span class="sp-tag-chip" v-for="tag in modelValue">
    {{ tag }}
    <button class="sp-tag-remove" @click="remove(tag)">×</button>
  </span>
  <input
    v-model="inputText"
    @keydown.enter.prevent="add"
    @keydown.,="add"
    :placeholder="placeholder"
    :disabled="disabled"
  />
</div>
```

**验证**: 手动测试添加/删除/去重。

---

### Task 2: RoleBar 组件

**文件**: `cmd/agent-terminal/frontend/vue-app/components/RoleBar.js`

**功能**: 角色横栏 — 展示角色卡片 + 全部 + 新建角色入口。

**Props**:
- `roles: Array` — `[{key, name, icon}]`
- `activeKey: String` — 当前选中角色 key，`'all'` 表示全部
- `promptCounts: Object` — `{roleKey: number}` 每个角色下提示词数量

**Emit**:
- `select(roleKey)` — 点击角色
- `create-role` — 点击"+ 新建角色"
- `edit-role(role)` — 长按或右键编辑角色
- `delete-role(role)` — 删除角色

**数据来源**: 角色列表存储在 `ui/preferences`，key = `settings.promptRoles`。

**默认角色**（首次加载 preferences 为空时初始化）:
```json
[
  {"key": "coder", "name": "程序员", "icon": "💻"},
  {"key": "pm", "name": "产品经理", "icon": "📋"},
  {"key": "designer", "name": "设计师", "icon": "🎨"}
]
```

**渲染结构**:
```html
<div class="sp-role-bar">
  <div class="sp-role-card"
       v-for="role in roles"
       :class="{ active: activeKey === role.key }"
       @click="$emit('select', role.key)">
    <span class="sp-role-icon">{{ role.icon }}</span>
    <span class="sp-role-name">{{ role.name }}</span>
    <span class="sp-role-count">{{ promptCounts[role.key] || 0 }} 条</span>
  </div>
  <div class="sp-role-card" :class="{ active: activeKey === 'all' }"
       @click="$emit('select', 'all')">
    <span class="sp-role-icon">📂</span>
    <span class="sp-role-name">全部</span>
    <span class="sp-role-count">{{ totalCount }} 条</span>
  </div>
  <div class="sp-role-card sp-role-add" @click="$emit('create-role')">
    <span class="sp-role-icon">+</span>
    <span class="sp-role-name">新建角色</span>
  </div>
</div>
```

**角色编辑弹窗**（内联在 RoleBar 中，轻量实现）:

点击"新建角色"或编辑角色时弹出小弹窗：
- 角色名称输入框
- Emoji 选择：预设 20 个常用 emoji 网格（💻📋🎨🔧📊🧪📝🎯🛠️📦🔍🌐🤖📐💡🔒🎮📈🧩⚙️），点击选中
- 保存/取消/删除按钮

角色 key 自动从 name 生成（`name.toLowerCase().replace(/\s+/g, '-')`），不让用户手动输入。

**验证**: 手动测试角色 CRUD + tab 切换 + 计数。

---

### Task 3: 重构 SystemPromptPage — Tab 系统替换

**文件**: `cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js`

**改动**:

1. **替换 activeTab 逻辑**:
   - 旧: `activeTab` = `'main'` | `'sub'` | `'all'`
   - 新: `activeRoleKey` = 角色的 key（如 `'coder'`）| `'all'`

2. **新增角色状态**:
```javascript
const roles = ref([])  // 从 preferences 加载
const PREF_KEY_ROLES = 'settings.promptRoles'
const DEFAULT_ROLES = [
  { key: 'coder', name: '程序员', icon: '💻' },
  { key: 'pm', name: '产品经理', icon: '📋' },
  { key: 'designer', name: '设计师', icon: '🎨' },
]
```

3. **加载角色**:
```javascript
async function loadRoles() {
  const saved = await prefGet(PREF_KEY_ROLES)
  roles.value = (saved && Array.isArray(saved)) ? saved : DEFAULT_ROLES
}
```

4. **保存角色**:
```javascript
async function saveRoles(updated) {
  roles.value = updated
  await prefSet(PREF_KEY_ROLES, updated)
}
```

5. **替换 filterPromptCards**:
   - 旧: `filterPromptCards(cards, tab)` 按 `agentType === 'main'/'sub'` 过滤
   - 新: 按 `agentType === activeRoleKey` 过滤，`'all'` 显示全部

6. **计算 promptCounts**:
```javascript
const promptCounts = computed(() => {
  const counts = {}
  for (const card of promptCards.value) {
    const key = card.agentType || 'uncategorized'
    counts[key] = (counts[key] || 0) + 1
  }
  return counts
})
```

7. **替换模板中的 tab 导航**:
   - 删除 `<div class="sub-tabs">` 中的三个硬编码 tab
   - 替换为 `<role-bar>` 组件

**验证**: Tab 切换过滤正确，角色 CRUD 持久化到 preferences。

---

### Task 4: 重构 SystemPromptPage — 编辑器简化

**文件**: `cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js`

**改动**:

1. **扩展 form 状态**:
```javascript
const form = reactive({
  id: '',
  name: '',
  content: '',
  description: '',
  agentKey: '',      // 角色 key
  tags: [],          // 场景标签数组（新增）
  // 以下移入"高级设置"折叠区，保留但默认不显示
  matchWhen: '',
  priority: 0,
})
```

2. **新增 advancedOpen 状态**:
```javascript
const advancedOpen = ref(false)
```

3. **openEdit 中解析 tags**:
```javascript
function openEdit(item) {
  // ...existing field mapping...
  // 解析 tags：过滤掉 scope:// 内部标签
  const rawTags = item.tags || []
  form.tags = rawTags.filter(t => !t.startsWith('scope://'))
  advancedOpen.value = false
}
```

4. **savePrompt 中组装 tags 和自动生成 match_when**:
```javascript
async function savePrompt() {
  const payload = {
    id: form.id || '',
    name: name,
    content: form.content || '',
    description: form.description || '',
    agentType: form.agentKey || activeRoleKey.value,
    tags: JSON.stringify(form.tags || []),
    priority: Number(form.priority) || 0,
  }

  // 自动生成 match_when（如果用户没有手动设置）
  const userMatchWhen = form.matchWhen.trim()
  if (userMatchWhen) {
    // 用户手动设置了 match_when，尊重它
    applyMatchWhenToPayload(payload, userMatchWhen)
  } else if (form.tags.length > 0) {
    // 从标签自动生成
    payload.match_when = JSON.stringify({
      tags_has: form.tags.length === 1 ? form.tags[0] : form.tags
    })
  }
  // 否则不设 match_when（不参与自动路由）

  await callAPI('prompts/write', withCwd(getCwd(), payload))
  // ...
}
```

5. **重写编辑器模板 — 基础区域**:
```html
<!-- 基础字段（始终可见） -->
<div class="sp-field">
  <label>名称</label>
  <input v-model="form.name" placeholder="给提示词起个名字" />
</div>
<div class="sp-field">
  <label>一句话描述</label>
  <input v-model="form.description" placeholder="简要说明用途" />
</div>
<div class="sp-field">
  <label>角色</label>
  <select v-model="form.agentKey">
    <option v-for="r in roles" :value="r.key">{{ r.icon }} {{ r.name }}</option>
  </select>
</div>
<div class="sp-field">
  <label>场景标签</label>
  <tag-input v-model="form.tags" placeholder="输入标签后按回车" />
  <span class="sp-field-hint">用于自动匹配对话场景，如"代码审查"、"需求分析"</span>
</div>
<div class="sp-field">
  <label>提示词内容</label>
  <textarea v-model="form.content" rows="12" placeholder="输入系统提示词..." />
</div>
```

6. **重写编辑器模板 — 高级设置折叠区**:
```html
<!-- 高级设置（默认折叠） -->
<div class="sp-advanced-toggle" @click="advancedOpen = !advancedOpen">
  <span>{{ advancedOpen ? '▼' : '▶' }} 高级设置（开发者选项）</span>
</div>
<div v-show="advancedOpen" class="sp-advanced-body">
  <div class="sp-field">
    <label>自动匹配条件 (JSON)</label>
    <textarea v-model="form.matchWhen" rows="3"
      placeholder='留空则从场景标签自动生成' />
    <span class="sp-field-hint">手动设置会覆盖标签自动生成的匹配条件</span>
  </div>
  <div class="sp-field">
    <label>优先级</label>
    <input type="number" v-model.number="form.priority" />
    <span class="sp-field-hint">数字越大优先级越高，同时命中多条时生效</span>
  </div>
</div>
```

7. **编辑器 Tab 简化**:
   - 删除 `[基础]` 和 `[🔧 高级调试]` 两个 editor tab
   - 基础字段 + 高级设置折叠区在同一视图中
   - Sections 编辑器移入高级设置折叠区最底部，加分隔线

**验证**:
- 不填 match_when + 有标签 → 保存后 match_when 自动生成 tags_has
- 手填 match_when → 不覆盖
- 无标签 + 无手填 → match_when 为空

---

### Task 5: 重构 SystemPromptPage — 卡片展示

**文件**: `cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js`

**改动**:

1. **normalizePromptItem 中解析 tags**:
```javascript
function normalizePromptItem(raw) {
  // ...existing normalization...
  const rawTags = (() => {
    try { return JSON.parse(raw.tags || '[]') } catch { return [] }
  })()
  return {
    // ...existing fields...
    tags: rawTags.filter(t => typeof t === 'string' && !t.startsWith('scope://')),
  }
}
```

2. **替换卡片渲染**:

旧卡片:
```
标题 [default badge]
描述
预览框（灰色背景，2行截断）
12 行 · 3244 字符               ← 删除
[编辑] [复制] [设启动] [删除]
```

新卡片:
```
角色图标 + 标题 [启动中 badge]
描述
🏷 代码审查 · bug · 质量        ← 场景标签
预览框（灰色背景，2行截断）
[编辑] [复制] [设启动] [删除]
```

**模板代码**:
```html
<div class="sp-card" :class="{ active: item.id === activePromptId }">
  <div class="sp-card-header">
    <span class="sp-card-title">
      <span class="sp-card-role-icon">{{ roleIcon(item.agentType) }}</span>
      {{ item.name }}
    </span>
    <span v-if="item.id === activePromptId" class="sp-card-badge is-active">启动中</span>
  </div>
  <div v-if="item.description" class="sp-card-desc">{{ item.description }}</div>
  <div v-if="item.tags?.length" class="sp-card-tags">
    <span class="sp-card-tag" v-for="tag in item.tags">{{ tag }}</span>
  </div>
  <div class="sp-card-preview">
    <pre>{{ truncate(item.content, 120) }}</pre>
  </div>
  <div class="sp-card-actions">
    <button @click="openEdit(item)">编辑</button>
    <button @click="copyPromptContent(item)">复制</button>
    <button @click="...">{{ item.id === activePromptId ? '取消启动' : '设为启动' }}</button>
    <button class="is-danger" @click="deletePrompt(item)">删除</button>
  </div>
</div>
```

3. **新增 roleIcon 辅助函数**:
```javascript
function roleIcon(agentType) {
  const role = roles.value.find(r => r.key === agentType)
  return role ? role.icon : '📄'
}
```

**验证**: 卡片展示角色图标和标签，不再显示行数/字符数。

---

### Task 6: CSS 样式更新

**文件**: `cmd/agent-terminal/frontend/vue-app/styles/system-prompt.css`

**新增样式**:

1. **角色横栏**:
```css
.sp-role-bar {
  display: flex;
  gap: 10px;
  padding: 8px 16px;
  overflow-x: auto;
  scrollbar-width: thin;
}
.sp-role-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 10px 16px;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  background: var(--card);
  cursor: pointer;
  min-width: 80px;
  transition: all var(--transition);
}
.sp-role-card:hover { border-color: var(--border-hover); }
.sp-role-card.active {
  border-color: var(--primary);
  background: rgba(78, 117, 200, 0.1);
}
.sp-role-icon { font-size: 24px; }
.sp-role-name { font-size: 12px; color: var(--text); margin-top: 4px; }
.sp-role-count { font-size: 10px; color: var(--text-muted); }
.sp-role-add { border-style: dashed; }
```

2. **标签输入**:
```css
.sp-tag-input {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--card);
  min-height: 36px;
  align-items: center;
}
.sp-tag-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(78, 117, 200, 0.15);
  color: var(--text);
  font-size: 12px;
}
.sp-tag-remove {
  cursor: pointer;
  color: var(--text-muted);
  border: none;
  background: none;
  font-size: 14px;
  padding: 0 2px;
}
.sp-tag-input input {
  border: none;
  background: none;
  outline: none;
  color: var(--text);
  font-size: 12px;
  flex: 1;
  min-width: 80px;
}
```

3. **卡片标签行**:
```css
.sp-card-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  padding: 0 2px;
}
.sp-card-tag {
  font-size: 11px;
  padding: 1px 8px;
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.12);
  color: var(--text-muted);
}
.sp-card-role-icon {
  margin-right: 6px;
}
```

4. **高级设置折叠区**:
```css
.sp-advanced-toggle {
  padding: 10px 0;
  cursor: pointer;
  color: var(--text-muted);
  font-size: 12px;
  user-select: none;
  border-top: 1px solid var(--border);
  margin-top: 12px;
}
.sp-advanced-toggle:hover { color: var(--text); }
.sp-advanced-body {
  padding: 8px 0;
  border-top: 1px solid var(--border);
}
```

5. **字段提示文本**:
```css
.sp-field-hint {
  font-size: 11px;
  color: var(--text-subtle);
  margin-top: 2px;
}
.sp-field select {
  padding: 6px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--card);
  color: var(--text);
  font-size: 13px;
}
```

6. **删除旧样式**:
   - `.sp-card-meta`（行数·字符数） — 移除
   - `.sub-tabs` 中硬编码的三个 tab 样式保留但被新组件覆盖

**验证**: 暗色主题下各组件视觉正确，移动端响应式正常。

---

### Task 7: 端到端验证

1. **自动匹配链路**:
   - 创建提示词，角色=程序员，标签=["代码审查"]
   - 保存后检查 DB: tags 包含 "代码审查"，match_when 包含 `{"tags_has":"代码审查"}`
   - 新建对话，首条消息包含"代码审查" → Layer 2 命中该提示词

2. **分类器链路**:
   - 开启智能分类器
   - 创建提示词，名称="SQL专家"，描述="擅长复杂查询优化"，标签=["sql","数据库"]
   - 新建对话，首条消息="帮我优化这个 SQL 查询"
   - 检查分类器是否能看到 tags 字段并正确匹配

3. **角色管理**:
   - 新建角色"运营"（emoji: 📊）
   - 创建提示词选择"运营"角色
   - 切换角色 Tab → 正确过滤
   - 刷新页面 → 角色和提示词都持久化

4. **向后兼容**:
   - 现有 agentType='main' 的提示词 → 在"全部"中可见
   - 不设角色的提示词 → 在"全部"中可见

5. **高级设置**:
   - 展开高级设置 → 手写 match_when JSON → 保存成功
   - 手写 match_when 优先于标签自动生成
   - Sections 编辑器在高级设置区底部正常工作

---

## 任务依赖图

```
Task 0 (后端 tags)
    ↓
Task 1 (TagInput)  ──→  Task 4 (编辑器简化) ──→ Task 7 (验证)
                          ↑                         ↑
Task 2 (RoleBar)  ──→  Task 3 (Tab 替换) ──────────┘
                          ↑
                   Task 5 (卡片展示)
                          ↑
                   Task 6 (CSS)
```

可并行的：Task 1 和 Task 2 互不依赖。Task 5 和 Task 6 可与 Task 3/4 并行推进。

---

## 风险

1. **现有提示词的 agentType 不匹配新角色 key** — 通过"全部" Tab 兜底，不丢数据
2. **tags 字段与 scope tag 冲突** — 过滤 `scope://` 前缀，互不干扰
3. **match_when 自动生成与手动设置冲突** — 手动优先，明确提示用户
