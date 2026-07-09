import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';

const backend = vi.hoisted(() => ({
  callBackend: vi.fn(),
  checkAppUpdate: vi.fn(),
  downloadAppUpdate: vi.fn(),
  installAppUpdate: vi.fn(),
  installLatestAppUpdate: vi.fn(),
  readConfig: vi.fn(),
  getBuildInfo: vi.fn(),
  getVideoApiKey: vi.fn(),
  getWindowBootstrap: vi.fn(),
  getProjects: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadState: vi.fn(),
  getThreadMessages: vi.fn(),
  getMemorySnapshot: vi.fn(),
  setPreference: vi.fn(),
  setVideoApiKey: vi.fn(),
  getPreference: vi.fn(),
  readLspPromptHint: vi.fn(),
  writeLspPromptHint: vi.fn(),
  readBuiltinTools: vi.fn(),
  writeBuiltinTool: vi.fn(),
  listDashboardLogs: vi.fn(),
  readSharedFile: vi.fn(),
  copyTextToClipboard: vi.fn(),
  saveClipboardImage: vi.fn(),
  onFilesDropped: vi.fn(() => () => {}),
  onBridgeEvent: vi.fn(() => () => {}),
}));

vi.mock('./shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
  emitFrontendTraceEvent: vi.fn(),
}));

const builtInTools = [
  { id: 'tool-1', label: 'File Search', description: 'Search workspace files', enabled: true, provider: 'claude', filterMode: 'hard' },
  { id: 'tool-2', label: 'Command Exec', description: 'Run terminal commands', enabled: false, provider: 'codex', filterMode: 'soft' },
];

const enabledBuiltInTools = [
  { id: 'tool-1', label: 'File Search', description: 'Search workspace files', enabled: true, provider: 'claude', filterMode: 'hard' },
  { id: 'tool-2', label: 'Command Exec', description: 'Run terminal commands', enabled: true, provider: 'codex', filterMode: 'soft' },
];

function resetSettingsStore() {
  resetClientStoreForTests({
    bootstrapStatus: 'ready',
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activePage: 'settings',
    logLevel: 'info',
    logEntries: [],
  });
}

function resetSettingsStoreWithoutProject() {
  resetClientStoreForTests({
    bootstrapStatus: 'ready',
    cwd: '',
    activeProject: '',
    activePage: 'settings',
    logLevel: 'info',
    logEntries: [],
  });
}

function mockSettingsBootstrap() {
  backend.getBuildInfo.mockResolvedValue({
    version: 'v1.2.3',
    runtime: 'linux/amd64',
    buildTime: '2026-05-30T07:00:00Z',
    commit: 'abc123def456',
  });
  backend.readConfig.mockResolvedValue({ cwd: '/repo/app' });
  backend.getWindowBootstrap.mockResolvedValue({ ok: true });
  backend.callBackend.mockResolvedValue({});
  backend.getVideoApiKey.mockResolvedValue({ configured: false, masked: '' });
  backend.setVideoApiKey.mockResolvedValue({ ok: true });
  backend.checkAppUpdate.mockResolvedValue({ available: false });
  backend.downloadAppUpdate.mockResolvedValue({ ok: true });
  backend.installAppUpdate.mockResolvedValue({ ok: true });
  backend.installLatestAppUpdate.mockResolvedValue({ ok: true });
  backend.getProjects.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
  backend.getSidebarState.mockResolvedValue({ threads: [], activeThreadId: '' });
  backend.getMemorySnapshot.mockResolvedValue({
    overview: {
      enabled: true,
      autoDreamEnabled: false,
      autoDreamIntent: null,
      projectRoot: '/repo/app',
      health: { preferenceCount: 0, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
    },
    private: { entries: [] },
    team: { entries: [] },
  });
}

function mockSettingsPreferences() {
  backend.getPreference.mockImplementation(({ key }) => {
    if (key === 'settings.provider.codex.summary') return Promise.resolve('concise');
    if (key === 'settings.provider.codex.approvalPolicy') return Promise.resolve('untrusted');
    if (key === 'settings.showInjectedPromptInChat') return Promise.resolve(true);
    return Promise.resolve(null);
  });
}

function mockSettingsConfigApi() {
  backend.readLspPromptHint.mockResolvedValue({
    hint: 'effective prompt text',
    defaultHint: 'default prompt text',
    overrideHint: 'custom override text',
    usingDefault: false,
  });
  backend.readBuiltinTools.mockResolvedValue({ tools: builtInTools });
  backend.writeLspPromptHint.mockResolvedValue({
    hint: 'custom override text',
    defaultHint: 'default prompt text',
    overrideHint: 'custom override text',
    usingDefault: false,
  });
  backend.writeBuiltinTool.mockResolvedValue({ tools: enabledBuiltInTools });
  backend.listDashboardLogs.mockResolvedValue({ logs: [] });
  backend.copyTextToClipboard.mockResolvedValue(true);
}

function resetSettingsPageTestState() {
  vi.clearAllMocks();
  resetSettingsStore();
  mockSettingsBootstrap();
  mockSettingsPreferences();
  mockSettingsConfigApi();
}

async function renderSettingsPage() {
  render(<App skipBootstrap />);
  await screen.findByTestId('settings-page');
}

async function expectPromptSettingsLoaded() {
  await waitFor(() => {
    expect(screen.getByRole('textbox', { name: '当前生效内容（只读）' })).toHaveValue('effective prompt text');
    expect(screen.getByRole('textbox', { name: '自定义覆盖（可编辑，空=默认）' })).toHaveValue('custom override text');
    expect(screen.getByTestId('settings-show-injected-toggle-input')).toBeChecked();
  });
}

async function expectPromptVisibilitySaved(value) {
  await waitFor(() => {
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'settings.showInjectedPromptInChat',
      value,
    });
  });
}

