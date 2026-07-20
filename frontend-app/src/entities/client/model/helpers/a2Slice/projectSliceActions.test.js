import { describe, expect, it, vi } from 'vitest';

import { createProjectActionSet } from './projectSliceActions.js';

function deferred() {
  let reject;
  let resolve;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, reject, resolve };
}

function createActionHarness() {
  let state = {
    activeProject: '/repo/app',
    projects: ['/repo/app', '/repo/older', '/repo/newer'],
  };
  const runtime = {
    addWarning: vi.fn(),
    applyProjects: vi.fn((projects) => {
      state = { ...state, activeProject: projects.active, projects: projects.projects };
    }),
    get: () => state,
    notifyAction: vi.fn(),
    refreshChatSurfaceForCwdInBackground: vi.fn(),
    requireProjectScopeCwd: vi.fn(() => '/repo/app'),
    saveActiveComposerDraft: vi.fn(),
    set: vi.fn((patch) => {
      state = { ...state, ...patch };
    }),
  };
  const deps = {
    addProject: vi.fn(),
    normalizePath: (path) => path,
    projectShortLabel: (path) => path,
    setActiveProject: vi.fn(),
  };
  const action = createProjectActionSet(runtime, deps).setActiveProjectPath;

  return { action, deps, runtime, state: () => state };
}

describe('projectSliceActions', () => {
  it('keeps the newest CWD when an older switch succeeds last', async () => {
    const { action, deps, runtime, state } = createActionHarness();
    const older = deferred();
    const newer = deferred();
    deps.setActiveProject
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);

    const olderSwitch = action('/repo/older');
    await Promise.resolve();
    const newerSwitch = action('/repo/newer');
    await Promise.resolve();

    newer.resolve({
      active: '/repo/newer',
      projects: ['/repo/app', '/repo/older', '/repo/newer'],
    });
    await expect(newerSwitch).resolves.toBe(true);

    older.resolve({
      active: '/repo/older',
      projects: ['/repo/app', '/repo/older', '/repo/newer'],
    });
    await expect(olderSwitch).resolves.toBe(false);

    expect(state().activeProject).toBe('/repo/newer');
    expect(runtime.applyProjects).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledWith('已切换项目：/repo/newer', 'success');
  });

  it('does not roll back the newest CWD when an older switch fails last', async () => {
    const { action, deps, runtime, state } = createActionHarness();
    const older = deferred();
    const newer = deferred();
    deps.setActiveProject
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);

    const olderSwitch = action('/repo/older');
    await Promise.resolve();
    const newerSwitch = action('/repo/newer');
    await Promise.resolve();

    newer.resolve({
      active: '/repo/newer',
      projects: ['/repo/app', '/repo/older', '/repo/newer'],
    });
    await expect(newerSwitch).resolves.toBe(true);

    older.reject(new Error('older switch failed'));
    await expect(olderSwitch).resolves.toBe(false);

    expect(state().activeProject).toBe('/repo/newer');
    expect(runtime.applyProjects).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledWith('已切换项目：/repo/newer', 'success');
    expect(runtime.addWarning).not.toHaveBeenCalled();
  });
});
