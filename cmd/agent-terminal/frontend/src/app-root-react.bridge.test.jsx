import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  getBuildInfo: vi.fn(),
  bridgeCb: null,
  quitCb: null,
  bridgeOff: vi.fn(),
  quitOff: vi.fn(),
}));

const stores = vi.hoisted(() => ({
  projectStore: {
    state: { active: '/repo' },
    setScopeCwd: vi.fn(),
    reloadProjects: vi.fn(async () => {}),
    addProject: vi.fn(async () => {}),
    setActive: vi.fn(async (cwd) => {
      stores.projectStore.state.active = cwd;
    }),
  },
  threadStore: {
    state: { activeThreadId: '', activeCmdThreadId: '' },
    setPreferenceScopeCwd: vi.fn(),
    refreshSidebarState: vi.fn(async () => {}),
    handleBridgeEvent: vi.fn(),
    startThread: vi.fn(async () => 'thread-new'),
    sendMessage: vi.fn(async () => {}),
    saveActiveThread: vi.fn(async () => {}),
  },
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  getBuildInfo: apiMock.getBuildInfo,
  onBridgeEvent: vi.fn((cb) => {
    apiMock.bridgeCb = cb;
    return apiMock.bridgeOff;
  }),
  onAppWillQuit: vi.fn((cb) => {
    apiMock.quitCb = cb;
    return apiMock.quitOff;
  }),
}));

vi.mock('./stores/projects.js', () => ({ useProjectStore: () => stores.projectStore }));
vi.mock('./stores/threads.js', () => ({ useThreadStore: () => stores.threadStore }));
vi.mock('./utils/thread-page-utils.js', () => ({
  requestHistoryLoad: vi.fn(async () => {}),
  ensureThreadSelectionFresh: vi.fn(async () => {}),
  isStaleThreadSelectionError: () => false,
}));

vi.mock('./components/SidebarNav.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return {
    SidebarNav: ({ items, onChange }) => ReactActual.createElement(
      'nav',
      { 'data-testid': 'sidebar-nav' },
      items.map((item) => ReactActual.createElement(
        'button',
        {
          key: item.key,
          type: 'button',
          'data-testid': `nav-${item.key}`,
          onClick: () => onChange(item.key),
        },
        item.label
      ))
    ),
  };
});
vi.mock('./components/ProjectModal.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { ProjectModal: () => ReactActual.createElement('div', { 'data-testid': 'project-modal' }) };
});
vi.mock('./pages/UnifiedChatPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { UnifiedChatPage: () => ReactActual.createElement('section', { 'data-testid': 'chat-page' }) };
});
vi.mock('./pages/SystemPromptPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { SystemPromptPage: () => ReactActual.createElement('section', { 'data-testid': 'prompts-page' }) };
});
vi.mock('./pages/DagsPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { DagsPage: () => ReactActual.createElement('section', { 'data-testid': 'dags-page' }) };
});
vi.mock('./pages/TasksPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { TasksPage: () => ReactActual.createElement('section', { 'data-testid': 'tasks-page' }) };
});
vi.mock('./pages/SkillsPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { SkillsPage: () => ReactActual.createElement('section', { 'data-testid': 'skills-page' }) };
});
vi.mock('./pages/CommandsPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { CommandsPage: () => ReactActual.createElement('section', { 'data-testid': 'commands-page' }) };
});
vi.mock('./pages/MemoryCenterPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { MemoryCenterPage: () => ReactActual.createElement('section', { 'data-testid': 'memory-center-page' }) };
});
vi.mock('./pages/SharedFilesPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { SharedFilesPage: () => ReactActual.createElement('section', { 'data-testid': 'shared-files-page' }) };
});
vi.mock('./pages/SettingsPage.jsx', async () => {
  const ReactActual = await vi.importActual('react');
  return { SettingsPage: () => ReactActual.createElement('section', { 'data-testid': 'settings-page' }) };
});

import { AppRoot } from './App.jsx';

function dashboardPayload() {
  return {
    agents: [],
    dags: [],
    taskAcks: [],
    taskTraces: [],
    skills: [{ name: 'demo' }],
    commandCards: [],
    memory: [],
    finalOutputRefs: [],
    sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
  };
}

beforeEach(() => {
  stores.projectStore.state = { active: '/repo' };
  stores.projectStore.setScopeCwd.mockClear();
  stores.projectStore.reloadProjects.mockClear().mockResolvedValue(undefined);
  stores.projectStore.addProject.mockClear().mockResolvedValue(undefined);
  stores.projectStore.setActive.mockClear().mockImplementation(async (cwd) => {
    stores.projectStore.state.active = cwd;
  });
  stores.threadStore.state = { activeThreadId: '', activeCmdThreadId: '' };
  stores.threadStore.setPreferenceScopeCwd.mockClear();
  stores.threadStore.refreshSidebarState.mockClear().mockResolvedValue(undefined);
  stores.threadStore.handleBridgeEvent.mockClear();
  stores.threadStore.startThread.mockClear().mockResolvedValue('thread-new');
  stores.threadStore.sendMessage.mockClear().mockResolvedValue(undefined);
  stores.threadStore.saveActiveThread.mockClear().mockResolvedValue(undefined);

  apiMock.bridgeCb = null;
  apiMock.quitCb = null;
  apiMock.bridgeOff.mockClear();
  apiMock.quitOff.mockClear();
  apiMock.getBuildInfo.mockReset().mockResolvedValue({ version: 'test' });
  apiMock.callAPI.mockReset().mockImplementation(async (method) => {
    if (method === 'config/read') return { cwd: '/repo' };
    if (method === 'ui/windowBootstrap/get') return { snapshot: null };
    if (method === 'ui/dashboard/get') return dashboardPayload();
    if (method === 'ui/memory/get') return { overview: {}, private: { entries: [] }, team: { entries: [] } };
    return {};
  });
});

afterEach(() => {
  cleanup();
});

describe('AppRoot React bridge subscription', () => {
  it('renders the exit overlay without requesting a missing image asset', () => {
    render(<AppRoot />);

    const overlay = document.querySelector('.app-exit-overlay');
    expect(overlay).toBeTruthy();
    expect(overlay.querySelector('img')).toBeNull();
    expect(overlay.querySelector('.app-exit-overlay-icon')).toBeTruthy();
  });

  it('refreshes the currently visible skills dashboard after page changes', async () => {
    render(<AppRoot />);

    await waitFor(() => expect(apiMock.bridgeCb).toBeTypeOf('function'));

    fireEvent.click(screen.getByTestId('nav-skills'));
    await waitFor(() => expect(screen.getByTestId('skills-page')).toBeTruthy());
    await waitFor(() => {
      expect(apiMock.callAPI).toHaveBeenCalledWith('ui/dashboard/get', { page: 'skills', cwd: '/repo' });
    });

    apiMock.callAPI.mockClear();
    apiMock.bridgeCb({ method: 'skills/changed', payload: {} });

    await waitFor(() => {
      expect(apiMock.callAPI).toHaveBeenCalledWith('ui/dashboard/get', { page: 'skills', cwd: '/repo' });
    });
  });
});
