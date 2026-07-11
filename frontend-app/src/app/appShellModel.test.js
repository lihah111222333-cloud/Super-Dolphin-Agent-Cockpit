import { describe, expect, it } from 'vitest';
import {
  APP_SHELL_STORE_KEYS,
  appPageFromPathname,
  appRouteForPage,
  normalizeAppPathname,
  normalizeColorTheme,
  selectAppShellStore,
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

  it('normalizes unsupported themes to the light default', () => {
    expect(normalizeColorTheme('dark')).toBe('dark');
    expect(normalizeColorTheme('light')).toBe('light');
    expect(normalizeColorTheme('system')).toBe('light');
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
    expect(selected.threadRecoveryPendingByThread).toBe('threadRecoveryPendingByThread-value');
    expect(selected.unrelatedLargeSlice).toBeUndefined();
  });
});
