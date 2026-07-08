import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { TestChatPageWrapper, createActiveThreadStore, createFakeStore, deferred, getThreadCardByName, locateCodeFile, openCodeFile, openPath, saveCodeFile } from './__tests__/chatPageTestSupport.js';
import { ChatPage } from './ChatPage.jsx';
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
    expect(within(actions).getByRole('button', { name: '置顶对话' })).toBeInTheDocument();
    expect(within(actions).getByRole('button', { name: '删除会话' })).toBeInTheDocument();
    expect(within(actions).queryByRole('button', { name: '重命名会话' })).not.toBeInTheDocument();
    expect(within(actions).queryByRole('button', { name: '归档会话' })).not.toBeInTheDocument();
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
    expect(within(preview).queryByLabelText('文件预览内容')).not.toBeInTheDocument();
    expect(within(preview).queryByRole('button', { name: '保存预览更改' })).not.toBeInTheDocument();
    expect(saveCodeFile).not.toHaveBeenCalled();
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

  it('keeps snippet code previews read-only even when the snippet covers all returned lines', async () => {
    openCodeFile.mockResolvedValue({
      ok: true,
      filePath: '/repo/app/src/one-line.js',
      relative: 'src/one-line.js',
      previewMode: 'snippet',
      startLine: 1,
      endLine: 1,
      totalLines: 1,
      snippet: [{ line: 1, text: 'const snippet = true;' }],
    });
    const store = createActiveThreadStore([
      {
        id: 'assistant-snippet',
        role: 'assistant',
        text: ':codex-file-citation[]{path="src/one-line.js" line_range_start="1" line_range_end="1"}',
        time: '2026-06-02T08:00:00Z',
      },
    ], {
      activeProject: '/repo/app',
      projects: ['/repo/app'],
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.click(await screen.findByRole('button', { name: '打开文件引用 src/one-line.js' }));

    const preview = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(preview).getByText('const snippet = true;')).toBeInTheDocument();
    expect(within(preview).queryByLabelText('文件预览内容')).not.toBeInTheDocument();
    expect(within(preview).queryByRole('button', { name: '保存预览更改' })).not.toBeInTheDocument();
    expect(saveCodeFile).not.toHaveBeenCalled();
  });

  it('ignores stale file preview responses when a newer timeline preview is open', async () => {
    const firstOpen = deferred();
    const secondOpen = deferred();
    locateCodeFile.mockImplementation(({ filePath }) => Promise.resolve({
      ok: true,
      paths: [`/repo/app/${filePath}`],
      matches: [{ path: `/repo/app/${filePath}`, relative: filePath }],
    }));
    openCodeFile.mockImplementation(({ filePath }) => {
      if (filePath.endsWith('src/a.js')) return firstOpen.promise;
      if (filePath.endsWith('src/b.js')) return secondOpen.promise;
      throw new Error(`unexpected open path ${filePath}`);
    });
    saveCodeFile.mockResolvedValue({ ok: true, filePath: '/repo/app/src/b.js', relative: 'src/b.js', totalLines: 1 });
    const store = createActiveThreadStore([
      {
        id: 'assistant-race',
        role: 'assistant',
        text: [
          ':codex-file-citation[]{path="src/a.js" line_range_start="1" line_range_end="1"}',
          ':codex-file-citation[]{path="src/b.js" line_range_start="1" line_range_end="1"}',
        ].join(' '),
        time: '2026-06-02T08:00:00Z',
      },
    ], {
      activeProject: '/repo/app',
      projects: ['/repo/app'],
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.click(await screen.findByRole('button', { name: '打开文件引用 src/a.js' }));
    await waitFor(() => expect(openCodeFile).toHaveBeenCalledTimes(1));
    fireEvent.click(await screen.findByRole('button', { name: '打开文件引用 src/b.js' }));
    await waitFor(() => expect(openCodeFile).toHaveBeenCalledTimes(2));

    await act(async () => {
      secondOpen.resolve({
        ok: true,
        filePath: '/repo/app/src/b.js',
        relative: 'src/b.js',
        previewMode: 'full',
        contentVersion: 'sha256:b-version',
        snippet: [{ line: 1, text: 'const latest = true;' }],
        startLine: 1,
        endLine: 1,
        totalLines: 1,
      });
    });
    const preview = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(preview).getByText('src/b.js')).toBeInTheDocument();
    expect(within(preview).getByLabelText('文件预览内容')).toHaveValue('const latest = true;');

    await act(async () => {
      firstOpen.resolve({
        ok: true,
        filePath: '/repo/app/src/a.js',
        relative: 'src/a.js',
        snippet: [{ line: 1, text: 'const stale = true;' }],
        startLine: 1,
        endLine: 1,
        totalLines: 1,
      });
    });

    expect(within(preview).getByText('src/b.js')).toBeInTheDocument();
    const editor = within(preview).getByLabelText('文件预览内容');
    expect(editor).toHaveValue('const latest = true;');
    fireEvent.change(editor, { target: { value: 'const latest = false;' } });
    fireEvent.click(within(preview).getByRole('button', { name: '保存预览更改' }));

    await waitFor(() => expect(saveCodeFile).toHaveBeenCalledTimes(1));
    expect(saveCodeFile).toHaveBeenCalledWith(expect.objectContaining({
      filePath: '/repo/app/src/b.js',
      content: 'const latest = false;',
      previewMode: 'full',
      contentVersion: 'sha256:b-version',
    }));
  });

  it('opens local markdown links from timeline messages directly', async () => {
    const store = createActiveThreadStore([
      {
        id: 'assistant-link',
        role: 'assistant',
        text: '[chat](frontend-app/src/pages/chat/)',
        time: '2026-06-02T08:00:00Z',
      },
    ], {
      activeProject: '/repo/app',
      projects: ['/repo/app'],
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.click(screen.getByRole('button', { name: /\u6253\u5f00\u6587\u4ef6 chat/ }));

    await waitFor(() => expect(openPath).toHaveBeenCalledWith({
      filePath: 'frontend-app/src/pages/chat/',
      line: 1,
      column: 0,
      project: '/repo/app',
      projects: ['/repo/app'],
    }));
    expect(openCodeFile).not.toHaveBeenCalled();
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
