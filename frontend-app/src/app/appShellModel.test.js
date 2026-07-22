import { describe, expect, it, vi } from 'vitest';
import {
  APP_SHELL_STORE_KEYS,
  THEME_STORAGE_KEY,
  appPageFromPathname,
  appRouteForPage,
  getStoredTheme,
  normalizeAppPathname,
  normalizeColorTheme,
  selectAppShellStore,
  syncThemeDOM,
} from './appShellModel.js';

describe('app shell model', () => {
  it('keeps app routing aliases in one testable matrix', () => {
    expect(normalizeAppPathname('/dags/')).toBe('/dags');
    expect(appPageFromPathname('/')).toBe('chat');
    expect(appPageFromPathname('/chat')).toBe('chat');
    expect(appPageFromPathname('/dags')).toBe('workflows');
    expect(appPageFromPathname('/workflows')).toBe('workflows');
    expect(appPageFromPathname('/memory-center')).toBe('memory');
    expect(appPageFromPathname('/shared-files')).toBe('files');
    expect(appRouteForPage('workflows')).toBe('/dags');
    expect(appRouteForPage('settings')).toBe('/settings');
    expect(appRouteForPage('unknown')).toBe('/');
  });

  it('accepts only explicit light and dark themes', () => {
    expect(normalizeColorTheme('dark')).toBe('dark');
    expect(normalizeColorTheme('light')).toBe('light');
    expect(() => normalizeColorTheme('system')).toThrowError(new Error('invalid color theme'));
  });

  it('does not mutate theme attributes when the requested theme is invalid', () => {
    document.documentElement.setAttribute('data-theme', 'dark');
    document.body.setAttribute('data-theme', 'dark');

    expect(() => syncThemeDOM('system')).toThrowError(new Error('invalid color theme'));
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    expect(document.body.getAttribute('data-theme')).toBe('dark');
  });

  it('throws when the theme document is unavailable', () => {
    vi.stubGlobal('document', undefined);
    try {
      expect(() => syncThemeDOM('light')).toThrow();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('uses light for the first run when no theme has been stored', () => {
    window.localStorage.removeItem(THEME_STORAGE_KEY);

    expect(getStoredTheme()).toBe('light');
  });

  it('limits AppShell subscriptions to the declared store surface', () => {
    const state = Object.fromEntries(APP_SHELL_STORE_KEYS.map((key) => [key, `${key}-value`]));
    state.unrelatedLargeSlice = 'do-not-subscribe';

    const selected = selectAppShellStore(state);

    expect(Object.keys(selected)).toEqual([...APP_SHELL_STORE_KEYS]);
    expect(selected.activePage).toBe('activePage-value');
    expect(selected.skillRevision).toBe('skillRevision-value');
    expect(selected.workflowRevision).toBe('workflowRevision-value');
    expect(APP_SHELL_STORE_KEYS).toContain('threadRecoveryPendingByThread');
    expect(APP_SHELL_STORE_KEYS).toContain('captureThreadSelection');
    expect(APP_SHELL_STORE_KEYS).toContain('composerCapabilities');
    expect(APP_SHELL_STORE_KEYS).not.toContain('rightPanelWidth');
    expect(APP_SHELL_STORE_KEYS).not.toContain('setRightPanelWidth');
    expect(selected.threadRecoveryPendingByThread).toBe('threadRecoveryPendingByThread-value');
    expect(selected.unrelatedLargeSlice).toBeUndefined();
  });
});
