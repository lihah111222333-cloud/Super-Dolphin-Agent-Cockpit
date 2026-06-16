import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TimelineLoadingPlaceholder, TimelineMessage, UserMessageAttachments } from './TimelineMessage.jsx';
import { resolveAttachmentImageSrc } from './timelineMessageModel.js';

const formatTime = () => '16:00';

describe('TimelineMessage', () => {
  it('renders user attachments as image previews and file pills', () => {
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
        formatTime={formatTime}
      />
    );

    expect(screen.getByRole('button', { name: '\u653e\u5927\u56fe\u7247 clip.png' })).toBeInTheDocument();
    expect(screen.getByText('summary.md')).toBeInTheDocument();
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
    expect(resolveAttachmentImageSrc({ previewUrl: 'data:image/png;base64,abc' })).toBe('data:image/png;base64,abc');
    expect(resolveAttachmentImageSrc({ previewUrl: '/clipboard/a.png' })).toBe('/clipboard/a.png');
    expect(resolveAttachmentImageSrc({ previewUrl: 'https://example.test/a.png' })).toBe('https://example.test/a.png');

    const { container } = render(<UserMessageAttachments attachments={[]} />);
    expect(container.firstChild).toBeNull();
  });
});

describe('TimelineLoadingPlaceholder', () => {
  it('renders a live loading placeholder', () => {
    render(<TimelineLoadingPlaceholder />);

    expect(screen.getByTestId('timeline-loading-placeholder')).toHaveAttribute('aria-live', 'polite');
    expect(screen.getByText('\u6b63\u5728\u540c\u6b65\u4f1a\u8bdd\u5386\u53f2')).toBeInTheDocument();
  });
});
