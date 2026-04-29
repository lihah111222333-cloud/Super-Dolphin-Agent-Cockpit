// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import { CODEX_IDENTITY_DEFAULTS, EFFORT_MODES_BY_PROVIDER, MODEL_OPTIONS_BY_PROVIDER } from './provider-config-options.js';
import { ProviderSettings } from './pages/settings/ProviderSettings.ts';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function createProviderSettings(overrides = {}) {
  const props = {
    projectStore: overrides.projectStore ?? { state: { active: '/repo' } },
  };
  return { props, vm: ProviderSettings.setup(props) };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
});

describe('ProviderSettings behavior', () => {
  it('loads active provider and provider-specific preferences', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method !== 'ui/preferences/get') return null;
      switch (payload.key) {
        case 'settings.provider.active': return 'claude';
        case 'settings.provider.claude.sandbox':
          return { type: 'readOnly', access: { type: 'restricted', readableRoots: ['/safe'] } };
        case 'settings.provider.claude.summary': return 'concise';
        case 'settings.provider.claude.approvalPolicy': return 'never';
        case 'settings.provider.claude.effort': return 'max';
        case 'settings.provider.claude.model': return 'claude-sonnet-4-6-20260401';
        case 'settings.provider.claude.personality': return 'friendly';
        default: return null;
      }
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();

    expect(vm.activeProvider.value).toBe('claude');
    expect(vm.sandboxMode.value).toBe('readOnly');
    expect(vm.readOnlyMode.value).toBe('restricted');
    expect(vm.readablePaths.value).toBe('/safe');
    expect(vm.summaryMode.value).toBe('concise');
    expect(vm.approvalMode.value).toBe('never');
    expect(vm.effortMode.value).toBe('high');
    expect(vm.providerModel.value).toBe('claude-sonnet-4-6-20260401');
    expect(vm.personality.value).toBe('friendly');
    expect(vm.providerModelOptions.value.at(-1)).toEqual({
      value: 'claude-sonnet-4-6-20260401',
      label: 'claude-sonnet-4-6-20260401',
    });
    expect(vm.providerEffortOptions.value.some((item) => item.value === 'max')).toBe(false);
  });


  it('normalizes object-shaped model preferences before saving', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method !== 'ui/preferences/get') return null;
      switch (payload.key) {
        case 'settings.provider.active': return 'claude';
        case 'settings.provider.claude.model': return { value: 'sonnet', label: 'Sonnet 4.7' };
        case 'settings.provider.claude.effort': return 'max';
        default: return null;
      }
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();

    expect(vm.providerModel.value).toBe('sonnet');
    expect(vm.effortMode.value).toBe('high');

    vm.sandboxMode.value = 'readOnly';
    apiMock.callAPI.mockClear();
    apiMock.callAPI.mockResolvedValue({ ok: true });
    await vm.saveProviderSettings();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.claude.model',
      value: 'sonnet',
      cwd: '/repo',
    });
  });

  it('loads and saves codex identity preferences with explicit defaults', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method !== 'ui/preferences/get') return { ok: true };
      switch (payload.key) {
        case 'settings.provider.active': return 'codex';
        case 'settings.provider.codex.codexHome': return '/Users/mac/.codex';
        case 'settings.provider.codex.codexInstanceKey': return 'primary';
        case 'settings.provider.codex.codexModelProvider': return 'openai-compatible';
        default: return null;
      }
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();

    expect(vm.codexHome.value).toBe('/Users/mac/.codex');
    expect(vm.codexInstanceKey.value).toBe('primary');
    expect(vm.codexModelProvider.value).toBe('openai-compatible');

    vm.codexHome.value = '';
    vm.codexInstanceKey.value = '';
    vm.codexModelProvider.value = '';
    vm.sandboxMode.value = 'readOnly';
    apiMock.callAPI.mockClear();
    apiMock.callAPI.mockResolvedValue({ ok: true });
    await vm.saveProviderSettings();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.codex.codexHome',
      value: CODEX_IDENTITY_DEFAULTS.codexHome,
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.codex.codexInstanceKey',
      value: CODEX_IDENTITY_DEFAULTS.codexInstanceKey,
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.codex.codexModelProvider',
      value: CODEX_IDENTITY_DEFAULTS.codexModelProvider,
      cwd: '/repo',
    });
  });

  it('switches model and effort lists by provider', () => {
    const { vm } = createProviderSettings();

    expect(vm.providerModelOptions.value).toEqual(MODEL_OPTIONS_BY_PROVIDER.codex);
    expect(vm.providerEffortOptions.value).toEqual(EFFORT_MODES_BY_PROVIDER.codex);

    vm.activeProvider.value = 'claude';
    vm.providerModel.value = 'sonnet';
    expect(vm.providerModelOptions.value).toEqual(MODEL_OPTIONS_BY_PROVIDER.claude);
    expect(vm.providerEffortOptions.value.some((item) => item.value === 'max')).toBe(false);

    vm.providerModel.value = 'best';
    expect(vm.providerEffortOptions.value.some((item) => item.value === 'max')).toBe(true);
  });

  it('persists active provider selection before reloading provider settings', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/set') return { ok: true };
      if (method !== 'ui/preferences/get') return null;
      switch (payload.key) {
        case 'settings.provider.claude.model': return 'sonnet';
        case 'settings.provider.claude.effort': return 'high';
        default: return null;
      }
    });

    const { vm } = createProviderSettings();
    vm.activeProvider.value = 'claude';

    await vm.onActiveProviderChange();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.active',
      value: 'claude',
      cwd: '/repo',
    });
    expect(vm.activeProvider.value).toBe('claude');
    expect(vm.providerModel.value).toBe('sonnet');
    expect(vm.effortMode.value).toBe('high');
  });

  it('ignores stale stored active-provider reads after a newer selection wins', async () => {
    const activeProviderRequest = deferred();
    apiMock.callAPI.mockImplementation((method, payload) => {
      if (method === 'ui/preferences/set') return Promise.resolve({ ok: true });
      if (method !== 'ui/preferences/get') return Promise.resolve(null);
      switch (payload.key) {
        case 'settings.provider.active':
          return activeProviderRequest.promise;
        case 'settings.provider.claude.model':
          return Promise.resolve('sonnet');
        case 'settings.provider.claude.effort':
          return Promise.resolve('high');
        default:
          return Promise.resolve(null);
      }
    });

    const { vm } = createProviderSettings();
    const staleLoad = vm.loadProviderSettings();
    vm.activeProvider.value = 'claude';
    await vm.onActiveProviderChange();
    activeProviderRequest.resolve('codex');
    await staleLoad;

    expect(vm.activeProvider.value).toBe('claude');
    expect(vm.providerModel.value).toBe('sonnet');
    expect(vm.effortMode.value).toBe('high');
  });

  it('blocks invalid workspaceWrite paths before saving', async () => {
    const { vm } = createProviderSettings();
    vm.sandboxMode.value = 'workspaceWrite';
    vm.writablePaths.value = 'relative/path';

    await vm.saveProviderSettings();

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.writablePathsError.value).toContain('路径必须以 / 开头');
  });

  it('saves valid provider settings with scoped preference keys', async () => {
    apiMock.callAPI.mockResolvedValue({ ok: true });
    const { vm } = createProviderSettings();

    vm.activeProvider.value = 'claude';
    vm.sandboxMode.value = 'workspaceWrite';
    vm.writablePaths.value = '/repo\n/tmp';
    vm.networkAccess.value = true;
    vm.summaryMode.value = 'detailed';
    vm.approvalMode.value = 'on-request';
    vm.effortMode.value = 'max';
    vm.providerModel.value = 'sonnet';
    vm.personality.value = 'pragmatic';

    await vm.saveProviderSettings();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.claude.sandbox',
      value: JSON.stringify({ type: 'workspaceWrite', writableRoots: ['/repo', '/tmp'], networkAccess: true }),
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.claude.model',
      value: 'sonnet',
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.claude.effort',
      value: 'high',
      cwd: '/repo',
    });
    expect(vm.sandboxNotice.message).toContain('已保存');
  });
});
