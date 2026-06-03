import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';

const backend = vi.hoisted(() => ({
  readConfig: vi.fn(),
  getWindowBootstrap: vi.fn(),
  getProjects: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadState: vi.fn(),
  getThreadMessages: vi.fn(),
  getMemorySnapshot: vi.fn(),
  setPreference: vi.fn(),
  getPreference: vi.fn(),
  callBackend: vi.fn(),
  onFilesDropped: vi.fn(() => () => {}),
  onBridgeEvent: vi.fn(() => () => {}),
}));

vi.mock('./shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
  emitFrontendTraceEvent: vi.fn(),
}));

// Mock navigator.clipboard
const mockClipboardWriteText = vi.fn();
Object.defineProperty(navigator, 'clipboard', {
  value: {
    writeText: mockClipboardWriteText,
  },
  writable: true,
});

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
  backend.readConfig.mockResolvedValue({ cwd: '/repo/app' });
  backend.getWindowBootstrap.mockResolvedValue({ ok: true });
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
  backend.callBackend.mockImplementation((method, _params) => {
    if (method === 'config/read') {
      return Promise.resolve({ cwd: '/repo/app' });
    }
    if (method === 'config/lspPromptHint/read') {
      return Promise.resolve({
        hint: 'effective prompt text',
        defaultHint: 'default prompt text',
        overrideHint: 'custom override text',
        usingDefault: false,
      });
    }
    if (method === 'config/builtinTools/read') {
      return Promise.resolve({ tools: builtInTools });
    }
    return Promise.resolve({});
  });
}

function resetSettingsPageTestState() {
  vi.clearAllMocks();
  mockClipboardWriteText.mockReset();
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
  mockClipboardWriteText.mockResolvedValueOnce();
  fireEvent.click(screen.getByTestId('settings-lsp-copy-button'));
  await waitFor(() => {
    expect(mockClipboardWriteText).toHaveBeenCalledWith('effective prompt text');
    expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveTextContent('已复制生效提示词');
    expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveAttribute('role', 'status');
  });
}

function mockPromptHintWriteSuccess() {
  backend.callBackend.mockImplementation((method, params) => {
    if (method === 'config/lspPromptHint/write') {
      expect(params).toEqual({ cwd: '/repo/app', hint: 'custom override text' });
      return Promise.resolve({
        hint: 'custom override text',
        defaultHint: 'default prompt text',
        overrideHint: 'custom override text',
        usingDefault: false,
      });
    }
    return Promise.resolve({});
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
  backend.callBackend.mockImplementation((method, params) => {
    if (method === 'config/builtinTools/write') {
      expect(params).toEqual({ cwd: '/repo/app', id: 'tool-2', enabled: true });
      return Promise.resolve({ tools: enabledBuiltInTools });
    }
    return Promise.resolve({});
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

    backend.callBackend.mockImplementation((method) => {
      if (method === 'config/lspPromptHint/write') {
        return Promise.reject(new Error('disk full'));
      }
      return Promise.resolve({});
    });

    fireEvent.click(screen.getByTestId('settings-lsp-save-button'));
    await waitFor(() => {
      expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveTextContent('保存失败');
      expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveAttribute('role', 'alert');
    });
  });
});

describe('SettingsPage built-in tool settings', () => {
  beforeEach(resetSettingsPageTestState);

  it('handles model built-in capabilities accordion and tool toggles', async () => {
    await renderSettingsPage();
    await expectBuiltinToolsLoaded();
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
});
