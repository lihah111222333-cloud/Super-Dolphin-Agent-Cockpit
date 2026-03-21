import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

export function useProviderMode() {
  const PROVIDER_ACTIVE_PREF_KEY = 'settings.provider.active';
  const useClaudeProvider = ref(false);

  async function loadProviderPreference() {
    try {
      const value = await callAPI('ui/preferences/get', { key: PROVIDER_ACTIVE_PREF_KEY });
      useClaudeProvider.value = (typeof value === 'string' && value.trim().toLowerCase() === 'claude');
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
