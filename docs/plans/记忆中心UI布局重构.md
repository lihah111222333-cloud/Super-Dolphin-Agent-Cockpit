# 记忆中心 UI 布局重构

> 执行本计划前，先使用超能力：子代理驱动开发 或 执行计划。

## Goal

将记忆中心页面从纵向卡片堆叠布局重构为 Bento Grid + Tab 切换 + 侧滑编辑面板的现代布局。所有 CSS token 色值不变，仅改布局和交互结构。数据层（API、props、composable）完全不动。

## Architecture

```
app.js (不改)
  └─ <MemoryCenterPage :model="memoryCenter" />  (重写模板+新增少量逻辑)
       ├─ useDurableMemoryEditor (不改)
       └─ useInlineDeleteConfirm (不改)

memory-center.css (重写)
  └─ 引用 tokens.css 变量 (不改)
```

## Tech Stack

- Vue 3 (Options API + setup(), 字符串模板)
- 原生 CSS（CSS 变量 `var(--xxx)` 引用 tokens.css）
- 无构建工具，直接 ESM import

## File Map

| 文件 | 操作 | 职责 |
|------|------|------|
| `cmd/agent-terminal/frontend/vue-app/styles/memory-center.css` | **重写** | Bento Grid、Tab、侧滑面板、条目卡片、响应式样式 |
| `cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js` | **重写** | 新模板结构 + activeTab/createDropdown 逻辑 |
| `cmd/agent-terminal/frontend/vue-app/composables/useMemoryEditors.js` | 不改 | — |
| `cmd/agent-terminal/frontend/vue-app/app.js` | 不改 | — |
| `cmd/agent-terminal/frontend/vue-app/styles/tokens.css` | 不改 | — |

---

## Task 1: 重写 memory-center.css — 基础布局骨架

**Files:**
- 修改: `cmd/agent-terminal/frontend/vue-app/styles/memory-center.css`

**步骤:**

- [ ] 1.1 备份现有 memory-center.css 内容（在终端 `cp` 一份到 `/tmp/`），然后清空文件
- [ ] 1.2 写入新的 CSS，包含以下区块（按顺序）：

**页面布局：**
```css
.mc-body { display: grid; gap: 12px; }
```

**标题栏工具栏：**
```css
.mc-toolbar { display: flex; align-items: center; gap: 8px; ... }
.mc-toolbar-icon { /* M 图标容器 28x28, border-radius:8px, background: var(--primary-dim) */ }
.mc-search { /* 搜索框，复用现有 .memory-center-search 结构 */ }
.mc-create-dropdown { /* 新建按钮下拉容器 */ }
.mc-create-menu { /* 下拉菜单绝对定位 */ }
```

**Bento Grid：**
```css
.mc-bento { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; padding: 0; }
.mc-bento.mc-bento-2col { grid-template-columns: repeat(2, 1fr); } /* R3: health 为 null 时降为 2 列 */
.mc-bento-card { border: 1px solid var(--border); border-radius: 10px; padding: 14px 16px; background: var(--card); }
.mc-bento-card::before { /* 顶部 1px 渐变光线 */ }
.mc-bento-card:hover { border-color: var(--border-hover); transform: translateY(-1px); }
.mc-bento-label { font-size: 11px; color: var(--text-muted); }
.mc-bento-num { font-size: 28px; font-weight: 700; color: var(--text); }
.mc-bento-sub { font-size: 11px; color: var(--text-muted); }
```

**健康度进度条：**
```css
.mc-health-row { display: flex; align-items: center; gap: 8px; }
.mc-health-track { flex: 1; height: 6px; background: var(--hover); border-radius: 3px; }
.mc-health-fill { height: 100%; border-radius: 3px; transition: width 0.5s ease; }
.mc-health-fill.health-bar-warning { background: var(--warning); }
.mc-health-fill.health-bar-danger { background: var(--error); }
```

**自动沉淀状态点脉冲：**
```css
.mc-status-dot { width: 8px; height: 8px; border-radius: 50%; }
.mc-status-dot.on { background: var(--success); box-shadow: 0 0 8px color-mix(in srgb, var(--success) 35%, transparent); animation: mc-pulse 2.5s infinite; }
@keyframes mc-pulse { 0%,100%{opacity:1} 50%{opacity:0.45} }
```

