import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SettingsPage } from './SettingsPage.jsx';

const backend = vi.hoisted(() => ({
  callBackend: vi.fn(),
  checkAppUpdate: vi.fn(),
  copyTextToClipboard: vi.fn(),
  downloadAppUpdate: vi.fn(),
  getBuildInfo: vi.fn(),
  getPreference: vi.fn(),
  getVideoApiKey: vi.fn(),
  installAppUpdate: vi.fn(),
  installLatestAppUpdate: vi.fn(),
  listDashboardLogs: vi.fn(),
  readBuiltinTools: vi.fn(),
  readConfig: vi.fn(),
  readLspPromptHint: vi.fn(),
  setPreference: vi.fn(),
  setVideoApiKey: vi.fn(),
  writeBuiltinTool: vi.fn(),
  writeLspPromptHint: vi.fn(),
}));

const clientStore = vi.hoisted(() => ({
  value: {
    activeProject: '/repo/app',
    cwd: '/repo/app',
    logEntries: [],
    logLevel: 'info',
    setLogLevel: vi.fn(),
  },
}));

vi.mock('../../shared/api/backendApi.js', () => backend);

vi.mock('../../entities/client/model/useClientStore.js', () => ({
  useClientStore: () => clientStore.value,
}));

function preferenceFixture(overrides = {}) {
  return {
    'settings.provider.active': 'codex',
    'settings.provider.codex.codexHome': '/Users/test/.codex',
    'settings.provider.codex.codexInstanceKey': 'desktop-main',
    'settings.provider.codex.codexModelProvider': 'openai',
    'settings.provider.codex.model': null,
    'settings.provider.codex.effort': null,
    'settings.provider.codex.personality': 'pragmatic',
    'settings.provider.codex.sandbox': { type: 'readOnly' },
    'settings.provider.codex.summary': 'detailed',
    'settings.provider.codex.approvalPolicy': 'on-request',
    stallThresholdSec: 60,
    'contextUsageAlerts.thresholds': [70, 85, 95],
    ...overrides,
  };
}

function renderSettingsPage(projectPath = '/repo/app') {
  render(<SettingsPage projectPath={projectPath} />);
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  vi.clearAllMocks();
  clientStore.value = {
    activeProject: '/repo/app',
    cwd: '/repo/app',
    logEntries: [],
    logLevel: 'info',
    setLogLevel: vi.fn(),
  };
  const preferences = preferenceFixture();
  backend.getBuildInfo.mockResolvedValue({
    version: 'v1.2.3',
    runtime: 'darwin/arm64',
    buildTime: '2026-06-03T08:00:00Z',
    commit: 'abc123',
  });
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));
  backend.callBackend.mockResolvedValue({});
  backend.getVideoApiKey.mockResolvedValue({ configured: false, masked: '' });
  backend.setVideoApiKey.mockResolvedValue({ ok: true });
  backend.checkAppUpdate.mockResolvedValue({ available: false });
  backend.downloadAppUpdate.mockResolvedValue({ ok: true });
  backend.installAppUpdate.mockResolvedValue({ ok: true });
  backend.installLatestAppUpdate.mockResolvedValue({ ok: true });
  backend.setPreference.mockResolvedValue({ ok: true });
  backend.readConfig.mockResolvedValue({ cwd: '/repo/app' });
  backend.readLspPromptHint.mockResolvedValue({
    hint: 'effective prompt',
    defaultHint: 'default prompt',
    overrideHint: '',
    usingDefault: true,
  });
  backend.writeLspPromptHint.mockResolvedValue({
    hint: 'saved prompt',
    defaultHint: 'default prompt',
    overrideHint: 'saved prompt',
    usingDefault: false,
  });
  backend.copyTextToClipboard.mockResolvedValue(true);
  backend.readBuiltinTools.mockResolvedValue({ tools: [] });
  backend.writeBuiltinTool.mockImplementation(({ id, enabled }) => Promise.resolve({
    tools: [{ id, label: '读文件', description: '读取文件', enabled, provider: 'claude', filterMode: 'hard', enforcement: enabled ? '' : 'native-hard' }],
  }));
  backend.listDashboardLogs.mockResolvedValue({ logs: [] });
});

afterEach(() => {
  cleanup();
});

describe('SettingsPage module', () => {
  it('exports the settings page component', () => {
    expect(SettingsPage).toBeTypeOf('function');
  });
});

