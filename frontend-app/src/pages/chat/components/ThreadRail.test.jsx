import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ThreadRail } from './ThreadRail.jsx';
import { computeThreadWindow } from './ThreadRailWindow.js';

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
  it('computes a bounded thread window for large lists', () => {
    const threads = Array.from({ length: 200 }, (_, index) => ({ id: `t-${index}` }));

    const firstWindow = computeThreadWindow(threads, { scrollTop: 0, viewportHeight: 340 });
    expect(firstWindow.virtualized).toBe(true);
    expect(firstWindow.rows[0].id).toBe('t-0');
    expect(firstWindow.rows).toHaveLength(17);
    expect(firstWindow.topSpacer).toBe(0);
    expect(firstWindow.bottomSpacer).toBeGreaterThan(0);

    const scrolledWindow = computeThreadWindow(threads, { scrollTop: 120 * 68, viewportHeight: 340 });
    expect(scrolledWindow.rows[0].id).toBe('t-114');
    expect(scrolledWindow.rows.some((thread) => thread.id === 't-120')).toBe(true);
    expect(scrolledWindow.topSpacer).toBe(114 * 68);
  });

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

  it('renders only the visible thread card window for large thread lists', () => {
    const store = createStore({
      activeThreadId: 't-0',
      threads: Array.from({ length: 200 }, (_, index) => ({
        id: `t-${index}`,
        name: `Thread ${index}`,
        provider: 'codex',
        status: 'idle',
        updatedAt: 1000 - index,
      })),
    });

    render(<ThreadRail store={store} />);
    const list = screen.getByTestId('thread-list');
    Object.defineProperty(list, 'clientHeight', { configurable: true, value: 340 });

    expect(screen.getByText('Thread 0')).toBeInTheDocument();
    expect(screen.queryByText('Thread 120')).not.toBeInTheDocument();
    expect(document.querySelectorAll('.thread-card').length).toBeLessThan(40);

    Object.defineProperty(list, 'scrollTop', { configurable: true, value: 120 * 68 });
    fireEvent.scroll(list);

    expect(screen.getByText('Thread 120')).toBeInTheDocument();
    expect(screen.queryByText('Thread 0')).not.toBeInTheDocument();
    expect(document.querySelectorAll('.thread-card').length).toBeLessThan(40);
  });

  it('anchors the virtual thread window on the active thread', () => {
    const store = createStore({
      activeThreadId: 't-120',
      threads: Array.from({ length: 200 }, (_, index) => ({
        id: `t-${index}`,
        name: `Thread ${index}`,
        provider: 'codex',
        status: 'idle',
        updatedAt: 1000 - index,
      })),
    });

    render(<ThreadRail store={store} />);

    const activeThread = screen.getByText('Thread 120');
    expect(activeThread).toBeInTheDocument();
    expect(activeThread.closest('.thread-card')).toHaveClass('active');
    expect(document.querySelectorAll('.thread-card').length).toBeLessThan(40);
  });
});
