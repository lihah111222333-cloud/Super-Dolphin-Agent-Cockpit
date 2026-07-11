import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  getSidebarState: vi.fn(),
}));

vi.mock('./shared/api/backendApi.js', () => ({
  getSidebarState: backend.getSidebarState,
}));

import { SidebarProjectTree } from './WorkbenchSidebarProjectTree.jsx';

function deferred() {
  let resolve;
  const promise = new Promise((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
}

function thread(id, cwd, name) {
  return { id, cwd, name, provider: 'codex', status: 'idle' };
}

describe('SidebarProjectTree thread selection intent', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('carries each click token through a cross-project A to B to A race', async () => {
    const projectSwitch = deferred();
    const threadA = thread('thread-a', '/repo/a', 'Thread A');
    const threadB = thread('thread-b', '/repo/b', 'Thread B');
    const intentA1 = { selectionIntentId: 1, targetThreadId: 'thread-a' };
    const intentB = { selectionIntentId: 2, targetThreadId: 'thread-b' };
    const intentA3 = { selectionIntentId: 3, targetThreadId: 'thread-a' };
    const store = {
      activeProject: '/repo/a',
      activeThreadId: 'thread-a',
      bootstrapStatus: 'ready',
      chatSurfaceLoadingCwd: '',
      projects: ['/repo/a', '/repo/b'],
      threads: [threadA],
      sidebarThreadsByProject: {
        '/repo/a': [threadA],
        '/repo/b': [threadB],
      },
      addWarning: vi.fn(),
      beginOpeningThread: vi.fn()
        .mockReturnValueOnce(intentA1)
        .mockReturnValueOnce(intentB)
        .mockReturnValueOnce(intentA3),
      setActiveProjectPath: vi.fn(() => projectSwitch.promise),
      setActiveThread: vi.fn().mockResolvedValue(true),
    };
    backend.getSidebarState.mockResolvedValue({ threads: [threadB] });

    render(<SidebarProjectTree projectPath="/repo/a" setActivePage={vi.fn()} store={store} />);

    fireEvent.click(screen.getByRole('button', { name: '打开项目聊天：Thread A' }));
    fireEvent.click(screen.getByRole('button', { name: '选择项目 b' }));
    fireEvent.click(await screen.findByRole('button', { name: '打开项目聊天：Thread B' }));
    fireEvent.click(screen.getByRole('button', { name: '打开项目聊天：Thread A' }));

    expect(store.setActiveProjectPath).toHaveBeenCalledWith('/repo/b', {
      preserveActiveThreadId: true,
      selectionIntent: intentB,
    });

    await act(async () => {
      projectSwitch.resolve({ projects: ['/repo/a', '/repo/b'], active: '/repo/b' });
      await projectSwitch.promise;
    });
    await waitFor(() => expect(store.setActiveThread).toHaveBeenCalledTimes(3));

    expect(store.setActiveThread.mock.calls).toEqual([
      ['thread-a', { selectionIntent: intentA1 }],
      ['thread-a', { selectionIntent: intentA3 }],
      ['thread-b', { selectionIntent: intentB }],
    ]);
    expect(store.setActiveThread).not.toHaveBeenCalledWith('', expect.anything());
  });

  it('cancels the matching intent without issuing a newer empty-thread selection', async () => {
    const threadB = thread('thread-b', '/repo/b', 'Thread B');
    const intentB = { selectionIntentId: 1, targetThreadId: 'thread-b' };
    const store = {
      activeProject: '/repo/a',
      activeThreadId: 'thread-a',
      bootstrapStatus: 'ready',
      chatSurfaceLoadingCwd: '',
      projects: ['/repo/a', '/repo/b'],
      threads: [],
      sidebarThreadsByProject: { '/repo/b': [threadB] },
      addWarning: vi.fn(),
      beginOpeningThread: vi.fn(() => intentB),
      cancelOpeningThread: vi.fn(),
      setActiveProjectPath: vi.fn().mockResolvedValue(false),
      setActiveThread: vi.fn(),
    };
    backend.getSidebarState.mockResolvedValue({ threads: [threadB] });

    render(<SidebarProjectTree projectPath="/repo/a" setActivePage={vi.fn()} store={store} />);
    fireEvent.click(screen.getByRole('button', { name: '选择项目 b' }));
    fireEvent.click(await screen.findByRole('button', { name: '打开项目聊天：Thread B' }));

    await waitFor(() => expect(store.setActiveProjectPath).toHaveBeenCalledTimes(1));
    expect(store.cancelOpeningThread).toHaveBeenCalledWith(intentB);
    expect(store.setActiveThread).not.toHaveBeenCalled();
  });
});
