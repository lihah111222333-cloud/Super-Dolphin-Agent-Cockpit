import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ThreadCardActions } from './ThreadCardActions.jsx';

const activeThread = {
  id: 'thread-1',
  archived: false,
  pinned: false,
  pinnedAt: 0,
};

function renderActions(overrides = {}) {
  const props = {
    thread: activeThread,
    threadLabel: 'AI 设计流程',
    copy: {
      threadActionsSuffix: 'actions',
      pinThread: 'Pin thread',
      unpinThread: 'Unpin thread',
      deleteThread: 'Delete thread',
    },
    editing: false,
    archiveLabel: 'Archive thread',
    hoveredArchiveThreadId: '',
    hoveredPinThreadId: '',
    loading: false,
    running: false,
    runningLabel: 'Thread running',
    onSetHoveredArchiveThreadId: vi.fn(),
    onSetHoveredPinThreadId: vi.fn(),
    onToggleArchive: vi.fn(),
    onTogglePin: vi.fn(),
    onBeginDelete: vi.fn(),
    ...overrides,
  };
  render(<ThreadCardActions {...props} />);
  return props;
}

describe('ThreadCardActions', () => {
  it('routes active thread card actions through props', () => {
    const props = renderActions();

    fireEvent.click(screen.getByRole('button', { name: 'Pin thread' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete thread' }));

    expect(props.onTogglePin).toHaveBeenCalledTimes(1);
    expect(props.onBeginDelete).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: 'Archive thread' })).not.toBeInTheDocument();
  });

  it('renders archived thread delete and restore actions', () => {
    const props = renderActions({
      thread: { ...activeThread, archived: true },
      archiveLabel: 'Restore thread',
      hoveredArchiveThreadId: 'thread-1',
    });

    fireEvent.click(screen.getByRole('button', { name: 'Delete thread' }));
    fireEvent.click(screen.getByRole('button', { name: 'Restore thread' }));

    expect(screen.getByTestId('thread-archive-tooltip')).toHaveTextContent('Restore thread');
    expect(screen.queryByRole('button', { name: 'Pin thread' })).not.toBeInTheDocument();
    expect(props.onBeginDelete).toHaveBeenCalledTimes(1);
    expect(props.onToggleArchive).toHaveBeenCalledTimes(1);
  });

  it('shows running state next to delete and hides actions while renaming', () => {
    renderActions({ running: true });

    expect(screen.getByLabelText('Thread running')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete thread' })).toBeInTheDocument();
  });

  it('hides action triggers while renaming', () => {
    renderActions({
      editing: true,
      loading: true,
    });

    expect(screen.queryByRole('button', { name: 'Pin thread' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete thread' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Archive thread' })).not.toBeInTheDocument();
  });
});
