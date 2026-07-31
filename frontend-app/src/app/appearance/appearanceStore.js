import { createStore } from 'zustand/vanilla';
import { requiredAppStoragePort } from '../../shared/api/browser/browserStorage.js';
import {
  APPEARANCE_FIELD_KEYS,
  APPEARANCE_INITIAL_SETTINGS,
  APPEARANCE_STORAGE_KEY,
  LEGACY_THEME_STORAGE_KEY,
  appearanceRootProjection,
  migrateLegacyTheme,
  parseAppearanceEnvelope,
  parseAppearanceSettings,
  resolveAppearanceTheme,
  serializeAppearanceEnvelope,
} from './appearanceSchema.js';

export const APPEARANCE_UI_CONTROLS = Object.freeze({
  themeMode: 'setThemeMode',
  uiScale: 'setUiScale',
  accent: 'setAccent',
});

export function createBrowserAppearanceStore() {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    throw new Error('appearance bootstrap requires a browser with matchMedia');
  }
  return createAppearanceStore({
    matchMedia: window.matchMedia.bind(window),
    storage: requiredAppStoragePort('appearance storage'),
  });
}

export function applyAppearanceToElement(element, state) {
  if (!element || typeof element.setAttribute !== 'function' || !element.style) {
    throw new TypeError('appearance target must be a styleable element');
  }
  const settings = Object.fromEntries(APPEARANCE_FIELD_KEYS.map((field) => [field, state[field]]));
  const projection = appearanceRootProjection(settings, state.resolvedTheme);
  Object.entries(projection.attributes).forEach(([name, value]) => element.setAttribute(name, value));
  Object.entries(projection.styles).forEach(([name, value]) => element.style.setProperty(name, value));
  return projection;
}

export function applyAppearanceToRootTargets(documentTarget, state) {
  if (!documentTarget?.documentElement || !documentTarget?.body) {
    throw new Error('appearance document targets are required');
  }
  applyAppearanceToElement(documentTarget.documentElement, state);
  applyAppearanceToElement(documentTarget.body, state);
}

function assertStoragePort(storage) {
  if (
    !storage
    || typeof storage.get !== 'function'
    || typeof storage.set !== 'function'
    || typeof storage.remove !== 'function'
  ) {
    throw new TypeError('Appearance storage must provide get, set, and remove functions');
  }
}

function actionName(field) {
  return `set${field[0].toUpperCase()}${field.slice(1)}`;
}

function currentSettings(state) {
  return Object.fromEntries(APPEARANCE_FIELD_KEYS.map((field) => [field, state[field]]));
}

function createFieldActions(get, persist) {
  const actions = {};
  for (const field of APPEARANCE_FIELD_KEYS) {
    actions[APPEARANCE_STORE_ACTIONS[field]] = (value) => persist({
      ...currentSettings(get()),
      [field]: value,
    });
  }
  return actions;
}

export const APPEARANCE_STORE_ACTIONS = Object.freeze(Object.fromEntries(
  APPEARANCE_FIELD_KEYS.map((field) => [field, actionName(field)]),
));

export function loadAppearanceSettings(storage) {
  assertStoragePort(storage);
  const stored = storage.get(APPEARANCE_STORAGE_KEY);
  const legacy = storage.get(LEGACY_THEME_STORAGE_KEY);
  if (stored !== null && legacy !== null) {
    throw new Error('appearance configuration has conflicting current and legacy owners');
  }
  if (stored !== null) return parseAppearanceEnvelope(stored).settings;

  const settings = legacy === null
    ? APPEARANCE_INITIAL_SETTINGS
    : migrateLegacyTheme(legacy);
  storage.set(APPEARANCE_STORAGE_KEY, serializeAppearanceEnvelope(settings));
  if (legacy !== null) storage.remove(LEGACY_THEME_STORAGE_KEY);
  return settings;
}

export function createAppearanceStore({ matchMedia, storage }) {
  assertStoragePort(storage);
  const initial = loadAppearanceSettings(storage);
  const resolve = (settings) => resolveAppearanceTheme(settings.themeMode, matchMedia);
  return createStore((set, get) => {
    const persist = (settings) => {
      const parsed = parseAppearanceSettings(settings);
      storage.set(APPEARANCE_STORAGE_KEY, serializeAppearanceEnvelope(parsed));
      set({ ...parsed, resolvedTheme: resolve(parsed) });
    };
    const actions = createFieldActions(get, persist);
    const reload = () => {
      const settings = loadAppearanceSettings(storage);
      set({ ...settings, resolvedTheme: resolve(settings) });
    };
    const reset = () => persist(APPEARANCE_INITIAL_SETTINGS);
    const refreshSystemTheme = () => {
      const current = get();
      set({ resolvedTheme: resolve(current) });
    };
    return {
      ...initial,
      resolvedTheme: resolve(initial),
      ...actions,
      reload,
      reset,
      refreshSystemTheme,
    };
  });
}
