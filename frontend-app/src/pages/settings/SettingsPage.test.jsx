import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SettingsPage } from './SettingsPage.jsx';

const backend = vi.hoisted(() => ({
  applyModelProvider: vi.fn(),
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
  listModelProviders: vi.fn(),
  readBuiltinTools: vi.fn(),
  readConfig: vi.fn(),
  readLspPromptHint: vi.fn(),
  setPreference: vi.fn(),
  setVideoApiKey: vi.fn(),
  saveModelProviders: vi.fn(),
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

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function settingsPageView(queryClient, projectPath) {
  return (
    <QueryClientProvider client={queryClient}>
      <SettingsPage projectPath={projectPath} />
    </QueryClientProvider>
  );
}

function renderSettingsPage(projectPath = '/repo/app') {
  const queryClient = createTestQueryClient();
  const result = render(settingsPageView(queryClient, projectPath));
  return {
    queryClient,
    ...result,
    rerenderSettingsPage: (nextProjectPath) => result.rerender(settingsPageView(queryClient, nextProjectPath)),
  };
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
  backend.listModelProviders.mockResolvedValue({
    activeVendorId: '',
    vendors: [
      { id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1', configured: true, maskedEnv: '********', envStatus: 'configured', budget: { dailyUsd: 5, monthlyUsd: 100 }, tokenPool: { priority: 10, fallbackVendorId: 'deepseek' } },
      { id: 'deepseek', label: 'DeepSeek', enabled: false, baseURL: 'https://api.deepseek.com/v1', envKey: 'DEEPSEEK_API_KEY', codexModelProvider: 'deepseek', defaultModel: 'deepseek-chat', configured: false, maskedEnv: '', envStatus: 'missing', budget: {}, tokenPool: { priority: 20, fallbackVendorId: 'qwen' } },
      { id: 'qwen', label: 'Qwen', enabled: false, baseURL: 'https://dashscope.aliyuncs.com/compatible-mode/v1', envKey: 'QWEN_API_KEY', codexModelProvider: 'qwen', defaultModel: 'qwen-plus', configured: false, maskedEnv: '', envStatus: 'missing', budget: {}, tokenPool: { priority: 30 } },
    ],
  });
  backend.saveModelProviders.mockResolvedValue({ ok: true });
  backend.applyModelProvider.mockResolvedValue({
    activeVendorId: 'openrouter',
    vendors: [
      { id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1', configured: true, maskedEnv: '********', envStatus: 'configured', budget: { dailyUsd: 5, monthlyUsd: 100 }, tokenPool: { priority: 10, fallbackVendorId: 'deepseek' } },
    ],
  });
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

  it('renders mobile account cards without enabling unsupported logout', async () => {
    renderSettingsPage('/repo/app');

    const panel = screen.getByTestId('settings-mobile-account');
    await screen.findByTestId('settings-update-card');
    expect(panel).toHaveTextContent('燧元');
    expect(panel).toHaveTextContent('app');
    expect(panel).toHaveTextContent('/repo/app');
    expect(panel).toHaveTextContent('Codex');
    expect(panel).toHaveTextContent('待鉴权接入');
    expect(within(panel).getByRole('button', { name: '菜单' })).toBeDisabled();
    expect(within(panel).getByTestId('settings-mobile-logout-button')).toBeDisabled();
    expect(within(panel).getByRole('button', { name: '账号' })).toBeDisabled();
    expect(within(panel).getByRole('button', { name: '设置' })).toBeDisabled();
    const logOutButtons = within(panel).getAllByRole('button', { name: '退出登录' });
    expect(logOutButtons).toHaveLength(2);
    logOutButtons.forEach((button) => expect(button).toBeDisabled());
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

  it('prefers the packaged app version in the app update card', async () => {
    backend.getBuildInfo.mockResolvedValueOnce({
      version: 'v0.0.0-20260608130413-c9d1688c7e99+dirty',
      appVersion: '1.0.2',
      runtime: 'darwin/arm64',
      buildTime: '2026-06-08T13:04:13Z',
      commit: 'c9d1688c7e99',
    });

    renderSettingsPage();

    const updateCard = await screen.findByTestId('settings-update-card');
    expect(updateCard).toHaveTextContent('当前版本 v1.0.2');
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

    const writableRoots = await screen.findByLabelText('Writable Roots');
    await waitFor(() => expect(writableRoots).toHaveValue('/repo/app\n/Users/test/shared'));
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

    const writableRoots = await screen.findByLabelText('Writable Roots');
    await waitFor(() => expect(writableRoots).toHaveValue('C:\\Users\\alice\\project\n\\\\server\\share\\repo'));
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
    expect(screen.getByText(/新建线程时生效/)).toBeInTheDocument();
  });

  it('fails fast when the backend returns an invalid active provider preference', async () => {
    const preferences = preferenceFixture({
      'settings.provider.active': 'bad-provider',
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    renderSettingsPage();

    expect(await screen.findByRole('alert')).toHaveTextContent('invalid UI preference response for settings.provider.active');
    expect(screen.getByRole('combobox', { name: 'Active Provider' })).toHaveValue('codex');
  });

  it.each([
    ['stallThresholdSec', 29, '统一超时阈值', 30],
    ['contextUsageAlerts.thresholds', [70, 70, 95], 'Warn 阈值', 70],
    ['settings.provider.codex.effort', 'max', 'Provider Effort', 'xhigh'],
    ['settings.provider.codex.sandbox', { type: 'workspaceWrite', writableRoots: 'bad', networkAccess: 'yes' }, 'Sandbox Policy', 'workspaceWrite'],
  ])('rejects malformed %s without applying a fallback value', async (key, value, controlName, defaultValue) => {
    const preferences = preferenceFixture({ [key]: value });
    backend.getPreference.mockImplementation(({ key: requestedKey }) => Promise.resolve(preferences[requestedKey] ?? null));

    renderSettingsPage();

    expect(await screen.findByRole('alert')).toHaveTextContent(`invalid UI preference response for ${key}`);
    expect(screen.getByLabelText(controlName)).toHaveValue(defaultValue);
  });

  it('rejects a string boolean preference without toggling prompt visibility', async () => {
    const preferences = preferenceFixture({ 'settings.showInjectedPromptInChat': 'true' });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    renderSettingsPage();

    expect(await screen.findByText(/invalid UI preference response for settings.showInjectedPromptInChat/)).toBeInTheDocument();
    expect(screen.getByTestId('settings-show-injected-toggle-input')).not.toBeChecked();
  });

  it('keeps active provider selection codex-only', async () => {
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
      expect(activeProvider).toHaveValue('codex');
    });
    expect(screen.queryByRole('option', { name: 'Claude' })).not.toBeInTheDocument();
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.active',
      value: 'claude',
    }));
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

    const summaryMode = await screen.findByTestId('provider-summary-mode-select');
    await waitFor(() => expect(summaryMode).toHaveValue('concise'));
    expect(screen.getByTestId('provider-approval-mode-select')).toHaveValue('on-request');
  });

  it('does not reload runtime preferences on window focus when a runtime form is dirty', async () => {
    const reads = [];
    const preferences = preferenceFixture();
    backend.getPreference.mockImplementation(({ key }) => {
      reads.push(key);
      return Promise.resolve(preferences[key] ?? null);
    });

    renderSettingsPage('/repo/app');

    const threshold = await screen.findByLabelText('统一超时阈值');
    await waitFor(() => expect(threshold).toHaveValue(60));
    fireEvent.change(threshold, { target: { value: '45' } });
    reads.length = 0;

    await act(async () => {
      window.dispatchEvent(new Event('focus'));
      await Promise.resolve();
    });

    await waitFor(() => expect(threshold).toHaveValue(45));
    expect(reads).toEqual([]);
  });

  it('does not reload provider properties on window focus when the provider form is dirty', async () => {
    const reads = [];
    const preferences = preferenceFixture();
    backend.getPreference.mockImplementation(({ key }) => {
      reads.push(key);
      return Promise.resolve(preferences[key] ?? null);
    });

    renderSettingsPage('/repo/app');

    const summaryMode = await screen.findByTestId('provider-summary-mode-select');
    await waitFor(() => expect(summaryMode).toHaveValue('detailed'));
    fireEvent.change(summaryMode, { target: { value: 'concise' } });
    reads.length = 0;

    await act(async () => {
      window.dispatchEvent(new Event('focus'));
      await Promise.resolve();
    });

    await waitFor(() => expect(summaryMode).toHaveValue('concise'));
    expect(reads).toEqual([]);
  });

  it('keeps dirty provider properties when provider preferences refetch in the background', async () => {
    const preferences = preferenceFixture();
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    const { queryClient } = renderSettingsPage('/repo/app');
    const summaryMode = await screen.findByTestId('provider-summary-mode-select');
    await waitFor(() => expect(summaryMode).toHaveValue('detailed'));
    fireEvent.change(summaryMode, { target: { value: 'concise' } });

    preferences['settings.provider.codex.summary'] = 'auto';
    backend.getPreference.mockClear();

    await act(async () => {
      await queryClient.invalidateQueries({
        predicate: (query) => query.queryKey[0] === 'settings' && query.queryKey[1] === 'provider-preferences',
      });
    });

    await waitFor(() => {
      expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.summary' });
    });
    expect(summaryMode).toHaveValue('concise');
  });

  it('keeps provider properties scoped to codex after an unsupported provider change', async () => {
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
      expect(activeProvider).toHaveValue('codex');
      expect(screen.getByTestId('provider-approval-mode-select')).toHaveValue('on-request');
    });

    await act(async () => {
      staleCodexSummary.resolve('concise');
      await staleCodexSummary.promise;
      await Promise.resolve();
      await Promise.resolve();
	});
	expect(activeProvider).toHaveValue('codex');
	await waitFor(() => expect(screen.getByTestId('provider-summary-mode-select')).toHaveValue('concise'));
	expect(screen.getByTestId('provider-approval-mode-select')).toHaveValue('on-request');
});

  it('surfaces unsupported active provider preferences without switching provider', async () => {
    const preferences = preferenceFixture({
      'settings.provider.active': 'claude',
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    renderSettingsPage();

    const activeProvider = await screen.findByRole('combobox', { name: 'Active Provider' });
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid UI preference response for settings.provider.active');
    expect(activeProvider).toHaveValue('codex');
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.active',
      value: 'claude',
    }));
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

describe('SettingsPage model provider management', () => {
  it('renders model vendors with redacted API key status', async () => {
    renderSettingsPage();

    const card = await screen.findByTestId('settings-model-providers-card');
    expect(card).toHaveTextContent('Model Providers');
    expect(card).toHaveTextContent('OpenRouter');
    expect(card).toHaveTextContent('OPENROUTER_API_KEY');
    expect(card).toHaveTextContent('configured');
    expect(card).not.toHaveTextContent('sk-openrouter-secret');
  });

  it('saves the edited vendor registry through the facade', async () => {
    renderSettingsPage();
    const card = await screen.findByTestId('settings-model-providers-card');
    fireEvent.change(within(card).getByLabelText('Default Model'), { target: { value: 'openai/gpt-4.1-mini' } });
    fireEvent.change(within(card).getByLabelText('Daily Budget USD'), { target: { value: '' } });
    fireEvent.change(within(card).getByLabelText('Token Priority'), { target: { value: '12' } });
    fireEvent.click(within(card).getByRole('button', { name: '保存厂商配置' }));

    await waitFor(() => {
      expect(backend.saveModelProviders).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        registry: expect.objectContaining({
          vendors: expect.arrayContaining([expect.objectContaining({ id: 'openrouter', defaultModel: 'openai/gpt-4.1-mini' })]),
        }),
      }));
    });
    const payload = backend.saveModelProviders.mock.calls.at(-1)[0];
    const openrouter = payload.registry.vendors.find((vendor) => vendor.id === 'openrouter');
    expect(openrouter.budget.dailyUsd).toBe(0);
    expect(openrouter.budget.monthlyUsd).toBe(100);
    expect(openrouter.tokenPool.priority).toBe(12);
    expect(openrouter).not.toHaveProperty('configured');
    expect(openrouter).not.toHaveProperty('maskedEnv');
    expect(openrouter).not.toHaveProperty('envStatus');
  });

  it('ignores stale model provider loads after cwd changes', async () => {
    const firstLoad = deferred();
    const secondLoad = deferred();
    const firstRegistry = {
      activeVendorId: 'one-vendor',
      vendors: [
        { id: 'one-vendor', label: 'ProjectOneAI', enabled: true, baseURL: 'https://one.example/v1', envKey: 'ONE_API_KEY', codexModelProvider: 'one', defaultModel: 'one-model', configured: true, maskedEnv: '********', envStatus: 'configured', budget: { dailyUsd: 1, monthlyUsd: 10 }, tokenPool: { priority: 1 } },
      ],
    };
    const secondRegistry = {
      activeVendorId: 'two-vendor',
      vendors: [
        { id: 'two-vendor', label: 'ProjectTwoAI', enabled: true, baseURL: 'https://two.example/v1', envKey: 'TWO_API_KEY', codexModelProvider: 'two', defaultModel: 'two-model', configured: true, maskedEnv: '********', envStatus: 'configured', budget: { dailyUsd: 2, monthlyUsd: 20 }, tokenPool: { priority: 2 } },
      ],
    };
    backend.listModelProviders
      .mockReturnValueOnce(firstLoad.promise)
      .mockReturnValueOnce(secondLoad.promise);

    const { rerenderSettingsPage } = renderSettingsPage('/repo/one');
    await waitFor(() => {
      expect(backend.listModelProviders).toHaveBeenCalledWith({ cwd: '/repo/one' });
    });

    rerenderSettingsPage('/repo/two');
    await waitFor(() => {
      expect(backend.listModelProviders).toHaveBeenCalledWith({ cwd: '/repo/two' });
    });

    await act(async () => {
      secondLoad.resolve(secondRegistry);
      await secondLoad.promise;
    });
    const card = await screen.findByTestId('settings-model-providers-card');
    expect(card).toHaveTextContent('ProjectTwoAI');
    expect(card).not.toHaveTextContent('ProjectOneAI');

    await act(async () => {
      firstLoad.resolve(firstRegistry);
      await firstLoad.promise;
    });
    expect(card).toHaveTextContent('ProjectTwoAI');
    expect(card).not.toHaveTextContent('ProjectOneAI');

    const saveButton = card.querySelectorAll('.settings-provider-actions button')[1];
    expect(saveButton).toBeTruthy();
    fireEvent.click(saveButton);

    await waitFor(() => {
      expect(backend.saveModelProviders).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/two',
        registry: expect.objectContaining({
          vendors: expect.arrayContaining([expect.objectContaining({ id: 'two-vendor', defaultModel: 'two-model' })]),
        }),
      }));
    });
    const payload = backend.saveModelProviders.mock.calls.at(-1)[0];
    expect(payload.registry.vendors).toHaveLength(1);
    expect(payload.registry.vendors[0].id).toBe('two-vendor');
  });

  it('keeps dirty model provider drafts when cached registry data refetches in the background', async () => {
    backend.listModelProviders
      .mockResolvedValueOnce({
        activeVendorId: 'openrouter',
        vendors: [
          { id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1', configured: true, maskedEnv: '********', envStatus: 'configured', budget: { dailyUsd: 5, monthlyUsd: 100 }, tokenPool: { priority: 10, fallbackVendorId: '' } },
        ],
      })
      .mockResolvedValueOnce({
        activeVendorId: 'openrouter',
        vendors: [
          { id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1-refetched', configured: true, maskedEnv: '********', envStatus: 'configured', budget: { dailyUsd: 5, monthlyUsd: 100 }, tokenPool: { priority: 10, fallbackVendorId: '' } },
        ],
      });

    const { queryClient } = renderSettingsPage();
    const card = await screen.findByTestId('settings-model-providers-card');
    fireEvent.change(within(card).getByLabelText('Default Model'), { target: { value: 'openai/gpt-4.1-draft' } });

    await act(async () => {
      await queryClient.invalidateQueries({
        predicate: (query) => query.queryKey[0] === 'settings' && query.queryKey[1] === 'modelProviders',
      });
    });

    await waitFor(() => {
      expect(backend.listModelProviders).toHaveBeenCalledTimes(2);
    });
    expect(within(card).getByLabelText('Default Model')).toHaveValue('openai/gpt-4.1-draft');
    expect(card).not.toHaveTextContent('openai/gpt-4.1-refetched');
  });

  it('shows missing env status without API key input fields', async () => {
    renderSettingsPage();
    const card = await screen.findByTestId('settings-model-providers-card');
    const deepseekRow = within(card).getByRole('button', { name: /DeepSeek/ });
    expect(deepseekRow).toHaveTextContent('disabled');
    expect(deepseekRow).toHaveTextContent('missing');
    fireEvent.click(deepseekRow);
    expect(within(card).getAllByText('missing').length).toBeGreaterThan(0);
    expect(within(card).queryByLabelText('API Key')).not.toBeInTheDocument();
  });

  it('does not apply a disabled configured vendor', async () => {
    backend.listModelProviders.mockResolvedValueOnce({
      activeVendorId: '',
      vendors: [
        { id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1', configured: true, maskedEnv: '********', envStatus: 'configured', budget: { dailyUsd: 5, monthlyUsd: 100 }, tokenPool: { priority: 10, fallbackVendorId: 'deepseek' } },
        { id: 'deepseek', label: 'DeepSeek', enabled: false, baseURL: 'https://api.deepseek.com/v1', envKey: 'DEEPSEEK_API_KEY', codexModelProvider: 'deepseek', defaultModel: 'deepseek-chat', configured: true, maskedEnv: '********', envStatus: 'configured', budget: {}, tokenPool: { priority: 20, fallbackVendorId: 'qwen' } },
      ],
    });
    renderSettingsPage();
    const card = await screen.findByTestId('settings-model-providers-card');
    const deepseekRow = within(card).getByRole('button', { name: /DeepSeek/ });
    expect(deepseekRow).toHaveTextContent('disabled');
    expect(deepseekRow).toHaveTextContent('configured');
    fireEvent.click(deepseekRow);

    const applyButton = within(card).getByRole('button', { name: '应用厂商' });
    expect(applyButton).toBeDisabled();
    fireEvent.click(applyButton);
    expect(backend.applyModelProvider).not.toHaveBeenCalled();
  });

  it('applies a configured vendor and refreshes active state', async () => {
    renderSettingsPage();
    const card = await screen.findByTestId('settings-model-providers-card');
    fireEvent.click(within(card).getByRole('button', { name: '应用厂商' }));
    await waitFor(() => {
      expect(backend.applyModelProvider).toHaveBeenCalledWith({ cwd: '/repo/app', vendorId: 'openrouter' });
      expect(card).toHaveTextContent('已应用 OpenRouter');
    });
  });

  it('saves the current provider draft before applying a configured vendor', async () => {
    renderSettingsPage();
    const card = await screen.findByTestId('settings-model-providers-card');
    fireEvent.change(within(card).getByLabelText('Codex Home'), { target: { value: '/repo/app/.codex-openrouter' } });
    fireEvent.change(within(card).getByLabelText('Codex Model Provider'), { target: { value: 'openrouter-project' } });

    fireEvent.click(within(card).getByRole('button', { name: '应用厂商' }));

    await waitFor(() => {
      expect(backend.saveModelProviders).toHaveBeenCalledWith(expect.objectContaining({ cwd: '/repo/app' }));
      expect(backend.applyModelProvider).toHaveBeenCalledWith({ cwd: '/repo/app', vendorId: 'openrouter' });
    });
    const savePayload = backend.saveModelProviders.mock.calls[0][0];
    expect(savePayload.registry.vendors).toEqual(expect.arrayContaining([
      expect.objectContaining({
        id: 'openrouter',
        codexHome: '/repo/app/.codex-openrouter',
        codexModelProvider: 'openrouter-project',
      }),
    ]));
    expect(backend.saveModelProviders.mock.invocationCallOrder[0]).toBeLessThan(backend.applyModelProvider.mock.invocationCallOrder[0]);
  });
});

describe('SettingsPage prompt settings', () => {
  it('ignores stale prompt settings loads after cwd changes', async () => {
    const firstLoad = deferred();
    const secondLoad = deferred();
    backend.readLspPromptHint
      .mockReturnValueOnce(firstLoad.promise)
      .mockReturnValueOnce(secondLoad.promise);

    const { rerenderSettingsPage } = renderSettingsPage('/repo/one');
    await waitFor(() => {
      expect(backend.readLspPromptHint).toHaveBeenCalledWith({ cwd: '/repo/one' });
    });

    rerenderSettingsPage('/repo/two');
    await waitFor(() => {
      expect(backend.readLspPromptHint).toHaveBeenCalledWith({ cwd: '/repo/two' });
    });

    await act(async () => {
      secondLoad.resolve({
        hint: 'project two effective prompt',
        defaultHint: 'project two default prompt',
        overrideHint: 'project two override prompt',
        usingDefault: false,
      });
    });
    await waitFor(() => {
      expect(screen.getByTestId('settings-lsp-prompt-input')).toHaveValue('project two override prompt');
      expect(screen.getByTestId('settings-lsp-effective-output')).toHaveValue('project two effective prompt');
    });

    await act(async () => {
      firstLoad.resolve({
        hint: 'project one effective prompt',
        defaultHint: 'project one default prompt',
        overrideHint: 'project one override prompt',
        usingDefault: false,
      });
    });

    expect(screen.getByTestId('settings-lsp-prompt-input')).toHaveValue('project two override prompt');
    expect(screen.getByTestId('settings-lsp-effective-output')).toHaveValue('project two effective prompt');
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

  it('ignores stale builtin tool loads after cwd changes', async () => {
    const firstLoad = deferred();
    const secondLoad = deferred();
    backend.readBuiltinTools
      .mockReturnValueOnce(firstLoad.promise)
      .mockReturnValueOnce(secondLoad.promise);

    const { rerenderSettingsPage } = renderSettingsPage('/repo/one');
    await waitFor(() => {
      expect(backend.readBuiltinTools).toHaveBeenCalledWith({ cwd: '/repo/one' });
    });

    rerenderSettingsPage('/repo/two');
    await waitFor(() => {
      expect(backend.readBuiltinTools).toHaveBeenCalledWith({ cwd: '/repo/two' });
    });

    await act(async () => {
      secondLoad.resolve({ tools: [{ id: 'TwoRead', label: 'Project Two Read', description: 'two', enabled: false, provider: 'claude', filterMode: 'hard', enforcement: 'native-hard' }] });
    });
    await waitFor(() => {
      expect(screen.getByTestId('settings-builtin-tools-summary')).toHaveTextContent('已管控 1 / 1');
    });
    await act(async () => {
      firstLoad.resolve({
        tools: [
          { id: 'OneRead', label: 'Project One Read', description: 'one', enabled: false, provider: 'claude', filterMode: 'hard', enforcement: 'native-hard' },
          { id: 'OneWrite', label: 'Project One Write', description: 'one write', enabled: false, provider: 'claude', filterMode: 'hard', enforcement: 'native-hard' },
        ],
      });
    });

    await waitFor(() => {
      expect(screen.getByTestId('settings-builtin-tools-summary')).toHaveTextContent('已管控 1 / 1');
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

  it('shows save failures from the named video facade method', async () => {
    backend.setVideoApiKey.mockRejectedValueOnce(new Error('credential store unavailable'));

    renderSettingsPage();

    const card = await screen.findByTestId('settings-video-card');
    fireEvent.change(within(card).getByLabelText('API Key'), { target: { value: 'sk-test-video-key' } });
    fireEvent.click(within(card).getByRole('button', { name: '保存' }));

    await waitFor(() => {
      expect(screen.getByTestId('settings-video-notice')).toHaveTextContent('保存失败：credential store unavailable');
      expect(screen.getByTestId('settings-video-notice')).toHaveAttribute('role', 'alert');
    });
    expect(backend.setVideoApiKey).toHaveBeenCalledWith({ apiKey: 'sk-test-video-key' });
    expect(backend.callBackend).not.toHaveBeenCalled();
  });
});
