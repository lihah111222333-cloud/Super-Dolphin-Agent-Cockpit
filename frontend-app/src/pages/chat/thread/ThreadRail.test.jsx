import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
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
    const copy = { ...APP_COPY.zh.chat, threadRunning: 'Thread running' };

    render(<ThreadRail copy={copy} store={store} />);

    fireEvent.click(screen.getByRole('button', { name: /Active thread/ }));
    fireEvent.click(screen.getByRole('button', { name: copy.newThread }));
    fireEvent.click(screen.getAllByRole('button', { name: copy.pinThread })[0]);

    expect(store.setActiveThread).toHaveBeenCalledWith('t1');
    expect(store.newThread).toHaveBeenCalledTimes(1);
    expect(store.toggleThreadPin).toHaveBeenCalledWith('t1');
    expect(screen.getByLabelText('Thread running')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: copy.archiveThread })).not.toBeInTheDocument();
    expect(store.archiveThread).not.toHaveBeenCalled();
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

  it('keeps the committed thread active while another selection is pending', () => {
    render(<ThreadRail store={createStore({ pendingActiveThreadId: 't2' })} />);

    expect(screen.getByRole('button', { name: /Active thread/ }).closest('.thread-card')).toHaveClass('active');
    expect(screen.getByRole('button', { name: /Older thread/ }).closest('.thread-card')).not.toHaveClass('active');
  });
});
