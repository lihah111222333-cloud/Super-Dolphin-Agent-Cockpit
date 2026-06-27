# Frontend UI Backend Interface Optimization Report

日期：2026-06-02

仓库：`/Users/ai/Desktop/Super-Dolphin`

最终 CodeTrust JSON：`/tmp/codetrust-super-dolphin-frontend-ui-backend-2026-06-02.json`

## 摘要

本轮按用户要求开启 5 个子代理，分别测试并优化：

- 聊天页面
- 提示词页面
- 自动化页面
- 记忆中心页面
- 链路追踪页面

目标不是重做视觉风格，而是按 `baseline-ui` 和 `karpathy-guidelines` 做最小、可验证的用户任务改进：打通前端页面和后端 API mock 路径，补齐关键 loading/error/status 可访问状态，并避免改变后端接口契约。

最终结果：

| 项 | 结果 |
| --- | ---: |
| CodeTrust score | 91 |
| CodeTrust grade | `HIGH_TRUST` |
| filesConsidered | 39 |
| filesScanned | 38 |
| issuesFound | 1244 |
| Security | 100 |
| Coverage | 100 |
| 完整测试 | 19 files / 362 tests passed |

## 使用的技能原则

`baseline-ui` 应用点：

- 页面必须能说明当前位置、可执行动作、加载/错误/空态、成功反馈和恢复路径。
- 关键异步状态使用 `role="status"` / `aria-live` 或语义状态元素。
- 错误状态使用 `role="alert"`，避免只靠瞬时提示。
- tab/展开区域等交互补充 `aria-selected`、`aria-busy` 等状态。

`karpathy-guidelines` 应用点：

- 每个页面只做最小可验证改动。
- 子代理写入范围互相隔离，避免并发覆盖。
- 不新增 UI 库、状态库或后端接口。
- 每个修改都必须有相邻测试或命令验证。

## 子代理结果

| 子代理范围 | 修改文件 | 验证重点 |
| --- | --- | --- |
| 聊天页 | `frontend-app/src/pages/chat/ChatPage.test.jsx` | fake store 渲染、无项目禁用状态、线程 timeline、发送动作、右侧运行面板 `syncThreadState` |
| 提示词页 | `frontend-app/src/features/prompts/PromptPageView.jsx`、`PromptPageView.test.jsx` | `listPromptAssets`、`getPreference`、`writePrompt` payload；loading 改为语义 `output` |
| 自动化页 | `frontend-app/src/pages/workflows/WorkflowPage.test.jsx` | `getDashboardPage({ page: 'dags' })`、`getDagDetail`、`getDagRuns`、`startDag` payload |
| 记忆中心 | `frontend-app/src/pages/memory/MemoryPage.jsx`、`MemoryPage.test.jsx` | `getMemorySnapshot` 加载、tab `aria-selected`、`upsertMemoryEntry` payload |
| 链路追踪 | `frontend-app/src/pages/observability/ObservabilityPage.jsx`、`ObservabilityPage.test.jsx` | `listObservabilityRecent`、`getObservabilityTrace`、`copyTextToClipboard`；错误/loading aria 状态 |

说明：本轮使用当前可用的 `multi_agent_v1` 子代理工具，这是允许的原生子代理路径；主线程统一集成和验证，未记录持久 mcp-orch DAG 生命周期。

## 页面接口验证

### 聊天页面

验证路径：

- `bootstrapStatus`、`activeProject/cwd` 控制 composer、附件、provider 动作禁用。
- active thread 渲染线程卡片、timeline 用户/助手消息、token usage。
- 点击发送按钮调用 `store.sendDraft()`。
- 打开运行面板调用：

```js
store.syncThreadState('thread-1', {
  includeArchived: true,
  includeDiff: true,
  loadMessages: false,
  preserveActiveThreadId: true,
});
```

### 提示词页面

验证路径：

- 加载 dashboard：

```js
listPromptAssets({ cwd: '/repo/app' });
```

- 读取当前强制提示词：

```js
getPreference({ cwd: '/repo/app', key: 'settings.activePromptKey' });
```

- 编辑保存 payload：

