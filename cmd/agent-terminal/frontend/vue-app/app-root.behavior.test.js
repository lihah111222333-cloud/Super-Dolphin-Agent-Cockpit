// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const hooks = vi.hoisted(() => ({ mounted: [], unmounted: [] }));
const stores = vi.hoisted(() => ({
  projectStore: {
    state: { active: '/repo' },
    reloadProjects: vi.fn(async () => {}),
    addProject: vi.fn(async () => {}),
    setActive: vi.fn(async () => {}),
  },

  threadStore: {
    state: { activeThreadId: 'thread-existing' },
    setPreferenceScopeCwd: vi.fn(),
    refreshSidebarState: vi.fn(async () => {}),
    handleAgentEvent: vi.fn(),
    handleBridgeEvent: vi.fn(),
    startThread: vi.fn(async () => 'thread-new'),
    sendMessage: vi.fn(async () => {}),
  },
}));
const apiMock = vi.hoisted(() => {
  const api = {
    callAPI: vi.fn(),
    getBuildInfo: vi.fn(),
    agentCb: null,
    bridgeCb: null,
    quitCb: null,
    agentOff: vi.fn(),
    bridgeOff: vi.fn(),
    quitOff: vi.fn(),
  };
  api.onAgentEvent = vi.fn((cb) => {
    api.agentCb = cb;
    return api.agentOff;
  });
  api.onBridgeEvent = vi.fn((cb) => {
    api.bridgeCb = cb;
    return api.bridgeOff;
  });
  api.onAppWillQuit = vi.fn((cb) => {
    api.quitCb = cb;
    return api.quitOff;
  });
  return api;
});
const historyMock = vi.hoisted(() => ({
  requestHistoryLoad: vi.fn(),
  ensureThreadSelectionFresh: vi.fn(async () => ({ requestedHistory: false, syncedThreadState: false, forcedHistoryReload: false })),
}));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: (fn) => hooks.mounted.push(fn),
    onBeforeUnmount: (fn) => hooks.unmounted.push(fn),
  };
});

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  getBuildInfo: apiMock.getBuildInfo,
  onAgentEvent: apiMock.onAgentEvent,
  onBridgeEvent: apiMock.onBridgeEvent,
  onAppWillQuit: apiMock.onAppWillQuit,
}));
vi.mock('./stores/projects.js', () => ({ useProjectStore: () => stores.projectStore }));
vi.mock('./stores/threads.js', () => ({ useThreadStore: () => stores.threadStore }));
vi.mock('./utils/thread-page-utils.js', () => ({
  requestHistoryLoad: historyMock.requestHistoryLoad,
  ensureThreadSelectionFresh: historyMock.ensureThreadSelectionFresh,
}));
vi.mock('./components/SidebarNav.js', () => ({ SidebarNav: { name: 'SidebarNav' } }));
vi.mock('./components/ProjectModal.js', () => ({ ProjectModal: { name: 'ProjectModal' } }));
vi.mock('./pages/UnifiedChatPage.js', () => ({ UnifiedChatPage: { name: 'UnifiedChatPage' } }));
vi.mock('./pages/DataPage.js', () => ({ DataPage: { name: 'DataPage' } }));
vi.mock('./pages/SkillsPage.js', () => ({ SkillsPage: { name: 'SkillsPage' } }));
vi.mock('./pages/TasksPage.js', () => ({ TasksPage: { name: 'TasksPage' } }));
vi.mock('./pages/CommandsPage.js', () => ({ CommandsPage: { name: 'CommandsPage' } }));
vi.mock('./pages/SettingsPage.ts', () => ({ SettingsPage: { name: 'SettingsPage' } }));
vi.mock('./pages/MemoryCenterPage.js', () => ({ MemoryCenterPage: { name: 'MemoryCenterPage' } }));
vi.mock('./pages/SharedFilesPage.js', () => ({ SharedFilesPage: { name: 'SharedFilesPage' } }));

import { AppRoot } from './app.js';

