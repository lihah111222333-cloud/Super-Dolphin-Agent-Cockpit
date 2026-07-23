import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  getSidebarState: vi.fn(),
}));

vi.mock('./shared/api/backendApi.js', () => ({
  getSidebarState: backend.getSidebarState,
}));

import { SidebarProjectTree, SidebarTaskSummary } from './WorkbenchSidebarProjectTree.jsx';

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
      captureThreadSelection: vi.fn()
        .mockReturnValueOnce(intentA1)
        .mockReturnValueOnce(intentB)
        .mockReturnValueOnce(intentA3),
      setActiveProjectPath: vi.fn(() => projectSwitch.promise),
      openThreadById: vi.fn().mockResolvedValue(true),
    };
    backend.getSidebarState.mockResolvedValue({ threads: [threadB] });

    render(<SidebarProjectTree projectPath="/repo/a" setActivePage={vi.fn()} store={store} />);

    fireEvent.click(screen.getByRole('button', { name: '打开项目聊天：Thread A' }));
    fireEvent.click(screen.getByRole('button', { name: '选择项目 b' }));
    fireEvent.click(await screen.findByRole('button', { name: '打开项目聊天：Thread B' }));
    fireEvent.click(screen.getByRole('button', { name: '打开项目聊天：Thread A' }));

    expect(store.setActiveProjectPath).toHaveBeenCalledWith('/repo/b', {
      preserveActiveThreadId: true,
    });

    await act(async () => {
      projectSwitch.resolve({ projects: ['/repo/a', '/repo/b'], active: '/repo/b' });
      await projectSwitch.promise;
    });
    await waitFor(() => expect(store.openThreadById).toHaveBeenCalledTimes(3));

    expect(store.openThreadById.mock.calls).toEqual([
      ['thread-a', { source: 'sidebar-project-tree', selectionSnapshot: intentA1 }],
      ['thread-a', { source: 'sidebar-project-tree', selectionSnapshot: intentA3 }],
      ['thread-b', { source: 'sidebar-project-tree', selectionSnapshot: intentB }],
    ]);
    expect(store.openThreadById).not.toHaveBeenCalledWith('', expect.anything());
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
      captureThreadSelection: vi.fn(() => intentB),
      openThreadById: vi.fn(),
      setActiveProjectPath: vi.fn().mockResolvedValue(false),
    };
    backend.getSidebarState.mockResolvedValue({ threads: [threadB] });

    render(<SidebarProjectTree projectPath="/repo/a" setActivePage={vi.fn()} store={store} />);
    fireEvent.click(screen.getByRole('button', { name: '选择项目 b' }));
    fireEvent.click(await screen.findByRole('button', { name: '打开项目聊天：Thread B' }));

    await waitFor(() => expect(store.setActiveProjectPath).toHaveBeenCalledTimes(1));
    expect(store.openThreadById).not.toHaveBeenCalled();
  });
});

describe('sidebar thread rename actions', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('uses an explicit accessible project-thread rename button while title double-click only navigates', async () => {
    const projectThread = thread('thread-a', '/repo/a', 'Thread A');
    const store = {
      activeProject: '/repo/a', activeThreadId: '', bootstrapStatus: 'ready', chatSurfaceLoadingCwd: '',
      projects: ['/repo/a'], threads: [projectThread], sidebarThreadsByProject: { '/repo/a': [projectThread] },
      captureThreadSelection: vi.fn(() => ({ selectionIntentId: 1, targetThreadId: 'thread-a' })),
      openThreadById: vi.fn().mockResolvedValue(true), renameThread: vi.fn().mockResolvedValue(true),
    };
    render(<SidebarProjectTree projectPath="/repo/a" setActivePage={vi.fn()} store={store} />);

    const title = screen.getByRole('button', { name: '打开项目聊天：Thread A' });
    fireEvent.click(title);
    fireEvent.doubleClick(title);
    expect(screen.queryByRole('textbox', { name: '会话名称' })).not.toBeInTheDocument();
    expect(store.openThreadById).toHaveBeenCalledTimes(1);

    const rename = screen.getByRole('button', { name: '重命名会话：Thread A' });
    expect(rename.tagName).toBe('BUTTON');
    rename.focus();
    fireEvent.click(rename);
    const input = await screen.findByRole('textbox', { name: '会话名称' });
    fireEvent.change(input, { target: { value: 'Renamed A' } });
    fireEvent.click(screen.getByRole('button', { name: '保存会话名称' }));
    await waitFor(() => expect(store.renameThread).toHaveBeenCalledWith('thread-a', 'Renamed A'));
  });

  it('keeps explicit rename available for a running task thread', async () => {
    const taskThread = { ...thread('task-a', '/repo/a', 'Task A'), status: 'running', kind: 'automation' };
    const store = { activeThreadId: '', threads: [taskThread], renameThread: vi.fn().mockResolvedValue(true) };
    render(<SidebarTaskSummary setActivePage={vi.fn()} store={store} />);

    expect(screen.getByLabelText('会话运行中')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重命名会话：Task A' }));
    expect(await screen.findByRole('textbox', { name: '会话名称' })).toBeInTheDocument();
  });
});
