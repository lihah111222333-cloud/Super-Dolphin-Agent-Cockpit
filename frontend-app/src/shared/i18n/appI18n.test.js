import { afterEach, describe, expect, it } from 'vitest';

import { APP_LANGUAGE_STORAGE_KEY, APP_LOCALES, initialAppLocale, normalizeAppLocale } from './appI18n.js';

afterEach(() => {
  window.localStorage.clear();
});

describe('appI18n', () => {
  it('normalizes unsupported locales to Chinese', () => {
    expect(normalizeAppLocale('fr')).toBe(APP_LOCALES.zh);
    expect(normalizeAppLocale(APP_LOCALES.en)).toBe(APP_LOCALES.en);
  });

  it('loads the saved locale from localStorage', () => {
    window.localStorage.setItem(APP_LANGUAGE_STORAGE_KEY, APP_LOCALES.en);

    expect(initialAppLocale()).toBe(APP_LOCALES.en);
  });
});
