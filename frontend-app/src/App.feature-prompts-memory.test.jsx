import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  expect,
  it,
  useClientStore,
  normalizeMemorySnapshotForFacade,
  App,
  backend,
  deferred,
  waitForBackendThreadHeading,
  openPluginsAndSkillsPage,
  findThreadCardByName,
} = testEnv;

it('keeps an empty archived thread selection out of the new-chat intro state', async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: 'thread-1',
    threads: [
      { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
      { id: 'thread-archived', name: '空归档线程', provider: 'codex', status: 'archived' },
    ],
  });
  backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
    activeThreadId: threadId,
    threads: [
      { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
      { id: 'thread-archived', name: '空归档线程', provider: 'codex', status: 'idle' },
    ],
    timelinesByThread: threadId === 'thread-1'
      ? { 'thread-1': [{ id: 'active-msg', kind: 'assistant', text: '活跃线程内容' }] }
      : { 'thread-archived': [] },
  }));

  render(<App />);
  await screen.findByText('活跃线程内容');

  fireEvent.click(screen.getByLabelText('打开归档列表'));
  fireEvent.click(await screen.findByRole('button', { name: /空归档线程/ }));

  await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-archived'));
  expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: /空归档线程/ }).closest('.thread-card')).toHaveClass('active');
  expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
});

it('loads archived thread messages from the legacy messages RPC', async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: 'thread-1',
    threads: [
      { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
      { id: 'thread-archived', name: '消息归档线程', provider: 'codex', status: 'archived' },
    ],
  });
  backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
    activeThreadId: threadId,
    threads: [
      { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
      { id: 'thread-archived', name: '消息归档线程', provider: 'codex', status: 'idle' },
    ],
    timelinesByThread: threadId === 'thread-1'
      ? { 'thread-1': [{ id: 'active-msg', kind: 'assistant', text: '活跃线程内容' }] }
      : { 'thread-archived': [] },
  }));
  backend.getThreadMessages.mockImplementation(({ threadId }) => Promise.resolve({
    messages: threadId === 'thread-archived'
      ? [{ id: 'archived-message', role: 'assistant', content: '来自 thread/messages 的归档内容', createdAt: '2026-05-30T00:00:00Z' }]
      : [],
    total: threadId === 'thread-archived' ? 1 : 0,
    hasMore: false,
    nextBefore: '',
  }));

  render(<App />);
  await screen.findByText('活跃线程内容');

  fireEvent.click(screen.getByLabelText('打开归档列表'));
  fireEvent.click(await screen.findByRole('button', { name: /消息归档线程/ }));

  expect(await screen.findByText('来自 thread/messages 的归档内容')).toBeInTheDocument();
  expect(backend.getThreadMessages).toHaveBeenCalledWith({ threadId: 'thread-archived', limit: 300 });
  expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
});

it('cleans stale archived threads through the legacy delete RPC', async () => {
  const staleArchiveAt = Date.now() - (8 * 24 * 60 * 60 * 1000);
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: 'thread-1',
    threads: [
      { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
      { id: 'thread-stale', name: '旧归档线程', provider: 'codex', status: 'archived', archivedAt: staleArchiveAt },
      { id: 'thread-fresh', name: '近期归档线程', provider: 'codex', status: 'archived', archivedAt: Date.now() },
    ],
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: 'thread-1',
    timelinesByThread: {},
  });

  render(<App />);
  await findThreadCardByName('活跃线程');

  fireEvent.click(screen.getByLabelText('打开归档列表'));
  expect(await screen.findByText('旧归档线程')).toBeInTheDocument();
  expect(screen.getByText('近期归档线程')).toBeInTheDocument();
  expect(screen.getByText('超7天')).toBeInTheDocument();

  fireEvent.click(screen.getByLabelText('清理无用对话'));
  fireEvent.click(screen.getByText('确认'));

  await waitFor(() => {
    expect(backend.deleteThread).toHaveBeenCalledWith({ threadId: 'thread-stale' });
    expect(backend.deleteThread).not.toHaveBeenCalledWith({ threadId: 'thread-fresh' });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'archivedThreadAtById.thread-stale',
      value: null,
    });
    expect(screen.queryByText('旧归档线程')).not.toBeInTheDocument();
    expect(screen.getByText('近期归档线程')).toBeInTheDocument();
    expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('已删除 1 个无用会话');
  });
});