describe('SettingsPage app update entry', () => {
  it('renders the app update area as a prominent about card', async () => {
    renderSettingsPage();

    const updateCard = await screen.findByTestId('settings-update-card');
    expect(updateCard).toHaveTextContent('应用更新');
    expect(updateCard).toHaveTextContent('当前版本 v1.2.3');
    expect(within(updateCard).getByTestId('settings-update-check-button')).toHaveTextContent('检查更新');
  });

  it('shows an available update and installs it', async () => {
    backend.checkAppUpdate.mockResolvedValueOnce({
      enabled: true,
      available: true,
      version: 'v1.2.4',
      artifact: { platform: 'darwin-arm64' },
    });

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId('settings-update-check-button'));

    await waitFor(() => {
      expect(backend.checkAppUpdate).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('发现新版本 v1.2.4 (darwin-arm64)');
    });

    fireEvent.click(screen.getByTestId('settings-update-install-button'));

    await waitFor(() => {
      expect(backend.installLatestAppUpdate).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('正在安装更新 v1.2.4 (darwin-arm64)');
    });
    expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
  });

  it('hides the install action while install is pending', async () => {
    const pendingInstall = deferred();
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: true, available: true, version: 'v1.2.4', artifact: { platform: 'darwin-arm64' } });
    backend.installLatestAppUpdate.mockReturnValueOnce(pendingInstall.promise);

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId('settings-update-check-button'));
    await screen.findByTestId('settings-update-install-button');
    fireEvent.click(screen.getByTestId('settings-update-install-button'));

    await waitFor(() => {
      expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
      expect(screen.getByTestId('settings-update-check-button')).toBeDisabled();
      expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('正在安装更新 v1.2.4 (darwin-arm64)');
    });

    await act(async () => {
      pendingInstall.resolve({ ok: true });
      await pendingInstall.promise;
    });
    expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
    expect(screen.getByTestId('settings-update-check-button')).toBeDisabled();
  });

  it('clears stale update details when no update is available', async () => {
    backend.checkAppUpdate
      .mockResolvedValueOnce({ enabled: true, available: true, version: 'v1.2.4' })
      .mockResolvedValueOnce({ enabled: true, available: false });

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId('settings-update-check-button'));
    await screen.findByTestId('settings-update-install-button');

    fireEvent.click(screen.getByTestId('settings-update-check-button'));

    await waitFor(() => {
      expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('已是最新版本');
      expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
    });
  });

  it('shows a disabled update notice instead of saying the dev build is current', async () => {
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: false, available: false });

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId('settings-update-check-button'));

    await waitFor(() => {
      expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('当前构建未启用应用更新');
      expect(screen.getByTestId('settings-update-notice')).toHaveAttribute('role', 'status');
    });
    expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
  });

  it('clears stale update details when checking fails', async () => {
    backend.checkAppUpdate
      .mockResolvedValueOnce({ enabled: true, available: true, version: 'v1.2.4' })
      .mockRejectedValueOnce(new Error('manifest unavailable'));

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId('settings-update-check-button'));
    await screen.findByTestId('settings-update-install-button');

    fireEvent.click(screen.getByTestId('settings-update-check-button'));

    await waitFor(() => {
      expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('检查更新失败：manifest unavailable');
      expect(screen.getByTestId('settings-update-notice')).toHaveAttribute('role', 'alert');
      expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
    });
  });

  it('shows backend manifest and signature errors as actionable check failures', async () => {
    backend.checkAppUpdate.mockRejectedValueOnce(new Error('GitHub release missing update manifest asset Super-Dolphin-darwin-arm64.update.json'));

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId('settings-update-check-button'));

    await waitFor(() => {
      expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('检查更新失败：GitHub release missing update manifest asset Super-Dolphin-darwin-arm64.update.json');
      expect(screen.getByTestId('settings-update-notice')).toHaveAttribute('role', 'alert');
    });
    expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
  });

  it('shows install failures and allows retry', async () => {
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: true, available: true, version: 'v1.2.4' });
    backend.installLatestAppUpdate
      .mockRejectedValueOnce(new Error('permission denied'))
      .mockResolvedValueOnce({ ok: true });

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId('settings-update-check-button'));
    fireEvent.click(await screen.findByTestId('settings-update-install-button'));

    await waitFor(() => {
      expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('安装更新失败：permission denied');
      expect(screen.getByTestId('settings-update-install-button')).toBeEnabled();
    });

    fireEvent.click(screen.getByTestId('settings-update-install-button'));

    await waitFor(() => {
      expect(backend.installLatestAppUpdate).toHaveBeenCalledTimes(2);
      expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
    });
  });
});