async function copyEffectivePromptHint() {
  backend.copyTextToClipboard.mockResolvedValueOnce(true);
  fireEvent.click(screen.getByTestId('settings-lsp-copy-button'));
  await waitFor(() => {
    expect(backend.copyTextToClipboard).toHaveBeenCalledWith('effective prompt text');
    expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveTextContent('已复制生效提示词');
    expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveAttribute('role', 'status');
  });
}

function mockPromptHintWriteSuccess() {
  backend.writeLspPromptHint.mockImplementation((params) => {
    expect(params).toEqual({ cwd: '/repo/app', hint: 'custom override text' });
    return Promise.resolve({
      hint: 'custom override text',
      defaultHint: 'default prompt text',
      overrideHint: 'custom override text',
      usingDefault: false,
    });
  });
}

async function savePromptHintAndExpectSuccess() {
  fireEvent.click(screen.getByTestId('settings-lsp-save-button'));
  await waitFor(() => {
    expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveTextContent('提示词已保存');
    expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveAttribute('role', 'status');
  });
}

function mockBuiltinToolWriteSuccess() {
  backend.writeBuiltinTool.mockImplementation((params) => {
    expect(params).toEqual({ cwd: '/repo/app', id: 'tool-2', enabled: true });
    return Promise.resolve({ tools: enabledBuiltInTools });
  });
}

async function expectBuiltinToolsLoaded() {
  await waitFor(() => {
    expect(screen.getByTestId('settings-builtin-tools-summary')).toHaveTextContent('已管控 1 / 2');
  });
}

function openSoftAuditBuiltinToolGroup() {
  const softAuditHeader = screen.getByTestId('settings-builtin-tool-group-head-soft-audit');
  expect(softAuditHeader).toBeInTheDocument();
  expect(softAuditHeader).toHaveAttribute('aria-expanded', 'false');
  fireEvent.click(softAuditHeader);
  expect(softAuditHeader).toHaveAttribute('aria-expanded', 'true');
}

async function toggleBuiltinToolAndExpectEnabled() {
  const checkbox = screen.getByTestId('settings-builtin-tool-input-tool-2');
  expect(checkbox).toBeChecked();
  mockBuiltinToolWriteSuccess();
  fireEvent.click(checkbox);
  await waitFor(() => {
    expect(checkbox).not.toBeChecked();
    expect(screen.getByTestId('settings-builtin-tools-notice')).toHaveTextContent('Command Exec 已启用');
    expect(screen.getByTestId('settings-builtin-tools-notice')).toHaveAttribute('role', 'status');
  });
}

