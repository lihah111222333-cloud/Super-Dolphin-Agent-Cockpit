// @ts-nocheck
import { CODEX_IDENTITY_DEFAULTS, normalizeProviderConfigValue } from '../provider-config-options.js';
import { isProviderPreferenceAbsent, isProviderPreferenceTombstone } from './provider-preferences.js';

function normalizeCodexIdentityValue(value, fallback) {
  if (typeof value === 'boolean') return fallback;
  return normalizeProviderConfigValue(value) || fallback;
}

export function buildCodexIdentityConfig(home, instanceKey, modelProvider) {
  const config = {};
  if (normalizeProviderConfigValue(home)) config.codexHome = normalizeProviderConfigValue(home);
  if (normalizeProviderConfigValue(instanceKey)) config.codexInstanceKey = normalizeCodexIdentityValue(instanceKey, CODEX_IDENTITY_DEFAULTS.codexInstanceKey);
  if (normalizeProviderConfigValue(modelProvider)) config.codexModelProvider = normalizeCodexIdentityValue(modelProvider, CODEX_IDENTITY_DEFAULTS.codexModelProvider);
  return config;
}

export function normalizeCodexSandboxPreference(value) {
  if (isProviderPreferenceAbsent(value) || isProviderPreferenceTombstone(value)) return null;
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const mode = normalizeProviderConfigValue(value.type || value.mode);
    const hasModeKey = Object.prototype.hasOwnProperty.call(value, 'type') || Object.prototype.hasOwnProperty.call(value, 'mode');
    if (Object.keys(value).length === 0) return null;
    if (!mode && hasModeKey) throw new Error(`invalid codex sandbox preference: ${String(value.type || value.mode)}`);
    return codexSandboxPayload(mode, value);
  }
  if (typeof value === 'string') {
    const normalized = normalizeProviderConfigValue(value);
    if (!normalized) return null;
    try {
      const parsed = JSON.parse(normalized);
      if (isProviderPreferenceTombstone(parsed) || isProviderPreferenceAbsent(parsed)) return null;
      if ((parsed && typeof parsed === 'object' && !Array.isArray(parsed)) || typeof parsed === 'string') {
        return normalizeCodexSandboxPreference(parsed);
      }
      throw new Error(`invalid codex sandbox preference: ${normalized}`);
    } catch (error) {
      if (!(error instanceof SyntaxError)) throw error;
      return codexSandboxPayload(normalized);
    }
  }
  throw new Error(`invalid codex sandbox preference: ${String(value)}`);
}

function defaultCodexLaunchSandbox(cwd) {
  const root = normalizeProviderConfigValue(cwd);
  const writableRoots = root && root !== '.' ? [root] : [];
  return {
    mode: 'workspace-write',
    writable_roots: writableRoots,
    network_access: false,
  };
}

export function buildCodexLaunchSandboxPreference(value, cwd) {
  const explicit = normalizeCodexSandboxPreference(value);
  if (explicit) return explicit;
  return defaultCodexLaunchSandbox(cwd);
}

function codexSandboxPayload(value, payload = {}) {
  const mode = (value || '').toString().trim();
  if (mode === 'readOnly' || mode === 'read-only') {
    const access = payload.access;
    if (access && access.type === 'restricted') {
      let readableRoots = [];
      if (Array.isArray(access.readable_roots)) readableRoots = access.readable_roots;
      else if (Array.isArray(access.readableRoots)) readableRoots = access.readableRoots;
      return {
        mode: 'read-only',
        access: {
          type: 'restricted',
          readable_roots: readableRoots,
          include_platform_defaults: Boolean(access.include_platform_defaults ?? access.includePlatformDefaults),
        },
      };
    }
    return { mode: 'read-only' };
  }
  if (mode === 'dangerFullAccess' || mode === 'danger-full-access') return { mode: 'danger-full-access' };
  if (mode === 'workspaceWrite' || mode === 'workspace-write') {
    let writableRoots = [];
    if (Array.isArray(payload.writable_roots)) writableRoots = payload.writable_roots;
    else if (Array.isArray(payload.writableRoots)) writableRoots = payload.writableRoots;
    return {
      mode: 'workspace-write',
      writable_roots: writableRoots,
      network_access: Boolean(payload.network_access ?? payload.networkAccess),
    };
  }
  throw new Error(`invalid codex sandbox preference: ${String(value)}`);
}
