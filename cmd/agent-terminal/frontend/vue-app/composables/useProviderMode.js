import { onScopeDispose, ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { getPreferenceCached, onPreferenceChange } from '../stores/preferences.js';

const PROVIDER_ACTIVE_PREF_KEY = 'settings.provider.active';

function isClaudeProviderValue(value) {
  return typeof value === 'string' && value.trim().toLowerCase() === 'claude';
}

export function useProviderMode() {
  // Seed from the preferences store cache so a fresh mount picks up the
  // last value persisted by any other consumer (ProviderSettings save,
  // another window, bridge-event echo) without an extra round-trip.
  const initial = isClaudeProviderValue(getPreferenceCached(PROVIDER_ACTIVE_PREF_KEY));
  const useClaudeProvider = ref(initial);

  // Cross-page reactivity: any savePreference / bridge-event update for
  // the active-provider key syncs every live useProviderMode() instance.
  // The listener is unsubscribed when the consuming effect scope (i.e.
  // the component setup that called useProviderMode) is disposed, so the
  // listener does not outlive its captured `useClaudeProvider` ref.
  // Outside a component setup (raw test calls), onScopeDispose is a
  // documented no-op, which is fine — tests use __resetPreferenceStoreForTest.
  const off = onPreferenceChange(PROVIDER_ACTIVE_PREF_KEY, (value) => {
    useClaudeProvider.value = isClaudeProviderValue(value);
  });
  onScopeDispose(off);

  async function loadProviderPreference() {
    try {
      const value = await callAPI('ui/preferences/get', { key: PROVIDER_ACTIVE_PREF_KEY });
      useClaudeProvider.value = isClaudeProviderValue(value);
    } catch {
      useClaudeProvider.value = false;
    }
  }

  async function toggleProviderMode() {
    const next = !useClaudeProvider.value;
    useClaudeProvider.value = next;
    try {
      await callAPI('ui/preferences/set', { key: PROVIDER_ACTIVE_PREF_KEY, value: next ? 'claude' : 'codex' });
    } catch {
      useClaudeProvider.value = !next;
    }
  }

  return { useClaudeProvider, loadProviderPreference, toggleProviderMode };
}
