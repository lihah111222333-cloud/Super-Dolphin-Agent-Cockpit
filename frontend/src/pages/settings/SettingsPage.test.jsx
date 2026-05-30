// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import SettingsPage from './SettingsPage.jsx';
import { useProjectStore } from '../../entities/project/model/useProjectStore';

const backend = vi.hoisted(() => ({
  getBuildInfo: vi.fn(),
  getPreference: vi.fn(),
  setPreference: vi.fn(),
}));

vi.mock('../../shared/api/backendApi', () => ({
  getBuildInfo: (...args) => backend.getBuildInfo(...args),
  getPreference: (...args) => backend.getPreference(...args),
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
  setPreference: (...args) => backend.setPreference(...args),
}));

describe('web SettingsPage backend integration', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useProjectStore.setState({
      projects: ['/repo/app'],
      active: '/repo/app',
      scopeCwd: '/repo/app',
      showModal: false,
      modalPath: '',
      browsing: false,
    });
    backend.getBuildInfo.mockResolvedValue({
      version: 'v2.0.0',
      runtime: 'linux/amd64',
      buildTime: '2026-05-30T09:00:00Z',
      commit: '123456abcdef',
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      stallThresholdSec: 60,
      'contextUsageAlerts.thresholds': [66, 81, 96],
      'settings.provider.active': 'codex',
      'settings.provider.codex.codexHome': '/home/test/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.sandbox': { type: 'workspaceWrite', writableRoots: ['/repo/app'], networkAccess: false },
    }[key] ?? null));
    backend.setPreference.mockResolvedValue({ ok: true });
  });

  afterEach(() => {
    cleanup();
  });

  it('loads build metadata and saves runtime/provider settings with explicit cwd', async () => {
    render(<SettingsPage />);

    expect(await screen.findByText('Agent Orchestrator v2.0.0')).toBeInTheDocument();
    expect(screen.getByText('linux/amd64')).toBeInTheDocument();
    expect(screen.getByText('2026-05-30T09:00:00Z')).toBeInTheDocument();
    expect(screen.getByText('123456abcdef')).toBeInTheDocument();
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'contextUsageAlerts.thresholds' });

    fireEvent.change(screen.getByLabelText('统一超时阈值'), { target: { value: '180' } });
    fireEvent.change(screen.getByLabelText('Warn 阈值'), { target: { value: '70' } });
    fireEvent.change(screen.getByLabelText('Danger 阈值'), { target: { value: '85' } });
    fireEvent.change(screen.getByLabelText('Critical 阈值'), { target: { value: '95' } });
    fireEvent.click(screen.getByRole('button', { name: '保存运行阈值' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'stallThresholdSec', value: 180 });
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'contextUsageAlerts.thresholds', value: [70, 85, 95] });
    });

    fireEvent.change(screen.getByLabelText('Active Provider'), { target: { value: 'claude' } });
    fireEvent.change(screen.getByLabelText('Sandbox Policy'), { target: { value: 'dangerFullAccess' } });
    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.active', value: 'claude' });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.claude.sandbox',
        value: { type: 'dangerFullAccess' },
      });
    });

    backend.getBuildInfo.mockResolvedValueOnce({
      version: 'v2.0.1',
      runtime: 'linux/amd64',
      buildTime: '2026-05-30T09:30:00Z',
      commit: 'abcdef123456',
    });
    fireEvent.click(screen.getByRole('button', { name: '刷新构建信息' }));

    expect(await screen.findByText('Agent Orchestrator v2.0.1')).toBeInTheDocument();
    expect(screen.getByText('abcdef123456')).toBeInTheDocument();
  });
});
