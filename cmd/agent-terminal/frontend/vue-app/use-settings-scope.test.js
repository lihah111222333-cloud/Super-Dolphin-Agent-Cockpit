// @ts-nocheck
import { describe, expect, it } from 'vitest';

import { useSettingsScope } from './pages/settings/useSettingsScope.ts';

describe('useSettingsScope', () => {
  it('derives the active project cwd and omits dot scope', () => {
    const scoped = useSettingsScope({ state: { active: '/repo/project' } });
    const unscoped = useSettingsScope({ state: { active: '.' } });

    expect(scoped.activeProjectCwd.value).toBe('/repo/project');
    expect(unscoped.activeProjectCwd.value).toBe('');
  });

  it('merges cwd into payloads only when a scoped cwd exists', () => {
    const scoped = useSettingsScope({ state: { active: '/repo/project' } });
    const unscoped = useSettingsScope({ state: { active: '' } });

    expect(scoped.withProjectCwd({ key: 'abc' })).toEqual({ key: 'abc', cwd: '/repo/project' });
    expect(unscoped.withProjectCwd({ key: 'abc' })).toEqual({ key: 'abc' });
  });

  it('parses boolean-like preferences from booleans, numbers and strings', () => {
    const scope = useSettingsScope(null);

    expect(scope.parseBoolPreference(true)).toBe(true);
    expect(scope.parseBoolPreference(1)).toBe(true);
    expect(scope.parseBoolPreference('YES')).toBe(true);
    expect(scope.parseBoolPreference('off')).toBe(false);
    expect(scope.parseBoolPreference('unknown')).toBe(false);
  });
});
