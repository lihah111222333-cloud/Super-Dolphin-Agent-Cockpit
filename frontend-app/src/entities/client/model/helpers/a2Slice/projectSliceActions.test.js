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
    rebindBridgeEventScope: vi.fn(() => Promise.resolve(true)),
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
  it('serializes switches so the newest intent is the backend final active project', async () => {
    const { action, deps, runtime, state } = createActionHarness();
    const older = deferred();
    const newer = deferred();
    let backendActive = '/repo/app';
    deps.setActiveProject
      .mockImplementationOnce(() => older.promise.then((result) => {
        backendActive = result.active;
        return result;
      }))
      .mockImplementationOnce(() => newer.promise.then((result) => {
        backendActive = result.active;
        return result;
      }));

    const olderSwitch = action('/repo/older');
    await vi.waitFor(() => {
      expect(deps.setActiveProject).toHaveBeenCalledTimes(1);
    });
    const newerSwitch = action('/repo/newer');
    await Promise.resolve();

    expect(deps.setActiveProject).toHaveBeenCalledTimes(1);
    older.resolve({
      active: '/repo/older',
      projects: ['/repo/app', '/repo/older', '/repo/newer'],
    });
    await expect(olderSwitch).resolves.toBe(false);
    await vi.waitFor(() => {
      expect(deps.setActiveProject).toHaveBeenCalledTimes(2);
    });

    newer.resolve({
      active: '/repo/newer',
      projects: ['/repo/app', '/repo/older', '/repo/newer'],
    });
    await expect(newerSwitch).resolves.toBe(true);

    expect(backendActive).toBe('/repo/newer');
    expect(state().activeProject).toBe('/repo/newer');
    expect(runtime.applyProjects).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledWith('已切换项目：/repo/newer', 'success');
  });

  it('coalesces an unstarted older intent before it can call the backend', async () => {
    const { action, deps, runtime, state } = createActionHarness();
    const newer = deferred();
    deps.setActiveProject.mockReturnValueOnce(newer.promise);

    const olderSwitch = action('/repo/older');
    const newerSwitch = action('/repo/newer');
    await Promise.resolve();

    await expect(olderSwitch).resolves.toBe(false);
    expect(deps.setActiveProject).toHaveBeenCalledTimes(1);
    expect(deps.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/newer' });
    newer.resolve({
      active: '/repo/newer',
      projects: ['/repo/app', '/repo/older', '/repo/newer'],
    });
    await expect(newerSwitch).resolves.toBe(true);

    expect(state().activeProject).toBe('/repo/newer');
    expect(runtime.applyProjects).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledWith('已切换项目：/repo/newer', 'success');
  });

  it('keeps the current project when bridge scope preparation is rejected', async () => {
    const { action, deps, runtime, state } = createActionHarness();
    const capacityError = new Error('frontend-app: turn terminal scope ledger capacity exhausted');
    runtime.rebindBridgeEventScope.mockRejectedValueOnce(capacityError);

    await expect(action('/repo/newer')).rejects.toBe(capacityError);

    expect(state()).toEqual({
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/older', '/repo/newer'],
    });
    expect(deps.setActiveProject).not.toHaveBeenCalled();
    expect(runtime.refreshChatSurfaceForCwdInBackground).not.toHaveBeenCalled();
    expect(runtime.notifyAction).toHaveBeenCalledWith('切换项目失败，请重试。', 'error');
  });
});