describe('SettingsPage provider migration', () => {
  it('loads legacy JSON sandbox preferences from the internal preference RPC', async () => {
    const preferences = preferenceFixture({
      'settings.provider.codex.sandbox': JSON.stringify({
        type: 'workspaceWrite',
        writableRoots: ['/repo/app', '/Users/test/shared'],
        networkAccess: true,
      }),
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    renderSettingsPage();

    expect(await screen.findByLabelText('Writable Roots')).toHaveValue('/repo/app\n/Users/test/shared');
    expect(screen.getByLabelText('Network Access')).toBeChecked();
  });

  it('accepts Windows absolute writable roots when saving provider settings', async () => {
    const preferences = preferenceFixture({
      'settings.provider.codex.sandbox': {
        type: 'workspaceWrite',
        writableRoots: ['C:\\Users\\alice\\project', '\\\\server\\share\\repo'],
        networkAccess: false,
      },
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    renderSettingsPage();

    expect(await screen.findByLabelText('Writable Roots')).toHaveValue('C:\\Users\\alice\\project\n\\\\server\\share\\repo');
    backend.setPreference.mockClear();

    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.sandbox',
        value: {
          type: 'workspaceWrite',
          writableRoots: ['C:\\Users\\alice\\project', '\\\\server\\share\\repo'],
          networkAccess: false,
        },
      });
    });
  });

  it('fails fast when the backend returns an invalid active provider preference', async () => {
    const preferences = preferenceFixture({
      'settings.provider.active': 'bad-provider',
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    renderSettingsPage();

    expect(await screen.findByRole('alert')).toHaveTextContent('invalid provider preference: bad-provider');
    expect(screen.getByRole('combobox', { name: 'Active Provider' })).toHaveValue('codex');
  });

  it.skip('persists active provider changes immediately before saving provider details', async () => {
    const preferences = preferenceFixture({
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
      'settings.provider.claude.personality': 'friendly',
      'settings.provider.claude.sandbox': { type: 'readOnly' },
      'settings.provider.claude.summary': 'auto',
      'settings.provider.claude.approvalPolicy': 'on-failure',
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    renderSettingsPage();

    const activeProvider = await screen.findByRole('combobox', { name: 'Active Provider' });
    fireEvent.change(activeProvider, { target: { value: 'claude' } });

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.active',
        value: 'claude',
      });
      expect(activeProvider).toHaveValue('claude');
    });
  });

  it('loads provider summary and approval through scoped/global fallback with tombstones', async () => {
    const scopedPreferences = preferenceFixture({
      'settings.provider.codex.summary': null,
      'settings.provider.codex.approvalPolicy': { cleared: true },
    });
    const globalPreferences = preferenceFixture({
      'settings.provider.codex.summary': 'concise',
      'settings.provider.codex.approvalPolicy': 'never',
    });
    backend.getPreference.mockImplementation(({ cwd, key }) => Promise.resolve((cwd ? scopedPreferences : globalPreferences)[key] ?? null));

    renderSettingsPage();

    expect(await screen.findByTestId('provider-summary-mode-select')).toHaveValue('concise');
    expect(screen.getByTestId('provider-approval-mode-select')).toHaveValue('on-request');
  });

  it.skip('ignores stale provider properties loads after switching active provider', async () => {
    const staleCodexSummary = deferred();
    const preferences = preferenceFixture({
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
      'settings.provider.claude.personality': 'friendly',
      'settings.provider.claude.sandbox': { type: 'readOnly' },
      'settings.provider.claude.summary': 'auto',
      'settings.provider.claude.approvalPolicy': 'on-failure',
      'settings.provider.codex.approvalPolicy': 'on-request',
    });
    backend.getPreference.mockImplementation(({ key }) => {
      if (key === 'settings.provider.codex.summary') return staleCodexSummary.promise;
      return Promise.resolve(preferences[key] ?? null);
    });

    renderSettingsPage();

    const activeProvider = await screen.findByRole('combobox', { name: 'Active Provider' });
    fireEvent.change(activeProvider, { target: { value: 'claude' } });

    await waitFor(() => {
      expect(activeProvider).toHaveValue('claude');
      expect(screen.getByTestId('provider-summary-mode-select')).toHaveValue('auto');
      expect(screen.getByTestId('provider-approval-mode-select')).toHaveValue('on-failure');
    });

    await act(async () => {
      staleCodexSummary.resolve('concise');
      await staleCodexSummary.promise;
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.getByTestId('provider-summary-mode-select')).toHaveValue('auto');
    expect(screen.getByTestId('provider-approval-mode-select')).toHaveValue('on-failure');
  });

  it.skip('ignores stale provider preference loads after a newer active provider selection wins', async () => {
    const staleActiveProvider = deferred();
    backend.getPreference.mockImplementation(({ key }) => {
      if (key === 'settings.provider.active') return staleActiveProvider.promise;
      return Promise.resolve({
        'settings.provider.claude.model': 'sonnet',
        'settings.provider.claude.effort': 'high',
        'settings.provider.claude.personality': 'friendly',
        'settings.provider.claude.sandbox': { type: 'readOnly' },
        stallThresholdSec: 60,
        'contextUsageAlerts.thresholds': [70, 85, 95],
      }[key] ?? null);
    });

    renderSettingsPage();

    const activeProvider = await screen.findByRole('combobox', { name: 'Active Provider' });
    fireEvent.change(activeProvider, { target: { value: 'claude' } });

    await waitFor(() => {
      expect(activeProvider).toHaveValue('claude');
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.active',
        value: 'claude',
      });
    });

    await act(async () => {
      staleActiveProvider.resolve('codex');
      await staleActiveProvider.promise;
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(activeProvider).toHaveValue('claude');
  });

  it('hides the internal Codex model provider and clears Codex identity fields with tombstones', async () => {
    renderSettingsPage();

    const codexHome = await screen.findByLabelText('Codex Home');
    const instanceKey = screen.getByLabelText('Instance Key');
    await waitFor(() => {
      expect(codexHome).toHaveValue('/Users/test/.codex');
      expect(instanceKey).toHaveValue('desktop-main');
    });
    expect(screen.queryByLabelText('Model Provider')).not.toBeInTheDocument();

    fireEvent.change(codexHome, { target: { value: '' } });
    fireEvent.change(instanceKey, { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexHome', value: { cleared: true } });
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexInstanceKey', value: { cleared: true } });
    });
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.codex.codexModelProvider',
    }));
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.codex.model',
    }));
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.codex.effort',
    }));
  });
});

