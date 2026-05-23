// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const hooks = vi.hoisted(() => ({ mounted: [], unmounted: [] }));
const stores = vi.hoisted(() => ({
  projectStore: {
    state: { active: '/repo' },
    reloadProjects: vi.fn(async () => {}),
    setScopeCwd: vi.fn(),
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
    saveActiveThread: vi.fn(async (id) => { stores.threadStore.state.activeThreadId = id || ''; }),
    saveActiveCmdThread: vi.fn(async (id) => { stores.threadStore.state.activeCmdThreadId = id || ''; }),
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
  isStaleThreadSelectionError: (error) => {
    const text = [error?.message || '', error?.cause?.message || '', typeof error === 'string' ? error : ''].join('\n').toLowerCase();
    return text.includes('session not found')
      || text.includes('session is not available')
      || (text.includes('thread "') && text.includes('not found: store: not found'))
      || (text.includes('resolve session: thread') && text.includes('context deadline exceeded'));
  },
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
import { reactive } from '../lib/vue.esm-browser.prod.js';

const flush = async (times = 16) => {

  for (let index = 0; index < times; index += 1) {
    await Promise.resolve();
  }
};

function dashboardPayload(overrides = {}) {
  return {
    agents: [],
    dags: [],
    taskAcks: [],
    taskTraces: [],
    skills: [],
    commandCards: [],
    prompts: [],
    memory: [],
    finalOutputRefs: [],
    sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    ...overrides,
  };
}

function memoryCenterPayload(overrides = {}) {
  return {
    overview: {},
    private: { entries: [] },
    team: { entries: [] },
    ...overrides,
  };
}

function defaultAppAPI(method) {
  if (method === 'ui/windowBootstrap/get') return { snapshot: null };
  if (method === 'ui/dashboard/get') return dashboardPayload();
  if (method === 'ui/memory/get') return memoryCenterPayload();
  if (method === 'skills/candidate/list/pending') return { candidates: [] };
  return {};
}

beforeEach(() => {
  hooks.mounted.length = 0;
  hooks.unmounted.length = 0;

  stores.projectStore.state = reactive({ active: '/repo' });
  stores.projectStore.reloadProjects.mockReset().mockResolvedValue(undefined);
  stores.projectStore.setScopeCwd.mockReset();
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
  stores.threadStore.saveActiveThread.mockReset().mockImplementation(async (id) => { stores.threadStore.state.activeThreadId = id || ''; });
  stores.threadStore.saveActiveCmdThread.mockReset().mockImplementation(async (id) => { stores.threadStore.state.activeCmdThreadId = id || ''; });

  apiMock.callAPI.mockReset().mockImplementation(async (method) => defaultAppAPI(method));
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
    location: { search: '' },
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
      return defaultAppAPI(method);
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
    expect(apiMock.onBridgeEvent).toHaveBeenCalled();
    expect(apiMock.onAppWillQuit).toHaveBeenCalled();
    expect(vm.buildInfo.version).toBe('1.0.0');
    expect(vm.currentCwdDisplay.value).toContain('/window');

    hooks.unmounted.splice(0).forEach((fn) => fn());
    expect(apiMock.bridgeOff).toHaveBeenCalled();
    expect(apiMock.quitOff).toHaveBeenCalled();
    expect(globalThis.clearInterval).toHaveBeenCalledWith(42);
  });

  it('clears stale bootstrap thread selection when the live session is gone', async () => {
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    stores.threadStore.state.activeThreadId = 'agent-stale';
    stores.threadStore.state.activeCmdThreadId = 'agent-stale';
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      return defaultAppAPI(method);
    });
    historyMock.ensureThreadSelectionFresh.mockRejectedValueOnce(new Error('session not found for agent "agent-stale"'));

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(vm.bootstrapError.value).toBe('');
    expect(stores.threadStore.saveActiveThread).toHaveBeenCalledWith('');
    expect(stores.threadStore.saveActiveCmdThread).toHaveBeenCalledWith('');
    expect(stores.threadStore.state.activeThreadId).toBe('');
    expect(stores.threadStore.state.activeCmdThreadId).toBe('');
    expect(consoleWarn).toHaveBeenCalledWith(
      'cleared stale thread selection after session loss',
      expect.objectContaining({ thread_id: 'agent-stale', reason: 'bootstrap' }),
    );
  });

  it('surfaces bootstrap failures in fatal state', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') throw new Error('config unavailable');
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(vm.bootstrapError.value).toBe('config unavailable');
    expect(stores.projectStore.reloadProjects).not.toHaveBeenCalled();
    expect(consoleError).toHaveBeenCalledWith('bootstrap failed:', expect.any(Error));
  });

  it('surfaces malformed window bootstrap responses in fatal state', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'ui/windowBootstrap/get') return {};
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(vm.bootstrapError.value).toBe('window bootstrap response snapshot is required');
    expect(consoleError).toHaveBeenCalledWith('bootstrap failed:', expect.any(Error));
  });

  it('uses the runtime cwd as thread scope when the active project is dot', async () => {
    stores.projectStore.state.active = '.';
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window-root' };
      return defaultAppAPI(method);
    });

    AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(stores.threadStore.setPreferenceScopeCwd).toHaveBeenCalledWith('/window-root');
  });

  it('prefers the Wails window cwd query over the process cwd from config/read', async () => {
    stores.projectStore.state.active = '.';
    globalThis.window.location.search = '?ao_window_cwd=%2Fworktrees%2Ffeature-a';
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/old-process-cwd' };
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(vm.windowCwd.value).toBe('/worktrees/feature-a');
    expect(vm.currentCwdDisplay.value).toBe('当前窗口 CWD：/worktrees/feature-a');
    expect(stores.threadStore.setPreferenceScopeCwd).toHaveBeenCalledWith('/worktrees/feature-a');
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

  it('runs command cards from dot project scope through the window cwd', async () => {
    stores.projectStore.state.active = '.';
    stores.threadStore.state.activeThreadId = '';
    globalThis.window.location.search = '?ao_window_cwd=%2Fworktrees%2Fcommand';
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/old-process-cwd' };
      return defaultAppAPI(method);
    });
    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();
    vm.page.value = 'commands';

    await vm.runCommandCard({ command_template: 'echo hello' });

    expect(stores.threadStore.startThread).toHaveBeenCalledWith('/worktrees/command');
  });

  it('refreshes dashboard pages and reacts to skills changed events while on skills page', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'ui/dashboard/get' && payload?.page === 'skills') {
        return dashboardPayload({ skills: [{ name: 'SkillA' }] });
      }
      return defaultAppAPI(method);
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

  it('refreshes the skills dashboard when the active project changes while viewing skills', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'ui/dashboard/get' && payload?.page === 'skills' && payload?.cwd === '/repo') {
        return dashboardPayload({ skills: [{ name: 'RepoSkill' }] });
      }
      if (method === 'ui/dashboard/get' && payload?.page === 'skills' && payload?.cwd === '/repo-next') {
        return dashboardPayload({ skills: [{ name: 'NextSkill' }] });
      }
      return {};
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    vm.page.value = 'skills';
    await flush();
    expect(vm.dashboard.skills).toEqual([{ name: 'RepoSkill' }]);

    stores.projectStore.state.active = '/repo-next';
    await flush();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/dashboard/get', { page: 'skills', cwd: '/repo-next' });
    expect(vm.dashboard.skills).toEqual([{ name: 'NextSkill' }]);
  });

  it('keeps final output refs from the memory dashboard response', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/dashboard/get' && payload?.page === 'memory') {
        return dashboardPayload({
          memory: [{ path: 'reports/final.pptx' }],
          finalOutputRefs: [{ path: 'reports/final.pptx', runKey: 'run-1' }],
        });
      }
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    await vm.refreshDashboardByPage('memory');

    expect(vm.dashboard.memory).toEqual([{ path: 'reports/final.pptx' }]);
    expect(vm.dashboard.finalOutputRefs).toEqual([{ path: 'reports/final.pptx', runKey: 'run-1' }]);
  });

  it('wires the DAG page to dashboard DAGs without exposing the legacy detail modal', () => {
    expect(AppRoot.template).toContain('<DagsPage');
    expect(AppRoot.template).toContain(':items="dashboard.dags"');
    expect(AppRoot.template).toContain(':loading="dashboardRequest.dags.loading"');
    expect(AppRoot.template).toContain(':error="dashboardRequest.dags.error"');
    expect(AppRoot.template).toContain('@open-chat="openDagChildThread"');
    expect(AppRoot.template).not.toContain('@select="dagDetail.open"');
    expect(AppRoot.template).not.toContain('<DagDetailModal');
  });

  it('opens a DAG child thread from the DAG page', async () => {
    const vm = AppRoot.setup();

    await vm.openDagChildThread('child-thread-1');

    expect(stores.threadStore.saveActiveThread).toHaveBeenCalledWith('child-thread-1');
    expect(vm.page.value).toBe('chat');
  });

  it('refreshes the DAG dashboard when entering the DAG page', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/dashboard/get' && payload?.page === 'dags') {
        return dashboardPayload({ dags: [{ dag_key: 'dag-1' }] });
      }
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    vm.page.value = 'dags';
    await flush();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/dashboard/get', { page: 'dags', cwd: '/repo' });
    expect(vm.dashboard.dags).toEqual([{ dag_key: 'dag-1' }]);
  });

  it('exposes DAG dashboard refresh failures instead of rendering them as empty lists', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/dashboard/get' && payload?.page === 'dags') {
        throw new Error('dag backend offline');
      }
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    vm.page.value = 'dags';
    await flush();

    expect(vm.dashboardRequest.dags.loading).toBe(false);
    expect(vm.dashboardRequest.dags.error).toContain('dag backend offline');
  });

  it('refreshes the DAG dashboard from node status bridge events while viewing DAGs', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/dashboard/get' && payload?.page === 'dags') {
        return dashboardPayload({ dags: [{ dag_key: 'dag-2', status: 'running' }] });
      }
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    vm.page.value = 'dags';
    await flush();
    apiMock.callAPI.mockClear();

    apiMock.bridgeCb?.({ method: 'task/node/statusChanged', payload: { dag_key: 'dag-2' } });
    await flush();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/dashboard/get', { page: 'dags', cwd: '/repo' });
    expect(vm.dashboard.dags).toEqual([{ dag_key: 'dag-2', status: 'running' }]);
  });

  it('rejects malformed dashboard responses instead of replacing missing fields with empty lists', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/dashboard/get' && payload?.page === 'memory') {
        return { memory: [] };
      }
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();

    await expect(vm.refreshDashboardByPage('memory')).rejects.toThrow('dashboard agents must be an array');
  });

  it('requires scoped cwd for project dashboard pages', async () => {
    stores.projectStore.state.active = '.';
    const vm = AppRoot.setup();

    await expect(vm.refreshDashboardByPage('memory')).rejects.toThrow('dashboard memory cwd is required');
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/dashboard/get', expect.anything());
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
      return defaultAppAPI(method);
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

  it('uses window cwd for bootstrap task starts when snapshot cwd is empty', async () => {
    globalThis.window.location.search = '?ao_window_cwd=%2Fworktrees%2Fbootstrap';
    stores.projectStore.state.active = '.';
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/old-process-cwd' };
      if (method === 'ui/windowBootstrap/get') {
        return {
          snapshot: {
            page: 'chat',
            taskStart: { focusMode: 'cmd', config: { taskId: 'task-window' } },
          },
        };
      }
      return defaultAppAPI(method);
    });

    AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(stores.threadStore.startThread).toHaveBeenCalledWith('/worktrees/bootstrap', {
      focusMode: 'cmd',
      config: { taskId: 'task-window' },
    });
  });

  it('marks the memory center nav when similar memories need merging', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'ui/memory/get') {
        return memoryCenterPayload({
          overview: {
            health: {
              similarGroups: [
                { nameA: 'A', nameB: 'B' },
                { nameA: 'C', nameB: 'D' },
              ],
            },
          },
        });
      }
      return defaultAppAPI(method);
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

  it('does not request legacy pending skill candidates or mark the skills nav badge', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    const legacyCandidateCalls = [];
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'skills/candidate/list/pending') {
        legacyCandidateCalls.push(method);
        return { candidates: [{ id: 'candidate-1' }] };
      }
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(legacyCandidateCalls).toEqual([]);
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('skills/candidate/list/pending', expect.anything());
    expect(vm.sidebarBadges.value.skills).toBeUndefined();
  });

  it('keeps the memory center nav badge without restoring the legacy skills badge', async () => {
    apiMock.getBuildInfo.mockResolvedValueOnce({ version: '1.0.0' });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'config/read') return { cwd: '/window' };
      if (method === 'skills/candidate/list/pending') {
        return { candidates: [{ id: 'candidate-1' }, { id: 'candidate-2' }] };
      }
      if (method === 'ui/memory/get') {
        return memoryCenterPayload({
          overview: { health: { similarGroups: [{ nameA: 'A', nameB: 'B' }] } },
        });
      }
      return defaultAppAPI(method);
    });

    const vm = AppRoot.setup();
    hooks.mounted.forEach((fn) => fn());
    await flush();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('skills/candidate/list/pending', expect.anything());
    expect(vm.sidebarBadges.value.skills).toBeUndefined();
    expect(vm.sidebarBadges.value['memory-center']).toBe(1);
  });
});
