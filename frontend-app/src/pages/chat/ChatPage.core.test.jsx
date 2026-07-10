import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { TestChatPageWrapper, createActiveThreadStore, createFakeStore, getThreadCardByName } from './__tests__/chatPageTestSupport.js';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
  it('exports the chat page component', () => {
    expect(TestChatPageWrapper).toBeTypeOf('function');
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

    const introHeading = screen.getByRole('heading', { name: '我们应该在 燧元 中构建什么？' });
    expect(introHeading).toBeInTheDocument();
    expect(within(introHeading).getByText('燧元').tagName).toBe('EM');
    expect(screen.getByText('探索 AI 驱动界面的可能性。开启对话、分析文件或编排复杂任务。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '总结文档 上传 PDF 或文本文件，快速提炼关键要点。' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '代码审查 粘贴代码片段，检查性能、正确性与潜在缺陷。' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '创意头脑风暴 生成营销文案、产品命名或结构化提纲。' })).toBeInTheDocument();
    expect(screen.queryByText('Explore the possibilities of AI-driven interfaces. Start a conversation, analyze files, or orchestrate complex tasks effortlessly.')).not.toBeInTheDocument();
    expect(screen.queryByText('Code Review')).not.toBeInTheDocument();
    expect(screen.queryByText('Creative Brainstorm')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '聊天页面' })).not.toBeInTheDocument();
    expect(screen.getByTestId('chat-page')).toHaveClass('chat-page--intro');
    expect(screen.getByTestId('conversation-drop-zone')).toHaveClass('conversation--intro');
    expect(screen.getByTestId('composer-dock')).toHaveClass('composer--floating');
    expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--docked');
    expect(screen.getByTestId('chat-intro-light-logo')).toBeInTheDocument();
    expect(screen.getByTestId('chat-intro-dark-logo')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '添加文件' }).querySelector('.lucide-paperclip')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '聊天操作' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '滚动到底部' })).not.toBeInTheDocument();
  });

  it('renders localized intro suggestions in English mode', () => {
    const store = createFakeStore();

    render(<TestChatPageWrapper copy={APP_COPY.en.chat} store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('heading', { name: 'What should we build in 燧元?' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', {
      name: 'Summarize Document Upload a PDF or text file and get a concise overview of key points.',
    }));
    expect(screen.getByRole('button', {
      name: 'Code Review Paste your snippet to check performance, correctness, and potential defects.',
    })).toBeInTheDocument();
    expect(screen.getByRole('button', {
      name: 'Creative Brainstorm Generate marketing copy, product names, or structured outlines.',
    })).toBeInTheDocument();
    const addFileButton = screen.getByRole('button', { name: 'Add file' });
    expect(addFileButton.textContent).toBe('');
    expect(addFileButton.querySelector('svg')).toBeInTheDocument();
    expect(screen.getByText('Suiyuan AI can make mistakes. Consider verifying critical information.')).toBeInTheDocument();
    expect(screen.queryByText('总结文档')).not.toBeInTheDocument();
    expect(screen.queryByText('燧元 AI 可能出错，请核对重要信息。')).not.toBeInTheDocument();
    expect(store.setDraft).toHaveBeenCalledWith('Please summarize this document, highlighting key conclusions, risks, and next steps.');
  });

  it('keeps successful new-chat notices out of the intro title bar', () => {
    const store = createFakeStore({
      actionNotice: { message: '已创建新对话草稿', tone: 'success' },
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('heading', { name: '我们应该在 燧元 中构建什么？' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '聊天页面' })).not.toBeInTheDocument();
    expect(screen.getByTestId('chat-action-feedback')).toHaveClass('chat-action-toast');
    expect(screen.getByTestId('chat-action-feedback')).toHaveAttribute('role', 'alert');
    expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('已创建新对话草稿');
  });

  it('labels attachment failures independently from send failures', () => {
    const store = createFakeStore({
      actionNotice: { category: 'attachment', message: 'runtime picker unavailable', tone: 'error' },
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const alert = screen.getByTestId('chat-action-feedback');
    expect(alert).toHaveTextContent('附件选择失败');
    expect(alert).toHaveTextContent('runtime picker unavailable');
    expect(alert).not.toHaveTextContent('发送失败');
  });

  it('shows approval action failures as a visible alert', async () => {
    const store = createActiveThreadStore([
      {
        id: 'approval-1',
        kind: 'approval',
        role: 'assistant',
        requestId: 5,
        title: 'Run command',
        text: 'Allow command execution?',
        time: '2026-06-15T08:00:00Z',
      },
    ], {
      respondApproval: vi.fn().mockRejectedValue(new Error('approval backend offline')),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    fireEvent.click(screen.getByRole('button', { name: '同意审批 5' }));

    const alert = await screen.findByTestId('approval-action-feedback');
    expect(alert).toHaveAttribute('role', 'alert');
    expect(alert).toHaveClass('approval-action-feedback');
    expect(alert).not.toHaveClass('sr-only');
    expect(alert).toHaveTextContent('approval backend offline');
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

  it('renders an active thread with an empty message array without falling back to intro mode', () => {
    const store = createActiveThreadStore([], {
      threads: [{ id: 'thread-1', name: '空消息线程', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('heading', { name: '空消息线程' })).toBeInTheDocument();
    expect(screen.getByTestId('chat-page')).not.toHaveClass('chat-page--intro');
    expect(screen.getByTestId('conversation-drop-zone')).not.toHaveClass('conversation--intro');
    expect(screen.getByTestId('composer-dock')).toHaveClass('composer--docked');
  });

  it('shows invalid message timestamps as an explicit placeholder', () => {
    const store = createActiveThreadStore([
      { id: 'msg-invalid-time', role: 'user', text: '坏时间戳应该可见', time: 'not-a-valid-timestamp' },
    ], {
      threads: [{ id: 'thread-1', name: '时间戳校验', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByText('坏时间戳应该可见')).toBeInTheDocument();
    expect(screen.getByText('--:--')).toBeInTheDocument();
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
    expect(screen.getByRole('button', { name: '重新连接后端' })).toBeEnabled();
    expect(screen.getByText('我们应该在 燧元 中构建什么？')).toBeInTheDocument();
    expect(screen.getByText('暂无会话，点击「新建对话」开始草稿')).toBeInTheDocument();
    expect(screen.getByTestId('composer-input')).toHaveValue('请修复测试');
    expect(screen.getByRole('button', { name: '发送消息' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '添加文件' })).toBeDisabled();
    // expect(screen.getByRole('button', { name: '请先连接后端并选择项目' })).toBeDisabled();
  });

  it('keeps composer actions disabled while a failed bootstrap retry is loading', () => {
    const store = createFakeStore({
      bootstrapStatus: 'loading',
      draft: '等待重新连接',
      error: 'backend unavailable',
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    expect(screen.getByRole('button', { name: '正在重新连接后端' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '发送消息' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '添加文件' })).toBeDisabled();
  });

  it('renders an active thread timeline, sends through the store, and opens the runtime panel', async () => {
    const activityStats = { commands: 2, fileEdits: 1, toolCalls: { grep: 3 } };
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

    const { container } = render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

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
