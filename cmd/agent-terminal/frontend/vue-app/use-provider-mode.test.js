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
  });

  it('falls back to codex mode when loading preference fails', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));
    const vm = useProviderMode();
    vm.useClaudeProvider.value = true;

    await vm.loadProviderPreference();

    expect(vm.useClaudeProvider.value).toBe(false);
  });

  it('toggles provider mode and persists the next value', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    const vm = useProviderMode();

    await vm.toggleProviderMode();
    expect(vm.useClaudeProvider.value).toBe(true);
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', { key: 'settings.provider.active', value: 'claude' });

    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    await vm.toggleProviderMode();
    expect(vm.useClaudeProvider.value).toBe(false);
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('ui/preferences/set', { key: 'settings.provider.active', value: 'codex' });
  });

  it('rolls back the toggle when persisting fails', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('save failed'));
    const vm = useProviderMode();

    await vm.toggleProviderMode();

    expect(vm.useClaudeProvider.value).toBe(false);
  });
});