**相似记忆警告条：**
```css
.mc-similar-bar { padding: 10px 16px; border-radius: 10px; border: 1px solid color-mix(in srgb, var(--warning) 15%, var(--border)); background: linear-gradient(135deg, color-mix(in srgb, var(--warning) 6%, transparent), transparent 70%); }
```

**Tab 栏：**
```css
.mc-tabs { display: flex; gap: 0; border-bottom: 1px solid var(--border); overflow-x: auto; -webkit-overflow-scrolling: touch; } /* O1: 窄屏防溢出 */
.mc-tab { padding: 8px 20px 12px; font-size: 13px; color: var(--text-muted); cursor: pointer; border-bottom: 2.5px solid transparent; transition: all 0.2s; white-space: nowrap; }
.mc-tab.active { color: var(--text); border-bottom-color: var(--primary); }
.mc-tab-count { padding: 1px 8px; border-radius: 999px; background: var(--hover); font-size: 11px; }
.mc-tab.active .mc-tab-count { background: var(--primary-dim); color: var(--primary); }
```

**条目卡片：**
```css
.mc-entry-grid { display: grid; gap: 10px; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); }
.mc-entry-card { border: 1px solid var(--border); border-radius: 10px; padding: 14px 16px 14px 20px; background: var(--card); position: relative; overflow: hidden; transition: all 0.25s; }
.mc-entry-card:hover { border-color: var(--border-hover); transform: translateY(-2px); box-shadow: 0 6px 20px rgba(0,0,0,0.2); }
.mc-entry-card::before { /* 左侧 3px 彩条 */ content: ''; position: absolute; left: 0; top: 10px; bottom: 10px; width: 3px; border-radius: 2px; }
.mc-entry-card.type-pref::before { background: var(--warning); }
.mc-entry-card.type-proj::before { background: var(--primary); }
.mc-entry-actions { opacity: 0; transition: opacity 0.2s; }
.mc-entry-card:hover .mc-entry-actions { opacity: 1; }
```

**预览区（复用现有渐隐逻辑）：**
```css
.mc-entry-preview { /* 基本同现有 .memory-entry-preview，max-height:3.6em, ::after 渐隐 */ }
.mc-entry-preview.is-expanded { max-height: none; }
.mc-entry-preview.is-expanded::after { opacity: 0; }
```

**侧滑编辑面板：**
```css
.mc-panel-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.35); z-index: 9999; }
.mc-panel { position: fixed; top: 0; right: 0; bottom: 0; width: min(420px, 45vw); background: var(--surface); border-left: 1px solid var(--border); transform: translateX(100%); transition: transform 0.3s ease; z-index: 10000; overflow-y: auto; padding: 20px 24px; }
.mc-panel.is-open { transform: translateX(0); }
.mc-panel-head { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 16px; }
.mc-form-row { margin-bottom: 12px; }
.mc-form-label { font-size: 11px; color: var(--text-muted); margin-bottom: 4px; display: block; }
.mc-form-textarea { min-height: 160px; resize: vertical; }
```

**删除/整合确认弹窗（保持居中 modal，z-index 高于侧滑面板）：**
```css
/* 复用现有 .modal-overlay / .modal-box / .memory-modal 样式 */
/* R1: 弹窗 z-index 必须高于侧滑面板(10000)，避免被遮挡 */
.mc-modal-overlay { z-index: 10001; }
.mc-modal-overlay .modal-box { z-index: 10002; }
```

**按钮覆盖（同现有 #page-memory-center 作用域）：**
```css
#page-memory-center .btn-primary { color: #111; }
#page-memory-center .btn-danger { /* 复用现有红色样式 */ }
```

**响应式断点：**
```css
@media (max-width: 960px) {
  .mc-bento { grid-template-columns: 1fr 1fr; }
  .mc-bento-card:last-child { grid-column: 1 / -1; }
  .mc-entry-grid { grid-template-columns: 1fr; }
  .mc-panel { width: min(380px, 50vw); }
}
@media (max-width: 720px) {
  .mc-bento { grid-template-columns: 1fr; }
  .mc-bento-num { font-size: 22px; } /* O3: 单列时缩小数字 */
  .mc-panel { width: 100%; }
  /* O2: 搜索框图标展开式，默认收起，点击展开 */
  .mc-search-wrap .mc-search-input { width: 0; padding: 0; border: none; overflow: hidden; transition: width 0.25s ease, padding 0.25s ease; }
  .mc-search-wrap.is-open .mc-search-input { width: 180px; padding: 0 12px 0 32px; border: 1px solid var(--border); }
  .mc-search-wrap .mc-search-toggle { display: flex; } /* 窄屏显示搜索图标 */
}
```

