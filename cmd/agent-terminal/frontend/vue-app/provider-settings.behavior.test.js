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
