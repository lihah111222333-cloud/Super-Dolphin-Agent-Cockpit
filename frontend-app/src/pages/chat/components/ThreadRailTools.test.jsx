import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ThreadRailTools } from './ThreadRailTools.jsx';

function renderTools(overrides = {}) {
  const props = {
    count: 3,
    confirmCleanMode: false,
    showArchivedThreads: false,
    staleThreadIds: [],
    toggleArchiveLabel: '打开归档列表',
    onNewThread: vi.fn(),
    onCleanConfirm: vi.fn(),
    onCleanMode: vi.fn(),
    onCancelClean: vi.fn(),
    onToggleArchive: vi.fn(),
    ...overrides,
  };
  render(<ThreadRailTools {...props} />);
  return props;
}

describe('ThreadRailTools', () => {
  it('renders the active list controls and routes actions through props', () => {
    const props = renderTools();

    expect(screen.getByLabelText('3 个 Agent')).toHaveTextContent('3');
    fireEvent.click(screen.getByRole('button', { name: '新建对话' }));
    fireEvent.click(screen.getByRole('button', { name: '打开归档列表' }));

    expect(screen.queryByRole('button', { name: '清理无用对话' })).not.toBeInTheDocument();
    expect(props.onNewThread).toHaveBeenCalledTimes(1);
    expect(props.onToggleArchive).toHaveBeenCalledTimes(1);
  });

  it('shows archived cleanup entry only when stale archived threads exist', () => {
    const props = renderTools({
      showArchivedThreads: true,
      staleThreadIds: ['thread-1'],
      toggleArchiveLabel: '返回会话列表',
    });

    fireEvent.click(screen.getByRole('button', { name: '清理无用对话' }));
    fireEvent.click(screen.getByRole('button', { name: '返回会话列表' }));

    expect(props.onCleanMode).toHaveBeenCalledTimes(1);
    expect(props.onToggleArchive).toHaveBeenCalledTimes(1);
  });

  it('renders cleanup confirmation controls while confirm mode is active', () => {
    const props = renderTools({
      confirmCleanMode: true,
      showArchivedThreads: true,
      staleThreadIds: ['thread-1'],
      toggleArchiveLabel: '返回会话列表',
    });

    fireEvent.click(screen.getByRole('button', { name: '确认' }));
    fireEvent.click(screen.getByRole('button', { name: '取消' }));

    expect(screen.queryByRole('button', { name: '清理无用对话' })).not.toBeInTheDocument();
    expect(props.onCleanConfirm).toHaveBeenCalledTimes(1);
    expect(props.onCancelClean).toHaveBeenCalledTimes(1);
  });
});
