import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import mermaid from 'mermaid';
import { ChatPage } from './ChatPage.jsx';

vi.mock('../../shared/api/backendApi.js', () => ({
  copyTextToClipboard: vi.fn(),
  onFilesDropped: vi.fn(() => () => {}),
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
    hasActiveThreadActions: vi.fn(() => Boolean(store.activeThreadId)),
    hasInterruptibleThreadAction: vi.fn(() => false),
    interruptActiveThread: vi.fn(),
    loadOlderThreadMessages: vi.fn(),
    loadThreadConfig: vi.fn(),
    newThread: vi.fn(),
    openNewWindow: vi.fn(),
    pendingActiveThreadId: '',
    permission: '工作区写入',
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
    setDraft: vi.fn((value) => {
      store.draft = value;
    }),
    setPermission: vi.fn((value) => {
      store.permission = value;
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

function TestChatPageWrapper({ store, projectPath, rightPanelOpen: initialOpen = false }) {
  const [open, setOpen] = React.useState(initialOpen);
  const bootstrapFailureMessage = store.bootstrapStatus === 'failed' && store.error
    ? `连接后端失败：${store.error}`
    : '';
  const feedback = store.actionNotice?.message
    ? store.actionNotice
    : (bootstrapFailureMessage ? { message: bootstrapFailureMessage, tone: 'error' } : null);

  return (
    <div>
      <button type="button" onClick={() => setOpen((prev) => !prev)}>
        显示侧边栏
      </button>
      {feedback?.message ? (
        <span
          className={`action-feedback ${feedback.tone || 'info'}`}
          data-testid="chat-action-feedback"
          role="status"
        >
          {feedback.message}
        </span>
      ) : null}
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
});

describe('ChatPage module', () => {
  it('exports the chat page component', () => {
    expect(ChatPage).toBeTypeOf('function');
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
    expect(screen.getByText('暂无会话，点击「新建对话」开始草稿')).toBeInTheDocument();
    expect(screen.getByTestId('composer-input')).toHaveValue('请修复测试');
    expect(screen.getByRole('button', { name: '发送消息' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '添加文件' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '请先连接后端并选择项目' })).toBeDisabled();
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

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByText('修复会话')).toBeInTheDocument();
    expect(screen.getByText('哪里失败了？')).toBeInTheDocument();
    expect(screen.getByText('测试在聊天页缺少覆盖。')).toBeInTheDocument();
    expect(screen.getByText('12 / 1000 tokens')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    expect(store.sendDraft).toHaveBeenCalledTimes(1);

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

    const card = screen.getByText('启动中间态会话').closest('.thread-card');
    expect(card).toHaveTextContent('codex');
    expect(card).not.toHaveTextContent('created');
    expect(card.querySelector('.thread-status-label')).toBeNull();
    expect(card.querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--idle');
    expect(card.querySelector('.thread-status-dot')).toHaveAttribute('title', '空闲');
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
    Object.defineProperty(timeline, 'scrollTop', { configurable: true, value: 0 });
    fireEvent.scroll(timeline);

    expect(screen.getByText('滚动历史消息 1')).toBeInTheDocument();
    expect(screen.queryByTestId('timeline-older-marker')).not.toBeInTheDocument();
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
    Object.defineProperty(timeline, 'scrollTop', { configurable: true, value: 0 });
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
});
