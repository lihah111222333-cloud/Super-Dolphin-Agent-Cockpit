import { describe, expect, it } from 'vitest';
import {
  APPEARANCE_INITIAL_SETTINGS,
  APPEARANCE_STORAGE_KEY,
  LEGACY_THEME_STORAGE_KEY,
  parseAppearanceEnvelope,
  serializeAppearanceEnvelope,
} from './appearanceSchema.js';
import { createAppearanceStore, loadAppearanceSettings } from './appearanceStore.js';

function memoryStorage(entries = {}) {
  const values = new Map(Object.entries(entries));
  return {
    get: (key) => values.get(key) ?? null,
    remove: (key) => values.delete(key),
    set: (key, value) => values.set(key, value),
    values,
  };
}

const lightMedia = () => ({ matches: false });

describe('appearance store', () => {
  it('documents and persists the first-run initial settings', () => {
    const storage = memoryStorage();
    const store = createAppearanceStore({ matchMedia: lightMedia, storage });
    expect(store.getState()).toMatchObject({ ...APPEARANCE_INITIAL_SETTINGS, resolvedTheme: 'light' });
    expect(parseAppearanceEnvelope(storage.values.get(APPEARANCE_STORAGE_KEY)).settings)
      .toEqual(APPEARANCE_INITIAL_SETTINGS);
  });

  it('migrates valid legacy storage exactly once', () => {
    const storage = memoryStorage({ [LEGACY_THEME_STORAGE_KEY]: 'dark' });
    expect(loadAppearanceSettings(storage)).toEqual({ ...APPEARANCE_INITIAL_SETTINGS, themeMode: 'dark' });
    expect(storage.values.has(LEGACY_THEME_STORAGE_KEY)).toBe(false);
    expect(storage.values.has(APPEARANCE_STORAGE_KEY)).toBe(true);
  });

  it('blocks dual owners and invalid legacy data', () => {
    const current = serializeAppearanceEnvelope(APPEARANCE_INITIAL_SETTINGS);
    expect(() => loadAppearanceSettings(memoryStorage({
      [APPEARANCE_STORAGE_KEY]: current,
      [LEGACY_THEME_STORAGE_KEY]: 'dark',
    }))).toThrow('conflicting');
    expect(() => loadAppearanceSettings(memoryStorage({
      [LEGACY_THEME_STORAGE_KEY]: 'system',
    }))).toThrow('legacy appearance theme');
  });

  it('persists updates, reloads external data, and resets explicitly', () => {
    const storage = memoryStorage();
    const store = createAppearanceStore({ matchMedia: lightMedia, storage });
    store.getState().setThemeMode('dark');
    store.getState().setUiScale(125);
    store.getState().setAccent('mint');
    expect(store.getState()).toMatchObject({ themeMode: 'dark', uiScale: 125, accent: 'mint' });

    storage.set(APPEARANCE_STORAGE_KEY, serializeAppearanceEnvelope({
      themeMode: 'light',
      uiScale: 90,
      accent: 'rose',
    }));
    store.getState().reload();
    expect(store.getState()).toMatchObject({ themeMode: 'light', uiScale: 90, accent: 'rose' });

    store.getState().reset();
    expect(store.getState()).toMatchObject(APPEARANCE_INITIAL_SETTINGS);
  });
});
