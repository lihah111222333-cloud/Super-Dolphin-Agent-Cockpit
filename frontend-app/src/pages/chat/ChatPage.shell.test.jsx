import React from 'react';
import { readFileSync } from 'node:fs';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { TestChatPageWrapper, createActiveThreadStore, createFakeStore, createShellLayoutTestHarness } from './__tests__/chatPageTestSupport.js';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { resetVisibleActionFailureForTest } from '../../shared/ui/actionFailureSink.js';
import { ActionFailureSink } from '../../shared/ui/actionFailureSink.jsx';

function approvalMessage(requestId, status = 'pending', overrides = {}) {
  return {
    id: `approval-${requestId}`,
    kind: 'approval',
    role: 'assistant',
    sessionScope: 'session-scope-a',
    callId: `call-${requestId}`,
    requestId,
    status,
    text: `Approval ${requestId}`,
    time: '2026-06-02T08:00:00Z',
    done: status !== 'pending',
    ...overrides,
  };
}

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
    expect(screen.queryByText(
      'Explore the possibilities of AI-driven interfaces. Start a conversation, analyze files, or orchestrate complex tasks effortlessly.',
    )).not.toBeInTheDocument();
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
    const rawCause = 'approval backend offline token=secret';
    const store = createActiveThreadStore([
      {
        id: 'approval-1',
        kind: 'approval',
        role: 'assistant',
        sessionScope: 'session-scope-a',
        callId: 'call-5',
        requestId: 5,
        status: 'pending',
        title: 'Run command',
        text: 'Allow command execution?',
        time: '2026-06-15T08:00:00Z',
      },
    ], {
      respondApproval: vi.fn().mockRejectedValue(new Error(rawCause)),
    });

    resetVisibleActionFailureForTest();
    render(
      <>
        <TestChatPageWrapper store={store} projectPath="/repo/app" />
        <ActionFailureSink />
      </>,
    );
    fireEvent.click(screen.getByRole('button', { name: '同意' }));
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));

    const alert = await screen.findByTestId('global-action-failure');
    expect(alert).toHaveAttribute('role', 'alert');
    expect(alert).toHaveClass('global-action-failure');
    expect(alert).toHaveTextContent('操作失败，当前页面状态已保留。');
    expect(alert).toHaveTextContent('诊断 ID：');
    expect(alert).not.toHaveTextContent(rawCause);
  });

  it.each([
    ['disappears', []],
    ['becomes terminal', [approvalMessage(5, 'approved')]],
  ])('focuses the still-mounted composer when a same-thread pending approval %s', async (_label, settledMessages) => {
    const pendingStore = createActiveThreadStore([approvalMessage(5)]);
    const { rerender } = render(<TestChatPageWrapper store={pendingStore} projectPath="/repo/app" />);
    const originalTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    const nonComposerControl = screen.getByRole('button', { name: '测试切换侧边栏' });
    nonComposerControl.focus();
    expect(document.activeElement).toBe(nonComposerControl);
    expect(document.activeElement).not.toBe(originalTextarea);

    rerender(
      <TestChatPageWrapper
        store={createActiveThreadStore(settledMessages)}
        projectPath="/repo/app"
      />,
    );

    const settledTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    expect(settledTextarea).toBe(originalTextarea);
    await Promise.resolve();
    expect(settledTextarea).toHaveFocus();
  });

  it('does not focus the new thread composer when an approval settles after a thread switch', async () => {
    const threadOnePending = createActiveThreadStore([approvalMessage(5)]);
    const { rerender } = render(<TestChatPageWrapper store={threadOnePending} projectPath="/repo/app" />);
    const originalTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    const nonComposerControl = screen.getByRole('button', { name: '测试切换侧边栏' });
    nonComposerControl.focus();
    expect(document.activeElement).toBe(nonComposerControl);
    expect(document.activeElement).not.toBe(originalTextarea);

    const threadTwoStore = createActiveThreadStore([], {
      activeThreadId: 'thread-2',
      threads: [{ id: 'thread-2', name: '第二线程', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:01:00Z' }],
      threadTimelineReadyByThread: { 'thread-1': true, 'thread-2': true },
      timelinesByThread: {
        'thread-1': [approvalMessage(5, 'approved')],
        'thread-2': [],
      },
    });
    rerender(<TestChatPageWrapper store={threadTwoStore} projectPath="/repo/app" />);
    await Promise.resolve();

    const threadTwoTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    expect(threadTwoTextarea).not.toHaveFocus();
    expect(document.activeElement).toBe(nonComposerControl);
  });

  it('does not focus when a settled approval is replaced by a new pending approval', async () => {
    const pendingStore = createActiveThreadStore([approvalMessage(5)]);
    const { rerender } = render(<TestChatPageWrapper store={pendingStore} projectPath="/repo/app" />);
    const originalTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    const nonComposerControl = screen.getByRole('button', { name: '测试切换侧边栏' });
    nonComposerControl.focus();
    expect(document.activeElement).toBe(nonComposerControl);
    expect(document.activeElement).not.toBe(originalTextarea);

    rerender(
      <TestChatPageWrapper
        store={createActiveThreadStore([
          approvalMessage(5, 'approved'),
          approvalMessage(6),
        ])}
        projectPath="/repo/app"
      />,
    );
    await Promise.resolve();

    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    expect.soft(textarea).toBe(originalTextarea);
    expect.soft(textarea).not.toHaveFocus();
    expect.soft(screen.getByTestId('composer-dock')).toHaveAttribute('inert', '');
  });

  it('does not focus when a pending approval settles alongside a new terminal approval', async () => {
    const pendingStore = createActiveThreadStore([approvalMessage(5)]);
    const { rerender } = render(<TestChatPageWrapper store={pendingStore} projectPath="/repo/app" />);
    const originalTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    const nonComposerControl = screen.getByRole('button', { name: '测试切换侧边栏' });
    nonComposerControl.focus();

    rerender(
      <TestChatPageWrapper
        store={createActiveThreadStore([
          approvalMessage(5, 'approved'),
          approvalMessage(6, 'rejected'),
        ])}
        projectPath="/repo/app"
      />,
    );
    await Promise.resolve();

    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    expect(textarea).toBe(originalTextarea);
    expect(textarea).not.toHaveFocus();
    expect(document.activeElement).toBe(nonComposerControl);
  });

  it('does not focus when a pending approval settles alongside another identity with the same request id', async () => {
    const firstIdentity = {
      id: 'approval-first',
      sessionScope: 'session-scope-a',
      callId: 'call-a',
    };
    const secondIdentity = {
      id: 'approval-second',
      sessionScope: 'session-scope-b',
      callId: 'call-b',
    };
    const pendingStore = createActiveThreadStore([approvalMessage(5, 'pending', firstIdentity)]);
    const { rerender } = render(<TestChatPageWrapper store={pendingStore} projectPath="/repo/app" />);
    const originalTextarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    const nonComposerControl = screen.getByRole('button', { name: '测试切换侧边栏' });
    nonComposerControl.focus();

    rerender(
      <TestChatPageWrapper
        store={createActiveThreadStore([
          approvalMessage(5, 'approved', firstIdentity),
          approvalMessage(5, 'rejected', secondIdentity),
        ])}
        projectPath="/repo/app"
      />,
    );
    await Promise.resolve();

    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    expect(textarea).toBe(originalTextarea);
    expect(textarea).not.toHaveFocus();
    expect(document.activeElement).toBe(nonComposerControl);
  });

  it('keeps Conversation as the composer ref owner and isolates focus policy in its interaction hook', () => {
    const source = readFileSync('src/pages/chat/thread/Conversation.jsx', 'utf8');
    const interactionSource = readFileSync('src/pages/chat/thread/conversation/useConversationInteraction.js', 'utf8');

    expect.soft(source.match(/const composerInputRef = useRef\(null\)/g) || []).toHaveLength(1);
    expect.soft(source).toContain('composerInputRef,');
    expect.soft(source).toContain('useConversationInteraction');
    expect.soft(interactionSource).toMatch(/composerInputRef\.current[\s\S]{0,40}\.focus\(/);
    expect.soft(interactionSource).toMatch(/node\s*&&[\s\S]{0,80}previous\.node === node/);
    expect.soft(interactionSource).not.toContain('document.querySelector');
    expect.soft(interactionSource).not.toContain('document.getElementById');
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

  it('renders a persisted shell layout width without rewriting storage', () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const shellLayout = createShellLayoutTestHarness('480.5');
    const store = createActiveThreadStore([]);

    render(
      <TestChatPageWrapper
        rightPanelOpen
        shellLayoutStore={shellLayout.store}
        store={store}
        projectPath="/repo/app"
      />,
    );

    expect(screen.getByTestId('chat-layout')).toHaveStyle({
      gridTemplateColumns: 'minmax(0, 1fr) 6px 480.5px',
    });
    expect(shellLayout.storage.set).not.toHaveBeenCalled();
  });