```js
writePrompt({
  cwd: '/repo/app',
  id: 'main/reviewer',
  name: '审查提示词',
  description: '审查代码质量',
  agentType: 'coder',
  priority: 5,
  when_to_use: '用户要求代码审查时使用',
  content: '先检查阻塞问题',
  tags: ['review'],
  enabled: true,
  scope: 'project',
});
```

### 自动化页面

验证路径：

```js
getDashboardPage({ cwd: '/repo/app', page: 'dags' });
getDagDetail({ dagKey: 'daily-brief' });
getDagRuns({ dagKey: 'daily-brief', limit: 5 });
getDagRuns({ dagKey: 'daily-brief', status: 'running', limit: 1 });
startDag({
  dagKey: 'daily-brief',
  triggerSource: 'manual',
  idempotencyKey: 'ui-...',
});
```

### 记忆中心

验证路径：

```js
getMemorySnapshot({ cwd: '/repo/app' });
upsertMemoryEntry({
  cwd: '/repo/app',
  target: 'private',
  existingPath: '',
  name: 'feedback-...',
  description: '回复时使用中文',
  title: '',
  type: 'feedback',
  content: '规则\n默认中文回复',
});
```

UI 小修：

- 记忆分类 tab 补充 `aria-selected`。

### 链路追踪页面

验证路径：

```js
listObservabilityRecent({
  limit: 50,
  status: 'error',
  component: '',
  method: '',
  traceId: '',
  threadId: '',
  agentId: '',
  keyword: 'thread/start',
});

getObservabilityTrace({ traceId: 'trace-frontend-1', limit: 50 });
copyTextToClipboard('trace-frontend-1');
```

UI 小修：

- 查询表单增加 `aria-busy`。
- 页面错误与 trace 错误增加 `role="alert"`。
- trace 加载状态增加 `role="status"`，trace 详情区域增加 `aria-busy`。

## CodeTrust 复扫

扫描命令使用 `find`，覆盖未跟踪的新页面和新测试文件：

```bash
node <<'NODE'
const { spawnSync } = require('child_process');
const fs = require('fs');
const rawJsonPath = '/tmp/codetrust-super-dolphin-frontend-ui-backend-2026-06-02.json';
const find = spawnSync('find', [
  'frontend-app/src',
  'frontend-app/public/wails/runtime.js',
  'frontend-app/public/wails/runtime.test.js',
  '-type',
  'f',
], { encoding: 'utf8' });
const files = find.stdout
  .split('\n')
  .filter((file) => /\.(js|jsx|ts|tsx|css|json|cjs|mjs)$/.test(file));
const scan = spawnSync('codetrust', ['scan', '--format', 'json', ...files], {
  encoding: 'utf8',
  maxBuffer: 1024 * 1024 * 300,
});
const out = `${scan.stdout || ''}${scan.stderr || ''}`;
if (scan.status !== 0) {
  console.error(out);
  process.exit(scan.status || 1);
}
const result = JSON.parse(out.slice(out.indexOf('{')));
fs.writeFileSync(rawJsonPath, JSON.stringify(result, null, 2));
NODE
```

结果：

| 指标 | 值 |
| --- | ---: |
| Score | 91 |
| Grade | `HIGH_TRUST` |
| filesScanned | 38 |
| issuesFound | 1244 |
| Medium | 224 |
| Low | 1019 |
| Info | 1 |
| High/Critical | 0 |

维度：

| 维度 | Score | Issues |
| --- | ---: | ---: |
| Security | 100 | 0 |
| Logic | 90 | 1016 |
| Structure | 65.7 | 228 |
| Style | 100 | 0 |
| Coverage | 100 | 0 |

Top rules：

| Rule | Count |
| --- | ---: |
| `logic/duplicate-string` | 558 |
| `logic/magic-number` | 246 |
| `logic/unused-variables` | 208 |
| `structure/long-function` | 171 |
| `structure/high-cyclomatic-complexity` | 52 |
| `structure/too-many-params` | 4 |
| `logic/no-async-without-await` | 3 |
| `structure/high-cognitive-complexity` | 1 |
| `logic/console-in-code` | 1 |

Top files：

