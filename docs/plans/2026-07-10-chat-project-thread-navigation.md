# Chat Project and Thread Navigation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在聊天页面的同一侧栏中恢复“项目 -> 会话”两级导航，并让输入栏项目控件可切换项目且与侧栏同步。

**Architecture:** 复用现有 `SidebarProjectTree` 处理项目展开、项目会话加载和会话动作，`SuiyuanSidebar` 只负责在聊天页激活时挂载它。输入栏复用现有 `ProjectSelector`，项目树和项目菜单均通过 `useClientStore` 的 `activeProject`、`projects` 与已有动作同步，不新增状态或 RPC。

**Tech Stack:** React 19、Zustand、React Aria Components、Vitest、Testing Library、CSS、Vite。

**Verification Surface:** `frontend-app`，重点覆盖 `App` 侧栏集成、聊天 composer、Suiyuan 侧栏样式与浏览器响应式交互。

---

## 文件结构

- Modify: `frontend-app/src/App.jsx` - 在新版 Suiyuan 侧栏中挂载聊天项目与会话树，并传递当前 store 与项目路径。
- Modify: `frontend-app/src/App.test.jsx` - 固定聊天页才显示两级树、项目会话归属和主导航顺序。
- Modify: `frontend-app/src/pages/chat/composer/ComposerMeta.jsx` - 在启用项目选择时渲染现有 `ProjectSelector`。
- Modify: `frontend-app/src/pages/chat/thread/Conversation.jsx` - 为实际聊天 composer 启用项目选择器。
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx` - 固定输入栏项目菜单的可点击和切换行为。
- Modify: `frontend-app/src/AppShell.css` - 添加 Suiyuan 聊天树的紧凑布局、滚动和文本截断规则。
- Modify: `frontend-app/src/pages/chat/ChatPageWorkbench.css` - 让项目选择器在 composer 单行工具栏内稳定收缩。
- Modify: `frontend-app/src/styles.test.js` - 固定树形导航和 composer 项目按钮的结构尺寸。

**共享工作区约束：** 当前分支已有大量未提交 UI 与 Go 改动。不得 stage、commit、stash、清理或回滚 unrelated 文件；除非用户另行明确要求，本计划以逐任务测试和 `git diff --check` 代替提交步骤。

---

### Task 1: 将项目与会话树接回聊天侧栏

**Files:**
- Modify: `frontend-app/src/App.jsx:395-463`
- Modify: `frontend-app/src/App.jsx:552-561`
- Test: `frontend-app/src/App.test.jsx:761-780`
- Test: `frontend-app/src/App.test.jsx:1052-1076`

- [ ] **Step 1: 编写聊天树可见性失败测试**

把当前“侧栏不含项目线程区”的断言改为聊天页显示树、离开聊天页后隐藏：

```jsx
it('shows the project-thread tree only while chat is active', async () => {
  render(<App />);

  const sidebar = await screen.findByTestId('app-sidebar');
  const nav = within(sidebar).getByRole('navigation', { name: 'Suiyuan navigation' });
  expect(await within(nav).findByRole('region', { name: '项目' })).toBeInTheDocument();
  expect(within(nav).getByRole('button', { name: '添加项目目录' })).toBeInTheDocument();

  fireEvent.click(within(nav).getByRole('button', { name: '插件与技能' }));
  await waitFor(() => expect(within(nav).queryByRole('region', { name: '项目' })).not.toBeInTheDocument());

  fireEvent.click(within(nav).getByRole('button', { name: '聊天页面' }));
  expect(await within(nav).findByRole('region', { name: '项目' })).toBeInTheDocument();
});
```

- [ ] **Step 2: 编写项目归属会话失败测试**

复用当前 backend mock，让两个项目返回不同会话；只验证项目展开与会话归属，不重复树组件已有的重命名/删除行为：

```jsx
it('lists conversations under their owning projects', async () => {
  backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/app' });
  backend.getSidebarState.mockImplementation(({ cwd }) => Promise.resolve(cwd === '/repo/other' ? {
    activeThreadId: 'thread-other',
    threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'idle', cwd: '/repo/other' }],
  } : {
    activeThreadId: 'thread-1',
    threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
  }));

  render(<App />);

  const sidebar = await screen.findByTestId('app-sidebar');
  const appChats = await within(sidebar).findByRole('list', { name: 'app 聊天记录' });
  const otherChats = await within(sidebar).findByRole('list', { name: 'other 聊天记录' });
  expect(within(appChats).getByTitle('后端线程')).toBeInTheDocument();
  expect(within(otherChats).queryByTitle('Other project chat')).not.toBeInTheDocument();

  fireEvent.click(within(sidebar).getByRole('button', { name: '选择项目 other' }));
  expect(await within(otherChats).findByTitle('Other project chat')).toBeInTheDocument();
  expect(within(appChats).queryByTitle('Other project chat')).not.toBeInTheDocument();
});
```

- [ ] **Step 3: 运行测试确认 RED**

Run:

```bash
cd frontend-app
npx vitest run src/App.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL，因为当前 `SuiyuanSidebar` 只渲染主导航，不挂载 `SidebarProjectTree`。

