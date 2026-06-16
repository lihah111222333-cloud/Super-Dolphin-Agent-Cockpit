// @ts-check

export const RUNTIME_PROVIDER = 'codex';
export const KNOWN_PROVIDERS = Object.freeze(['codex', 'claude']);

function normalizeString(value) {
  if (value === null || value === undefined) return '';
  return String(value).trim();
}

export function normalizeProviderPreferenceValue(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const key of ['value', 'id', 'key', 'name', 'model', 'provider']) {
      const normalized = normalizeString(value[key]);
      if (normalized) return normalized;
    }
    return '';
  }
  return normalizeString(value);
}

export function normalizeKnownProviderName(value) {
  const provider = normalizeProviderPreferenceValue(value).toLowerCase();
  if (!provider) return '';
  if (KNOWN_PROVIDERS.includes(provider)) return provider;
  throw new Error(`invalid provider preference: ${normalizeProviderPreferenceValue(value)}`);
}

export function unsupportedRuntimeProviderMessage(provider, reason) {
  return `${reason}: unsupported provider preference "${provider}"; current desktop UI supports codex only`;
}

export function normalizeRuntimeProviderName(value, reason) {
  const provider = normalizeKnownProviderName(value);
  if (!provider) return '';
  if (provider === RUNTIME_PROVIDER) return provider;
  throw new Error(unsupportedRuntimeProviderMessage(provider, reason));
}

export function providerPreferenceKey(provider, suffix) {
  return `settings.provider.${provider}.${suffix}`;
}
