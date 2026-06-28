import { computed } from '../../../lib/vue.esm-browser.prod.js';

type SettingsScopeProjectStore = { state?: { active?: string } } | null | undefined;
type ScopedPayload = Record<string, unknown> & { cwd?: string };

export function useSettingsScope(projectStore: SettingsScopeProjectStore) {
  const activeProjectCwd: { value: string } = computed(() => {
    const cwd = (projectStore?.state?.active || '').toString().trim();
    if (!cwd || cwd === '.') return '';
    return cwd;
  });

  function withProjectCwd(payload: ScopedPayload = {}): ScopedPayload {
    const next = payload && typeof payload === 'object' ? { ...payload } : {};
    if (activeProjectCwd.value) {
      return { ...next, cwd: activeProjectCwd.value };
    }
    return next;
  }

  function parseBoolPreference(value: unknown): boolean {
    if (typeof value === 'boolean') return value;
    if (typeof value === 'number') return value !== 0;
    if (typeof value === 'string') {
      const normalized = value.trim().toLowerCase();
      if (['1', 'true', 'yes', 'on'].includes(normalized)) return true;
      if (['0', 'false', 'no', 'off'].includes(normalized)) return false;
    }
    return false;
  }

  return { activeProjectCwd, withProjectCwd, parseBoolPreference };
}
