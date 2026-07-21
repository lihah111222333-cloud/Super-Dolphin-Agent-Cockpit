import React from 'react';
import { readFileSync } from 'node:fs';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { TestChatPageWrapper, createActiveThreadStore, createFakeStore, createShellLayoutTestHarness, getThreadCardByName } from './__tests__/chatPageTestSupport.js';

function installTimelineMetrics(timeline) {
  let scrollHeight = 1000;
  let scrollTop = 600;
  Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
  Object.defineProperty(timeline, 'scrollHeight', { configurable: true, get: () => scrollHeight });
  Object.defineProperty(timeline, 'scrollTop', {
    configurable: true,
    get: () => scrollTop,
    set: (value) => {
      scrollTop = Number(value);
    },
  });
  return {
    getScrollTop: () => scrollTop,
    setScrollHeight: (value) => {
      scrollHeight = value;
    },
    setScrollTop: (value) => {
      scrollTop = value;
    },
  };
}

function scrollIntentMessages(text = '初始回复') {
  return [
    { id: 'scroll-user-1', role: 'user', text: '继续分析滚动行为', time: '2026-06-02T08:00:00Z' },
    { id: 'scroll-assistant-1', role: 'assistant', text, time: '2026-06-02T08:01:00Z', done: false },
  ];
}

  it('renders an active thread timeline, sends through the store, and opens the runtime panel', async () => {
    const activityStats = { commands: 2, fileEdits: 1, toolCalls: { grep: 3 } };
    const shellLayout = createShellLayoutTestHarness();
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
      activityStatsByThread: { 'thread-1': activityStats },
      diffTextByThread: { 'thread-1': 'diff --git a/ChatPage.test.jsx b/ChatPage.test.jsx\n+expect(screen.getByTestId("runtime-panel"))' },
    });

    const { container } = render(
      <TestChatPageWrapper
        shellLayoutStore={shellLayout.store}
        store={store}
        projectPath="/repo/app"
      />,
    );

    expect(screen.getByRole('heading', { name: '修复会话' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '筛选消息' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '消息列表' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '布局视图' })).toHaveAttribute('aria-pressed', 'false');
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

    fireEvent.click(screen.getByRole('button', { name: '布局视图' }));
    await waitFor(() => expect(screen.getByTestId('runtime-panel')).toBeInTheDocument());
    expect(store.setRightPanelWidth).toBeUndefined();
    expect(shellLayout.storage.set).toHaveBeenCalledWith(
      'super-dolphin.shell.right-panel-width',
      expect.any(String),
    );
    expect(shellLayout.store.getState().rightPanelWidth).toBeGreaterThan(0);
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

    fireEvent.click(within(menu).getByRole('menuitem', { name: '新窗口（独立进程）' }));
    expect(store.openNewWindow).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('menuitem', { name: '复制当前线程' }));
    expect(store.copyActiveThreadInfo).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('menuitem', { name: '继承当前对话' }));
    expect(store.openForkDraft).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('menuitem', { name: '停止' }));
    expect(store.interruptActiveThread).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('menuitem', { name: '强制完成' }));
    expect(store.forceCompleteActiveThread).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('menuitem', { name: '进程恢复' }));
    expect(store.recoverActiveThread).toHaveBeenCalledTimes(1);

    menu = openMenu();
    fireEvent.click(within(menu).getByRole('menuitem', { name: '显示侧边栏' }));
    await waitFor(() => expect(screen.getByTestId('runtime-panel')).toBeInTheDocument());
  });

  it('disables force complete controls when the selected thread has no active turn target', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '空闲线程', time: '2026-06-02T08:00:00Z' },
    ], {
      hasActiveThreadActions: vi.fn(() => true),
      hasInterruptibleThreadAction: vi.fn(() => false),
      hasForceCompleteThreadAction: vi.fn(() => false),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('button', { name: '强制完成（不可用）' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '聊天操作' }));
    const menu = screen.getByTestId('chat-actions-menu');
    expect(within(menu).getByRole('menuitem', { name: '强制完成（不可用）' })).toHaveAttribute('aria-disabled', 'true');
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

  it('does not interrupt the active thread when Escape closes an image preview', () => {
    const store = createActiveThreadStore([
      {
        id: 'msg-image-preview',
        role: 'assistant',
        text: '请看截图：![sample.png](data:image/png;base64,AA==)',
        time: '2026-06-02T08:00:00Z',
      },
    ], {
      hasInterruptibleThreadAction: vi.fn(() => true),
      statuses: { 'thread-1': { state: 'running' } },
      threads: [{ id: 'thread-1', name: '运行会话', provider: 'codex', status: 'running', updatedAt: '2026-06-02T08:00:00Z' }],
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.click(screen.getByRole('button', { name: /sample\.png/ }));
    expect(screen.getByRole('dialog', { name: /sample\.png/ })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(screen.queryByRole('dialog', { name: /sample\.png/ })).not.toBeInTheDocument();
    expect(store.interruptActiveThread).not.toHaveBeenCalled();
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

  it.each([
    ['wheel up', (timeline) => fireEvent.wheel(timeline, { ctrlKey: false, deltaX: 0, deltaY: -40 })],
    ['touch toward older content', (timeline) => {
      fireEvent.touchStart(timeline, { touches: [{ clientY: 70 }] });
      fireEvent.touchMove(timeline, { touches: [{ clientY: 120 }] });
    }],
    ['PageUp', (timeline) => fireEvent.keyDown(timeline, { key: 'PageUp' })],
    ['Home', (timeline) => fireEvent.keyDown(timeline, { key: 'Home' })],
  ])('keeps streaming from stealing reading position after %s intent', (_label, leaveSticky) => {
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 41);
    const { rerender } = render(<TestChatPageWrapper store={createActiveThreadStore(scrollIntentMessages())} projectPath="/repo/app" />);
    const timeline = screen.getByTestId('chat-timeline');
    const metrics = installTimelineMetrics(timeline);
    requestAnimationFrameSpy.mockClear();

    leaveSticky(timeline);
    metrics.setScrollHeight(1280);
    rerender(<TestChatPageWrapper store={createActiveThreadStore(scrollIntentMessages('增长中的回复'))} projectPath="/repo/app" />);

    expect(requestAnimationFrameSpy).not.toHaveBeenCalled();
    expect(metrics.getScrollTop()).toBe(600);
    requestAnimationFrameSpy.mockRestore();
  });

  it('ignores zoom, horizontal wheel, and downward touch while sticky', () => {
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 41);
    const { rerender } = render(<TestChatPageWrapper store={createActiveThreadStore(scrollIntentMessages())} projectPath="/repo/app" />);
    const timeline = screen.getByTestId('chat-timeline');
    const metrics = installTimelineMetrics(timeline);

    fireEvent.wheel(timeline, { ctrlKey: true, deltaX: 0, deltaY: -40 });
    fireEvent.wheel(timeline, { ctrlKey: false, deltaX: 80, deltaY: -10 });
    fireEvent.touchStart(timeline, { touches: [{ clientY: 120 }] });
    fireEvent.touchMove(timeline, { touches: [{ clientY: 70 }] });
    requestAnimationFrameSpy.mockClear();
    metrics.setScrollHeight(1280);
    rerender(<TestChatPageWrapper store={createActiveThreadStore(scrollIntentMessages('仍应跟随的回复'))} projectPath="/repo/app" />);

    expect(requestAnimationFrameSpy).toHaveBeenCalled();
    requestAnimationFrameSpy.mockRestore();
  });

  it('restores sticky intent through End, returning to the bottom, and the explicit bottom button', () => {
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 41);
    const { rerender } = render(<TestChatPageWrapper store={createActiveThreadStore(scrollIntentMessages())} projectPath="/repo/app" />);
    const timeline = screen.getByTestId('chat-timeline');
    const metrics = installTimelineMetrics(timeline);

    fireEvent.wheel(timeline, { deltaX: 0, deltaY: -40 });
    fireEvent.keyDown(timeline, { key: 'End' });
    requestAnimationFrameSpy.mockClear();
    metrics.setScrollHeight(1200);
    rerender(<TestChatPageWrapper store={createActiveThreadStore(scrollIntentMessages('End 后跟随'))} projectPath="/repo/app" />);
    expect(requestAnimationFrameSpy).toHaveBeenCalled();

    fireEvent.wheel(timeline, { deltaX: 0, deltaY: -40 });
    metrics.setScrollTop(800);
    fireEvent.scroll(timeline);
    requestAnimationFrameSpy.mockClear();
    metrics.setScrollHeight(1400);
    rerender(<TestChatPageWrapper store={createActiveThreadStore(scrollIntentMessages('回到底部后跟随'))} projectPath="/repo/app" />);
    expect(requestAnimationFrameSpy).toHaveBeenCalled();

    fireEvent.wheel(timeline, { deltaX: 0, deltaY: -40 });
    metrics.setScrollTop(600);
    fireEvent.click(screen.getByRole('button', { name: '滚动到底部' }));
    expect(metrics.getScrollTop()).toBe(1400);
    requestAnimationFrameSpy.mockRestore();
  });

  it('keeps scroll intent policy outside the Conversation facade', () => {
    const source = readFileSync('src/pages/chat/thread/conversation/useConversationInteraction.js', 'utf8');
    const timelineSource = readFileSync('src/pages/chat/thread/conversation/ConversationTimeline.jsx', 'utf8');

    expect(source).not.toContain('shouldStickToBottomRef');
    expect(source).not.toContain('userScrolledRef');
    expect(source).toContain('useScrollIntentManager');
    expect(timelineSource).toContain('onScrollIfSticky={scrollIfSticky}');
  });
