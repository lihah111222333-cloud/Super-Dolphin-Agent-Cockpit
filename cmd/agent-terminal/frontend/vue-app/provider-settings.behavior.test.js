// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import { EFFORT_MODES_BY_PROVIDER, MODEL_OPTIONS_BY_PROVIDER } from './provider-config-options.js';
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

  it('loads and saves editable codex identity preferences without exposing the internal model provider', async () => {
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
    expect('codexModelProvider' in vm).toBe(false);

    vm.codexHome.value = '';
    vm.codexInstanceKey.value = '';
    vm.sandboxMode.value = 'readOnly';
    apiMock.callAPI.mockClear();
    apiMock.callAPI.mockResolvedValue({ ok: true });
    await vm.saveProviderSettings();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.codex.codexHome',
      value: { cleared: true },
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.codex.codexInstanceKey',
      value: { cleared: true },
      cwd: '/repo',
    });
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/preferences/set', expect.objectContaining({
      key: 'settings.provider.codex.codexModelProvider',
    }));
    expect(ProviderSettings.template).not.toContain('provider-codex-model-provider-input');
    expect(ProviderSettings.template).not.toContain('super-dolphin-relay');
  });

  it('uses the default codex active provider without materializing a scoped preference', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/set') return { ok: true };
      if (method !== 'ui/preferences/get') return null;
      return null;
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.active',
      value: 'codex',
      cwd: '/repo',
    });
    expect(vm.activeProvider.value).toBe('codex');
  });

  it('displays effective project-over-global provider preferences without erasing global partials', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method !== 'ui/preferences/get') return null;
      switch (payload.key) {
        case 'settings.provider.active':
          return payload.cwd === '/repo' ? null : 'codex';
        case 'settings.provider.codex.model':
          return payload.cwd === '/repo' ? 'gpt-5.4' : 'gpt-5.5';
        case 'settings.provider.codex.effort':
          return payload.cwd === '/repo' ? null : 'high';
        case 'settings.provider.codex.codexHome':
          return payload.cwd === '/repo' ? '' : '/Users/global/.codex';
        case 'settings.provider.codex.codexInstanceKey':
          return payload.cwd === '/repo' ? 'project-instance' : 'global-instance';
        default:
          return null;
      }
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();

    expect(vm.activeProvider.value).toBe('codex');
    expect(vm.providerModel.value).toBe('gpt-5.4');
    expect(vm.effortMode.value).toBe('high');
    expect(vm.codexHome.value).toBe('/Users/global/.codex');
    expect(vm.codexInstanceKey.value).toBe('project-instance');
  });

  it('writes tombstones when clearing Codex identity fields instead of packaged defaults', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method !== 'ui/preferences/get') return { ok: true };
      switch (payload.key) {
        case 'settings.provider.active': return 'codex';
        case 'settings.provider.codex.codexHome': return '/Users/mac/.codex';
        case 'settings.provider.codex.codexInstanceKey': return 'primary';
        default: return null;
      }
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();
    vm.codexHome.value = '';
    vm.codexInstanceKey.value = '';
    vm.sandboxMode.value = 'readOnly';
    apiMock.callAPI.mockClear();
    apiMock.callAPI.mockResolvedValue({ ok: true });

    await vm.saveProviderSettings();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.codex.codexHome',
      value: { cleared: true },
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.codex.codexInstanceKey',
      value: { cleared: true },
      cwd: '/repo',
    });
  });

  it('does not persist packaged Codex model or effort defaults when saving unrelated settings without explicit preferences', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/set') return { ok: true };
      if (method !== 'ui/preferences/get') return null;
      switch (payload.key) {
        case 'settings.provider.active': return 'codex';
        case 'settings.provider.codex.model': return null;
        case 'settings.provider.codex.effort': return null;
        default: return null;
      }
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();

    expect(vm.providerModel.value).toBe('gpt-5-codex');
    expect(vm.effortMode.value).toBe('xhigh');

    vm.sandboxMode.value = 'readOnly';
    vm.summaryMode.value = 'concise';
    apiMock.callAPI.mockClear();
    await vm.saveProviderSettings();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.codex.summary',
      value: 'concise',
      cwd: '/repo',
    });
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/preferences/set', expect.objectContaining({
      key: 'settings.provider.codex.model',
    }));
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/preferences/set', expect.objectContaining({
      key: 'settings.provider.codex.effort',
    }));
  });

  it('fails fast instead of materializing codex when active provider is invalid', async () => {
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get' && payload.key === 'settings.provider.active') return 'bad-provider';
      if (method === 'ui/preferences/set') return { ok: true };
      return null;
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/preferences/set', expect.objectContaining({
      key: 'settings.provider.active',
      value: 'codex',
    }));
    expect(vm.sandboxNotice.level).toBe('error');
    expect(vm.sandboxNotice.message).toContain('invalid provider preference');
  });

  it('blocks provider settings load when the sandbox preference is invalid', async () => {
    const readKeys = [];
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method !== 'ui/preferences/get') return null;
      readKeys.push(payload.key);
      switch (payload.key) {
        case 'settings.provider.active': return 'codex';
        case 'settings.provider.codex.sandbox': return '{bad-json';
        case 'settings.provider.codex.summary': return 'concise';
        default: return null;
      }
    });

    const { vm } = createProviderSettings();
    await vm.loadProviderSettings();

    expect(vm.sandboxNotice.level).toBe('error');
    expect(vm.sandboxNotice.message).toContain('加载 Sandbox 失败');
    expect(readKeys).not.toContain('settings.provider.codex.summary');
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