describe('SettingsPage provider settings', () => {
  beforeEach(resetSettingsPageTestState);

  it('renders preference settings cards and triggers save/refresh', async () => {
    render(<App skipBootstrap />);

    // Verify preference select values
    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: '推理摘要 (Summary)' })).toHaveValue('concise');
      expect(screen.getByRole('combobox', { name: '审批策略 (ApprovalPolicy)' })).toHaveValue('untrusted');
    });

    // Change value and save
    fireEvent.change(screen.getByTestId('provider-summary-mode-select'), { target: { value: 'auto' } });
    fireEvent.change(screen.getByTestId('provider-approval-mode-select'), { target: { value: 'never' } });
    fireEvent.click(screen.getByTestId('provider-sandbox-save-button'));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.summary',
        value: 'auto',
      });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.approvalPolicy',
        value: 'never',
      });
      expect(screen.getByText('已保存：auto / never')).toHaveAttribute('role', 'status');
    });
  }, 10_000);

  it('surfaces unsupported Claude active provider preferences instead of loading them', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'claude',
      'settings.provider.claude.summary': 'auto',
      'settings.provider.claude.approvalPolicy': 'on-failure',
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
      'settings.provider.claude.sandbox': { type: 'workspaceWrite', writableRoots: ['/repo/app'], networkAccess: false },
    }[key] ?? null));

    await renderSettingsPage();

    expect(await screen.findByRole('alert')).toHaveTextContent('settings.provider.active: unsupported provider preference "claude"; current desktop UI supports codex only');
    expect(screen.getByRole('combobox', { name: 'Active Provider' })).toHaveValue('codex');
    expect(backend.setPreference).not.toHaveBeenCalled();
  });

  it('loads provider model effort personality and saves read-only restricted roots', async () => {
    const preferences = {
      stallThresholdSec: 60,
      'contextUsageAlerts.thresholds': [65, 80, 95],
      'settings.provider.active': 'codex',
      'settings.provider.codex.codexHome': '/home/test/.codex',
      'settings.provider.codex.codexInstanceKey': 'main',
      'settings.provider.codex.codexModelProvider': 'openai',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.personality': 'pragmatic',
      'settings.provider.codex.sandbox': {
        type: 'readOnly',
        access: {
          type: 'restricted',
          readableRoots: ['/repo/app', '/Users/ai/shared'],
          includePlatformDefaults: true,
        },
      },
    };
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    await renderSettingsPage();

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: 'Provider Model' })).toHaveValue('gpt-5.5');
      expect(screen.getByRole('combobox', { name: 'Provider Effort' })).toHaveValue('xhigh');
      expect(screen.getByRole('combobox', { name: 'Personality' })).toHaveValue('pragmatic');
      expect(screen.getByRole('combobox', { name: 'Read Only Mode' })).toHaveValue('restricted');
      expect(screen.getByLabelText('Readable Roots')).toHaveValue('/repo/app\n/Users/ai/shared');
    });

    fireEvent.change(screen.getByLabelText('Readable Roots'), { target: { value: '/repo/app\n/Users/ai/docs' } });
    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.personality',
        value: 'pragmatic',
      });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.sandbox',
        value: {
          type: 'readOnly',
          access: {
            type: 'restricted',
            readableRoots: ['/repo/app', '/Users/ai/docs'],
            includePlatformDefaults: true,
          },
        },
      });
    });
  });

  it('blocks provider save when writable roots contain relative paths', async () => {
    const preferences = {
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.sandbox': {
        type: 'workspaceWrite',
        writableRoots: ['/repo/app'],
        networkAccess: false,
      },
    };
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    await renderSettingsPage();

    const writableRoots = screen.getByLabelText('Writable Roots');
    await waitFor(() => expect(writableRoots).toHaveValue('/repo/app'));

    fireEvent.change(writableRoots, { target: { value: '/repo/app\nrelative/path' } });
    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('路径必须是绝对路径：relative/path');
    });
    expect(backend.setPreference).not.toHaveBeenCalled();
  });

  it('keeps default provider model and effort inherited until the user changes them', async () => {
    const preferences = {
      'settings.provider.active': 'codex',
      'settings.provider.codex.sandbox': {
        type: 'workspaceWrite',
        writableRoots: ['/repo/app'],
        networkAccess: false,
      },
      'settings.provider.codex.personality': 'pragmatic',
      'settings.provider.codex.codexHome': '/home/test/.codex',
      'settings.provider.codex.codexInstanceKey': 'main',
      'settings.provider.codex.codexModelProvider': 'openai',
    };
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    await renderSettingsPage();

    await waitFor(() => {
      expect(screen.getByLabelText('Writable Roots')).toHaveValue('/repo/app');
    });

    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.sandbox',
        value: {
          type: 'workspaceWrite',
          writableRoots: ['/repo/app'],
          networkAccess: false,
        },
      });
    });
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.codex.model',
    }));
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.codex.effort',
    }));

    fireEvent.change(screen.getByRole('combobox', { name: 'Provider Model' }), { target: { value: 'gpt-5.5' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Provider Effort' }), { target: { value: 'high' } });
    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.model',
        value: 'gpt-5.5',
      });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.effort',
        value: 'high',
      });
    });
  });

  it('blocks workspaceWrite provider save when writable roots are empty', async () => {
    const preferences = {
      'settings.provider.active': 'codex',
      'settings.provider.codex.sandbox': {
        type: 'workspaceWrite',
        writableRoots: [],
        networkAccess: false,
      },
    };
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferences[key] ?? null));

    await renderSettingsPage();

    await waitFor(() => {
      expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.sandbox' });
    });

    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('请至少填写一个绝对路径');
    });
    expect(backend.setPreference).not.toHaveBeenCalled();
  });

  it('falls back from scoped provider preferences to global Codex values', async () => {
    backend.getPreference.mockImplementation(({ cwd, key }) => {
      const scoped = {
        'settings.provider.active': null,
        'settings.provider.codex.model': null,
        'settings.provider.codex.effort': null,
        'settings.provider.codex.sandbox': null,
      };
      const global = {
        'settings.provider.active': 'codex',
        'settings.provider.codex.model': 'gpt-5.5',
        'settings.provider.codex.effort': 'xhigh',
        'settings.provider.codex.personality': 'friendly',
        'settings.provider.codex.sandbox': { type: 'workspaceWrite', writableRoots: ['/repo/app'], networkAccess: false },
      };
      return Promise.resolve(cwd ? scoped[key] ?? null : global[key] ?? null);
    });

    await renderSettingsPage();

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: 'Active Provider' })).toHaveValue('codex');
    });
    expect(screen.getByRole('combobox', { name: 'Provider Model' })).toHaveValue('gpt-5.5');
    expect(screen.getByRole('combobox', { name: 'Provider Effort' })).toHaveValue('xhigh');
    expect(screen.getByRole('combobox', { name: 'Personality' })).toHaveValue('friendly');
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.model' });
    expect(backend.getPreference).toHaveBeenCalledWith({ key: 'settings.provider.codex.model' });

    fireEvent.change(screen.getByRole('combobox', { name: 'Provider Model' }), { target: { value: 'gpt-5' } });

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: 'Provider Model' })).toHaveValue('gpt-5');
    });

    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));
    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.model',
        value: 'gpt-5',
      });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.effort',
        value: 'xhigh',
      });
    });
  });

  it('treats scoped provider tombstones as cleared instead of falling back to globals', async () => {
    backend.getPreference.mockImplementation(({ cwd, key }) => {
      const scoped = {
        'settings.provider.active': { cleared: true },
      };
      const global = {
        'settings.provider.active': 'claude',
      };
      return Promise.resolve(cwd ? scoped[key] ?? null : global[key] ?? null);
    });

    await renderSettingsPage();

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: 'Active Provider' })).toHaveValue('codex');
    });
    expect(screen.getByRole('combobox', { name: 'Provider Model' })).toHaveValue('gpt-5.5');
    expect(backend.getPreference).not.toHaveBeenCalledWith({ key: 'settings.provider.active' });
  });
});