describe('SettingsPage builtin tools migration', () => {
  it('loads grouped builtin tools and toggles through config/builtinTools write facade', async () => {
    backend.readBuiltinTools.mockResolvedValueOnce({
      tools: [
        { id: 'Read', label: '读文件', description: '读取文件', enabled: false, provider: 'claude', filterMode: 'hard', enforcement: 'native-hard' },
        { id: 'WebFetch', label: '抓取网页', description: '网页', enabled: true, provider: 'claude', filterMode: 'hard' },
      ],
    });

    renderSettingsPage();

    await waitFor(() => {
      expect(screen.getByTestId('settings-builtin-tools-summary')).toHaveTextContent('已管控 1 / 2');
    });
    fireEvent.click(screen.getByTestId('settings-builtin-tool-group-head-native-hard'));
    const readToggle = screen.getByTestId('settings-builtin-tool-input-Read');
    expect(readToggle).toBeChecked();

    fireEvent.click(readToggle);

    await waitFor(() => {
      expect(backend.writeBuiltinTool).toHaveBeenCalledWith({ cwd: '/repo/app', id: 'Read', enabled: true });
    });
  });
});

describe('SettingsPage video settings', () => {
  it('reports API key load failures instead of silently ignoring them', async () => {
    backend.getVideoApiKey.mockRejectedValueOnce(new Error('credential store unavailable'));

    renderSettingsPage();

    expect(await screen.findByTestId('settings-video-card')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId('settings-video-notice')).toHaveTextContent('读取视频 API Key 失败：credential store unavailable');
      expect(screen.getByTestId('settings-video-notice')).toHaveAttribute('role', 'alert');
    });
    expect(backend.callBackend).not.toHaveBeenCalled();
  });

  it('saves API keys through the named video facade method', async () => {
    renderSettingsPage();

    const card = await screen.findByTestId('settings-video-card');
    fireEvent.change(within(card).getByLabelText('API Key'), { target: { value: 'sk-test-video-key' } });
    fireEvent.click(within(card).getByRole('button', { name: '保存' }));

    await waitFor(() => {
      expect(backend.setVideoApiKey).toHaveBeenCalledWith({ apiKey: 'sk-test-video-key' });
    });
    expect(backend.callBackend).not.toHaveBeenCalled();
  });
});