**Notice 和空状态（复用+微调）：**
```css
.mc-notice { /* 同现有 .settings-prompt-notice + .memory-notice-fade */ }
.mc-empty { /* 同现有 .memory-empty 结构 */ }
```

- [ ] 1.3 确认所有颜色值仅使用 `var(--xxx)` 和 `color-mix()`，无硬编码色值（`#111` 按钮文字色除外）

**验收标准:** CSS 文件语法正确无报错，所有类名以 `mc-` 为前缀避免与其他页面冲突。

---

## Task 2: 重写 MemoryCenterPage.js — setup 逻辑

**Files:**
- 修改: `cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js`

**步骤:**

- [ ] 2.1 保留现有 import、props、emits 声明不变（import 补充 `onMounted`）
- [ ] 2.2 保留所有现有 computed/ref/函数，新增以下逻辑：

```javascript
// Tab 切换
const activeTab = ref('pref'); // 'pref' | 'proj' | 'all'

// 当前 tab 展示的条目（消除模板重复的关键）
const visibleEntries = computed(() => {
  const search = searchText.value;
  if (activeTab.value === 'pref') return filterEntries(preferenceEntries.value, search);
  if (activeTab.value === 'proj') return filterEntries(projectEntries.value, search);
  return filterEntries([...preferenceEntries.value, ...projectEntries.value], search);
});

// 新建 dropdown 开关
const createMenuOpen = ref(false);

function switchTab(tab) { activeTab.value = tab; }

function toggleCreateMenu() { createMenuOpen.value = !createMenuOpen.value; }

function handleCreatePreference() {
  createMenuOpen.value = false;
  createPreference();   // 复用现有函数
}

function handleCreateProject() {
  createMenuOpen.value = false;
  createProject();      // 复用现有函数
}

// O4: 全局点击关闭 dropdown——覆盖 header 和 body 区域
function closeCreateMenu() { createMenuOpen.value = false; }
onMounted(() => {
  document.addEventListener('click', closeCreateMenu);
});
onBeforeUnmount(() => {
  document.removeEventListener('click', closeCreateMenu);
});

// 窄屏搜索展开/折叠
const searchExpanded = ref(false);
function toggleSearch() { searchExpanded.value = !searchExpanded.value; }
```

- [ ] 2.3 在 return 对象中补充新增的变量和函数
- [ ] 2.4 删除不再需要的 return 项：`filteredPreferenceEntries`、`filteredProjectEntries`（被 `visibleEntries` 替代），`guideCollapsed`、`toggleGuide`（指引卡移除）

**验收标准:** setup 函数返回所有模板需要的变量，无 JS 语法错误。

---

## Task 3: 重写 MemoryCenterPage.js — 模板结构

**Files:**
- 修改: `cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js`

**步骤:**

- [ ] 3.1 重写 template 字符串，新结构如下：

