import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ThreadRail } from './ThreadRail.jsx';

function createStore(overrides = {}) {
  return {
    activeThreadId: 't1',
    activityThreadAtById: {},
    archiveThread: vi.fn(),
    chatSurfaceLoadingCwd: '',
    deleteStaleThreads: vi.fn(),
    newThread: vi.fn(),
    pendingActiveThreadId: '',
    pinnedThreadAtById: {},
    renameThread: vi.fn(async () => true),
    setActiveThread: vi.fn(),
    threadArchiveLoadingByThread: {},
    threads: [
      { id: 't1', name: 'Active thread', provider: 'codex', status: 'running', updatedAt: 20 },
      { id: 't2', name: 'Older thread', provider: 'claude', status: 'idle', updatedAt: 10 },
      { id: 'archived-empty', name: 'archived-empty', provider: 'codex', archived: true, status: 'idle', updatedAt: 1 },
    ],
    toggleThreadPin: vi.fn(),
    ...overrides,
  };
}

describe('ThreadRail', () => {
  it('renders active threads and routes thread actions through the store', () => {
    const store = createStore();

    render(<ThreadRail store={store} />);

    fireEvent.click(screen.getByRole('button', { name: /Active thread/ }));
    fireEvent.click(screen.getByRole('button', { name: '新建对话' }));
    fireEvent.click(screen.getAllByRole('button', { name: '置顶对话' })[0]);
    fireEvent.click(screen.getAllByRole('button', { name: '归档会话' })[0]);

    expect(store.setActiveThread).toHaveBeenCalledWith('t1');
    expect(store.newThread).toHaveBeenCalledTimes(1);
    expect(store.toggleThreadPin).toHaveBeenCalledWith('t1');
    expect(store.archiveThread).toHaveBeenCalledWith('t1', true);
  });

  it('switches to archived threads and confirms stale cleanup', () => {
    const store = createStore();

    render(<ThreadRail store={store} />);

    fireEvent.click(screen.getByRole('button', { name: '打开归档列表' }));
    expect(screen.getByLabelText('归档列表')).toBeInTheDocument();
    expect(screen.getByText('空对话')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '清理无用对话' }));
    fireEvent.click(screen.getByRole('button', { name: '确认' }));

    expect(store.deleteStaleThreads).toHaveBeenCalledWith(['archived-empty']);
  });
});
