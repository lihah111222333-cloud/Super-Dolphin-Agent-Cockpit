# Chat Turn Process and Composer Redesign Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将每个用户 turn 的过程消息聚合为默认折叠组件，并把聊天输入栏重绘为稳定、单行且附件图标化的结构。

**Architecture:** 在 `frontend-app` 视图层新增纯 turn 分组模型，不修改 store 或后端协议；`ConversationTimeline` 将分组结果渲染为普通消息或 turn 过程组件。输入栏保留现有业务动作，只调整 `ComposerMeta` 标记和 composer/workbench CSS 尺寸契约。

**Tech Stack:** React 19、Vite、Vitest、Testing Library、原生 `details/summary`、Lucide React、CSS custom properties。

**Verification Surface:** `frontend-app` 的 timeline model/component、composer component/styles、i18n、LSP diagnostics、ESLint、Vitest、Vite build、内置浏览器桌面与移动宽度。

**Session Constraint:** 当前工作区已有未提交用户变更，本计划执行时不创建提交；每个任务以定向测试和 `git diff --check` 作为边界检查。

---

### Task 1: Turn 分组纯模型

**Files:**
- Create: `frontend-app/src/pages/chat/thread/chatTurnGroupingModel.js`
- Create: `frontend-app/src/pages/chat/thread/chatTurnGroupingModel.test.js`

- [ ] **Step 1: Write the failing model tests**

测试覆盖：首个用户前孤立消息、多 turn、最后一条普通 assistant 常显、此前普通 assistant 与 reasoning 进入过程集合、approval 独立、只有最终答复时不创建空过程组。

```js
import { describe, expect, it } from 'vitest';
import { materializeTurnTimelineEntries } from './chatTurnGroupingModel.js';

describe('materializeTurnTimelineEntries', () => {
  it('groups process items before the final assistant reply for each user turn', () => {
    const entries = materializeTurnTimelineEntries([
      { id: 'orphan', role: 'assistant', kind: 'assistant', text: '历史孤立消息' },
      { id: 'u1', role: 'user', kind: 'user', text: '开始' },
      { id: 'progress', role: 'assistant', kind: 'assistant', text: '正在定位' },
      { id: 'tool', role: 'assistant', kind: 'tool', title: 'grep', text: 'result' },
      { id: 'final', role: 'assistant', kind: 'assistant', text: '处理完成' },
    ], { activeCurrentTurn: true });

    expect(entries.map((entry) => entry.type)).toEqual(['message', 'message', 'process', 'message']);
    expect(entries[2]).toMatchObject({ active: true, messages: [{ id: 'progress' }, { id: 'tool' }] });
    expect(entries[3].message.id).toBe('final');
  });

  it('keeps approvals outside the collapsed process group', () => {
    const entries = materializeTurnTimelineEntries([
      { id: 'u1', role: 'user', text: '执行' },
      { id: 'thinking', role: 'assistant', kind: 'thinking', text: '分析' },
      { id: 'approval', role: 'assistant', kind: 'approval', requestId: 7, status: 'pending' },
      { id: 'final', role: 'assistant', kind: 'assistant', text: '完成' },
    ]);

    expect(entries.find((entry) => entry.type === 'process')?.messages.map((item) => item.id)).toEqual(['thinking']);
    expect(entries.find((entry) => entry.message?.id === 'approval')?.type).toBe('message');
  });

  it('does not create an empty process group for a direct answer', () => {
    const entries = materializeTurnTimelineEntries([
      { id: 'u1', role: 'user', text: '直接回答' },
      { id: 'final', role: 'assistant', kind: 'assistant', text: '答案' },
    ]);
    expect(entries.map((entry) => entry.type)).toEqual(['message', 'message']);
  });
});
```

- [ ] **Step 2: Run the model test and verify RED**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/thread/chatTurnGroupingModel.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because `chatTurnGroupingModel.js` does not exist.

- [ ] **Step 3: Implement the minimal grouping model**

Create a pure function with this public shape:

```js
export function materializeTurnTimelineEntries(messages = [], options = {}) {
  // Returns ordered entries:
  // { type: 'message', key, message }
  // { type: 'process', key, messages, active }
}
```

Implementation rules:

```js
function isOrdinaryAssistant(message) {
  return message?.role === 'assistant' &&
    !isReasoningMessage(message) &&
    !isApprovalMessage(message) &&
    Boolean(trimmedText(message?.text));
}

function materializeTurn(userMessage, turnMessages, active) {
  let finalIndex = -1;
  for (let index = turnMessages.length - 1; index >= 0; index -= 1) {
    if (isOrdinaryAssistant(turnMessages[index])) {
      finalIndex = index;
      break;
    }
  }
  const finalMessage = finalIndex >= 0 ? turnMessages[finalIndex] : null;
  const approvals = turnMessages.filter((message) => isApprovalMessage(message));
  const processMessages = turnMessages.filter((message, index) => (
    index !== finalIndex && !isApprovalMessage(message)
  ));
  return { userMessage, processMessages, approvals, finalMessage, active };
}
```

