// @ts-check

import {
  normalizeKnownProviderName,
  normalizeRuntimeProviderName,
  RUNTIME_PROVIDER,
} from './providerPreferences.js';

export const PROVIDER_ACTIVE_PREF_KEY = 'settings.provider.active';

const PROVIDER_DISPLAY_DEFAULT_CONFIGS = Object.freeze({
  codex: Object.freeze({ model: 'gpt-5.5', effort: 'xhigh' }),
  claude: Object.freeze({ model: 'sonnet', effort: 'high' }),
});

function normalizeString(value) {
  return (value || '').toString().trim();
}

export function normalizeProviderConfigValue(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const key of ['value', 'id', 'key', 'name', 'model', 'provider']) {
      const normalized = normalizeString(value[key]);
      if (normalized) return normalized;
    }
    return '';
  }
  return normalizeString(value);
}

export function normalizeActiveProviderName(value, reason) {
  return normalizeRuntimeProviderName(value, reason);
}

export function knownProviderName(value) {
  try {
    return normalizeKnownProviderName(value);
  } catch {
    return '';
  }
}

export function requireActiveProviderPreference(value, reason) {
  const provider = normalizeActiveProviderName(value, reason);
  if (!provider) {
    throw new Error(`${reason}: settings.provider.active preference is required`);
  }
  return provider;
}

export function normalizeCodexIdentityValue(value) {
  if (typeof value === 'boolean') return '';
  return normalizeProviderConfigValue(value);
}

export function isPreferenceTombstone(value) {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value) && value.cleared === true);
}

export function isPreferenceAbsent(value) {
  return value === null || value === undefined || (typeof value === 'string' && value.trim() === '');
}

export function codexLaunchConfigFromPreferences({ codexHome, codexInstanceKey, codexModelProvider }) {
  const home = normalizeProviderConfigValue(codexHome);
  const instanceKey = normalizeCodexIdentityValue(codexInstanceKey);
  const modelProvider = normalizeCodexIdentityValue(codexModelProvider);
  if (!home && !instanceKey && !modelProvider) return null;

  if (!home || !instanceKey || !modelProvider) {
    throw new Error('startThread: complete Codex identity requires codexHome, codexInstanceKey, and codexModelProvider');
  }

  return { codexHome: home, codexInstanceKey: instanceKey, codexModelProvider: modelProvider };
}

export function providerDisplayDefaultConfig(provider) {
  return PROVIDER_DISPLAY_DEFAULT_CONFIGS[provider] || PROVIDER_DISPLAY_DEFAULT_CONFIGS[RUNTIME_PROVIDER];
}

export function normalizeProviderRuntimeConfig(raw = {}, providerValue = RUNTIME_PROVIDER) {
  const provider = normalizeActiveProviderName(providerValue, 'provider.config') || RUNTIME_PROVIDER;
  return {
    provider,
    model: normalizeProviderConfigValue(raw.model),
    effort: normalizeProviderConfigValue(raw.effort),
    codexModelProvider: normalizeCodexIdentityValue(raw.codexModelProvider),
  };
}

export function requireProviderPreferenceValue(value, key, reason) {
  const normalized = normalizeProviderConfigValue(value);
  if (!normalized) {
    throw new Error(`${reason}: ${key} preference is required`);
  }
  return normalized;
}
