// @vitest-environment jsdom
import React from 'react';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import AppShell from './AppShell';
import { useLogStore } from '../../entities/log/model/useLogStore';
import { usePreferenceStore } from '../../entities/preference/model/usePreferenceStore';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { useThreadStore } from '../../entities/thread/model/useThreadStore';

const mockBackend = vi.hoisted(() => ({
  getPreference: vi.fn(),
  getProjects: vi.fn(),
  getSidebarState: vi.fn(),
  onBridgeEvent: vi.fn(() => () => {}),
  readConfig: vi.fn(),
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
  setPreference: vi.fn(),
}));

vi.mock('../../shared/api/backendApi', () => ({
  getPreference: (...args) => mockBackend.getPreference(...args),
  getProjects: (...args) => mockBackend.getProjects(...args),
  getSidebarState: (...args) => mockBackend.getSidebarState(...args),
  onBridgeEvent: (...args) => mockBackend.onBridgeEvent(...args),
  readConfig: (...args) => mockBackend.readConfig(...args),
  registerBridgeLogStore: (...args) => mockBackend.registerBridgeLogStore(...args),
  sendFrontendLogBatch: (...args) => mockBackend.sendFrontendLogBatch(...args),
  setPreference: (...args) => mockBackend.setPreference(...args),
}));

describe('AppShell backend bootstrap', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockBackend.getPreference.mockResolvedValue(null);
    mockBackend.getProjects.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    mockBackend.getSidebarState.mockResolvedValue({ threads: [], activeThreadId: '' });
    mockBackend.readConfig.mockResolvedValue({ cwd: '/repo/app' });

    useLogStore.setState({ entries: [], bridgeQueue: [] });
    usePreferenceStore.getState().destroy();
    usePreferenceStore.setState({ theme: 'dark' });
    useProjectStore.setState({
      projects: [],
      active: '.',
      scopeCwd: '',
      showModal: false,
      modalPath: '',
      browsing: false,
    });
    useThreadStore.getState().destroy();
    useThreadStore.setState({
      threads: [],
      statuses: {},
      timelinesByThread: {},
      tokenUsageByThread: {},
      diffTextByThread: {},
      activeThreadId: '',
      activeCmdThreadId: '',
    });
  });

  afterEach(() => {
    cleanup();
    usePreferenceStore.getState().destroy();
    useThreadStore.getState().destroy();
  });

  it('loads cwd from config/read before project and sidebar RPCs', async () => {
    render(
      <AppShell activePage="chat" setActivePage={vi.fn()}>
        <div data-testid="child">ready</div>
      </AppShell>,
    );

    expect(screen.getByTestId('child')).toBeTruthy();

    await waitFor(() => {
      expect(mockBackend.readConfig).toHaveBeenCalledWith();
      expect(mockBackend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
      expect(mockBackend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
    });

    expect(useProjectStore.getState().scopeCwd).toBe('/repo/app');
    expect(useProjectStore.getState().active).toBe('/repo/app');
  });

  it('does not render the removed task and command navigation entries', () => {
    render(
      <AppShell activePage="chat" setActivePage={vi.fn()}>
        <div data-testid="child">ready</div>
      </AppShell>,
    );

    expect(screen.queryByText('任务')).toBeNull();
    expect(screen.queryByText('命令')).toBeNull();
  });
});
