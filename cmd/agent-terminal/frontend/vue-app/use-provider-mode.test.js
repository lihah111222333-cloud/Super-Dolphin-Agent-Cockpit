// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import { useProviderMode } from './composables/useProviderMode.js';

beforeEach(() => {
  apiMock.callAPI.mockReset();
});

describe('useProviderMode', () => {
  it('loads the claude preference from persisted settings', async () => {
    apiMock.callAPI.mockResolvedValueOnce('claude');
    const vm = useProviderMode();

    await vm.loadProviderPreference();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/get', { key: 'settings.provider.active' });
    expect(vm.useClaudeProvider.value).toBe(true);
    expect(vm.providerPreferenceReady.value).toBe(true);
  });

  it('uses the codex display default without materializing a global preference', async () => {
    apiMock.callAPI.mockResolvedValueOnce(null);
    const vm = useProviderMode();

    expect(vm.providerPreferenceReady.value).toBe(false);

    await vm.loadProviderPreference();

    expect(apiMock.callAPI).toHaveBeenNthCalledWith(1, 'ui/preferences/get', { key: 'settings.provider.active' });
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/preferences/set', expect.anything());
    expect(vm.useClaudeProvider.value).toBe(false);
    expect(vm.providerPreferenceReady.value).toBe(true);
  });

  it('falls back to global active provider for a selected project scope without materializing the project default', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce('claude');
    const vm = useProviderMode('/repo');

    await vm.loadProviderPreference();

    expect(apiMock.callAPI).toHaveBeenNthCalledWith(1, 'ui/preferences/get', {
      key: 'settings.provider.active',
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenNthCalledWith(2, 'ui/preferences/get', {
      key: 'settings.provider.active',
    });
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/preferences/set', expect.anything());
    expect(vm.useClaudeProvider.value).toBe(true);
    expect(vm.providerPreferenceReady.value).toBe(true);
  });

  it('does not expose fake codex readiness when loading preference fails', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));
    const vm = useProviderMode();
    vm.useClaudeProvider.value = true;
    vm.providerPreferenceReady.value = true;

    await vm.loadProviderPreference();

    expect(vm.useClaudeProvider.value).toBe(true);
    expect(vm.providerPreferenceReady.value).toBe(false);
    expect(vm.providerPreferenceError.value).toContain('boom');
  });

  it('toggles provider mode and persists the next value', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    const vm = useProviderMode();

    await vm.toggleProviderMode();
    expect(vm.useClaudeProvider.value).toBe(true);
    expect(vm.providerPreferenceReady.value).toBe(true);
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', { key: 'settings.provider.active', value: 'claude' });

    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    await vm.toggleProviderMode();
    expect(vm.useClaudeProvider.value).toBe(false);
    expect(vm.providerPreferenceReady.value).toBe(true);
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('ui/preferences/set', { key: 'settings.provider.active', value: 'codex' });
  });

  it('rolls back the toggle when persisting fails', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('save failed'));
    const vm = useProviderMode();

    await vm.toggleProviderMode();

    expect(vm.useClaudeProvider.value).toBe(false);
    expect(vm.providerPreferenceReady.value).toBe(false);
  });
});