```
<section id="page-memory-center" class="page active mc-page" data-testid="memory-center-page">

  <!-- 标题栏 -->
  <div class="panel-header">
    <div class="ph-bar"></div>
    <div class="ph-text">
      <h2><span class="mc-toolbar-icon">M</span> 记忆中心</h2>
    </div>
    <div class="mc-toolbar">
      <!-- 搜索框（窄屏可折叠） -->
      <div class="mc-search-wrap" :class="{ 'is-open': searchExpanded }">
        <button class="mc-search-toggle" @click="toggleSearch">🔍</button>
        <input v-model="searchText" class="mc-search-input" data-testid="memory-center-search" placeholder="搜索..." />
        <button v-if="searchText" class="mc-search-clear" @click="clearSearch">×</button>
      </div>
      <!-- 刷新 -->
      <button class="btn btn-ghost btn-toolbar-sm" data-testid="memory-center-refresh" :disabled="refreshing" @click="handleRefresh">
        <span v-if="refreshing" class="memory-refresh-spin"></span>
        {{ refreshing ? '刷新中' : '刷新' }}
      </button>
      <!-- 新建 dropdown -->
      <div class="mc-create-dropdown" @click.stop>
        <button class="btn btn-primary btn-toolbar-sm" @click="toggleCreateMenu">+ 新建 ▾</button>
        <div v-if="createMenuOpen" class="mc-create-menu">
          <button class="mc-create-option" @click="handleCreatePreference">新建偏好</button>
          <button class="mc-create-option" @click="handleCreateProject">新建项目</button>
        </div>
      </div>
    </div>
  </div>

  <!-- 主体 -->
  <div class="panel-body mc-body" @click="closeCreateMenu">

    <!-- Bento Grid 统计区（R3: health 为 null 时动态 2 列） -->
    <div class="mc-bento" :class="{ 'mc-bento-2col': !health }">
      <!-- 卡片1: 总览 -->
      <div class="mc-bento-card">
        <div class="mc-bento-label">
          <svg ...>...</svg> 总览
        </div>
        <div class="mc-bento-num">{{ totalEntries }}</div>
        <div class="mc-bento-sub">
          <span><span class="mc-dot mc-dot-pref"></span>{{ preferenceEntries.length }} 偏好</span>
          <span><span class="mc-dot mc-dot-proj"></span>{{ projectEntries.length }} 项目</span>
        </div>
      </div>
      <!-- 卡片2: 健康度 -->
      <div v-if="health" class="mc-bento-card">
        <div class="mc-bento-label">
          <svg ...>...</svg> 健康度
        </div>
        <div class="mc-health-row">
          <span class="mc-health-lbl">偏好</span>
          <div class="mc-health-track">
            <div class="mc-health-fill" :class="healthBarClass(healthPrefPercent)" :style="{ width: healthPrefPercent + '%' }"></div>
          </div>
          <span class="mc-health-val">{{ health.preferenceCount }} / {{ health.maxPerCategory }}</span>
        </div>
        <!-- 项目进度条同理 -->
      </div>
      <!-- 卡片3: 自动沉淀 -->
      <div class="mc-bento-card">
        <div class="mc-bento-label">
          <svg ...>...</svg> 自动沉淀
        </div>
        <div class="mc-auto-status">
          <span class="mc-status-dot" :class="autoDreamEnabled ? 'on' : 'off'"></span>
          {{ autoDreamStatusLabel }}
        </div>
        <div class="mc-auto-sub">对话结束后自动整理重要内容</div>
        <button class="mc-auto-toggle" :disabled="autoDreamToggling" data-testid="memory-center-auto-dream-toggle" @click="toggleAutoDream">
          {{ autoDreamEnabled ? '关闭' : '开启' }}
        </button>
        <!-- R7: 保留重启生效提示 -->
        <div v-if="autoDreamPendingRestart" class="mc-auto-pending" data-testid="memory-center-auto-dream-pending">已保存切换，重启 agent-terminal 后生效</div>
      </div>
    </div>

    <!-- 相似记忆警告条 -->
    <div v-if="health && health.similarGroups && health.similarGroups.length" class="mc-similar-bar">
      <svg ...>⚠</svg>
      <span>{{ health.similarGroups.length }} 组条目内容相似，建议整理</span>
      <!-- 点击展开已有的整合列表 -->
    </div>

    <!-- 通知、加载、错误状态 -->
    <div v-if="notice.message" class="mc-notice memory-notice-fade" :class="'is-' + notice.level" data-testid="memory-center-notice">{{ notice.message }}</div>
    <div v-if="isLoading" class="mc-notice is-info" data-testid="memory-center-loading">正在加载...</div>
    <!-- R8: 保留 model.error 展示 -->
    <div v-if="model.error" class="mc-notice is-error" data-testid="memory-center-error">{{ model.error }}</div>

    <!-- Tab 栏 -->
    <div class="mc-tabs">
      <div class="mc-tab" :class="{ active: activeTab === 'pref' }" @click="switchTab('pref')">
        <span class="mc-dot mc-dot-pref"></span>
        偏好 <span class="mc-tab-count">{{ preferenceEntries.length }}</span>
      </div>
      <div class="mc-tab" :class="{ active: activeTab === 'proj' }" @click="switchTab('proj')">
        <span class="mc-dot mc-dot-proj"></span>
        项目 <span class="mc-tab-count">{{ projectEntries.length }}</span>
      </div>
      <div class="mc-tab" :class="{ active: activeTab === 'all' }" @click="switchTab('all')">
        全部 <span class="mc-tab-count">{{ totalEntries }}</span>
      </div>
    </div>

    <!-- 条目列表（统一模板，消除重复） -->
    <div v-if="visibleEntries.length === 0" class="mc-empty">
      <svg class="mc-empty-illustration" ...>...</svg>
      <div class="mc-empty-title">{{ searchText ? '没有匹配的条目' : '暂无记忆' }}</div>
      <div v-if="searchText" class="mc-empty-actions">
        <button class="btn btn-secondary btn-toolbar-sm" @click="clearSearch">清空搜索</button>
      </div>
    </div>
    <div v-else class="mc-entry-grid">
      <article
        v-for="(entry, idx) in visibleEntries"
:key="entry._target + ':' + (entry.path || entry.name || idx)"
        class="mc-entry-card"
        :class="entry.type === 'project' || entry.type === 'reference' ? 'type-proj' : 'type-pref'"
      >
        <div class="mc-entry-head">
          <div class="mc-entry-title" :title="entry.name">{{ entry.name || '未命名' }}</div>
          <div class="mc-entry-badges">
            <span class="jr-badge" :class="typeBadgeClass(entry.type)">{{ typeBadgeLabel(entry.type) }}</span>
            <span class="jr-badge jr-badge-scope">{{ entry._scope === 'team' ? '团队' : '私有' }}</span>
            <span v-if="entry.source === 'dream'" class="jr-badge jr-badge-dream">梦境</span>
          </div>
        </div>
        <div v-if="entry.description" class="mc-entry-desc">{{ entry.description }}</div>
        <pre class="mc-entry-preview" @click="$event.currentTarget.classList.toggle('is-expanded')">{{ entry.preview || '暂无预览' }}</pre>
        <div class="mc-entry-foot">
          <span class="mc-entry-time">{{ formatTimestamp(entry.updatedAt) }}</span>
          <div class="mc-entry-actions">
            <button class="btn btn-secondary btn-xs" :disabled="busyPath === entry._target + ':' + entry.path" @click="memoryEditor.openEdit(entry._target, entry)">
              {{ busyPath === entry._target + ':' + entry.path ? '加载中...' : '编辑' }}
            </button>
            <button class="btn btn-danger btn-xs" @click="inlineDelete.ask(entry._target, entry)">删除</button>
          </div>
        </div>
      </article>
    </div>

    <!-- R6: 保留共享文件入口（次要链接） -->
    <div class="mc-footer-link">
      <button class="btn btn-ghost btn-xs" data-testid="memory-center-open-shared-files" @click="openSharedFiles">查看共享文件 →</button>
    </div>

  </div>

  <!-- 侧滑编辑面板（替代居中弹窗） -->
  <div v-if="memoryEditor.open" class="mc-panel-overlay" @click.self="memoryEditor.close"></div>
    <div class="mc-panel" :class="{ 'is-open': memoryEditor.open }" data-testid="memory-center-editor">
    <div class="mc-panel-head">
      <div>
        <div class="modal-title">{{ memoryEditor.mode === 'edit' ? '编辑记忆' : '新建记忆' }}</div>
        <div class="mc-panel-tip">{{ memoryEditor.form.target === 'team' ? '团队记忆' : '私有记忆' }}</div>
      </div>
      <button class="btn btn-ghost" data-testid="memory-center-editor-close" @click="memoryEditor.close">×</button>
    </div>
    <div class="mc-form-row">
      <label class="mc-form-label">目标</label>
      <select v-model="memoryEditor.form.target" class="modal-input" data-testid="memory-center-editor-target" :disabled="memoryIdentityLocked">
        <option value="private">私有</option><option value="team">团队</option>
      </select>
    </div>
    <div class="mc-form-row">
      <label class="mc-form-label">类型</label>
      <select v-model="memoryEditor.form.type" class="modal-input" data-testid="memory-center-editor-type" :disabled="memoryIdentityLocked">
        <option value="feedback">偏好</option><option value="project">项目</option>
      </select>
    </div>
    <div class="mc-form-row">
      <label class="mc-form-label">名称</label>
      <input v-model="memoryEditor.form.name" class="modal-input" data-testid="memory-center-editor-name" :disabled="memoryIdentityLocked" placeholder="例如：reply-in-chinese" />
    </div>
    <div class="mc-form-row">
      <label class="mc-form-label">描述</label>
      <input v-model="memoryEditor.form.description" class="modal-input" placeholder="一句话描述" />
    </div>
    <div v-if="memoryIdentityLocked" class="mc-form-helper">名称和类型已锁定；如需改名，请删除后重建。</div>
    <div class="mc-form-row">
      <label class="mc-form-label">内容</label>
      <textarea v-model="memoryEditor.form.content" rows="12" class="modal-input mc-form-textarea" data-testid="memory-center-editor-content"></textarea>
    </div>
    <div class="mc-form-helper">
      <button class="btn btn-secondary btn-xs" @click="memoryEditor.fillTemplate">套用模板</button>
    </div>
    <div class="mc-panel-actions">
      <button class="btn btn-ghost" @click="memoryEditor.close">取消</button>
      <button v-if="memoryEditor.form.existingPath" class="btn btn-danger" @click="askEditorDelete">删除</button>
      <button class="btn btn-primary" data-testid="memory-center-editor-save" :disabled="memoryEditor.saving || !memoryEditor.form.name.trim() || !memoryEditor.form.description.trim() || !memoryEditor.form.content.trim()" @click="memoryEditor.save">
        {{ memoryEditor.saving ? '保存中...' : '保存' }}
      </button>
    </div>
  </div>

  <!-- 删除确认弹窗（保持居中 modal 不变） -->
  <!-- 整合确认弹窗（保持居中 modal 不变） -->

</section>
```

