import React from 'react';
import { act, createEvent, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import mermaid from 'mermaid';
import { ChatPage } from './ChatPage.jsx';
import { copyTextToClipboard, locateCodeFile, onFilesDropped, openCodeFile } from '../../shared/api/backendApi.js';

vi.mock('../../shared/api/backendApi.js', () => ({
  copyTextToClipboard: vi.fn(),
  locateCodeFile: vi.fn(),
  openCodeFile: vi.fn(),
  onFilesDropped: vi.fn(() => () => {}),
  saveCodeFile: vi.fn(),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

function createFakeStore(overrides = {}) {
  const store = {
    actionNotice: null,
    activeProject: '/repo/app',
    activeThreadId: '',
    activeTurnByThread: {},
    activityStatsByThread: {},
    activityThreadAtById: {},
    archiveThread: vi.fn(),
    attachDroppedFilesForComposer: vi.fn(),
    attachPastedImagesForComposer: vi.fn(),
    attachPathsForComposer: vi.fn(),
    attachments: [],
    bootstrapStatus: 'ready',
    chatSurfaceLoadingCwd: '',
    copyActiveThreadInfo: vi.fn(),
    cwd: '/repo/app',
    deleteStaleThreads: vi.fn(),
    diffTextByThread: {},
    draft: '',
    error: '',
    forceCompleteActiveThread: vi.fn(),
    hasActiveThreadActions: vi.fn(() => Boolean(store.activeThreadId)),
    hasInterruptibleThreadAction: vi.fn(() => false),
    interruptActiveThread: vi.fn(),
    loadOlderThreadMessages: vi.fn(),
    loadThreadConfig: vi.fn(),
    newThread: vi.fn(),
    openNewWindow: vi.fn(),
    openForkDraft: vi.fn(),
    pendingActiveThreadId: '',
    pinnedThreadAtById: {},
    provider: 'codex',
    providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
    recoverActiveThread: vi.fn(),
    removeAttachment: vi.fn(),
    renameThread: vi.fn(),
    rightPanelWidth: 0,
    runtimeResultEntries: [],
    saveComposerModelConfig: vi.fn(),
    selectFilesForComposer: vi.fn(),
    selectThread: vi.fn(),
    sendDraft: vi.fn(),
    sending: false,
    smoothStreaming: true,
    setDraft: vi.fn((value) => {
      store.draft = value;
    }),
    setRightPanelWidth: vi.fn((value) => {
      store.rightPanelWidth = value;
    }),
    statuses: {},
    syncThreadState: vi.fn(),
    threadArchiveLoadingByThread: {},
    threadConfigByThread: {},
    threadConfigLoadingByThread: {},
    threadConfigSaving: false,
    threadDiffReadyByThread: {},
    threadMessagePaginationByThread: {},
    threadStateLoadingByThread: {},
    threadTimelineReadyByThread: {},
    threads: [],
    timelinesByThread: {},
    toggleProviderMode: vi.fn(),
    tokenUsageByThread: {},
    warningEntries: [],
    ...overrides,
  };
  return store;
}

function createActiveThreadStore(messages, overrides = {}) {
  return createFakeStore({
    activeThreadId: 'thread-1',
    threads: [{ id: 'thread-1', name: '渲染窗口会话', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
    threadTimelineReadyByThread: { 'thread-1': true },
    timelinesByThread: {
      'thread-1': messages,
    },
    ...overrides,
  });
}

function getThreadCardByName(name) {
  const card = screen.getAllByText(name)
    .map((node) => node.closest('.thread-card'))
    .find(Boolean);
  if (!card) throw new Error(`Thread card not found: ${name}`);
  return card;
}

function TestChatPageWrapper({ store, projectPath, rightPanelOpen: initialOpen = false }) {
  const [open, setOpen] = React.useState(initialOpen);

  return (
    <div>
      <button type="button" onClick={() => setOpen((prev) => !prev)}>
        测试切换侧边栏
      </button>
      <ChatPage
        store={store}
        projectPath={projectPath}
        rightPanelOpen={open}
        setRightPanelOpen={setOpen}
      />
    </div>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  delete window.matchMedia;
  locateCodeFile.mockResolvedValue({
    ok: true,
    paths: ['/repo/app/src/main.go'],
    matches: [{ path: '/repo/app/src/main.go', relative: 'src/main.go' }],
  });
  openCodeFile.mockResolvedValue({
    ok: true,
    filePath: '/repo/app/src/main.go',
    relative: 'src/main.go',
    startLine: 9,
    endLine: 11,
    totalLines: 20,
    snippet: [
      { line: 9, text: 'func main() {' },
      { line: 10, text: '  run()' },
      { line: 11, text: '}' },
    ],
  });
});

describe('ChatPage module', () => {
  it('exports the chat page component', () => {
    expect(ChatPage).toBeTypeOf('function');
  });

  it('uses the active thread name as the chat detail title', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '已连接后端线程', time: '2026-06-02T08:00:00Z' },
    ], {
      threads: [{ id: 'thread-1', name: '介绍功能与能力', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('heading', { name: '介绍功能与能力' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '聊天页面' })).not.toBeInTheDocument();
  });

  it('keeps the empty new-chat intro free of the generic page title bar', () => {
    const store = createFakeStore();

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('heading', { name: '我们应该在 Super-Dolphin 中构建什么？' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '聊天页面' })).not.toBeInTheDocument();
    expect(screen.getByTestId('chat-page')).toHaveClass('chat-page--intro');
    expect(screen.getByTestId('conversation-drop-zone')).toHaveClass('conversation--intro');
    expect(screen.getByTestId('composer-dock')).toHaveClass('composer--floating');
    expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--docked');
    expect(screen.queryByRole('button', { name: '聊天操作' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '滚动到底部' })).not.toBeInTheDocument();
  });

  it('keeps the generic title when active thread metadata is missing', () => {
    const store = createFakeStore({
      activeThreadId: 'missing-thread',
      threads: [],
      threadTimelineReadyByThread: { 'missing-thread': true },
      timelinesByThread: { 'missing-thread': [] },
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('heading', { name: '聊天页面' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '新对话' })).not.toBeInTheDocument();
  });

  it('disables project actions when the backend is not ready or no project cwd is selected', () => {
    const store = createFakeStore({
      activeProject: '',
      bootstrapStatus: 'failed',
      cwd: '',
      draft: '请修复测试',
      error: 'backend unavailable',
    });

    render(<TestChatPageWrapper store={store} projectPath="未选择项目" />);

    expect(screen.getByText('连接后端失败：backend unavailable')).toBeInTheDocument();
    expect(screen.getByText('我们应该在 Super-Dolphin 中构建什么？')).toBeInTheDocument();
    expect(screen.getByText('暂无会话，点击「新建对话」开始草稿')).toBeInTheDocument();
    expect(screen.getByTestId('composer-input')).toHaveValue('请修复测试');
    expect(screen.getByRole('button', { name: '发送消息' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '添加文件' })).toBeDisabled();
    // expect(screen.getByRole('button', { name: '请先连接后端并选择项目' })).toBeDisabled();
  });

  it('renders an active thread timeline, sends through the store, and opens the runtime panel', async () => {
    const store = createFakeStore({
      activeThreadId: 'thread-1',
      draft: '继续修复',
      threads: [{ id: 'thread-1', name: '修复会话', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
      threadTimelineReadyByThread: { 'thread-1': true },
      timelinesByThread: {
        'thread-1': [
          { id: 'msg-1', role: 'user', text: '哪里失败了？', time: '2026-06-02T08:00:00Z' },
          { id: 'msg-2', role: 'assistant', text: '测试在聊天页缺少覆盖。', time: '2026-06-02T08:01:00Z' },
        ],
      },
      tokenUsageByThread: { 'thread-1': { usedTokens: 12, contextWindowTokens: 1000, usedPercent: 1.2 } },
      activityStatsByThread: { 'thread-1': { commands: 2, fileEdits: 1, toolCalls: { grep: 3 } } },
      diffTextByThread: { 'thread-1': 'diff --git a/ChatPage.test.jsx b/ChatPage.test.jsx\n+expect(screen.getByTestId("runtime-panel"))' },
    });

    const { container } = render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('heading', { name: '修复会话' })).toBeInTheDocument();
    expect(screen.getByTestId('chat-page')).not.toHaveClass('chat-page--intro');
    expect(screen.getByTestId('conversation-drop-zone')).not.toHaveClass('conversation--intro');
    expect(screen.getByTestId('composer-dock')).toHaveClass('composer--docked');
    expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--floating');
    expect(getThreadCardByName('修复会话')).toBeInTheDocument();
    expect(screen.getByText('哪里失败了？')).toBeInTheDocument();
    expect(screen.getByText('测试在聊天页缺少覆盖。')).toBeInTheDocument();
    expect(container.querySelector('.message.user.no-avatar')).not.toBeNull();
    expect(container.querySelector('.message.assistant.no-avatar')).not.toBeNull();
    expect(container.querySelector('.message.assistant .assistant-footer')).not.toBeNull();
    expect(container.querySelector('.avatar')).toBeNull();
    expect(container.querySelector('.work-status')).toBeNull();

    const timeline = screen.getByTestId('chat-timeline');
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 960 });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      value: 180,
      writable: true,
    });
    const animationFrameCallbacks = [];
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      animationFrameCallbacks.push(callback);
      return animationFrameCallbacks.length;
    });

    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    expect(store.sendDraft).toHaveBeenCalledTimes(1);
    expect(timeline.scrollTop).toBe(180);
    expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(1);

    act(() => {
      for (const callback of animationFrameCallbacks) callback(16);
    });

    expect(timeline.scrollTop).toBe(960);
    requestAnimationFrameSpy.mockRestore();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    await waitFor(() => expect(screen.getByTestId('runtime-panel')).toBeInTheDocument());
    expect(store.setRightPanelWidth).toHaveBeenCalledWith(expect.any(Number));
    expect(store.syncThreadState).toHaveBeenCalledWith('thread-1', {
      includeArchived: true,
      includeDiff: true,
      loadMessages: false,
      preserveActiveThreadId: true,
    });
    expect(screen.getByTestId('diff-view')).toBeInTheDocument();
  });

  it('exposes backend thread actions from the header action menu', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '已连接后端线程', time: '2026-06-02T08:00:00Z' },
    ], {
      hasInterruptibleThreadAction: vi.fn(() => true),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const openMenu = () => {
      fireEvent.click(screen.getByRole('button', { name: '聊天操作' }));
      return screen.getByTestId('chat-actions-menu');
    };

    let menu = openMenu();

    expect(within(menu).getByRole('button', { name: '选择项目' })).toBeInTheDocument();

    fireEvent.click(within(menu).getByRole('button', { name: '新窗口（独立进程）' }));
    expect(store.openNewWindow).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('button', { name: '复制当前线程' }));
    expect(store.copyActiveThreadInfo).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('button', { name: '继承当前对话' }));
    expect(store.openForkDraft).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('button', { name: '停止' }));
    expect(store.interruptActiveThread).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('button', { name: '强制完成' }));
    expect(store.forceCompleteActiveThread).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('button', { name: '进程恢复' }));
    expect(store.recoverActiveThread).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('button', { name: '显示侧边栏' }));
    await waitFor(() => expect(screen.getByTestId('runtime-panel')).toBeInTheDocument());
  });

  it('turns the composer primary button into an interrupt action while the active thread is running', () => {
    const store = createFakeStore({
      activeThreadId: 'thread-1',
      draft: '不要在运行中排队发送',
      hasInterruptibleThreadAction: vi.fn(() => true),
      threads: [{ id: 'thread-1', name: '运行会话', provider: 'codex', status: 'running', updatedAt: '2026-06-02T08:00:00Z' }],
      threadTimelineReadyByThread: { 'thread-1': true },
      timelinesByThread: {
        'thread-1': [
          { id: 'msg-1', role: 'assistant', text: '正在执行。', time: '2026-06-02T08:00:00Z' },
        ],
      },
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const interruptButton = screen.getByRole('button', { name: '中断当前执行' });
    expect(interruptButton).toHaveClass('send--interrupt');
    expect(interruptButton).toBeEnabled();
    expect(screen.queryByRole('button', { name: '发送消息' })).not.toBeInTheDocument();

    fireEvent.keyDown(screen.getByTestId('composer-input'), { key: 'Enter', code: 'Enter' });
    expect(store.sendDraft).not.toHaveBeenCalled();

    fireEvent.click(interruptButton);
    expect(store.interruptActiveThread).toHaveBeenCalledTimes(1);
    expect(store.sendDraft).not.toHaveBeenCalled();
  });

  it('accepts external file drops on the conversation window', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '把文件拖进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const dropped = new File(['notes'], 'outside-notes.txt', { type: 'text/plain' });
    Object.defineProperty(dropped, 'path', { value: '/tmp/outside-notes.txt' });
    const conversation = screen.getByTestId('conversation-drop-zone');

    fireEvent.dragEnter(conversation, {
      dataTransfer: {
        files: [dropped],
        items: [],
        types: ['Files'],
      },
    });

    expect(conversation).toHaveClass('drop-active');

    fireEvent.drop(conversation, {
      dataTransfer: {
        files: [dropped],
        items: [],
        types: ['Files'],
      },
    });

    expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]);
    expect(conversation).not.toHaveClass('drop-active');
  });

  it('accepts direct file drops on the composer input', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '把文件拖进输入框即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const dropped = new File(['notes'], 'input-notes.txt', { type: 'text/plain' });
    Object.defineProperty(dropped, 'path', { value: '/tmp/input-notes.txt' });
    const input = screen.getByTestId('composer-input');
    const dropEvent = createEvent.drop(input, {
      bubbles: false,
      cancelable: true,
      dataTransfer: {
        files: [dropped],
        items: [],
        types: ['Files'],
      },
    });

    fireEvent(input, dropEvent);

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]);
    });
    expect(dropEvent.defaultPrevented).toBe(true);
  });

  it('falls back to transfer file paths when a dropped DOM File has no path', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ], {
      attachDroppedFilesForComposer: vi.fn(() => 0),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const dropped = new File(['notes'], 'browser-only-notes.txt', { type: 'text/plain' });
    const conversation = screen.getByTestId('conversation-drop-zone');
    const dataTransfer = {
      files: [dropped],
      items: [],
      types: ['Files', 'text/uri-list'],
      getData: (type) => (type === 'text/uri-list' ? 'file:///tmp/browser-only-notes.txt' : ''),
    };

    fireEvent.drop(conversation, { dataTransfer });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]);
    });
    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/browser-only-notes.txt']);
  });

  it('uses transfer file paths when DOM files attach partially', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ], {
      attachDroppedFilesForComposer: vi.fn(() => 1),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const dropped = new File(['notes'], 'partial-fallback.txt', { type: 'text/plain' });
    const conversation = screen.getByTestId('conversation-drop-zone');
    const dataTransfer = {
      files: [dropped],
      items: [],
      types: ['Files', 'text/uri-list'],
      getData: (type) => (type === 'text/uri-list' ? 'file:///tmp/partial-fallback.txt' : ''),
    };

    fireEvent.drop(conversation, { dataTransfer });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]);
    });
    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/partial-fallback.txt']);
  });


  it('accepts native Wails file drops when target details only contain chat-area classes', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    const nativeDropHandler = onFilesDropped.mock.calls.at(-1)?.[0];

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-composer-class.txt'],
        details: { classList: ['composer-card'] },
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-composer-class.txt']);

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-timeline-class.txt'],
        details: { classList: ['timeline'] },
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-timeline-class.txt']);

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-attribute-class.txt'],
        details: { attributes: { class: 'timeline-shell' } },
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-attribute-class.txt']);
  });

  it('accepts native Wails file drops without target details only after entering a chat drop target', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    const nativeDropHandler = onFilesDropped.mock.calls.at(-1)?.[0];
    const conversation = screen.getByTestId('conversation-drop-zone');

    fireEvent.dragEnter(conversation, {
      dataTransfer: {
        files: [new File(['notes'], 'native-missing-details.txt', { type: 'text/plain' })],
        items: [],
        types: ['Files'],
      },
    });

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-missing-details.txt'],
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-missing-details.txt']);
    expect(conversation).not.toHaveClass('drop-active');
  });

  it('rejects native Wails file drops from clearly non-chat or unknown targets', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    const nativeDropHandler = onFilesDropped.mock.calls.at(-1)?.[0];

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-sidebar-drop.txt'],
        details: { id: 'sidebar-thread-item', classList: ['thread-card'] },
      });
      nativeDropHandler?.({
        files: ['/tmp/native-app-nav-drop.txt'],
        details: { classList: ['app-nav'] },
      });
      nativeDropHandler?.({
        files: ['/tmp/native-unknown-drop.txt'],
      });
    });

    expect(store.attachPathsForComposer).not.toHaveBeenCalled();
  });

  it('accepts external uri-list drops on the conversation window', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const conversation = screen.getByTestId('conversation-drop-zone');
    const dataTransfer = {
      files: [],
      items: [],
      types: ['text/uri-list'],
      getData: (type) => (type === 'text/uri-list' ? 'file:///tmp/dropped-uri-notes.txt' : ''),
    };

    fireEvent.dragEnter(conversation, { dataTransfer });

    expect(conversation).toHaveClass('drop-active');

    fireEvent.drop(conversation, { dataTransfer });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/dropped-uri-notes.txt']);
    expect(conversation).not.toHaveClass('drop-active');
  });

  it('attaches copied desktop file paths instead of pasting them as composer text', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [],
        items: [],
        types: ['x-special/gnome-copied-files', 'text/uri-list', 'text/plain'],
        getData: (type) => {
          if (type === 'x-special/gnome-copied-files') return 'copy\nfile:///tmp/copied-notes.txt';
          if (type === 'text/uri-list') return 'file:///tmp/copied-notes.txt';
          if (type === 'text/plain') return '/tmp/copied-notes.txt';
          return '';
        },
      },
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/copied-notes.txt']);
    expect(store.setDraft).not.toHaveBeenCalled();
  });

  it('falls back to plain copied file paths when custom clipboard types cannot be read', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [],
        items: [],
        types: ['x-special/gnome-copied-files', 'text/plain'],
        getData: (type) => {
          if (type === 'x-special/gnome-copied-files') throw new Error('clipboard type unavailable');
          if (type === 'text/plain') return "'/tmp/copied quoted.txt'";
          return '';
        },
      },
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/copied quoted.txt']);
    expect(store.setDraft).not.toHaveBeenCalled();
  });

  it('attaches ordinary clipboard.files File objects with a path instead of pasting text', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const file = new File(['notes'], 'copied-notes.txt', { type: 'text/plain' });
    Object.defineProperty(file, 'path', { value: '/tmp/copied-notes.txt' });

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [file],
        items: [],
        types: ['Files'],
        getData: () => '',
      },
    });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([file]);
    });
    expect(store.setDraft).not.toHaveBeenCalled();
  });

  it('attaches ordinary clipboard.items getAsFile File objects with a path', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const file = new File(['notes'], 'item-notes.txt', { type: 'text/plain' });
    Object.defineProperty(file, 'path', { value: '/tmp/item-notes.txt' });

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [],
        items: [
          { kind: 'file', type: 'text/plain', getAsFile: vi.fn(() => file) },
        ],
        types: ['Files'],
        getData: () => '',
      },
    });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([file]);
    });
  });

  it('routes PNG clipboard File objects with a path through dropped-file attachment handling', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴图片文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const file = new File(['png'], 'copied-image.png', { type: 'image/png' });
    Object.defineProperty(file, 'path', { value: '/tmp/copied-image.png' });

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [file],
        items: [],
        types: ['Files'],
        getData: () => '',
      },
    });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([file]);
    });
    expect(store.attachPastedImagesForComposer).not.toHaveBeenCalled();
  });

  it('keeps no-path image paste in attachment handling', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴截图。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const file = new File(['png'], 'screenshot.png', { type: 'image/png' });

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [file],
        items: [],
        types: ['Files'],
        getData: () => '',
      },
    });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([file]);
    });
  });

  it('renders compact assistant markdown block markers as formatted content', () => {
    const store = createActiveThreadStore([
      {
        id: 'assistant-compact-markdown',
        role: 'assistant',
        text: '我先说明当前进展。 ##Done-Done: 每天17:40自动触发，Cron: `4017***`-Done: 内容方向固定为AI工具-Done: 每天生成3条成片。',
        time: '2026-06-02T08:01:00Z',
      },
    ]);

    const { container } = render(<ChatPage store={store} projectPath="/repo/app" />);

    expect(screen.getByText('我先说明当前进展。')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Done-Done: 每天17:40自动触发/ })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
    expect(screen.getByText('Done: 内容方向固定为AI工具')).toBeInTheDocument();
    expect(screen.getByText('Done: 每天生成3条成片。')).toBeInTheDocument();
    expect(container.querySelector('.message-markdown p')).not.toHaveTextContent('##Done-Done');
  });

  it('treats backend created thread status as idle noise in thread cards', () => {
    const store = createActiveThreadStore([], {
      threads: [{ id: 'thread-1', name: '启动中间态会话', provider: 'codex', status: 'created', updatedAt: '2026-06-02T08:00:00Z' }],
    });

    render(<ChatPage store={store} projectPath="/repo/app" />);

    const card = getThreadCardByName('启动中间态会话');
    expect(card).toHaveTextContent('codex');
    expect(card).not.toHaveTextContent('created');
    expect(card.querySelector('.thread-status-label')).toBeNull();
    expect(card.querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--idle');
    expect(card.querySelector('.thread-status-dot')).toHaveAttribute('title', '空闲');
  });

  it('groups thread card actions separately from the main agent button', () => {
    const store = createFakeStore({
      activeThreadId: 'agent-design',
      threads: [{ id: 'agent-design', name: 'AI 设计流程', provider: 'unknown', status: 'idle' }],
    });

    render(<ChatPage store={store} projectPath="/repo/app" />);

    const card = getThreadCardByName('AI 设计流程');
    const actions = card.querySelector('.thread-card-actions');

    expect(actions).not.toBeNull();
    expect(within(actions).getByRole('button', { name: '重命名会话' })).toBeInTheDocument();
    expect(within(actions).getByRole('button', { name: '置顶对话' })).toBeInTheDocument();
    expect(within(actions).getByRole('button', { name: '归档会话' })).toBeInTheDocument();
    expect(card.querySelector('.thread-main .thread-pin')).toBeNull();
    expect(card.querySelector('.thread-main .thread-rename-trigger')).toBeNull();
  });

  it('renders an active conversation without the chat mode switch cards', () => {
    const store = createFakeStore({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '主线对话', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:03:00Z' },
      ],
      threadTimelineReadyByThread: { 'thread-1': true },
      timelinesByThread: {
        'thread-1': [
          { id: 'user-msg', role: 'user', text: '继续看这个会话', time: '2026-06-02T08:03:00Z' },
          { id: 'assistant-msg', role: 'assistant', text: '时间线保持正常显示。', time: '2026-06-02T08:04:00Z' },
        ],
      },
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.queryByRole('button', { name: '对话' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '命令工作区' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('cmd-workspace')).not.toBeInTheDocument();
    expect(screen.getByText('继续看这个会话')).toBeInTheDocument();
    expect(screen.getByText('时间线保持正常显示。')).toBeInTheDocument();
  });

  it('handles timeline file refs and actionable citation chips', async () => {
    const store = createActiveThreadStore([
      {
        id: 'assistant-1',
        role: 'assistant',
        text: [
          ':codex-file-citation[]{path="src/main.go" line_range_start="9" line_range_end="11"}',
          ':task-stub[Review the patch]{title="Review task"}',
          ':automation-update[Workflow rerun completed]{name="Nightly lint" prompt="Run lint on main"}',
          ':code-comment[Please rename this]{title="Naming" path="src/main.go" line_range_start="9" line_range_end="11"}',
          '[Follow-up](agent://thread-2)',
        ].join(' '),
        time: '2026-06-02T08:00:00Z',
      },
    ], {
      activeProject: '/repo/app',
      projects: ['/repo/app'],
      threads: [
        { id: 'thread-1', name: '主线程', provider: 'codex', status: 'idle' },
        { id: 'thread-2', name: '后续线程', provider: 'codex', status: 'idle' },
      ],
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.click(await screen.findByRole('button', { name: '打开文件引用 src/main.go' }));

    const preview = await screen.findByRole('dialog', { name: '文件预览' });
    expect(locateCodeFile).toHaveBeenCalledWith({
      filePath: 'src/main.go',
      line: 9,
      column: 0,
      project: '/repo/app',
      projects: ['/repo/app'],
    });
    expect(openCodeFile).toHaveBeenCalledWith({
      filePath: '/repo/app/src/main.go',
      line: 9,
      column: 0,
      project: '/repo/app',
      projects: ['/repo/app'],
    });
    expect(within(preview).getByText('src/main.go')).toBeInTheDocument();
    fireEvent.click(within(preview).getByRole('button', { name: '关闭' }));

    fireEvent.click(screen.getByRole('button', { name: 'Review task' }));
    expect(store.setDraft).toHaveBeenLastCalledWith('Review the patch');

    fireEvent.click(screen.getByRole('button', { name: 'Nightly lint' }));
    expect(store.setDraft).toHaveBeenLastCalledWith('Review the patch\n\nAutomation update (Nightly lint):\nRun lint on main');

    fireEvent.click(screen.getByRole('button', { name: /Naming/ }));
    await waitFor(() => {
      expect(store.setDraft).toHaveBeenLastCalledWith(expect.stringContaining('Please rename this'));
      expect(openCodeFile).toHaveBeenCalledTimes(2);
    });

    fireEvent.click(screen.getByRole('button', { name: 'Follow-up' }));
    expect(store.selectThread).toHaveBeenCalledWith('thread-2');
  });

  it('materializes only the recent timeline window until older messages are requested', () => {
    const messages = Array.from({ length: 120 }, (_, index) => ({
      id: `msg-${index + 1}`,
      role: index % 2 === 0 ? 'user' : 'assistant',
      text: `历史消息 ${index + 1}`,
      time: `2026-06-02T08:${String(index % 60).padStart(2, '0')}:00Z`,
    }));

    const { container } = render(<ChatPage store={createActiveThreadStore(messages)} projectPath="/repo/app" />);

    expect(screen.getByText('历史消息 120')).toBeInTheDocument();
    expect(screen.queryByText('历史消息 1')).not.toBeInTheDocument();
    expect(screen.getByTestId('timeline-older-marker')).toHaveTextContent('显示更早的消息（40 条）');
    expect(container.querySelectorAll('.message')).toHaveLength(80);

    fireEvent.click(screen.getByRole('button', { name: '显示更早的消息（40 条）' }));

    expect(screen.getByText('历史消息 1')).toBeInTheDocument();
    expect(container.querySelectorAll('.message')).toHaveLength(120);
    expect(screen.queryByTestId('timeline-older-marker')).not.toBeInTheDocument();
  });

  it('materializes older timeline messages when the user scrolls to the top', () => {
    const messages = Array.from({ length: 90 }, (_, index) => ({
      id: `scroll-msg-${index + 1}`,
      role: index % 2 === 0 ? 'user' : 'assistant',
      text: `滚动历史消息 ${index + 1}`,
      time: `2026-06-02T08:${String(index % 60).padStart(2, '0')}:00Z`,
    }));

    render(<ChatPage store={createActiveThreadStore(messages)} projectPath="/repo/app" />);

    expect(screen.queryByText('滚动历史消息 1')).not.toBeInTheDocument();

    const timeline = screen.getByTestId('chat-timeline');
    Object.defineProperty(timeline, 'scrollTop', { configurable: true, value: 0, writable: true });
    fireEvent.scroll(timeline);

    expect(screen.getByText('滚动历史消息 1')).toBeInTheDocument();
    expect(screen.queryByTestId('timeline-older-marker')).not.toBeInTheDocument();
  });

  it('keeps the bottom shortcut visible in active conversations', () => {
    const messages = [
      { id: 'bottom-msg-1', role: 'user', text: '先看上面的上下文', time: '2026-06-02T08:00:00Z' },
      { id: 'bottom-msg-2', role: 'assistant', text: '最新回复在底部。', time: '2026-06-02T08:01:00Z' },
    ];

    render(<ChatPage store={createActiveThreadStore(messages)} projectPath="/repo/app" />);

    const timeline = screen.getByTestId('chat-timeline');
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 1200 });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      value: 180,
      writable: true,
    });

    const bottomButton = screen.getByRole('button', { name: '滚动到底部' });
    expect(bottomButton).toHaveAttribute('title', '滚动到底部');

    fireEvent.click(bottomButton);

    expect(timeline.scrollTop).toBe(1200);
    expect(screen.getByRole('button', { name: '滚动到底部' })).toBe(bottomButton);
  });

  it('keeps the timeline pinned to the bottom while an active assistant reply grows', () => {
    const initialMessages = [
      { id: 'reply-user-1', role: 'user', text: '请继续分析', time: '2026-06-02T08:00:00Z' },
      { id: 'reply-assistant-1', role: 'assistant', text: '我先检查一下。', time: '2026-06-02T08:01:00Z' },
    ];
    const { rerender } = render(<ChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);
    const timeline = screen.getByTestId('chat-timeline');
    let scrollHeight = 1000;
    let scrollTop = 600;
    Object.defineProperty(timeline, 'clientHeight', { configurable: true, get: () => 400 });
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, get: () => scrollHeight });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      get: () => scrollTop,
      set: (value) => {
        scrollTop = Number(value);
      },
    });
    const animationFrameCallbacks = [];
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      animationFrameCallbacks.push(callback);
      return animationFrameCallbacks.length;
    });

    scrollHeight = 1260;
    rerender(<ChatPage store={createActiveThreadStore([
      initialMessages[0],
      { ...initialMessages[1], text: '我先检查一下。\n\n已经定位到滚动逻辑。' },
    ])} projectPath="/repo/app" />);

    expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(1);
    act(() => {
      for (const callback of animationFrameCallbacks) callback(16);
    });
    expect(scrollTop).toBe(1260);
    requestAnimationFrameSpy.mockRestore();
  });

  it('corrects scroll to bottom via MutationObserver when DOM mutations occur and sticky is active', async () => {
    const initialMessages = [
      { id: 'reply-user-1', role: 'user', text: '请继续分析', time: '2026-06-02T08:00:00Z' },
    ];
    const { rerender } = render(<ChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);
    const timeline = screen.getByTestId('chat-timeline');
    let scrollHeight = 1000;
    let scrollTop = 600;
    Object.defineProperty(timeline, 'clientHeight', { configurable: true, get: () => 400 });
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, get: () => scrollHeight });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      get: () => scrollTop,
      set: (value) => {
        scrollTop = Number(value);
      },
    });

    scrollHeight = 1400;

    await act(async () => {
      rerender(<ChatPage store={createActiveThreadStore([
        ...initialMessages,
        { id: 'reply-assistant-1', role: 'assistant', text: '全新回复。', time: '2026-06-02T08:01:00Z' },
      ])} projectPath="/repo/app" />);
    });

    await waitFor(() => {
      expect(scrollTop).toBe(1400);
    });
  });

  it('reveals an active assistant reply incrementally when a batched update grows the text', async () => {
    const initialMessages = [
      { id: 'reply-user-1', role: 'user', text: '请继续分析', time: '2026-06-02T08:00:00Z' },
      { id: 'reply-assistant-1', role: 'assistant', text: '我', time: '2026-06-02T08:01:00Z', done: false },
    ];
    const animationFrameCallbacks = [];
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      animationFrameCallbacks.push(callback);
      return animationFrameCallbacks.length;
    });
    const cancelAnimationFrameSpy = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});

    const { rerender } = render(<ChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);
    expect(screen.getByText('我')).toBeInTheDocument();

    const fullReply = '我先检查输出节奏，再继续。';
    rerender(<ChatPage store={createActiveThreadStore([
      initialMessages[0],
      { ...initialMessages[1], text: fullReply },
    ])} projectPath="/repo/app" />);

    expect(screen.queryByText(fullReply)).not.toBeInTheDocument();

    for (let index = 0; index < 16 && !screen.queryByText(fullReply); index += 1) {
      const callback = animationFrameCallbacks.shift();
      expect(callback).toBeTypeOf('function');
      await act(async () => {
        callback(16);
      });
    }

    expect(screen.getByText(fullReply)).toBeInTheDocument();
    requestAnimationFrameSpy.mockRestore();
    cancelAnimationFrameSpy.mockRestore();
  });

  it('reveals an active assistant reply immediately when smoothStreaming is disabled', () => {
    const initialMessages = [
      { id: 'reply-user-1', role: 'user', text: '请继续分析', time: '2026-06-02T08:00:00Z' },
      { id: 'reply-assistant-1', role: 'assistant', text: '我', time: '2026-06-02T08:01:00Z', done: false },
    ];
    const store = createActiveThreadStore(initialMessages, {
      smoothStreaming: false,
    });
    const { rerender } = render(<ChatPage store={store} projectPath="/repo/app" />);
    expect(screen.getByText('我')).toBeInTheDocument();

    const fullReply = '我先检查输出节奏，再继续。';
    rerender(<ChatPage store={createActiveThreadStore([
      initialMessages[0],
      { ...initialMessages[1], text: fullReply },
    ], {
      smoothStreaming: false,
    })} projectPath="/repo/app" />);

    expect(screen.getByText(fullReply)).toBeInTheDocument();
  });

  it('shows completed assistant replies immediately without streaming reveal', () => {
    const fullReply = '完成后的回复一次显示完整。';
    const initialMessages = [
      { id: 'reply-user-1', role: 'user', text: '请继续分析', time: '2026-06-02T08:00:00Z' },
      { id: 'reply-assistant-1', role: 'assistant', text: '完', time: '2026-06-02T08:01:00Z', done: false },
    ];
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 1);
    const cancelAnimationFrameSpy = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
    const { rerender } = render(<ChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);

    rerender(<ChatPage store={createActiveThreadStore([
      initialMessages[0],
      { ...initialMessages[1], text: fullReply, done: true },
    ])} projectPath="/repo/app" />);

    expect(screen.getByText(fullReply)).toBeInTheDocument();
    requestAnimationFrameSpy.mockRestore();
    cancelAnimationFrameSpy.mockRestore();
  });

  it('shows active assistant replies immediately when reduced motion is requested', () => {
    window.matchMedia = vi.fn((query) => ({
      matches: query === '(prefers-reduced-motion: reduce)',
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    const fullReply = '减少动画时直接显示完整回复。';

    render(<ChatPage store={createActiveThreadStore([
      { id: 'reply-user-1', role: 'user', text: '请继续分析', time: '2026-06-02T08:00:00Z' },
      { id: 'reply-assistant-1', role: 'assistant', text: fullReply, time: '2026-06-02T08:01:00Z', done: false },
    ])} projectPath="/repo/app" />);

    expect(screen.getByText(fullReply)).toBeInTheDocument();
  });

  it('copies the full assistant reply while the visible streaming text is still catching up', async () => {
    const initialMessages = [
      { id: 'reply-user-1', role: 'user', text: '请继续分析', time: '2026-06-02T08:00:00Z' },
      { id: 'reply-assistant-1', role: 'assistant', text: '我', time: '2026-06-02T08:01:00Z', done: false },
    ];
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 1);
    const cancelAnimationFrameSpy = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
    const { rerender } = render(<ChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);
    const fullReply = '我先检查输出节奏，再继续。';

    rerender(<ChatPage store={createActiveThreadStore([
      initialMessages[0],
      { ...initialMessages[1], text: fullReply },
    ])} projectPath="/repo/app" />);

    expect(screen.queryByText(fullReply)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '复制 AI 输出' }));

    await waitFor(() => expect(copyTextToClipboard).toHaveBeenCalledWith(fullReply));
    requestAnimationFrameSpy.mockRestore();
    cancelAnimationFrameSpy.mockRestore();
  });

  it('uses the timeline bottom scroll path when live assistant completion replaces pending reasoning', () => {
    const userMessage = { id: 'live-user-1', role: 'user', text: '请继续分析', time: '2026-06-02T08:00:00Z' };
    const runningStore = createActiveThreadStore([userMessage], {
      activeTurnByThread: {
        'thread-1': { id: 'turn-1', startedAt: '2026-06-02T08:00:05Z' },
      },
      sending: true,
      statuses: { 'thread-1': { state: 'running' } },
    });
    const { rerender } = render(<ChatPage store={runningStore} projectPath="/repo/app" />);

    expect(screen.getByLabelText('AI 思考记录')).toBeInTheDocument();

    const timeline = screen.getByTestId('chat-timeline');
    let scrollHeight = 980;
    let scrollTop = 580;
    Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, get: () => scrollHeight });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      get: () => scrollTop,
      set: (value) => {
        scrollTop = Number(value);
      },
    });
    const originalScrollIntoView = window.HTMLElement.prototype.scrollIntoView;
    const scrollIntoViewSpy = vi.fn();
    window.HTMLElement.prototype.scrollIntoView = scrollIntoViewSpy;
    const animationFrameCallbacks = [];
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      animationFrameCallbacks.push(callback);
      return animationFrameCallbacks.length;
    });

    try {
      scrollHeight = 1240;
      rerender(<ChatPage store={createActiveThreadStore([
        userMessage,
        { id: 'live-assistant-1', role: 'assistant', text: '已经定位到 live completion 后的滚动问题。', time: '2026-06-02T08:01:00Z' },
      ], {
        statuses: { 'thread-1': { state: 'completed' } },
      })} projectPath="/repo/app" />);

      expect(scrollIntoViewSpy).not.toHaveBeenCalled();
      expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(1);
      act(() => {
        for (const callback of animationFrameCallbacks) callback(16);
      });
      expect(scrollTop).toBe(1240);
    }
    finally {
      requestAnimationFrameSpy.mockRestore();
      if (originalScrollIntoView) {
        window.HTMLElement.prototype.scrollIntoView = originalScrollIntoView;
      }
      else {
        delete window.HTMLElement.prototype.scrollIntoView;
      }
    }
  });

  it('does not auto-scroll a second time when a user message is appended', () => {
    const initialMessages = [
      { id: 'existing-assistant-1', role: 'assistant', text: '上一轮回复。', time: '2026-06-02T08:00:00Z' },
    ];
    const { rerender } = render(<ChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);
    const timeline = screen.getByTestId('chat-timeline');
    Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 1000 });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      value: 600,
      writable: true,
    });
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 1);

    rerender(<ChatPage store={createActiveThreadStore([
      ...initialMessages,
      { id: 'new-user-1', role: 'user', text: '继续处理', time: '2026-06-02T08:01:00Z' },
    ])} projectPath="/repo/app" />);

    expect(requestAnimationFrameSpy).not.toHaveBeenCalled();
    requestAnimationFrameSpy.mockRestore();
  });

  it('loads an older backend message page when no local older messages are hidden', async () => {
    let resolveLoad;
    const loadPromise = new Promise((resolve) => {
      resolveLoad = resolve;
    });
    const loadOlderThreadMessages = vi.fn(() => loadPromise);
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'user', text: '当前页第一条', time: '2026-06-02T08:00:00Z' },
      { id: 'msg-2', role: 'assistant', text: '当前页第二条', time: '2026-06-02T08:01:00Z' },
    ], {
      loadOlderThreadMessages,
      threadMessagePaginationByThread: {
        'thread-1': { hasMore: true, nextBefore: 'msg-1', loading: false },
      },
    });

    render(<ChatPage store={store} projectPath="/repo/app" />);

    fireEvent.click(screen.getByRole('button', { name: '加载更早的消息' }));

    expect(loadOlderThreadMessages).toHaveBeenCalledTimes(1);
    expect(loadOlderThreadMessages).toHaveBeenCalledWith('thread-1');
    expect(screen.getByRole('button', { name: '正在加载更早的消息' })).toBeDisabled();

    const timeline = screen.getByTestId('chat-timeline');
    Object.defineProperty(timeline, 'scrollTop', { configurable: true, value: 0, writable: true });
    fireEvent.scroll(timeline);
    expect(loadOlderThreadMessages).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveLoad(true);
      await loadPromise;
    });
    expect(screen.getByRole('button', { name: '加载更早的消息' })).not.toBeDisabled();
  });

  it('reveals hidden local older messages before loading another backend page', () => {
    const loadOlderThreadMessages = vi.fn(() => Promise.resolve(true));
    const messages = Array.from({ length: 120 }, (_, index) => ({
      id: `local-hidden-msg-${index + 1}`,
      role: index % 2 === 0 ? 'user' : 'assistant',
      text: `本地隐藏历史消息 ${index + 1}`,
      time: `2026-06-02T08:${String(index % 60).padStart(2, '0')}:00Z`,
    }));
    const store = createActiveThreadStore(messages, {
      loadOlderThreadMessages,
      threadMessagePaginationByThread: {
        'thread-1': { hasMore: true, nextBefore: 'local-hidden-msg-1', loading: false },
      },
    });

    render(<ChatPage store={store} projectPath="/repo/app" />);

    expect(screen.queryByText('本地隐藏历史消息 1')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '显示更早的消息（40 条）' }));

    expect(screen.getByText('本地隐藏历史消息 1')).toBeInTheDocument();
    expect(loadOlderThreadMessages).not.toHaveBeenCalled();
  });

  it('does not render Mermaid diagrams from unmaterialized older history', async () => {
    const messages = Array.from({ length: 85 }, (_, index) => {
      if (index === 0) {
        return {
          id: 'hidden-mermaid',
          role: 'assistant',
          text: [
            '旧图表不应首屏渲染：',
            '```mermaid',
            'flowchart TD',
            '  Old[旧历史] --> Heavy[重渲染]',
            '```',
          ].join('\n'),
          time: '2026-06-02T07:00:00Z',
        };
      }
      return {
        id: `recent-${index}`,
        role: index % 2 === 0 ? 'user' : 'assistant',
        text: `最近消息 ${index}`,
        time: `2026-06-02T08:${String(index % 60).padStart(2, '0')}:00Z`,
      };
    });

    render(<ChatPage store={createActiveThreadStore(messages)} projectPath="/repo/app" />);

    expect(screen.getByText('最近消息 84')).toBeInTheDocument();
    expect(screen.queryByText('旧图表不应首屏渲染：')).not.toBeInTheDocument();
    expect(mermaid.render).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '显示更早的消息（5 条）' }));

    expect(screen.getByText('旧图表不应首屏渲染：')).toBeInTheDocument();
    await waitFor(() => expect(mermaid.render).toHaveBeenCalledTimes(1));
  });

  it('does not render compact non-standard markdown prefix as a heading', () => {
    const messages = [
      {
        id: 'msg-1',
        role: 'assistant',
        text: '随便贴一篇：##回家之路无论走了多远',
        time: '2026-06-02T08:00:00Z',
        done: true,
      },
    ];
    const store = createActiveThreadStore(messages);
    render(<ChatPage store={store} projectPath="/repo/app" />);
    // 应该以普通段落渲染，所以不应该有 heading 元素
    expect(screen.queryByRole('heading', { name: /回家之路无论走了多远/ })).not.toBeInTheDocument();
    expect(screen.getByText(/随便贴一篇：##回家之路无论走了多远/)).toBeInTheDocument();
  });

  it('supports deleting an individual archived thread with confirmation', () => {
    const store = createFakeStore({
      activeThreadId: 'thread-active',
      threads: [
        { id: 'thread-active', name: 'Active Thread', provider: 'codex', status: 'idle' },
        { id: 'thread-archived-1', name: 'Archived Thread 1', provider: 'codex', status: 'archived', archived: true },
      ],
    });

    render(<ChatPage store={store} projectPath="/repo/app" />);

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    expect(screen.getByText('Archived Thread 1')).toBeInTheDocument();

    const deleteBtn = screen.getByLabelText('删除会话');
    expect(deleteBtn).toBeInTheDocument();
    fireEvent.click(deleteBtn);

    expect(screen.getByText('确定删除该会话？')).toBeInTheDocument();
    const confirmBtn = screen.getByRole('button', { name: '确认' });
    const cancelBtn = screen.getByRole('button', { name: '取消' });
    expect(confirmBtn).toBeInTheDocument();
    expect(cancelBtn).toBeInTheDocument();

    fireEvent.click(cancelBtn);
    expect(screen.queryByText('确定删除该会话？')).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('删除会话'));
    fireEvent.click(screen.getByRole('button', { name: '确认' }));

    expect(store.deleteStaleThreads).toHaveBeenCalledWith(['thread-archived-1']);
  });
  it('[regression] renders user message image attachments inline (data: URL and clipboard route)', () => {
    const store = createActiveThreadStore([
      {
        id: 'user-with-image',
        role: 'user',
        text: '能先识别这张截图内容。',
        time: '2026-06-02T08:00:00Z',
        attachments: [
          {
            kind: 'image',
            name: 'screenshot.png',
            path: '/var/folders/abc/T/clipboard-123456.png',
            previewUrl: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==',
          },
        ],
      },
    ]);

    const { container } = render(<ChatPage store={store} projectPath="/repo/app" />);

    expect(screen.getByText('能先识别这张截图内容。')).toBeInTheDocument();
    // 图片附件应作为 img 元素渲染，而不是消失或变成文件 pill
    const img = container.querySelector('.user-attachment-gallery img');
    expect(img).not.toBeNull();
    expect(img.getAttribute('src')).toMatch(/^data:image\//);
    // 不应该显示为文件 pill
    expect(container.querySelector('.user-attachment-file-pill')).toBeNull();
  });

  it('[regression] renders user message clipboard-route image attachment from history', () => {
    const store = createActiveThreadStore([
      {
        id: 'user-clipboard-image',
        role: 'user',
        text: '看这张图。',
        time: '2026-06-02T08:00:00Z',
        attachments: [
          {
            kind: 'image',
            name: 'clipboard-987654321.png',
            path: '/var/folders/abc/T/clipboard-987654321.png',
            previewUrl: '/clipboard/clipboard-987654321.png',
          },
        ],
      },
    ]);

    const { container } = render(<ChatPage store={store} projectPath="/repo/app" />);

    expect(screen.getByText('看这张图。')).toBeInTheDocument();
    const img = container.querySelector('.user-attachment-gallery img');
    expect(img).not.toBeNull();
    expect(img.getAttribute('src')).toBe('/clipboard/clipboard-987654321.png');
    expect(container.querySelector('.user-attachment-file-pill')).toBeNull();
  });

  it('[regression] renders streaming list items correctly without splitting them', async () => {
    const store = createActiveThreadStore([
      {
        id: 'msg-stream',
        role: 'assistant',
        text: '# Title\n- item 1\n- item 2\n',
        time: '2026-06-02T08:00:00Z',
        done: false,
      },
    ], { smoothStreaming: false });

    const { rerender } = render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const updatedStore = createActiveThreadStore([
      {
        id: 'msg-stream',
        role: 'assistant',
        text: '# Title\n- item 1\n- item 2\n- item 3\n',
        time: '2026-06-02T08:00:00Z',
        done: false,
      },
    ], { smoothStreaming: false });

    rerender(<TestChatPageWrapper store={updatedStore} projectPath="/repo/app" />);

    const lists = screen.getAllByRole('list');
    expect(lists).toHaveLength(1);

    const listItems = screen.getAllByRole('listitem');
    expect(listItems).toHaveLength(3);
    expect(listItems[0]).toHaveTextContent('item 1');
    expect(listItems[1]).toHaveTextContent('item 2');
    expect(listItems[2]).toHaveTextContent('item 3');
  });

  it('[regression] scrolls to bottom after delay when timelineContentBlocked becomes false', async () => {
    vi.useFakeTimers();
    try {
      const messages = [
        { id: 'msg-1', role: 'user', text: 'hello', time: '2026-06-02T08:00:00Z' },
      ];
      const storeLoading = createActiveThreadStore([], {
        threadStateLoadingByThread: { 'thread-1': true },
        threadTimelineReadyByThread: { 'thread-1': false },
      });
      const { rerender } = render(<TestChatPageWrapper store={storeLoading} projectPath="/repo/app" />);
      
      const timeline = screen.getByTestId('chat-timeline');
      let scrollTop = 0;
      Object.defineProperty(timeline, 'clientHeight', { configurable: true, get: () => 400 });
      Object.defineProperty(timeline, 'scrollHeight', { configurable: true, get: () => 1000 });
      Object.defineProperty(timeline, 'scrollTop', {
        configurable: true,
        get: () => scrollTop,
        set: (value) => {
          scrollTop = Number(value);
        },
      });

      const storeLoaded = createActiveThreadStore(messages, {
        threadStateLoadingByThread: { 'thread-1': false },
        threadTimelineReadyByThread: { 'thread-1': true },
      });
      rerender(<TestChatPageWrapper store={storeLoaded} projectPath="/repo/app" />);

      expect(scrollTop).toBe(1000);

      scrollTop = 0;
      
      act(() => {
        vi.advanceTimersByTime(50);
      });

      expect(scrollTop).toBe(1000);
    } finally {
      vi.useRealTimers();
    }
  });

  it('adjusts scroll to bottom when a child resource loads and stickiness is enabled', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'user', text: '图片加载测试', time: '2026-06-02T08:00:00Z' }
    ]);
    render(<ChatPage store={store} projectPath="/repo/app" />);
    
    const timeline = screen.getByTestId('chat-timeline');
    
    let scrollTop = 500;
    Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 1500 });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      get: () => scrollTop,
      set: (val) => {
        scrollTop = val;
      },
    });

    const img = document.createElement('img');
    timeline.appendChild(img);
    
    const loadEvent = createEvent('load', img, {
      bubbles: false,
      cancelable: true,
    });
    
    act(() => {
      fireEvent(img, loadEvent);
    });
    
    expect(scrollTop).toBe(1500);
  });

  it('does not adjust scroll to bottom when a child resource loads but stickiness is disabled', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'user', text: '图片加载测试无粘性', time: '2026-06-02T08:00:00Z' }
    ]);
    render(<ChatPage store={store} projectPath="/repo/app" />);
    
    const timeline = screen.getByTestId('chat-timeline');
    
    let scrollTop = 500;
    Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 1500 });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      get: () => scrollTop,
      set: (val) => {
        scrollTop = val;
      },
    });

    // 触发 scroll 事件，使 shouldStickToBottomRef 变为 false
    act(() => {
      fireEvent.scroll(timeline);
    });

    const img = document.createElement('img');
    timeline.appendChild(img);
    
    const loadEvent = createEvent('load', img, {
      bubbles: false,
      cancelable: true,
    });
    
    act(() => {
      fireEvent(img, loadEvent);
    });
    
    expect(scrollTop).toBe(500); // 应该没有被重置到 1500
  });

  it('resets scrollTop to 0 when activeThreadId changes to prevent out-of-bounds rendering glitch', () => {
    const store1 = createActiveThreadStore([
      { id: 'msg-1', role: 'user', text: 'Thread 1 message', time: '2026-06-02T08:00:00Z' }
    ], { activeThreadId: 'thread-1' });

    let setScrollTopValue = null;
    const originalScrollTopDesc = Object.getOwnPropertyDescriptor(HTMLDivElement.prototype, 'scrollTop');
    
    Object.defineProperty(HTMLDivElement.prototype, 'scrollTop', {
      configurable: true,
      get() {
        return 500;
      },
      set(val) {
        setScrollTopValue = val;
      },
    });

    try {
      const { rerender } = render(<ChatPage store={store1} projectPath="/repo/app" />);
      
      const store2 = createActiveThreadStore([], {
        activeThreadId: 'thread-2',
        threadStateLoadingByThread: { 'thread-2': true },
        threadTimelineReadyByThread: { 'thread-2': false },
      });

      act(() => {
        rerender(<ChatPage store={store2} projectPath="/repo/app" />);
      });

      expect(setScrollTopValue).toBe(0);
    } finally {
      if (originalScrollTopDesc) {
        Object.defineProperty(HTMLDivElement.prototype, 'scrollTop', originalScrollTopDesc);
      } else {
        delete HTMLDivElement.prototype.scrollTop;
      }
    }
  });
});
