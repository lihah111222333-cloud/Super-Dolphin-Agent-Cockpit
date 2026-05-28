// @ts-nocheck
// Cross-page provider sync: when ProviderSettings (or any other source)
// pushes a new active-provider value through the preferences store, every
// live useProviderMode() consumer (e.g. the chat toolbar) updates without
// a page reload. This is the user-visible payoff of the bridge-event +
// preferences store wiring.

import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  onBridgeEvent: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  onBridgeEvent: apiMock.onBridgeEvent,
}));

import { effectScope } from '../lib/vue.esm-browser.prod.js';
import { ref } from '../lib/vue.esm-browser.prod.js';
import { useProviderMode } from './composables/useProviderMode.js';
import { __resetPreferenceStoreForTest, savePreference } from './stores/preferences.js';

let bridgeCallback = null;

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.onBridgeEvent.mockReset();
  bridgeCallback = null;
  apiMock.onBridgeEvent.mockImplementation((cb) => {
    bridgeCallback = cb;
    return () => { bridgeCallback = null; };
  });
  __resetPreferenceStoreForTest();
});

describe('useProviderMode cross-page reactivity', () => {
  it('seeds from cached active provider without waiting for load', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    await savePreference('settings.provider.active', 'claude', '/repo');

    const scope = effectScope();
    let vm;
    scope.run(() => {
      vm = useProviderMode(ref('/repo'));
    });

    expect(vm.providerPreferenceReady.value).toBe(true);
    expect(vm.useClaudeProvider.value).toBe(true);
    scope.stop();
  });

  it('reacts when another consumer calls savePreference for active provider', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });

    const vm = useProviderMode();
    expect(vm.useClaudeProvider.value).toBe(false);

    await savePreference('settings.provider.active', 'claude');

    expect(vm.useClaudeProvider.value).toBe(true);
  });

  it('reacts to backend bridge-event for active provider', () => {
    const vm = useProviderMode();
    expect(vm.useClaudeProvider.value).toBe(false);

    expect(typeof bridgeCallback).toBe('function');
    bridgeCallback({
      type: 'ui/preferences/changed',
      payload: { key: 'settings.provider.active', value: 'claude' },
    });

    expect(vm.useClaudeProvider.value).toBe(true);
  });

  it('reacts to scoped provider changes without letting global changes override the project scope', async () => {
    apiMock.callAPI.mockResolvedValue({ ok: true });
    const activeProjectCwd = ref('/repo');
    const vm = useProviderMode(activeProjectCwd);

    await savePreference('settings.provider.active', 'claude', '/repo');
    expect(vm.useClaudeProvider.value).toBe(true);

    await savePreference('settings.provider.active', 'codex');
    expect(vm.useClaudeProvider.value).toBe(true);
  });

  it('two useProviderMode() instances stay in sync', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });

    const a = useProviderMode();
    const b = useProviderMode();

    await savePreference('settings.provider.active', 'claude');

    expect(a.useClaudeProvider.value).toBe(true);
    expect(b.useClaudeProvider.value).toBe(true);
  });

  it('ignores unrelated preference changes', () => {
    const vm = useProviderMode();
    expect(typeof bridgeCallback).toBe('function');
    bridgeCallback({
      type: 'ui/preferences/changed',
      payload: { key: 'settings.theme', value: 'dark' },
    });
    expect(vm.useClaudeProvider.value).toBe(false);
  });

  it('unsubscribes the preference listener when the effect scope is disposed', async () => {
    // Mimics a Vue component's setup-then-unmount lifecycle.
    const scope = effectScope();
    let vm;
    scope.run(() => {
      vm = useProviderMode();
    });
    expect(vm.useClaudeProvider.value).toBe(false);

    // Dispose the scope; onScopeDispose inside useProviderMode should
    // run, which calls the unsubscribe returned by onPreferenceChange.
    scope.stop();

    // Subsequent updates must NOT flip vm.useClaudeProvider, otherwise
    // the listener is still attached and the captured ref outlives its
    // owner (the leak this test guards against).
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    await savePreference('settings.provider.active', 'claude');
    expect(vm.useClaudeProvider.value).toBe(false);

    // Bridge-event path is also dead.
    bridgeCallback({
      type: 'ui/preferences/changed',
      payload: { key: 'settings.provider.active', value: 'claude' },
    });
    expect(vm.useClaudeProvider.value).toBe(false);
  });
});