- [ ] **Step 4: 增加聚焦的聊天导航组组件**

在 `App.jsx` 导入现有树，并把聊天按钮与树封装为小组件，避免增加 `SuiyuanSidebar` 嵌套深度：

```jsx
import { SidebarProjectTree } from './WorkbenchSidebarProjectTree.jsx';

function SuiyuanChatNavGroup({ activePage, copy, item, projectPath, setActivePage, store }) {
  return (
    <div className="suiyuan-chat-nav-group">
      <SuiyuanNavButton
        activePage={activePage}
        copy={copy}
        item={item}
        memoryBadgeCount={0}
        setActivePage={setActivePage}
      />
      {activePage === 'chat' ? (
        <div className="suiyuan-chat-project-tree">
          <SidebarProjectTree copy={copy.workbench} projectPath={projectPath} setActivePage={setActivePage} store={store} />
        </div>
      ) : null}
    </div>
  );
}
```

- [ ] **Step 5: 在 Suiyuan 侧栏挂载聊天组**

将第一个聊天项单独渲染，其余主导航保持原顺序：

```jsx
function SuiyuanSidebar({ copy, projectPath, sidebar, store }) {
  const { activePage, isOpen, memorySimilarCount, setActivePage, startNewChat } = sidebar;
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);
  const chatItem = SUIYUAN_NAV_ITEMS[0];
  const remainingItems = SUIYUAN_NAV_ITEMS.slice(1);

  return (
    <nav className="suiyuan-nav" data-testid="sidebar-nav" aria-label="Suiyuan navigation">
      <SuiyuanChatNavGroup
        activePage={activePage}
        copy={copy}
        item={chatItem}
        projectPath={projectPath}
        setActivePage={setActivePage}
        store={store}
      />
      {remainingItems.map((item) => (
        <SuiyuanNavButton
          key={item.id}
          activePage={activePage}
          copy={copy}
          item={item}
          memoryBadgeCount={memoryBadgeCount}
          setActivePage={setActivePage}
        />
      ))}
    </nav>
  );
}
```

向 `SuiyuanSidebar` 传递现有 `projectPath` 和 shell `store`：

```jsx
<SuiyuanSidebar
  copy={copy}
  projectPath={projectPath}
  store={store}
  sidebar={{
    activePage: store.activePage,
    isOpen: sidebarOpen,
    memorySimilarCount: memoryBadge.memorySimilarCount,
    setActivePage: setActivePageFromSidebar,
    startNewChat,
  }}
/>
```

- [ ] **Step 6: 保持主导航顺序测试只统计主导航按钮**

项目树会新增按钮，原测试不得再用 `getAllByRole('button')` 统计整个 nav：

```jsx
const navButtons = Array.from(screen.getByTestId('sidebar-nav').querySelectorAll('.suiyuan-nav-item'));
expect(navButtons.map((button) => button.textContent)).toEqual([
  '聊天页面',
  '插件与技能',
  '自动化',
  '提示词',
  '共享文件',
  '记忆中心',
  '链路追踪',
]);
```

- [ ] **Step 7: 运行 App 测试确认 GREEN**

Run:

```bash
cd frontend-app
npx vitest run src/App.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS；聊天页显示项目树，其他页面隐藏，项目会话归属正确。

- [ ] **Step 8: 检查 LSP 与差异边界**

使用 LSP `file(diagnostics)` 检查 `src/App.jsx`、`src/App.test.jsx`，随后运行：

```bash
git diff --check -- frontend-app/src/App.jsx frontend-app/src/App.test.jsx
```

Expected: 无 diagnostics，命令无输出。

---

### Task 2: 让输入栏项目控件可切换项目

**Files:**
- Modify: `frontend-app/src/pages/chat/composer/ComposerMeta.jsx:15-78`
- Modify: `frontend-app/src/pages/chat/thread/Conversation.jsx:343-378`
- Test: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx:55-140`

- [ ] **Step 1: 编写项目菜单失败测试**