| File | Total | Medium | Main category |
| --- | ---: | ---: | --- |
| `frontend-app/src/App.test.jsx` | 338 | 45 | test structure / duplicate strings |
| `frontend-app/src/pages/chat/ChatPage.jsx` | 153 | 23 | chat component structure |
| `frontend-app/src/entities/client/model/useClientStore.test.js` | 117 | 16 | test structure / duplicate strings |
| `frontend-app/src/entities/client/model/useClientStore.js` | 93 | 57 | store structure |
| `frontend-app/src/styles.test.js` | 60 | 12 | test structure |
| `frontend-app/src/pages/settings/SettingsPage.jsx` | 58 | 1 | low logic findings |
| `frontend-app/src/pages/skills/SkillsPage.jsx` | 57 | 9 | skills structure |
| `frontend-app/src/features/prompts/PromptPageView.jsx` | 51 | 7 | prompt structure |
| `frontend-app/src/pages/workflows/WorkflowPage.jsx` | 50 | 2 | workflow low logic findings |
| `frontend-app/src/pages/memory/MemoryPage.jsx` | 46 | 6 | memory structure |

## 验证命令

### 子代理目标页统一验证

```bash
cd frontend-app
npx eslint \
  src/pages/chat/ChatPage.jsx src/pages/chat/ChatPage.test.jsx \
  src/pages/prompts/PromptPage.jsx \
  src/features/prompts/PromptPageView.jsx src/features/prompts/PromptPageView.test.jsx \
  src/pages/workflows/WorkflowPage.jsx src/pages/workflows/WorkflowPage.test.jsx \
  src/pages/memory/MemoryPage.jsx src/pages/memory/MemoryPage.test.jsx \
  src/pages/observability/ObservabilityPage.jsx src/pages/observability/ObservabilityPage.test.jsx

npx vitest run \
  src/pages/chat/ChatPage.test.jsx \
  src/features/prompts/PromptPageView.test.jsx \
  src/pages/workflows/WorkflowPage.test.jsx \
  src/pages/memory/MemoryPage.test.jsx \
  src/pages/observability/ObservabilityPage.test.jsx
```

结果：通过，`5` 个测试文件、`15` 个测试用例通过。

### 完整前端验证

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

结果：

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过，exit 0 |
| `npm test` | 通过，19 files / 362 tests |
| `npm run build` | 通过，exit 0；保留 Vite 大 chunk 警告 |

### React Doctor

```bash
cd frontend-app
npx react-doctor@latest --verbose --diff
```

结果：通过退出，评分 `92 / 100`，剩余 `29` 个 diff 诊断。

主要残留项：

- `PromptPageView.jsx`：派生状态/effect、PromptIntentWizard 多 useState、array iteration 性能建议。
- `FocusTrapDialog.jsx`：自定义 dialog/overlay 点击交互相关无障碍建议。
- `App.jsx`：memory badge 状态回传、barrel import、route ref initializer。

本轮只处理和五个页面后端接口打通及 baseline UI 状态直接相关的低风险项；这些 React Doctor 残留项建议后续单独分支处理。

## 残留风险

1. 本报告只覆盖当前 React/Vite 新 UI，不代表 Go 后端审计。
2. CodeTrust 扫描范围是 `frontend-app/src` 和 Wails runtime shim，未扫描 legacy Vue 前端、`dist`、`node_modules`、Go 文件或文档。
3. 新增相邻测试覆盖关键接口路径，但并未穷尽每个页面的所有动作：
   - 聊天页未覆盖附件拖拽/复制线程信息的全部路径。
   - 提示词页未覆盖草稿向导、删除、丢弃、强制启用全部路径。
   - 自动化页未在相邻测试覆盖 stop/delete/schedule/edit/design 全动作。
   - 记忆中心未在相邻测试覆盖删除、编辑详情、合并、忽略、一键整合全流程。
   - 链路追踪页未覆盖所有 filter 组合和 trace 空/错误分支。
4. `structure` 维度仍为 65.7；继续提升需要拆 `ChatPage`、`useClientStore`、`PromptPageView` 和大型测试文件。
5. 工作区在本轮开始前已有大量未提交改动，本轮没有回滚、暂存或提交。提交前需要人工区分本轮前端页面/测试/报告改动和既有 Go/工具目录改动。
