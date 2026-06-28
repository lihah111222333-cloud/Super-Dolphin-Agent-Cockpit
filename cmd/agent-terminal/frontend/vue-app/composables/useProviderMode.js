import { onScopeDispose, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { getPreferenceCached, onPreferenceChange } from '../stores/preferences.js';
import { isProviderPreferenceAbsent, normalizeProviderIDStrict } from '../stores/provider-preferences.js';

const PROVIDER_ACTIVE_PREF_KEY = 'settings.provider.active';
const DEFAULT_PROVIDER_ID = 'codex';

function normalizeProviderPreference(value) {
  try {
    return normalizeProviderIDStrict(value);
  } catch {
    return '';
  }
}

function providerPreferenceErrorMessage(error) {
  return error?.message || String(error || 'unknown provider preference error');
}

function resolveProviderPreferenceScope(scopeSource) {
  const raw = scopeSource && typeof scopeSource === 'object' && 'value' in scopeSource
    ? scopeSource.value
    : scopeSource;
  const scope = (raw || '').toString().trim();
  return scope === '.' ? '' : scope;
}

function providerPreferencePayload(scope, extra = {}) {
  const payload = { key: PROVIDER_ACTIVE_PREF_KEY, ...extra };
  if (scope) payload.cwd = scope;
  return payload;
}

export function useProviderMode(providerPreferenceScope = '') {
  const currentProviderPreferenceScope = () => resolveProviderPreferenceScope(providerPreferenceScope);
  // Seed from the preferences store cache so a fresh mount picks up the
  // last value persisted by any other consumer (ProviderSettings save,
  // another window, bridge-event echo) without an extra round-trip.
  const initialPreference = getPreferenceCached(PROVIDER_ACTIVE_PREF_KEY, currentProviderPreferenceScope());
  const initialProvider = normalizeProviderPreference(initialPreference);
  const useClaudeProvider = ref(initialProvider === 'claude');
  const providerPreferenceReady = ref(Boolean(initialProvider));
  const providerPreferenceError = ref('');
  let providerPreferenceSeq = 0;
  let unsubscribeProviderPreference = () => {};

  function beginProviderPreferenceRequest() {
    providerPreferenceSeq += 1;
    return providerPreferenceSeq;
  }

  function isCurrentProviderPreferenceRequest(seq) {
    return seq === providerPreferenceSeq;
  }

  function applyProviderPreference(provider) {
    const normalized = normalizeProviderPreference(provider);
    if (!normalized) return false;
    useClaudeProvider.value = normalized === 'claude' || normalized.startsWith('claude-');
    providerPreferenceReady.value = true;
    providerPreferenceError.value = '';
    return true;
  }

  function handleProviderPreferenceChange(value) {
    beginProviderPreferenceRequest();
    if (applyProviderPreference(value)) return;
    useClaudeProvider.value = false;
    providerPreferenceReady.value = false;
    providerPreferenceError.value = '';
  }

  function subscribeProviderPreference(scope) {
    unsubscribeProviderPreference();
    unsubscribeProviderPreference = onPreferenceChange(PROVIDER_ACTIVE_PREF_KEY, handleProviderPreferenceChange, scope);
  }

  // Cross-page reactivity: any savePreference / bridge-event update for
  // the active-provider key syncs every live useProviderMode() instance.
  // The listener is unsubscribed when the consuming effect scope (i.e.
  // the component setup that called useProviderMode) is disposed, so the
  // listener does not outlive its captured `useClaudeProvider` ref.
  // Outside a component setup (raw test calls), onScopeDispose is a
  // documented no-op, which is fine — tests use __resetPreferenceStoreForTest.
  subscribeProviderPreference(currentProviderPreferenceScope());
  onScopeDispose(() => unsubscribeProviderPreference());

  if (providerPreferenceScope && typeof providerPreferenceScope === 'object' && 'value' in providerPreferenceScope) {
    const stopScopeWatch = watch(
      () => currentProviderPreferenceScope(),
      (nextScope, previousScope) => {
        if (nextScope === previousScope) return;
        beginProviderPreferenceRequest();
        useClaudeProvider.value = false;
        providerPreferenceReady.value = false;
        providerPreferenceError.value = '';
        subscribeProviderPreference(nextScope);
        loadProviderPreference();
      },
    );
    onScopeDispose(stopScopeWatch);
  }

  async function loadProviderPreference() {
    const requestSeq = beginProviderPreferenceRequest();
    const scope = currentProviderPreferenceScope();
    try {
      const value = await callAPI('ui/preferences/get', providerPreferencePayload(scope));
      if (!isCurrentProviderPreferenceRequest(requestSeq)) return;
      if (applyProviderPreference(value)) return;
      if (!isProviderPreferenceAbsent(value)) {
        throw new Error(`invalid provider preference: ${String(value)}`);
      }
      if (scope) {
        const globalValue = await callAPI('ui/preferences/get', providerPreferencePayload(''));
        if (!isCurrentProviderPreferenceRequest(requestSeq)) return;
        if (applyProviderPreference(globalValue)) return;
        if (!isProviderPreferenceAbsent(globalValue)) {
          throw new Error(`invalid provider preference: ${String(globalValue)}`);
        }
      }
      if (!isCurrentProviderPreferenceRequest(requestSeq)) return;
      applyProviderPreference(DEFAULT_PROVIDER_ID);
    } catch (error) {
      if (!isCurrentProviderPreferenceRequest(requestSeq)) return;
      providerPreferenceReady.value = false;
      providerPreferenceError.value = providerPreferenceErrorMessage(error);
    }
  }

  async function toggleProviderMode() {
    const requestSeq = beginProviderPreferenceRequest();
    const scope = currentProviderPreferenceScope();
    const previousProvider = useClaudeProvider.value ? 'claude' : DEFAULT_PROVIDER_ID;
    const previousReady = providerPreferenceReady.value;
    const previousError = providerPreferenceError.value;
    const next = !useClaudeProvider.value;
    const nextProvider = next ? 'claude' : DEFAULT_PROVIDER_ID;
    useClaudeProvider.value = nextProvider === 'claude';
    providerPreferenceReady.value = false;
    providerPreferenceError.value = '';
    try {
      await callAPI('ui/preferences/set', providerPreferencePayload(scope, { value: nextProvider }));
      if (!isCurrentProviderPreferenceRequest(requestSeq)) return;
      applyProviderPreference(nextProvider);
    } catch (error) {
      if (!isCurrentProviderPreferenceRequest(requestSeq)) return;
      useClaudeProvider.value = previousProvider === 'claude';
      providerPreferenceReady.value = previousReady;
      providerPreferenceError.value = previousError || providerPreferenceErrorMessage(error);
    }
  }

  return {
    useClaudeProvider,
    providerPreferenceReady,
    providerPreferenceError,
    loadProviderPreference,
    toggleProviderMode,
  };
}
