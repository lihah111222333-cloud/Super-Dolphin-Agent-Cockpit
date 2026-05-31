import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.jsx';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';

let bridgeCallback;

const backend = vi.hoisted(() => ({
  readConfig: vi.fn(),
  getWindowBootstrap: vi.fn(),
  getProjects: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadState: vi.fn(),
  getThreadMessages: vi.fn(),
  setPreference: vi.fn(),
  getPreference: vi.fn(),
  callBackend: vi.fn(),
  onBridgeEvent: vi.fn((callback) => {
    bridgeCallback = callback;
    return () => {
      bridgeCallback = null;
    };
  }),
}));

vi.mock('./shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

// Mock navigator.clipboard
const mockClipboardWriteText = vi.fn();
Object.defineProperty(navigator, 'clipboard', {
  value: {
    writeText: mockClipboardWriteText,
  },
  writable: true,
});

describe('SettingsPage connected integration tests', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    bridgeCallback = null;
    mockClipboardWriteText.mockReset();

    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activePage: 'settings',
      logLevel: 'info',
      logEntries: [],
    });

    backend.readConfig.mockResolvedValue({ cwd: '/repo/app' });
    backend.getWindowBootstrap.mockResolvedValue({ ok: true });
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.getSidebarState.mockResolvedValue({ threads: [], activeThreadId: '' });

    // Default mock preference responses
    backend.getPreference.mockImplementation(({ key }) => {
      if (key === 'settings.provider.codex.summary') return Promise.resolve('concise');
      if (key === 'settings.provider.codex.approvalPolicy') return Promise.resolve('untrusted');
      if (key === 'settings.showInjectedPromptInChat') return Promise.resolve(true);
      return Promise.resolve(null);
    });

    // Default mock prompt config responses
    backend.callBackend.mockImplementation((method, params) => {
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
        return Promise.resolve({
          tools: [
            { id: 'tool-1', label: 'File Search', description: 'Search workspace files', enabled: true, provider: 'claude', filterMode: 'hard' },
            { id: 'tool-2', label: 'Command Exec', description: 'Run terminal commands', enabled: false, provider: 'codex', filterMode: 'soft' },
          ],
        });
      }
      return Promise.resolve({});
    });
  });

  it('renders preference settings cards and triggers save/refresh', async () => {
    render(<App skipBootstrap />);

    // Verify preference select values
    await waitFor(() => {
      expect(screen.getByTestId('provider-summary-mode-select')).toHaveValue('concise');
      expect(screen.getByTestId('provider-approval-mode-select')).toHaveValue('untrusted');
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
      expect(screen.getByText('已保存：auto / never')).toBeInTheDocument();
    });
  });

  it('loads prompt settings and executes prompt actions', async () => {
    render(<App skipBootstrap />);

    await waitFor(() => {
      expect(screen.getByTestId('settings-lsp-effective-output')).toHaveValue('effective prompt text');
      expect(screen.getByTestId('settings-lsp-prompt-input')).toHaveValue('custom override text');
      expect(screen.getByTestId('settings-show-injected-toggle-input')).toBeChecked();
    });

    // Test visible checkbox toggle
    fireEvent.click(screen.getByTestId('settings-show-injected-toggle-input'));
    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.showInjectedPromptInChat',
        value: false,
      });
    });

    // Copy effective prompt hint
    mockClipboardWriteText.mockResolvedValueOnce();
    fireEvent.click(screen.getByTestId('settings-lsp-copy-button'));
    await waitFor(() => {
      expect(mockClipboardWriteText).toHaveBeenCalledWith('effective prompt text');
      expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveTextContent('已复制生效提示词');
    });

    // Save prompt hint
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

    fireEvent.click(screen.getByTestId('settings-lsp-save-button'));
    await waitFor(() => {
      expect(screen.getByTestId('settings-lsp-prompt-notice')).toHaveTextContent('提示词已保存');
    });
  });

  it('handles model built-in capabilities accordion and tool toggles', async () => {
    render(<App skipBootstrap />);

    // Verify counts
    await waitFor(() => {
      expect(screen.getByTestId('settings-builtin-tools-summary')).toHaveTextContent('已管控 1 / 2');
    });

    // Verify accordion header shows up
    const softAuditHeader = screen.getByTestId('settings-builtin-tool-group-head-soft-audit');
    expect(softAuditHeader).toBeInTheDocument();
    expect(softAuditHeader).toHaveAttribute('aria-expanded', 'false');

    // Click to expand group
    fireEvent.click(softAuditHeader);
    expect(softAuditHeader).toHaveAttribute('aria-expanded', 'true');

    // Verify tool detail description is rendered
    expect(screen.getByText('Command Exec')).toBeInTheDocument();
    expect(screen.getByText(/Run terminal commands/)).toBeInTheDocument();

    // Toggle capability
    const checkbox = screen.getByTestId('settings-builtin-tool-input-tool-2');
    expect(checkbox).toBeChecked(); // Checked because tool is disabled (enabled=false)

    // Make mock return updated write response
    backend.callBackend.mockImplementation((method, params) => {
      if (method === 'config/builtinTools/write') {
        expect(params).toEqual({ cwd: '/repo/app', id: 'tool-2', enabled: true });
        return Promise.resolve({
          tools: [
            { id: 'tool-1', label: 'File Search', description: 'Search workspace files', enabled: true, provider: 'claude', filterMode: 'hard' },
            { id: 'tool-2', label: 'Command Exec', description: 'Run terminal commands', enabled: true, provider: 'codex', filterMode: 'soft' },
          ],
        });
      }
      return Promise.resolve({});
    });

    fireEvent.click(checkbox);
    await waitFor(() => {
      expect(checkbox).not.toBeChecked(); // Unchecked because tool is enabled (enabled=true)
      expect(screen.getByTestId('settings-builtin-tools-notice')).toHaveTextContent('Command Exec 已启用');
    });
  });

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
    const select = screen.getByTestId('settings-log-level-select');
    fireEvent.change(select, { target: { value: 'error' } });

    await waitFor(() => {
      expect(useClientStore.getState().logLevel).toBe('error');
    });
  });
});
