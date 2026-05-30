// @ts-nocheck
import { normalizeProviderConfigValue } from '../provider-config-options.js';

export const PROVIDER_TOMBSTONE = Object.freeze({ cleared: true });

export function isProviderPreferenceTombstone(value) {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value) && value.cleared === true);
}

export function isProviderPreferenceAbsent(value) {
  return value === null || value === undefined || (typeof value === 'string' && value.trim() === '');
}

export function normalizeProviderIDStrict(value) {
  const providerID = normalizeProviderConfigValue(value).toLowerCase();
  if (!providerID) return '';
  if (providerID === 'codex' || providerID === 'claude' || providerID.startsWith('claude-')) {
    return providerID;
  }
  throw new Error(`invalid provider preference: ${String(value)}`);
}

export function normalizeProviderPreferenceValue(value) {
  if (isProviderPreferenceTombstone(value) || isProviderPreferenceAbsent(value)) return '';
  return normalizeProviderConfigValue(value);
}

export async function resolveScopedProviderPreference(getPref, key, cwd = '') {
  const scope = (cwd || '').toString().trim();
  if (scope) {
    const scoped = await getPref({ key, cwd: scope });
    if (isProviderPreferenceTombstone(scoped)) return { value: '', cleared: true, source: 'project' };
    if (!isProviderPreferenceAbsent(scoped)) return { value: scoped, cleared: false, source: 'project' };
  }
  const global = await getPref({ key });
  if (isProviderPreferenceTombstone(global)) return { value: '', cleared: true, source: 'global' };
  if (!isProviderPreferenceAbsent(global)) return { value: global, cleared: false, source: 'global' };
  return { value: '', cleared: false, source: 'default' };
}

export async function resolveProviderConfigPreference(getPref, key, cwd = '') {
  const resolved = await resolveScopedProviderPreference(getPref, key, cwd);
  return {
    ...resolved,
    value: normalizeProviderPreferenceValue(resolved.value),
  };
}

export async function resolveActiveProviderPreference(getPref, cwd = '', fallback = '') {
  const resolved = await resolveScopedProviderPreference(getPref, 'settings.provider.active', cwd);
  if (resolved.cleared || isProviderPreferenceAbsent(resolved.value)) return fallback;
  return normalizeProviderIDStrict(resolved.value);
}

export function providerPreferenceSetValue(raw) {
  return normalizeProviderConfigValue(raw) ? normalizeProviderConfigValue(raw) : PROVIDER_TOMBSTONE;
}