Preserve orphan messages before the first user as ordinary entries. Apply `activeCurrentTurn` only to the final user turn's process entry. Do not mutate input arrays or messages.

- [ ] **Step 4: Run the model test and verify GREEN**

Expected: all tests in `chatTurnGroupingModel.test.js` pass.

- [ ] **Step 5: Check task boundary without committing**

Run:

```bash
git diff --check -- frontend-app/src/pages/chat/thread/chatTurnGroupingModel.js frontend-app/src/pages/chat/thread/chatTurnGroupingModel.test.js
```

Expected: exit 0.

### Task 2: Turn 过程折叠组件与时间线接入

**Files:**
- Create: `frontend-app/src/pages/chat/thread/TurnProcessGroup.jsx`
- Create: `frontend-app/src/pages/chat/thread/TurnProcessGroup.css`
- Create: `frontend-app/src/pages/chat/thread/TurnProcessGroup.test.jsx`
- Modify: `frontend-app/src/pages/chat/thread/Conversation.jsx`
- Modify: `frontend-app/src/pages/chat/ChatPage.timeline.test.jsx`
- Modify: `frontend-app/src/shared/i18n/appI18n.zh.json`
- Modify: `frontend-app/src/shared/i18n/appI18n.en.json`

- [ ] **Step 1: Write failing component and integration tests**

`TurnProcessGroup.test.jsx` must assert that the disclosure is closed by default, its summary has the correct completed/running text, and clicking the summary opens the existing process messages.

```jsx
render(<TurnProcessGroup
  active={false}
  messages={[{ id: 'tool-1', role: 'assistant', kind: 'tool', title: 'grep', text: 'result', done: true }]}
  copy={{ processComplete: '执行过程', processRunning: '正在执行' }}
  formatTime={() => '08:30'}
/>);
const group = screen.getByTestId('turn-process-group');
expect(group).not.toHaveAttribute('open');
expect(screen.getByText('执行过程 · 1 条')).toBeInTheDocument();
fireEvent.click(group.querySelector('summary'));
expect(group).toHaveAttribute('open');
expect(screen.getByText('result')).toBeInTheDocument();
```

Add a `ChatPage.timeline.test.jsx` case with user, progress assistant, tool and final assistant messages. Assert the final answer is outside `[data-testid="turn-process-group"]` and the progress/tool messages are inside it. Keep the existing streaming tests unchanged so the last active assistant remains visible.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/thread/TurnProcessGroup.test.jsx src/pages/chat/ChatPage.timeline.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because `TurnProcessGroup` and turn-level materialization are not connected.

- [ ] **Step 3: Implement the disclosure component**

Use native `details/summary`, no persisted open state:

```jsx
function TurnProcessGroup({ active, messages, copy, ...timelineProps }) {
  const label = active ? copy.processRunning : copy.processComplete;
  return (
    <details className={`turn-process${active ? ' is-active' : ''}`} data-testid="turn-process-group">
      <summary>
        <span className="turn-process-state" aria-hidden="true" />
        <span>{`${label} · ${messages.length} 条`}</span>
        <ChevronDown className="turn-process-chevron" size={16} aria-hidden="true" />
      </summary>
      <div className="turn-process-list">
        {messages.map((message) => (
          <TimelineMessage key={message.callId ? `tool-${message.callId}` : message.id} message={message} {...timelineProps} />
        ))}
      </div>
    </details>
  );
}
```

CSS requirements: content width equals `var(--conversation-content-width)`; 8px or less radius; 44px summary; transparent or subtle surface; nested `.message` and `.reasoning-message` width 100%, no outer margins or shadows; chevron rotates only when open; active state uses existing accent token without animation that ignores reduced motion.

- [ ] **Step 4: Integrate grouped entries into `ConversationTimeline`**

Pass `activeCurrentTurn={isBusy || sending}` from `Conversation` to `ConversationTimeline`. After appending `pendingReasoning`, call:

```js
const timelineEntries = materializeTurnTimelineEntries(timelineMessages, {
  activeCurrentTurn,
});
```

Render a `TurnProcessGroup` for process entries and `TimelineMessage` for message entries. Keep pagination, loading, auto-scroll and raw store messages unchanged.

- [ ] **Step 5: Add bilingual copy**

Add chat keys:

```json
// zh
"processComplete": "执行过程",
"processRunning": "正在执行"

// en
"processComplete": "Process",
"processRunning": "Running"
```

The numeric suffix remains localized in the component: Chinese uses `N 条`; English uses `N steps`, selected from the active copy locale or an additional `processCount` template key if the existing i18n object does not expose locale identity.

- [ ] **Step 6: Run focused tests and verify GREEN**

Expected: grouping, disclosure, existing reasoning and streaming tests all pass.

- [ ] **Step 7: Check task boundary without committing**