- [ ] 3.2 确保所有 `data-testid` 属性保留（改名但不删除），保持测试兼容
- [ ] 3.3 删除确认弹窗和整合确认弹窗的模板原样保留，只改外层类名前缀
- [ ] 3.4 删除原有的"指引卡"模板和相关的 `guideCollapsed`、`toggleGuide`、`GUIDE_PREF_KEY` 代码

**验收标准:** 模板渲染无报错，Tab 切换正常，条目卡片只有一套模板代码，编辑面板从右侧滑入。

---

## Task 4: 浏览器手动验证

**步骤:**

- [ ] 4.1 打开 agent-terminal，切到记忆中心页面
- [ ] 4.2 检查 Bento Grid 三列是否正确渲染
- [ ] 4.3 检查 Tab 切换（偏好 / 项目 / 全部）是否正确过滤条目
- [ ] 4.4 检查新建 dropdown 是否正常弹出/关闭
- [ ] 4.5 检查编辑面板是否从右侧滑入，表单交互正常
- [ ] 4.6 检查搜索框过滤是否正常
- [ ] 4.7 缩小窗口宽度，验证 960px / 720px 断点下的响应式布局
- [ ] 4.8 检查删除确认、整合确认弹窗是否正常工作

---

## 风险与缓解

| 风险 | 缓解 | 修订状态 |
|------|------|----------|
| 现有 data-testid 改名后测试断言失败 | R5: 核心 testid 全部保留（page/search/refresh/editor-*/delete-*/merge-*） | ✅ 已修订 |
| 侧滑面板(z:10000) 遮挡删除弹窗(z:9999) | R1: 弹窗 z-index 改为 10001/10002 | ✅ 已修订 |
| Bento Grid 在 health=null 时留空卡 | R3: `.mc-bento-2col` 动态降为 2 列 | ✅ 已修订 |
| “全部”tab 混合条目 v-for key 冲突 | R2: key 改为 `entry._target + ':' + entry.path` | ✅ 已修订 |
| open-shared-files emit 被移除 | R6: 页面底部保留“查看共享文件”次要链接 | ✅ 已修订 |
| autoDreamPendingRestart 提示遗漏 | R7: Bento 自动沉淀卡内补充 v-if 提示行 | ✅ 已修订 |
| model.error 展示遗漏 | R8: 通知区补充 error 渲染 | ✅ 已修订 |
| 硬编码色值 rgba(64,201,119) | R4: 改为 color-mix(var(--success)) | ✅ 已修订 |
| color-mix() 在旧 WebKit 中不支持 | Electron 内嵌 Chromium 已支持 | — |
| Tab 栏窄屏溢出 | O1: overflow-x:auto + white-space:nowrap | ✅ 已优化 |
| 搜索展开动效缺失 | O2: transition: width 0.25s ease | ✅ 已优化 |
| 单列 Bento 数字过大 | O3: 720px 以下 font-size:22px | ✅ 已优化 |
| Dropdown 关闭事件冒泡漏洞 | O4: document.addEventListener 全局监听 | ✅ 已优化 |
