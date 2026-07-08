import { APP_BRAND_NAME, APP_LANGUAGE_STORAGE_KEY, APP_LOCALES } from './appI18n.constants.js';
import { APP_COPY } from './appI18n.copy.js';

function normalizeAppLocale(value) {
  return value === APP_LOCALES.en ? APP_LOCALES.en : APP_LOCALES.zh;
}

function initialAppLocale() {
  if (typeof window === 'undefined') return APP_LOCALES.zh;
  let saved;
  try {
    const storage = window.localStorage;
    if (!storage || typeof storage.getItem !== 'function') {
      const error = new Error('app language storage is unavailable');
      error.name = 'AppI18nStorageUnavailableError';
      throw error;
    }
    saved = storage.getItem(APP_LANGUAGE_STORAGE_KEY);
  } catch (error) {
    if (error?.name === 'AppI18nStorageUnavailableError') throw error;
    const storageError = new Error('app language storage read failed');
    storageError.name = 'AppI18nStorageUnavailableError';
    storageError.cause = error;
    throw storageError;
  }
  if (saved) return normalizeAppLocale(saved);
  return APP_LOCALES.zh;
}

export {
  APP_BRAND_NAME,
  APP_COPY,
  APP_LANGUAGE_STORAGE_KEY,
  APP_LOCALES,
  initialAppLocale,
  normalizeAppLocale,
};

