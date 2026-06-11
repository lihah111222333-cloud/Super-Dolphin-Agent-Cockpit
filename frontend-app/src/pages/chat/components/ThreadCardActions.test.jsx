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
    editing: false,
    archiveLabel: '归档会话',
    hoveredArchiveThreadId: '',
    hoveredPinThreadId: '',
    loading: false,
    onBeginRename: vi.fn(),
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

    fireEvent.click(screen.getByRole('button', { name: '重命名会话' }));
    fireEvent.click(screen.getByRole('button', { name: '置顶对话' }));
    fireEvent.click(screen.getByRole('button', { name: '归档会话' }));

    expect(props.onBeginRename).toHaveBeenCalledTimes(1);
    expect(props.onTogglePin).toHaveBeenCalledTimes(1);
    expect(props.onToggleArchive).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: '删除会话' })).not.toBeInTheDocument();
  });

  it('renders archived thread delete and restore actions', () => {
    const props = renderActions({
      thread: { ...activeThread, archived: true },
      archiveLabel: '恢复会话',
      hoveredArchiveThreadId: 'thread-1',
    });

    fireEvent.click(screen.getByRole('button', { name: '删除会话' }));
    fireEvent.click(screen.getByRole('button', { name: '恢复会话' }));

    expect(screen.getByTestId('thread-archive-tooltip')).toHaveTextContent('恢复会话');
    expect(screen.queryByRole('button', { name: '置顶对话' })).not.toBeInTheDocument();
    expect(props.onBeginDelete).toHaveBeenCalledTimes(1);
    expect(props.onToggleArchive).toHaveBeenCalledTimes(1);
  });

  it('hides action triggers while renaming and disables archive while loading', () => {
    renderActions({
      editing: true,
      loading: true,
    });

    expect(screen.queryByRole('button', { name: '重命名会话' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '置顶对话' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '归档会话' })).toBeDisabled();
  });
});
