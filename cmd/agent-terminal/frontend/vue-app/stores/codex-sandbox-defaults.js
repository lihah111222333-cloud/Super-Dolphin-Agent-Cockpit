// @ts-nocheck
import { CODEX_IDENTITY_DEFAULTS, normalizeProviderConfigValue } from '../provider-config-options.js';

function normalizeCodexIdentityValue(value, fallback) {
  if (typeof value === 'boolean') return fallback;
  return normalizeProviderConfigValue(value) || fallback;
}

export function buildCodexIdentityConfig(home, instanceKey, modelProvider) {
  const config = {
    codexInstanceKey: normalizeCodexIdentityValue(instanceKey, CODEX_IDENTITY_DEFAULTS.codexInstanceKey),
    codexModelProvider: normalizeCodexIdentityValue(modelProvider, CODEX_IDENTITY_DEFAULTS.codexModelProvider),
  };
  if (normalizeProviderConfigValue(home)) config.codexHome = normalizeProviderConfigValue(home);
  return config;
}

export function normalizeCodexSandboxPreference(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return codexSandboxPayload(value.type, value);
  }
  if (typeof value === 'string' && value.trim()) {
    try {
      const parsed = JSON.parse(value);
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) return codexSandboxPayload(parsed.type, parsed);
    } catch {
      return codexSandboxPayload(value);
    }
  }
  return codexSandboxPayload('workspaceWrite');
}

function codexSandboxPayload(value, payload = {}) {
  const mode = (value || '').toString().trim();
  if (mode === 'readOnly' || mode === 'read-only') return { 'read-only': null };
  if (mode === 'dangerFullAccess' || mode === 'danger-full-access') return { 'danger-full-access': null };
  return { 'workspace-write': null };
}