```jsx
import { fireEvent, render, screen, within } from '@testing-library/react';

it('opens the project selector and switches the active project', async () => {
  const store = createStore({
    activeProject: '/repo/app',
    projects: ['/repo/app', '/repo/side-project'],
    addProjectFromPicker: vi.fn(),
    removeProjectPath: vi.fn(),
    setActiveProjectPath: vi.fn(),
  });

  render(<ComposerDock {...baseProps} showProjectSelector composer={createComposer()} store={store} />);

  const trigger = screen.getByRole('button', { name: '选择项目' });
  expect(trigger).toHaveTextContent('app');
  fireEvent.click(trigger);
  const menu = await screen.findByRole('menu');
  fireEvent.click(within(menu).getByRole('menuitem', { name: 'repo/side-project' }));
  expect(store.setActiveProjectPath).toHaveBeenCalledWith('/repo/side-project');
});
```

- [ ] **Step 2: 运行 composer 测试确认 RED**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/composer/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL，因为 `ComposerMeta` 当前忽略 `showProjectSelector` 并渲染只读 `div.composer-context`。

- [ ] **Step 3: 在 ComposerMeta 复用 ProjectSelector**

新增导入：

```jsx
import { ProjectSelector } from '../components/ProjectSelector.jsx';
```

把 prop 从忽略状态改为实际使用：

```jsx
showProjectSelector = false,
```

将当前只读项目块：

```jsx
<div className="composer-context" aria-label={copy.projects} title={projectTitle}>
  <Folder size={15} aria-hidden="true" />
  <span>{projectLabel}</span>
</div>
```

替换为：

```jsx
{showProjectSelector ? (
  <ProjectSelector copy={copy} store={store} projectPath={projectPath} />
) : (
  <div className="composer-context" aria-label={copy.projects} title={projectTitle}>
    <Folder size={15} aria-hidden="true" />
    <span>{projectLabel}</span>
  </div>
)}
```

保留只读 fallback 供显式关闭项目选择的测试或其他调用面使用；实际聊天 composer 启用选择器。

- [ ] **Step 4: 为聊天 composer 启用项目选择**

在 `ConversationComposer` 中将：

```jsx
showProjectSelector={false}
```

替换为：

```jsx
showProjectSelector
```

- [ ] **Step 5: 运行 composer 与聊天核心测试确认 GREEN**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/composer/ComposerDock.test.jsx src/pages/chat/ChatPage.core.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS；附件、模型、发送行为保持不变，项目按钮可打开菜单并切换项目。

- [ ] **Step 6: 检查 LSP 与差异边界**

使用 LSP `file(diagnostics)` 检查 `ComposerMeta.jsx`、`Conversation.jsx`、`ComposerDock.test.jsx`，随后运行：

```bash
git diff --check -- frontend-app/src/pages/chat/composer/ComposerMeta.jsx frontend-app/src/pages/chat/thread/Conversation.jsx frontend-app/src/pages/chat/composer/ComposerDock.test.jsx
```

Expected: 无 diagnostics，命令无输出。

---

### Task 3: 收紧两级树与项目按钮的响应式布局

**Files:**
- Modify: `frontend-app/src/AppShell.css:124-205`
- Modify: `frontend-app/src/pages/chat/ChatPageWorkbench.css:465-545`
- Test: `frontend-app/src/styles.test.js`

- [ ] **Step 1: 编写样式失败测试**

在 Suiyuan shell 样式测试中固定两级树滚动窗口和 composer 项目按钮收缩规则：

```js
it('keeps the chat project tree compact and independently scrollable', () => {
  const group = declarationsFor('.suiyuan-chat-nav-group');
  const tree = declarationsFor('.suiyuan-chat-project-tree');
  const threadTitle = declarationsFor('.suiyuan-chat-project-tree .sidebar-thread-title');

  expect(group.display).toBe('grid');
  expect(group['min-width']).toBe('0');
  expect(tree['max-height']).toBe('min(300px, 32vh)');
  expect(tree['overflow-y']).toBe('auto');
  expect(tree['overscroll-behavior']).toBe('contain');
  expect(threadTitle['text-overflow']).toBe('ellipsis');
});

it('lets the composer project selector shrink without wrapping the toolbar', () => {
  const wrap = declarationsFor('.composer-meta > .project-select-wrap');
  const button = declarationsFor('.composer-meta > .project-select-wrap .project-select');
  const meta = declarationsFor('.composer-meta');

  expect(wrap.flex).toBe('0 1 210px');
  expect(wrap['min-width']).toBe('0');
  expect(button.width).toBe('100%');
  expect(meta['flex-wrap']).toBe('nowrap');
});
```

- [ ] **Step 2: 运行样式测试确认 RED**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL，因为 Suiyuan 聊天树和 composer 项目选择器尚无这些 scoped 规则。

- [ ] **Step 3: 添加聊天树紧凑样式**

在 `AppShell.css` 的 `.suiyuan-nav` 规则附近添加：

