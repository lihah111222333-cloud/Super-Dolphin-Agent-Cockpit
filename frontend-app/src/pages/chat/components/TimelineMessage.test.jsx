import React from 'react';
import { homedir } from 'node:os';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TimelineLoadingPlaceholder, TimelineMessage, UserMessageAttachments } from './TimelineMessage.jsx';
import { resolveAttachmentImageSrc } from './timelineMessageModel.js';

const formatTime = () => '16:00';
const screenshotName = '\u5c4f\u5e55\u622a\u56fe 2026-06-13 170324.png';

function localScreenshotPath(separator) {
  const home = separator === '\\' ? homedir().replace(/\//g, '\\') : homedir().replace(/\\/g, '/');
  return [home, 'Pictures', 'Screenshots', screenshotName].join(separator);
}

describe('TimelineMessage', () => {
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

  it('delegates approval and reasoning message variants', () => {
    const { rerender } = render(
      <TimelineMessage
        message={{ kind: 'approval', requestId: 8, text: 'Approve action?', time: '2026-06-15T08:00:00Z' }}
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
});

describe('UserMessageAttachments', () => {
  it('normalizes supported image sources and empty attachment lists', () => {
    const screenshotPath = localScreenshotPath('/');
    expect(resolveAttachmentImageSrc({ previewUrl: 'data:image/png;base64,abc' })).toBe('data:image/png;base64,abc');
    expect(resolveAttachmentImageSrc({ previewUrl: 'blob:screen-preview' })).toBe('blob:screen-preview');
    expect(resolveAttachmentImageSrc({ previewUrl: '/clipboard/a.png' })).toBe('/clipboard/a.png');
    expect(resolveAttachmentImageSrc({ previewUrl: 'https://example.test/a.png' })).toBe('https://example.test/a.png');
    expect(resolveAttachmentImageSrc({ path: 'C:/Users/ai/AppData/Local/Temp/clipboard-222.png' })).toBe('/clipboard/clipboard-222.png');
    expect(resolveAttachmentImageSrc({ path: 'C:/Users/ai/AppData/Local/Temp/codex-clipboard-f05.png' })).toBe('/clipboard/codex-clipboard-f05.png');
    expect(resolveAttachmentImageSrc({ path: screenshotPath })).toBe(`/local-image?path=${encodeURIComponent(screenshotPath)}`);

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

  it('renders normal local screenshot paths through the local image route', () => {
    const screenshotPath = localScreenshotPath('\\');

    render(
      <UserMessageAttachments
        attachments={[
          { path: screenshotPath, name: screenshotName },
        ]}
      />,
    );

    const img = screen.getByRole('img', { name: screenshotName });
    expect(img).toHaveAttribute('src', `/local-image?path=${encodeURIComponent(screenshotPath)}`);
  });
});

describe('TimelineLoadingPlaceholder', () => {
  it('renders a live loading placeholder', () => {
    render(<TimelineLoadingPlaceholder />);

    expect(screen.getByTestId('timeline-loading-placeholder')).toHaveAttribute('aria-live', 'polite');
    expect(screen.getByText('\u6b63\u5728\u540c\u6b65\u4f1a\u8bdd\u5386\u53f2')).toBeInTheDocument();
  });
});
