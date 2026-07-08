import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ThreadDisplayCardContent } from './ThreadDisplayCardContent.jsx';

function renderCardContent(overrides = {}) {
  const props = {
    threadLabel: 'Agent 设计助手',
    providerLabel: 'Codex',
    statusDotState: 'busy',
    statusDotTitle: '正在运行',
    statusLabel: '运行中',
    staleReason: '',
    onBeginRename: vi.fn(),
    onSelect: vi.fn(),
    ...overrides,
  };
  render(<ThreadDisplayCardContent {...props} />);
  return props;
}

describe('ThreadDisplayCardContent', () => {
  it('renders thread identity, status, and routes selection through props', () => {
    const props = renderCardContent();

    expect(screen.getByRole('button', { name: /Agent 设计助手/ })).toBeInTheDocument();
    expect(screen.getByText('Codex')).toBeInTheDocument();
    expect(screen.getByText('运行中')).toBeInTheDocument();
    expect(screen.getByTitle('正在运行')).toHaveClass('thread-status-dot--busy');

    fireEvent.click(screen.getByRole('button', { name: /Agent 设计助手/ }));
    fireEvent.doubleClick(screen.getByRole('button', { name: /Agent 设计助手/ }));

    expect(props.onSelect).toHaveBeenCalledTimes(1);
    expect(props.onBeginRename).toHaveBeenCalledTimes(1);
  });

  it('renders stale archived thread badges from the provided reason', () => {
    const { rerender } = render(
      <ThreadDisplayCardContent
        threadLabel="旧会话"
        providerLabel="Claude"
        statusDotState="idle"
        statusDotTitle="已归档"
        statusLabel="已归档"
        staleReason="expired"
        onSelect={vi.fn()}
      />,
    );

    expect(screen.getByText('超7天')).toHaveAttribute('data-stale-reason', 'expired');

    rerender(
      <ThreadDisplayCardContent
        threadLabel="空会话"
        providerLabel="Claude"
        statusDotState="idle"
        statusDotTitle="已归档"
        statusLabel="已归档"
        staleReason="empty"
        onSelect={vi.fn()}
      />,
    );

    expect(screen.getByText('空对话')).toHaveAttribute('data-stale-reason', 'empty');
  });
});
