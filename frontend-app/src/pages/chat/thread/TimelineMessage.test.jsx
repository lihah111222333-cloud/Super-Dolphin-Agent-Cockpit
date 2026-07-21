import React from 'react';
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { TimelineLoadingPlaceholder, TimelineMessage, UserMessageAttachments } from './TimelineMessage.jsx';
import { resolveAttachmentImageSrc } from './timelineMessageModel.js';
import { copyTextToClipboard } from '../services/chatCodeService.js';

vi.mock('../services/chatCodeService.js', () => ({ copyTextToClipboard: vi.fn() }));

const formatTime = () => '16:00';
const screenshotName = '\u5c4f\u5e55\u622a\u56fe 2026-06-13 170324.png';

function localScreenshotPath(separator) {
  const home = separator === '\\' ? 'C:\\Users\\alice' : homedir().replace(/\\/g, '/');
  return [home, 'Pictures', 'Screenshots', screenshotName].join(separator);
}
  it('renders user attachments as image previews and file pills', () => {
    const onOpenPath = vi.fn();

    render(
      <TimelineMessage
        message={{
          id: 'user-1',
          role: 'user',
          text: 'with files',
          time: '2026-06-15T08:00:00Z',
          attachments: [
            { kind: 'image', previewUrl: '/clipboard/a.png', name: 'clip.png' },
            { kind: 'file', path: 'reports/summary.md' },
          ],
        }}
        actions={{ onOpenPath }}
        formatTime={formatTime}
      />
    );

    expect(screen.getByRole('button', { name: '\u653e\u5927\u56fe\u7247 clip.png' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /\u6253\u5f00\u6587\u4ef6 summary\.md/ }));
    expect(onOpenPath).toHaveBeenCalledWith(expect.objectContaining({
      path: 'reports/summary.md',
      raw: 'summary.md',
    }));
    expect(screen.getByText('with files')).toBeInTheDocument();
  });

  it('renders assistant footer actions and notifies sticky scrolling while streaming', () => {
    const onScrollIfSticky = vi.fn();

    const { rerender } = render(
      <TimelineMessage
        message={{ id: 'assistant-1', role: 'assistant', text: 'streaming reply', time: '2026-06-15T08:00:00Z', done: false }}
        activeThreadId="thread-1"
        smoothStreaming
        onScrollIfSticky={onScrollIfSticky}
        formatTime={formatTime}
      />
    );

    expect(screen.getByText('streaming reply')).toBeInTheDocument();
    expect(screen.getByText('\u601d\u8003\u4e2d')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /\u590d\u5236/ })).toBeInTheDocument();
    expect(onScrollIfSticky).toHaveBeenCalledWith(false);

    rerender(
      <TimelineMessage
        message={{ id: 'assistant-1', role: 'assistant', text: 'streaming reply', time: '2026-06-15T08:00:00Z', done: true }}
        activeThreadId="thread-1"
        smoothStreaming
        onScrollIfSticky={onScrollIfSticky}
        formatTime={formatTime}
      />
    );

    expect(screen.queryByText('\u601d\u8003\u4e2d')).not.toBeInTheDocument();
  });

  it('stops animation frames after catching up and restarts them for a later streaming token', async () => {
    const animationFrameCallbacks = [];
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      animationFrameCallbacks.push(callback);
      return animationFrameCallbacks.length;
    });
    const cancelAnimationFrameSpy = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
    const initialText = '已经追平';
    const fullText = '已经追平，继续输出';
    const nextTokenText = '已经追平，继续输出新的 token';

    try {
      const { rerender } = render(
        <TimelineMessage
          message={{ id: 'assistant-idle', role: 'assistant', text: initialText, time: '2026-06-15T08:00:00Z', done: false }}
          activeThreadId="thread-1"
          smoothStreaming
          formatTime={formatTime}
        />,
      );
      expect(requestAnimationFrameSpy).not.toHaveBeenCalled();

      rerender(
        <TimelineMessage
          message={{ id: 'assistant-idle', role: 'assistant', text: fullText, time: '2026-06-15T08:00:00Z', done: false }}
          activeThreadId="thread-1"
          smoothStreaming
          formatTime={formatTime}
        />,
      );
      for (let index = 0; index < 32 && !screen.queryByText(fullText); index += 1) {
        const callback = animationFrameCallbacks.shift();
        expect(callback).toBeTypeOf('function');
        await act(async () => {
          callback(16);
        });
      }
      expect(screen.getByText(fullText)).toBeInTheDocument();
      const framesAtCatchup = requestAnimationFrameSpy.mock.calls.length;
      expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(framesAtCatchup);
      expect(animationFrameCallbacks).toHaveLength(0);

      rerender(
        <TimelineMessage
          message={{ id: 'assistant-idle', role: 'assistant', text: nextTokenText, time: '2026-06-15T08:00:00Z', done: false }}
          activeThreadId="thread-1"
          smoothStreaming
          formatTime={formatTime}
        />,
      );
      expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(framesAtCatchup + 1);
      expect(animationFrameCallbacks).toHaveLength(1);
    }
    finally {
      requestAnimationFrameSpy.mockRestore();
      cancelAnimationFrameSpy.mockRestore();
    }
  });

  it('cancels a pending frame when reduced motion becomes active', () => {
    const originalMatchMedia = window.matchMedia;
    let onChange;
    const query = {
      matches: false,
      addEventListener: vi.fn((event, callback) => {
        if (event === 'change') onChange = callback;
      }),
      removeEventListener: vi.fn(),
    };
    window.matchMedia = vi.fn(() => query);
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 1);
    const cancelAnimationFrameSpy = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});

    try {
      const baseMessage = { id: 'assistant-reduced-motion', role: 'assistant', text: '我', time: '2026-06-15T08:00:00Z', done: false };
      const { rerender } = render(
        <TimelineMessage message={baseMessage} activeThreadId="thread-1" smoothStreaming formatTime={formatTime} />,
      );
      rerender(
        <TimelineMessage message={{ ...baseMessage, text: '我继续' }} activeThreadId="thread-1" smoothStreaming formatTime={formatTime} />,
      );
      expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(1);

      query.matches = true;
      act(() => onChange());
      expect(cancelAnimationFrameSpy).toHaveBeenCalledWith(1);
    }
    finally {
      requestAnimationFrameSpy.mockRestore();
      cancelAnimationFrameSpy.mockRestore();
      window.matchMedia = originalMatchMedia;
    }
  });

  it('cancels pending streaming frames when the stream key changes or the message unmounts', () => {
    const callbacks = [];
    const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      callbacks.push(callback);
      return callbacks.length;
    });
    const cancelAnimationFrameSpy = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});

    try {
      const baseMessage = { id: 'assistant-cleanup', role: 'assistant', text: '我', time: '2026-06-15T08:00:00Z', done: false };
      const { rerender, unmount } = render(
        <TimelineMessage
          message={baseMessage}
          activeThreadId="thread-1"
          smoothStreaming
          formatTime={formatTime}
        />,
      );

      rerender(
        <TimelineMessage
          message={{ ...baseMessage, text: '我继续' }}
          activeThreadId="thread-1"
          smoothStreaming
          formatTime={formatTime}
        />,
      );
      expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(1);

      rerender(
        <TimelineMessage
          message={{ ...baseMessage, text: '我继续' }}
          activeThreadId="thread-2"
          smoothStreaming
          formatTime={formatTime}
        />,
      );
      expect(cancelAnimationFrameSpy).toHaveBeenCalledWith(1);

      rerender(
        <TimelineMessage
          message={{ ...baseMessage, text: '我继续输出' }}
          activeThreadId="thread-2"
          smoothStreaming
          formatTime={formatTime}
        />,
      );
      expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(2);
      unmount();
      expect(cancelAnimationFrameSpy).toHaveBeenCalledWith(2);
    }
    finally {
      requestAnimationFrameSpy.mockRestore();
      cancelAnimationFrameSpy.mockRestore();
    }
  });

  it('renders failed terminals with executable diagnostics copy and user cancellation as neutral status', async () => {
    copyTextToClipboard.mockResolvedValueOnce(true);
    const { rerender } = render(
      <TimelineMessage
        message={{
          id: 'terminal-failed',
          role: 'assistant',
          kind: 'turn_terminal',
          terminalOutcome: 'failed',
          publicError: {
            code: 'PROVIDER_FAILED',
            title: 'provider-token=secret-value',
            message: 'TypeError: /private/agent/config.go\nstack: remote failure',
            diagnosticId: 'diag-terminal-1',
            retryable: false,
            recoveryActions: ['copy_diagnostics'],
          },
          time: '2026-07-16T01:00:00Z',
          done: true,
        }}
        formatTime={formatTime}
      />
    );

    expect(screen.getByRole('alert')).toHaveTextContent('提供方暂不可用');
    expect(screen.getByRole('alert')).toHaveTextContent('提供方未能完成本轮请求，请稍后重试。');
    expect(screen.getByRole('alert')).toHaveTextContent('诊断 ID：diag-terminal-1');
    expect(screen.queryByRole('button', { name: /重试/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '复制诊断信息 diag-terminal-1' }));
    await waitFor(() => expect(copyTextToClipboard).toHaveBeenCalledWith([
      'Diagnostic ID: diag-terminal-1',
      'Code: PROVIDER_FAILED',
      'Title: 提供方暂不可用',
      'Message: 提供方未能完成本轮请求，请稍后重试。',
    ].join('\n')));
    expect(screen.getByRole('alert')).not.toHaveTextContent(/secret-value|\/private\/|stack:/);
    expect(screen.getByRole('button', { name: '复制诊断信息 diag-terminal-1' })).toHaveTextContent('已复制');

    rerender(
      <TimelineMessage
        message={{
          id: 'terminal-cancelled',
          role: 'assistant',
          kind: 'turn_terminal',
          terminalOutcome: 'cancelled',
          terminationCause: 'user_request',
          time: '2026-07-16T01:00:00Z',
          done: true,
        }}
        formatTime={formatTime}
      />
    );

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('已取消');
  });

  it('shows copy failure feedback and omits actions the terminal did not advertise', async () => {
    copyTextToClipboard.mockRejectedValueOnce(new Error('clipboard unavailable'));
    const error = {
      code: 'REMOTE_SECRET_FAILURE',
      title: 'provider-token=secret-value',
      message: 'TypeError: /private/agent/config.go\nstack: remote failure',
      diagnosticId: 'diag-terminal-2',
      retryable: false,
      recoveryActions: ['copy_diagnostics'],
    };
    const { rerender } = render(
      <TimelineMessage
        message={{ id: 'terminal-2', role: 'assistant', kind: 'turn_terminal', terminalOutcome: 'failed', publicError: error, time: '2026-07-16T01:00:00Z', done: true }}
        formatTime={formatTime}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: '复制诊断信息 diag-terminal-2' }));
    await waitFor(() => expect(screen.getByRole('button', { name: '复制诊断信息 diag-terminal-2' })).toHaveTextContent('复制失败，请重试'));
    expect(screen.getByRole('alert')).toHaveTextContent('远端执行未完成');
    expect(screen.getByRole('alert')).not.toHaveTextContent(/secret-value|\/private\/|stack:/);

    rerender(
      <TimelineMessage
        message={{ ...error, id: 'terminal-3', role: 'assistant', kind: 'turn_terminal', terminalOutcome: 'failed', publicError: { ...error, recoveryActions: [] }, time: '2026-07-16T01:00:00Z', done: true }}
        formatTime={formatTime}
      />
    );
    expect(screen.queryByRole('button', { name: /复制诊断信息/ })).not.toBeInTheDocument();
  });

  it('opens generated image previews rendered inside assistant markdown', () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_lightbox.png';

    render(
      <TimelineMessage
        message={{ id: 'assistant-image', role: 'assistant', text: `图片已生成：${imagePath}`, time: '2026-06-15T08:00:00Z' }}
        activeThreadId="thread-1"
        formatTime={formatTime}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: '放大图片 ig_lightbox.png' }));

    expect(screen.getByRole('dialog', { name: '图片预览：ig_lightbox.png' })).toBeInTheDocument();
  });

  it('delegates approval and reasoning message variants', () => {
    const { rerender } = render(
      <TimelineMessage
        message={{ kind: 'approval', sessionScope: 'session-scope-a', callId: 'call-8', requestId: 8, status: 'pending', text: 'Approve action?', time: '2026-06-15T08:00:00Z' }}
        actions={{ onApproval: vi.fn() }}
        formatTime={formatTime}
      />
    );

    expect(screen.getByTestId('approval-request-8')).toHaveTextContent('Approve action?');

    rerender(
      <TimelineMessage
        message={{ kind: 'reasoning', id: 'reasoning-1', text: 'Thinking', done: false }}
        formatTime={formatTime}
      />
    );

    expect(screen.getByText('Thinking')).toBeInTheDocument();
  });

  it('classifies approval messages through the single approval adapter', () => {
    const source = readFileSync('src/pages/chat/thread/TimelineMessage.jsx', 'utf8');

    expect(source).toContain('features/approval/model/approvalDecision.js');
    expect(source).not.toContain('./chatApprovalModel.js');
  });

  it('normalizes supported image sources and empty attachment lists', () => {
    const screenshotPath = localScreenshotPath('/');
    expect(resolveAttachmentImageSrc({ previewUrl: 'data:image/png;base64,abc' })).toBe('data:image/png;base64,abc');
    expect(resolveAttachmentImageSrc({ previewUrl: 'blob:screen-preview' })).toBe('blob:screen-preview');
    expect(resolveAttachmentImageSrc({ previewUrl: '/clipboard/a.png' })).toBe('/clipboard/a.png');
    expect(resolveAttachmentImageSrc({ previewUrl: 'https://example.test/a.png' })).toBe('https://example.test/a.png');
    expect(resolveAttachmentImageSrc({ previewUrl: '/generated-image?path=/tmp/secret.png' })).toBe('');
    expect(resolveAttachmentImageSrc({ path: 'C:/Users/ai/AppData/Local/Temp/clipboard-222.png' })).toBe('/clipboard/clipboard-222.png');
    expect(resolveAttachmentImageSrc({ path: 'C:/Users/ai/AppData/Local/Temp/codex-clipboard-f05.png' })).toBe('/clipboard/codex-clipboard-f05.png');
    expect(resolveAttachmentImageSrc({ path: screenshotPath })).toBe('');

    const { container } = render(<UserMessageAttachments attachments={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders path-only clipboard images as image previews', () => {
    render(
      <UserMessageAttachments
        attachments={[
          { path: 'C:/Users/ai/AppData/Local/Temp/clipboard-222.png', name: 'clipboard-222.png' },
        ]}
      />,
    );

    const img = screen.getByRole('img', { name: 'clipboard-222.png' });
    expect(img).toHaveAttribute('src', '/clipboard/clipboard-222.png');
    expect(screen.queryByText('C:/Users/ai/AppData/Local/Temp/clipboard-222.png')).not.toBeInTheDocument();
  });

  it('renders Codex clipboard temp images as image previews', () => {
    render(
      <UserMessageAttachments
        attachments={[
          { path: 'C:/Users/ai/AppData/Local/Temp/codex-clipboard-f05.png', name: 'screen.png' },
        ]}
      />,
    );

    const img = screen.getByRole('img', { name: 'screen.png' });
    expect(img).toHaveAttribute('src', '/clipboard/codex-clipboard-f05.png');
  });

  it('renders normal local screenshot paths as file attachments', () => {
    const screenshotPath = localScreenshotPath('\\');
    const onOpenPath = vi.fn();

    render(
      <UserMessageAttachments
        attachments={[
          { path: screenshotPath, name: screenshotName },
        ]}
        actions={{ onOpenPath }}
      />,
    );

    expect(screen.queryByRole('img', { name: screenshotName })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: `打开文件 ${screenshotName}` }));
    expect(onOpenPath).toHaveBeenCalledWith(expect.objectContaining({ path: screenshotPath, raw: screenshotName }));
  });

  it('renders a live loading placeholder', () => {
    render(<TimelineLoadingPlaceholder />);

    expect(screen.getByTestId('timeline-loading-placeholder')).toHaveAttribute('aria-live', 'polite');
    expect(screen.getByText('\u6b63\u5728\u540c\u6b65\u4f1a\u8bdd\u5386\u53f2')).toBeInTheDocument();
  });