const flush = async (times = 16) => {

  for (let index = 0; index < times; index += 1) {
    await Promise.resolve();
  }
};

beforeEach(() => {
  hooks.mounted.length = 0;
  hooks.unmounted.length = 0;

  stores.projectStore.state.active = '/repo';
  stores.projectStore.reloadProjects.mockReset().mockResolvedValue(undefined);
  stores.projectStore.addProject.mockReset().mockResolvedValue(undefined);
  stores.projectStore.setActive.mockReset().mockImplementation(async (cwd) => {
    stores.projectStore.state.active = cwd;
  });

  stores.threadStore.state.activeThreadId = 'thread-existing';
  stores.threadStore.setPreferenceScopeCwd.mockReset();
  stores.threadStore.refreshSidebarState.mockReset().mockResolvedValue(undefined);
  stores.threadStore.handleAgentEvent.mockReset();
  stores.threadStore.handleBridgeEvent.mockReset();
  stores.threadStore.startThread.mockReset().mockResolvedValue('thread-new');
  stores.threadStore.sendMessage.mockReset().mockResolvedValue(undefined);

  apiMock.callAPI.mockReset();
  apiMock.getBuildInfo.mockReset();
  apiMock.agentCb = null;
  apiMock.bridgeCb = null;
  apiMock.quitCb = null;
  apiMock.agentOff.mockClear();
  apiMock.bridgeOff.mockClear();
  apiMock.quitOff.mockClear();

  historyMock.requestHistoryLoad.mockReset().mockResolvedValue(true);
  historyMock.ensureThreadSelectionFresh.mockReset().mockResolvedValue({ requestedHistory: false, syncedThreadState: false, forcedHistoryReload: false });

  vi.stubGlobal('window', {
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  });
  vi.stubGlobal('setInterval', vi.fn(() => 42));
  vi.stubGlobal('clearInterval', vi.fn());
});

afterEach(() => {
  for (const fn of hooks.unmounted.splice(0)) fn();
  hooks.mounted.length = 0;
  vi.unstubAllGlobals();
});