describe('SettingsPage project scope guard', () => {
  beforeEach(resetSettingsPageTestState);

  it('blocks runtime and provider writes when no project cwd is available', async () => {
    resetSettingsStoreWithoutProject();

    render(<App skipBootstrap />);
    await screen.findByTestId('settings-page');

    fireEvent.change(screen.getByLabelText('统一超时阈值'), { target: { value: '120' } });
    fireEvent.change(screen.getByLabelText('Warn 阈值'), { target: { value: '70' } });
    fireEvent.change(screen.getByLabelText('Danger 阈值'), { target: { value: '85' } });
    fireEvent.change(screen.getByLabelText('Critical 阈值'), { target: { value: '96' } });

    fireEvent.click(screen.getByTestId('settings-stall-threshold-save-button'));
    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('当前项目路径为空');
    });

    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));
    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('当前项目路径为空');
    });

    expect(backend.setPreference).not.toHaveBeenCalled();
  });
});

describe('SettingsPage prompt settings', () => {
  beforeEach(resetSettingsPageTestState);

  it('loads prompt settings and executes prompt actions', async () => {
    await renderSettingsPage();
    await expectPromptSettingsLoaded();
    expect(backend.readLspPromptHint).toHaveBeenCalledWith({ cwd: '/repo/app' });

    fireEvent.click(screen.getByTestId('settings-show-injected-toggle-input'));
    await expectPromptVisibilitySaved(false);

    await copyEffectivePromptHint();
    mockPromptHintWriteSuccess();
    await savePromptHintAndExpectSuccess();
  });

  it('marks prompt save failures as alert notices', async () => {
    render(<App skipBootstrap />);

    await waitFor(() => {
      expect(screen.getByTestId('settings-lsp-prompt-input')).toHaveValue('custom override text');
    });

    backend.writeLspPromptHint.mockRejectedValueOnce(new Error('disk full'));

    fireEvent.click(screen.getByTestId('settings-lsp-save-button'));
    await waitFor(() => {
      expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveTextContent('保存失败');
      expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveAttribute('role', 'alert');
    });
  });

  it('does not save prompt settings when the write response omits the default hint', async () => {
    render(<App skipBootstrap />);

    await waitFor(() => {
      expect(screen.getByTestId('settings-lsp-prompt-input')).toHaveValue('custom override text');
    });

    backend.writeLspPromptHint.mockResolvedValueOnce({
      hint: 'custom override text',
      overrideHint: 'custom override text',
      usingDefault: false,
    });

    fireEvent.click(screen.getByTestId('settings-lsp-save-button'));

    await waitFor(() => {
      expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveTextContent('保存失败');
      expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveAttribute('role', 'alert');
    });
    expect(screen.getByTestId('settings-lsp-prompt-notice')).not.toHaveTextContent('已保存自定义提示词');
  });
});

