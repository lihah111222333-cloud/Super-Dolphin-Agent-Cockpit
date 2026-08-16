import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import mermaid from 'mermaid';
import { TestChatPage, TestChatPageWrapper, copyTextToClipboard, createActiveThreadStore } from './__tests__/chatPageTestSupport.js';
const HIDDEN_MERMAID_TEXT = [
  '旧图表不应首屏渲染：',
  '```mermaid',
  'flowchart TD',
  '  Old[旧历史] --> Heavy[重渲染]',
  '```',
].join('\n');

const DATA_URL_IMAGE_ATTACHMENT = {
  kind: 'image',
  name: 'screenshot.png',
  path: '/var/folders/abc/T/clipboard-123456.png',
  previewUrl: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==',
};

const CLIPBOARD_IMAGE_ATTACHMENT = {
  kind: 'image',
  name: 'clipboard-987654321.png',
  path: '/var/folders/abc/T/clipboard-987654321.png',
  previewUrl: '/clipboard/clipboard-987654321.png',
};

it('collapses earlier assistant updates per turn while keeping the final reply visible', () => {
  render(<TestChatPage store={createActiveThreadStore([
    { id: 'turn-user', role: 'user', text: '检查问题', time: '2026-06-02T08:00:00Z' },
    { id: 'turn-progress', role: 'assistant', kind: 'assistant', text: '正在定位根因', time: '2026-06-02T08:00:10Z' },
    { id: 'turn-tool', role: 'assistant', kind: 'tool', title: 'grep', text: '命中目标文件', time: '2026-06-02T08:00:20Z' },
    { id: 'turn-final', role: 'assistant', kind: 'assistant', text: '已经完成修复', time: '2026-06-02T08:01:00Z' },
  ])} projectPath="/repo/app" />);

  const processGroup = screen.getByTestId('turn-process-group');
  expect(processGroup).not.toHaveAttribute('open');
  expect(within(processGroup).getByText('正在定位根因')).toBeInTheDocument();
  expect(within(processGroup).getByText('命中目标文件')).toBeInTheDocument();
  expect(screen.getByText('已经完成修复').closest('[data-testid="turn-process-group"]')).toBeNull();
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

    const { rerender } = render(<TestChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);
    expect(screen.getByText('我')).toBeInTheDocument();

    const fullReply = '我先检查输出节奏，再继续。';
    rerender(<TestChatPage store={createActiveThreadStore([
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
    const { rerender } = render(<TestChatPage store={store} projectPath="/repo/app" />);
    expect(screen.getByText('我')).toBeInTheDocument();

    const fullReply = '我先检查输出节奏，再继续。';
    rerender(<TestChatPage store={createActiveThreadStore([
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
    const { rerender } = render(<TestChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);

    rerender(<TestChatPage store={createActiveThreadStore([
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

    render(<TestChatPage store={createActiveThreadStore([
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
    const { rerender } = render(<TestChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);
    const fullReply = '我先检查输出节奏，再继续。';

    rerender(<TestChatPage store={createActiveThreadStore([
      initialMessages[0],
      { ...initialMessages[1], text: fullReply },
    ])} projectPath="/repo/app" />);

    expect(screen.queryByText(fullReply)).not.toBeInTheDocument();
    copyTextToClipboard.mockResolvedValue(undefined);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '复制 AI 输出' }));
    });

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
    const { rerender } = render(<TestChatPage store={runningStore} projectPath="/repo/app" />);

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
      const completedStatus = { state: 'completed' };
      const assistantMessage = {
        id: 'live-assistant-1',
        role: 'assistant',
        text: '已经定位到 live completion 后的滚动问题。',
        time: '2026-06-02T08:01:00Z',
      };
      const completedStore = createActiveThreadStore([
        userMessage,
        assistantMessage,
      ], {
        statuses: { 'thread-1': completedStatus },
      });
      rerender(<TestChatPage store={completedStore} projectPath="/repo/app" />);

      expect(scrollIntoViewSpy).not.toHaveBeenCalled();
      expect(requestAnimationFrameSpy).toHaveBeenCalled();
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
    const { rerender } = render(<TestChatPage store={createActiveThreadStore(initialMessages)} projectPath="/repo/app" />);
    const timeline = screen.getByTestId('chat-timeline');
    Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 1000 });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      value: 600,
      writable: true,
    });
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 1);

    rerender(<TestChatPage store={createActiveThreadStore([
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

    render(<TestChatPage store={store} projectPath="/repo/app" />);

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

    render(<TestChatPage store={store} projectPath="/repo/app" />);

    expect(screen.queryByText('本地隐藏历史消息 1')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '显示更早的消息（40 条）' }));

    expect(screen.getByText('本地隐藏历史消息 1')).toBeInTheDocument();
    expect(loadOlderThreadMessages).not.toHaveBeenCalled();
  });

  it('does not render Mermaid diagrams from unmaterialized older history', async () => {
    const mermaidRenderSpy = vi.spyOn(mermaid, 'render').mockResolvedValue({
      svg: '<svg role="img" aria-label="mock mermaid" />',
    });
    const messages = Array.from({ length: 85 }, (_, index) => {
      if (index === 0) {
        return {
          id: 'hidden-mermaid',
          role: 'assistant',
          text: HIDDEN_MERMAID_TEXT,
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

    render(<TestChatPage store={createActiveThreadStore(messages)} projectPath="/repo/app" />);

    expect(screen.getByText('最近消息 84')).toBeInTheDocument();
    expect(screen.queryByText('旧图表不应首屏渲染：')).not.toBeInTheDocument();
    expect(mermaidRenderSpy).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '显示更早的消息（5 条）' }));

    expect(screen.getByText('旧图表不应首屏渲染：')).toBeInTheDocument();
    mermaidRenderSpy.mockRestore();
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
    render(<TestChatPage store={store} projectPath="/repo/app" />);
    // 应该以普通段落渲染，所以不应该有 heading 元素
    expect(screen.queryByRole('heading', { name: /回家之路无论走了多远/ })).not.toBeInTheDocument();
    expect(screen.getByText(/随便贴一篇：##回家之路无论走了多远/)).toBeInTheDocument();
  });

  it('[regression] renders user message image attachments inline (data: URL and clipboard route)', () => {
    const store = createActiveThreadStore([
      {
        id: 'user-with-image',
        role: 'user',
        text: '能先识别这张截图内容。',
        time: '2026-06-02T08:00:00Z',
        attachments: [DATA_URL_IMAGE_ATTACHMENT],
      },
    ]);

    const { container } = render(<TestChatPage store={store} projectPath="/repo/app" />);

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
        attachments: [CLIPBOARD_IMAGE_ATTACHMENT],
      },
    ]);

    const { container } = render(<TestChatPage store={store} projectPath="/repo/app" />);

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