```css
.suiyuan-chat-nav-group {
  min-width: 0;
  display: grid;
  gap: 6px;
}

.suiyuan-chat-project-tree {
  min-width: 0;
  max-height: min(300px, 32vh);
  margin-left: 12px;
  padding: 4px 2px 8px 10px;
  border-left: 1px solid var(--sidebar-border);
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
}

.suiyuan-chat-project-tree .sidebar-project-tree {
  padding-top: 0;
  border-top: 0;
  overflow: visible;
}

.suiyuan-chat-project-tree .sidebar-tree-folder {
  min-height: 30px;
  font-size: 13px;
}

.suiyuan-chat-project-tree .sidebar-project-thread {
  min-height: 28px;
  font-size: 12px;
}

.suiyuan-chat-project-tree .sidebar-thread-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

- [ ] **Step 4: 添加 composer 项目选择器约束**

在 `ChatPageWorkbench.css` 的 `.composer-meta` 规则旁添加：

```css
.composer-meta > .project-select-wrap {
  flex: 0 1 210px;
  min-width: 0;
  max-width: min(210px, 32vw);
}

.composer-meta > .project-select-wrap .project-select {
  width: 100%;
  min-width: 0;
}

.composer-meta > .project-select-wrap .project-select span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

保留现有 `@media (max-width: 920px)` 和 `@media (max-width: 640px)` 中的单行工具栏约束，并确认移动规则继续将 `.project-select-wrap` 限制为 `flex: 0 1 112px`。不得改变附件 36px 和发送 40px 固定尺寸。

- [ ] **Step 5: 更新过期的“项目树不存在”样式断言**

将 `styles.test.js` 中：

```js
expect(appSource).not.toContain('<SidebarProjectTree');
```

改为：

```js
expect(appSource).toContain('<SidebarProjectTree');
```

- [ ] **Step 6: 运行相关样式与组件测试确认 GREEN**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/App.test.jsx src/pages/chat/composer/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS；项目树独立滚动，长名称省略，composer 工具栏保持单行。

- [ ] **Step 7: 检查 CSS LSP diagnostics 与差异边界**

使用 LSP `file(diagnostics, language_id="css")` 检查 `AppShell.css`、`ChatPageWorkbench.css`，随后运行：

```bash
git diff --check -- frontend-app/src/AppShell.css frontend-app/src/pages/chat/ChatPageWorkbench.css frontend-app/src/styles.test.js
```

Expected: 无 diagnostics，命令无输出。

---

### Task 4: 浏览器和全量验证

**Files:**
- Verify: `frontend-app/src/App.jsx`
- Verify: `frontend-app/src/pages/chat/composer/ComposerMeta.jsx`
- Verify: `frontend-app/src/AppShell.css`
- Verify: `frontend-app/src/pages/chat/ChatPageWorkbench.css`

- [ ] **Step 1: 运行聚焦回归**

Run:

```bash
cd frontend-app
npx vitest run src/App.test.jsx src/pages/chat/composer/ComposerDock.test.jsx src/pages/chat/components/ProjectSelector.test.jsx src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS。

- [ ] **Step 2: 在浏览器验证桌面行为**

打开 `http://127.0.0.1:5175/`，验证：

- “聊天页面”激活时，其下显示项目一级节点和会话二级节点。
- 当前项目自动展开，当前会话高亮。
- 展开其他项目只加载该项目会话，不混入当前项目列表。
- 点击会话打开对应聊天；项目级新建会话归属正确。
- 输入栏项目按钮可打开菜单，切换项目后侧栏高亮同步。
- 长项目名、长会话名和长模型名不重叠。

- [ ] **Step 3: 在浏览器验证页面切换和移动宽度**

使用 390x844 临时视口验证：

- 切换到“插件与技能”等非聊天页面后项目树隐藏。
- 返回聊天页后项目树恢复。
- 选择会话后移动侧栏抽屉关闭。
- 输入栏附件、项目、模型、发送按钮保持单行且页面无水平溢出。

完成后重置临时视口。

- [ ] **Step 4: 运行完整 LSP diagnostics**

对所有本计划修改的 JSX、JS 和 CSS 文件运行 LSP `file(diagnostics)`；所有 Error、Warning、Information、Hint 均须修复。

- [ ] **Step 5: 运行仓库前端验证**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: lint 退出码 0；全部测试通过；Vite 构建成功。允许报告既有 chunk-size warning，但不得将 warning 当作失败或静默忽略。

- [ ] **Step 6: 运行 React Doctor 差异扫描**

Run:

```bash
cd frontend-app
npx react-doctor@latest --verbose --scope changed
```

Expected: 本次差异无新增 React Doctor issue。

- [ ] **Step 7: 最终工作区审计**

Run:

```bash
git diff --check
git status --short
```

Expected: diff check 无输出；状态中保留用户已有改动，未 stage、未提交、未回滚 unrelated 文件。