describe('SettingsPage built-in tool settings', () => {
  beforeEach(resetSettingsPageTestState);

  it('handles model built-in capabilities accordion and tool toggles', async () => {
    await renderSettingsPage();
    await expectBuiltinToolsLoaded();
    expect(backend.readBuiltinTools).toHaveBeenCalledWith({ cwd: '/repo/app' });
    openSoftAuditBuiltinToolGroup();

    expect(screen.getByText('Command Exec')).toBeInTheDocument();
    expect(screen.getByText(/Run terminal commands/)).toBeInTheDocument();
    await toggleBuiltinToolAndExpectEnabled();
  });
});

describe('SettingsPage log settings', () => {
  beforeEach(resetSettingsPageTestState);

  it('renders log entries from store and handles log level changes', async () => {
    render(<App skipBootstrap />);

    // Mock direct push of log event
    act(() => {
      useClientStore.getState().addLog('debug', 'thread.sidebar.refreshed', { detail: 'test' });
    });

    // Verify log entry is displayed
    await waitFor(() => {
      expect(screen.getByText('thread.sidebar.refreshed')).toBeInTheDocument();
    });

    // Change log level
    const select = screen.getByRole('combobox', { name: '日志级别' });
    fireEvent.change(select, { target: { value: 'error' } });

    await waitFor(() => {
      expect(useClientStore.getState().logLevel).toBe('error');
    });
  });

  it('refreshes UI logs from the dashboard logs backend', async () => {
    await renderSettingsPage();

    backend.listDashboardLogs.mockResolvedValueOnce({
      logs: [{
        id: 42,
        timestamp: '2026-05-30T07:00:00Z',
        level: 'warn',
        component: 'ui',
        event_type: 'settings.refreshed',
        message: 'settings refresh completed',
      }],
    });

    fireEvent.click(screen.getByTestId('settings-log-refresh-button'));

    await waitFor(() => {
      expect(backend.listDashboardLogs).toHaveBeenCalledWith({ limit: 14 });
      expect(screen.getByText('ui.settings.refreshed')).toBeInTheDocument();
    });
  });
});
