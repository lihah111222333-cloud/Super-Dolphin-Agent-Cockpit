import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  getSidebarState: vi.fn(),
}));

vi.mock('./shared/api/backendApi.js', async (importOriginal) => ({
  ...await importOriginal(),
  getSidebarState: backend.getSidebarState,
}));

import { SidebarProjectTree } from './WorkbenchSidebarProjectTree.jsx';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';

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

function StoreSidebarProjectTree() {
  return <SidebarProjectTree projectPath="/repo/a" setActivePage={vi.fn()} store={useClientStore()} />;
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
    render(<SidebarProjectTree projectPath="/repo/a" setActivePage={vi.fn()} store={store} />);
    fireEvent.click(screen.getByRole('button', { name: '选择项目 b' }));
    fireEvent.click(await screen.findByRole('button', { name: '打开项目聊天：Thread B' }));

    await waitFor(() => expect(store.setActiveProjectPath).toHaveBeenCalledTimes(1));
    expect(store.cancelOpeningThread).toHaveBeenCalledWith(intentB);
    expect(store.setActiveThread).not.toHaveBeenCalled();
  });
});

describe('SidebarProjectTree store thread source', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('renders non-active bridge additions, renames, and deletes directly from the sidebar store', () => {
    const refreshSidebarSnapshotForCwdInBackground = vi.fn();
    const common = {
      activeProject: '/repo/a',
      activeThreadId: '',
      projects: ['/repo/a', '/repo/b'],
      threads: [],
      refreshSidebarSnapshotForCwdInBackground,
    };
    const { rerender } = render(
      <SidebarProjectTree
        projectPath="/repo/a"
        setActivePage={vi.fn()}
        store={{ ...common, sidebarThreadsByProject: { '/repo/a': [], '/repo/b': [] } }}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: '选择项目 b' }));
    expect(refreshSidebarSnapshotForCwdInBackground).toHaveBeenCalledWith('/repo/b');
    expect(screen.queryByRole('button', { name: '打开项目聊天：Bridge created' })).not.toBeInTheDocument();

    rerender(
      <SidebarProjectTree
        projectPath="/repo/a"
        setActivePage={vi.fn()}
        store={{ ...common, sidebarThreadsByProject: { '/repo/a': [], '/repo/b': [thread('bridge-thread', '/repo/b', 'Bridge created')] } }}
      />,
    );
    expect(screen.getByRole('button', { name: '打开项目聊天：Bridge created' })).toBeInTheDocument();

    rerender(
      <SidebarProjectTree
        projectPath="/repo/a"
        setActivePage={vi.fn()}
        store={{ ...common, sidebarThreadsByProject: { '/repo/a': [], '/repo/b': [thread('bridge-thread', '/repo/b', 'Bridge renamed')] } }}
      />,
    );
    expect(screen.getByRole('button', { name: '打开项目聊天：Bridge renamed' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '打开项目聊天：Bridge created' })).not.toBeInTheDocument();

    rerender(
      <SidebarProjectTree
        projectPath="/repo/a"
        setActivePage={vi.fn()}
        store={{ ...common, sidebarThreadsByProject: { '/repo/a': [], '/repo/b': [] } }}
      />,
    );
    expect(screen.queryByRole('button', { name: '打开项目聊天：Bridge renamed' })).not.toBeInTheDocument();
  });

  it('renders an expanded non-active project from the real client store refresh', async () => {
    const projectSidebar = deferred();
    resetClientStoreForTests({
      cwd: '/repo/a',
      projectScopeCwd: '/repo/a',
      activeProject: '/repo/a',
      projects: ['/repo/a', '/repo/b'],
      activeThreadId: 'thread-a',
      threads: [thread('thread-a', '/repo/a', 'Project A')],
      sidebarThreadsByProject: {
        '/repo/a': [thread('thread-a', '/repo/a', 'Project A')],
      },
    });
    backend.getSidebarState.mockReturnValueOnce(projectSidebar.promise);

    render(<StoreSidebarProjectTree />);
    fireEvent.click(screen.getByRole('button', { name: '选择项目 b' }));
    expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/b' });

    projectSidebar.resolve({
      activeThreadId: 'thread-b',
      threads: [{ id: 'thread-b', name: 'Project B', provider: 'codex', status: 'idle' }],
    });

    await waitFor(() => expect(screen.getByRole('button', { name: '打开项目聊天：Project B' })).toBeInTheDocument());
    expect(useClientStore.getState()).toEqual(expect.objectContaining({
      activeProject: '/repo/a',
      activeThreadId: 'thread-a',
      threads: [thread('thread-a', '/repo/a', 'Project A')],
    }));
  });
});
