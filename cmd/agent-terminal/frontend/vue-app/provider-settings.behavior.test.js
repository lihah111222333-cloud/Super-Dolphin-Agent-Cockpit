// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import { EFFORT_MODES, MODEL_OPTIONS } from './provider-config-options.js';
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
        case 'settings.provider.claude.effort': return 'high';
        case 'settings.provider.claude.model': return 'gpt-5.2';
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
    expect(vm.providerModel.value).toBe('gpt-5.2');
    expect(vm.personality.value).toBe('friendly');
  });

  it('reuses shared model and effort option constants', () => {
    const { vm } = createProviderSettings();

    expect(vm.MODEL_OPTIONS).toBe(MODEL_OPTIONS);
    expect(vm.EFFORT_MODES).toBe(EFFORT_MODES);
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
    vm.effortMode.value = 'xhigh';
    vm.providerModel.value = 'gpt-5.4';
    vm.personality.value = 'pragmatic';

    await vm.saveProviderSettings();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.claude.sandbox',
      value: JSON.stringify({ type: 'workspaceWrite', writableRoots: ['/repo', '/tmp'], networkAccess: true }),
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'settings.provider.claude.model',
      value: 'gpt-5.4',
      cwd: '/repo',
    });
    expect(vm.sandboxNotice.message).toContain('已保存');
  });
});