Run `git diff --check` for the files listed in this task. Expected: exit 0.

### Task 3: 输入栏结构与尺寸重绘

**Files:**
- Modify: `frontend-app/src/pages/chat/composer/ComposerMeta.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.css`
- Modify: `frontend-app/src/pages/chat/ChatPageWorkbench.css`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`
- Modify: `frontend-app/src/styles.test.js`

- [ ] **Step 1: Write failing composer structure tests**

Extend `ComposerDock.test.jsx`:

```js
const addFileButton = screen.getByRole('button', { name: '添加文件' });
expect(addFileButton).toHaveAccessibleName('添加文件');
expect(addFileButton).toHaveTextContent('');
expect(addFileButton.querySelector('svg')).toBeInTheDocument();
expect(addFileButton.querySelector('.composer-attach-label')).toBeNull();
```

Keep click assertions to prove `selectFiles` still runs.

Extend `styles.test.js` to assert the final cascade:

```js
expect(composerCard['border-radius']).toBe('20px');
expect(composerTextarea.height).toBe('76px');
expect(composerMeta['flex-wrap']).toBe('nowrap');
expect(composerMeta['min-height']).toBe('48px');
expect(attach.width).toBe('36px');
expect(attach.height).toBe('36px');
expect(attach.padding).toBe('0');
expect(send.width).toBe('40px');
expect(send.height).toBe('40px');
```

- [ ] **Step 2: Run composer and style tests and verify RED**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/composer/ComposerDock.test.jsx src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the visible attachment label and old 104px/66px/42px sizing remain.

- [ ] **Step 3: Remove only the visible attachment label**

Change `ComposerMeta.jsx` from icon plus span to:

```jsx
<Paperclip size={18} aria-hidden="true" />
```

Retain `aria-label`, `title`, disabled behavior and `runUIAction(() => selectFiles())`.

- [ ] **Step 4: Implement the composer layout contract**

In the final high-specificity cascade, use:

```css
.composer-card,
.composer--docked .composer-card {
  border-radius: 20px;
}

.composer textarea,
.composer--floating textarea {
  height: 76px;
  min-height: 76px;
  padding: 18px 20px 12px;
  border-radius: 20px 20px 0 0;
}

.composer-meta {
  min-height: 48px;
  display: flex;
  flex-wrap: nowrap;
  gap: 8px;
  padding: 6px 10px;
}

.composer-attach {
  flex: 0 0 36px;
  width: 36px;
  min-width: 36px;
  height: 36px;
  padding: 0;
}

.composer-context,
.composer-model-wrap {
  min-width: 0;
  overflow: hidden;
}

.composer .send {
  flex: 0 0 40px;
  width: 40px;
  min-width: 40px;
  height: 40px;
}
```

Align the baseline `ComposerDock.css` and the overriding workbench declarations. Remove obsolete `.composer-attach-label` rules. Keep model dropdown overflow visible and selected attachment rows above the textarea.

- [ ] **Step 5: Add responsive constraints**

At desktop and mobile breakpoints, keep `.composer-meta { flex-wrap: nowrap; }`; fixed icon buttons never shrink. Allow `.composer-context` and `.composer-model-wrap` to shrink with ellipsis. Do not hide project/model controls or introduce a second toolbar row.

- [ ] **Step 6: Run focused tests and verify GREEN**

Expected: all composer component and style tests pass.

- [ ] **Step 7: Check task boundary without committing**

Run `git diff --check` for the files listed in this task. Expected: exit 0.

### Task 4: Diagnostics and browser verification

**Files:**
- Verify all files changed in Tasks 1-3.

- [ ] **Step 1: Run LSP diagnostics**

Request `file(diagnostics)` for every changed JS, JSX, CSS and JSON file. Expected: no Error, Warning, Information or Hint diagnostics.

- [ ] **Step 2: Run focused regression suite**

```bash
cd frontend-app
npx vitest run src/pages/chat/thread/chatTurnGroupingModel.test.js src/pages/chat/thread/TurnProcessGroup.test.jsx src/pages/chat/ChatPage.timeline.test.jsx src/pages/chat/composer/ComposerDock.test.jsx src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: all focused tests pass.

- [ ] **Step 3: Verify in the in-app browser**

At `http://127.0.0.1:5175/` verify:

- each user turn has at most one process disclosure;
- the disclosure is closed by default and expands on click;
- the final assistant message remains visible;
- approval controls remain outside the disclosure;
- the attachment button is icon-only with accessible name `添加文件`;
- project/model/send controls remain one row in light and dark themes;
- desktop and mobile widths have no horizontal overflow or vertical attachment text.

- [ ] **Step 4: Run full frontend verification**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: ESLint exit 0, all Vitest files pass, Vite production build exits 0. Existing chunk-size warnings may remain but no new build errors are allowed.

- [ ] **Step 5: Final diff audit**

Run:

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; unrelated dirty Go files and `.superpowers/` remain untouched and uncommitted.