it('renders warning log entries from bridge events', async () => {
  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
  fireEvent.keyDown(screen.getByTestId('activity-panel-resizer'), { key: 'ArrowUp' });

  act(() => {
    backend.__bridgeCallback({
      type: 'rpc.failed',
      payload: { method: 'turn/start', threadId: 'thread-1', traceId: 'trace-123' },
    });
  });

  const warningLine = await screen.findByRole('button', { name: /rpc.failed/ });
  expect(screen.queryByText(/turn\/start/)).not.toBeInTheDocument();

  fireEvent.mouseEnter(warningLine);
  expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
  fireEvent.click(warningLine);

  expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('rpc.failed');
  expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('turn/start');

  fireEvent.keyDown(screen.getByRole('dialog', { name: 'rpc.failed' }), { key: 'Escape' });

  await waitFor(() => {
    expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
  });
});

it('navigates to screenshot-style secondary pages', async () => {
  render(<App />);
  await screen.findByLabelText('插件与技能');

  expect(screen.queryByLabelText('命令')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('任务')).not.toBeInTheDocument();

  openPluginsAndSkillsPage();
  expect(await screen.findByRole('heading', { name: 'MCP工具' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Skill工具' })).not.toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'Skill工具' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '新增工具' })).not.toBeInTheDocument();
  expect(screen.queryByText('本地技能库')).not.toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: '后端' })).not.toBeInTheDocument();
  expect(backend.listSkillTools).not.toHaveBeenCalled();
  expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });
  expect(backend.listSkillResolutions).not.toHaveBeenCalled();

  fireEvent.click(screen.getByLabelText('共享文件'));
  expect(await screen.findByText('文件产物')).toBeInTheDocument();
  await waitFor(() => {
    expect(backend.listSharedFiles).toHaveBeenCalledWith();
  });
});

it.each([
  ['提示词', '个性化', '暂无内容', () => expect(backend.listPromptAssets).not.toHaveBeenCalled()],
  ['自动化', '自动化', '创建首个自动化', () => expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '未选择项目', page: 'dags' })],
  ['记忆中心', '记忆中心', '暂无记忆', () => expect(backend.getMemorySnapshot).not.toHaveBeenCalledWith({ cwd: '未选择项目' })],
])('keeps the %s route visible while project context resolves', async (navLabel, heading, settledText, assertNoInvalidLoad) => {
  const config = deferred();
  backend.readConfig.mockReturnValueOnce(config.promise);

  render(<App />);
  fireEvent.click(screen.getByLabelText(navLabel));

  await waitFor(() => expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent(heading));
  assertNoInvalidLoad();

  await act(async () => {
    config.resolve({ cwd: '/repo/app' });
    await Promise.resolve();
    await Promise.resolve();
  });

  expect(await screen.findByText(settledText)).toBeInTheDocument();
});

it('loads global shared files while project context resolves', async () => {
  const config = deferred();
  backend.readConfig.mockReturnValueOnce(config.promise);

  render(<App />);
  fireEvent.click(screen.getByLabelText('共享文件'));

  await waitFor(() => expect(document.querySelector('.suiyuan-appbar-title h1')).toHaveTextContent('文件产物'));
  expect(screen.queryByText('正在连接本地项目...')).not.toBeInTheDocument();
  await waitFor(() => {
    expect(backend.listSharedFiles).toHaveBeenCalledWith();
  });
  expect(backend.listSharedFiles).not.toHaveBeenCalledWith({ cwd: '未选择项目' });

  await act(async () => {
    config.resolve({ cwd: '/repo/app' });
    await Promise.resolve();
    await Promise.resolve();
  });
  expect(await screen.findByText('还没有文件产物')).toBeInTheDocument();
});

it('does not mark the memory center nav when no similar memories need merging', async () => {
  render(<App />);
  await waitForBackendThreadHeading();

  expect(screen.getByLabelText('记忆中心').querySelector('i')).toBeNull();
});

it('marks the memory center nav only for similar memories that need merging', async () => {
  backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: false,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: {
        preferenceCount: 0,
        projectCount: 0,
        maxPerCategory: 15,
        similarGroups: [{
          nameA: 'A', targetA: 'private', pathA: 'feedback/a.md',
          nameB: 'B', targetB: 'team', pathB: 'feedback/b.md',
          score: 0.88,
        }, {
          nameA: 'C', targetA: 'private', pathA: 'feedback/c.md',
          nameB: 'D', targetB: 'team', pathB: 'feedback/d.md',
          score: 0.82,
        }],
      },
    },
    private: { entries: [] },
    team: { entries: [] },
  }));

  render(<App />);
  await waitForBackendThreadHeading();

  await waitFor(() => {
    expect(screen.getByLabelText('记忆中心').querySelector('i')).toHaveAttribute('title', '2 条待整合相似记忆');
  });
});