describe('AppRoot behavior', () => {
  it('bootstraps runtime state, subscribes events and cleans up on unmount', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      return {};
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(apiMock.getBuildInfo).toHaveBeenCalled();
    expect(apiMock.callAPI).toHaveBeenCalledWith('config/read', {});
    expect(stores.projectStore.reloadProjects).toHaveBeenCalled();
    expect(stores.threadStore.setPreferenceScopeCwd).toHaveBeenCalledWith('/repo');
    expect(stores.threadStore.refreshSidebarState).toHaveBeenCalled();
    expect(historyMock.ensureThreadSelectionFresh).toHaveBeenCalledWith(stores.threadStore, 'thread-existing', { reason: 'bootstrap' });
    expect(apiMock.onAgentEvent).toHaveBeenCalled();
    expect(apiMock.onBridgeEvent).toHaveBeenCalled();
    expect(apiMock.onAppWillQuit).toHaveBeenCalled();
    expect(vm.buildInfo.version).toBe('1.0.0');
    expect(vm.currentCwdDisplay.value).toContain('/window');

    hooks.unmounted.splice(0).forEach((fn) => fn());
    expect(apiMock.agentOff).toHaveBeenCalled();
    expect(apiMock.bridgeOff).toHaveBeenCalled();
    expect(apiMock.quitOff).toHaveBeenCalled();
    expect(globalThis.clearInterval).toHaveBeenCalledWith(42);
  });

  it('runs command cards through an ensured active thread', async () => {
    stores.threadStore.state.activeThreadId = '';
    const vm = AppRoot.setup();
    vm.page.value = 'commands';

    await vm.runCommandCard({ command_template: 'echo hello' });
    expect(stores.threadStore.startThread).toHaveBeenCalledWith('/repo');
    expect(historyMock.requestHistoryLoad).toHaveBeenCalledWith(stores.threadStore, 'thread-new');
    expect(stores.threadStore.sendMessage).toHaveBeenCalledWith('thread-new', expect.stringContaining('echo hello'));
    expect(vm.page.value).toBe('chat');
  });

  it('refreshes dashboard pages and reacts to skills changed events while on skills page', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'ui/dashboard/get' && payload?.page === 'skills') {
        return { skills: [{ name: 'SkillA' }] };
      }
      return {};
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    vm.page.value = 'skills';
    await flush();
    expect(vm.dashboard.skills).toEqual([{ name: 'SkillA' }]);

    apiMock.bridgeCb?.({ method: 'skills/changed' });
    await flush();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/dashboard/get', { page: 'skills', cwd: '/repo' });
  });

  it('keeps final output refs from the memory dashboard response', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/dashboard/get' && payload?.page === 'memory') {
        return {
          memory: [{ path: 'reports/final.pptx' }],
          finalOutputRefs: [{ path: 'reports/final.pptx', runKey: 'run-1' }],
        };
      }
      return {};
    });

    const vm = AppRoot.setup();
    await vm.refreshDashboardByPage('memory');

    expect(vm.dashboard.memory).toEqual([{ path: 'reports/final.pptx' }]);
    expect(vm.dashboard.finalOutputRefs).toEqual([{ path: 'reports/final.pptx', runKey: 'run-1' }]);
  });

  it('consumes window bootstrap snapshot and starts the continued task in a new window', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'ui/windowBootstrap/get') {
        return {
          snapshot: {
            page: 'chat',
            cwd: '/task-repo',
            taskStart: {
              focusMode: 'chat',
              config: {
                taskId: 'task-demo',
                taskTitle: 'Memory Center Refactor',
                handoffFile: 'handoff/tasks/task-demo.md',
                continueTask: true,
                autoTaskHandoff: true,
              },
            },
          },
        };
      }
      return {};
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(stores.projectStore.addProject).toHaveBeenCalledWith('/task-repo');
    expect(stores.projectStore.setActive).toHaveBeenCalledWith('/task-repo');
    expect(stores.threadStore.startThread).toHaveBeenCalledWith('/task-repo', {
      focusMode: 'chat',
      config: {
        taskId: 'task-demo',
        taskTitle: 'Memory Center Refactor',
        handoffFile: 'handoff/tasks/task-demo.md',
        continueTask: true,
        autoTaskHandoff: true,
      },
    });
    expect(vm.page.value).toBe('chat');
  });

  it('marks the memory center nav when similar memories need merging', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'ui/memory/get') {
        return {
          overview: {
            health: {
              similarGroups: [
                { nameA: 'A', nameB: 'B' },
                { nameA: 'C', nameB: 'D' },
              ],
            },
          },
          private: { entries: [] },
          team: { entries: [] },
        };
      }
      return {};
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/memory/get', { cwd: '/repo' });
    expect(vm.sidebarBadges.value['memory-center']).toBe(2);
  });

  it('does not mark the memory center nav when no similar memories need merging', () => {
    const vm = AppRoot.setup();

    vm.memoryCenter.overview = { health: { similarGroups: [] } };

    expect(vm.sidebarBadges.value['memory-center']).toBeUndefined();
  });

  it('marks the skills nav when pending skill candidates need approval', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'skills/candidate/list/pending') {
        return { candidates: [{ id: 'candidate-1' }] };
      }
      return {};
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/candidate/list/pending', { cwd: '/repo', limit: 20, offset: 0 });
    expect(vm.sidebarBadges.value.skills).toBe(1);
  });

  it('keeps skills and memory center nav badges at the same time', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'skills/candidate/list/pending') {
        return { candidates: [{ id: 'candidate-1' }, { id: 'candidate-2' }] };
      }
      if (method === 'ui/memory/get') {
        return {
          overview: { health: { similarGroups: [{ nameA: 'A', nameB: 'B' }] } },
          private: { entries: [] },
          team: { entries: [] },
        };
      }
      return {};
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(vm.sidebarBadges.value.skills).toBe(2);
    expect(vm.sidebarBadges.value['memory-center']).toBe(1);
  });
});
