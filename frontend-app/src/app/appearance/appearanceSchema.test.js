import { describe, expect, it } from 'vitest';
import {
  APPEARANCE_INITIAL_SETTINGS,
  appearanceRootProjection,
  migrateLegacyTheme,
  parseAppearanceEnvelope,
  resolveAppearanceTheme,
  serializeAppearanceEnvelope,
} from './appearanceSchema.js';

describe('appearance schema', () => {
  it('roundtrips the exact v1 envelope', () => {
    const raw = serializeAppearanceEnvelope(APPEARANCE_INITIAL_SETTINGS);
    expect(parseAppearanceEnvelope(raw)).toEqual({
      version: 1,
      settings: APPEARANCE_INITIAL_SETTINGS,
    });
  });

  it.each([
    ['missing', JSON.stringify({ version: 1, settings: { themeMode: 'system', uiScale: 100 } })],
    ['unknown', JSON.stringify({ version: 1, settings: { ...APPEARANCE_INITIAL_SETTINGS, extra: true } })],
    ['stale', JSON.stringify({ version: 0, settings: APPEARANCE_INITIAL_SETTINGS })],
    ['malformed', '{bad'],
  ])('rejects %s persisted data', (_case, raw) => {
    expect(() => parseAppearanceEnvelope(raw)).toThrow();
  });

  it('migrates only explicit legacy themes', () => {
    expect(migrateLegacyTheme('dark')).toEqual({ ...APPEARANCE_INITIAL_SETTINGS, themeMode: 'dark' });
    expect(() => migrateLegacyTheme('system')).toThrow('legacy appearance theme');
  });

  it('resolves system from matchMedia and projects every root attribute', () => {
    const resolved = resolveAppearanceTheme('system', () => ({ matches: true }));
    const projection = appearanceRootProjection(APPEARANCE_INITIAL_SETTINGS, resolved);
    expect(resolved).toBe('dark');
    expect(projection.attributes).toMatchObject({
      'data-accent': 'violet',
      'data-theme': 'dark',
      'data-theme-mode': 'system',
      'data-ui-scale': '100',
    });
    expect(projection.styles['--ui-scale']).toBe('1');
  });
});
