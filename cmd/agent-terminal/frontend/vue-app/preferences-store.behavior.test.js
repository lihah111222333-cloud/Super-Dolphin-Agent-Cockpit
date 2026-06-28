// @ts-nocheck
// Preferences store: end-to-end reactive bridge between
//   * `ui/preferences/set` / `ui/preferences/get` RPCs
//   * `ui/preferences/changed` bridge-event from the Go backend
// All consumers (useProviderMode, ProviderSettings, settings page) read
// the same cache and listen on the same per-key subscription set, so any
// mutation propagates without per-page reload.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  onBridgeEvent: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  onBridgeEvent: apiMock.onBridgeEvent,
}));

import {
  __resetPreferenceStoreForTest,
  getPreferenceCached,
  loadPreference,
  onPreferenceChange,
  savePreference,
} from './stores/preferences.js';

let bridgeCallback = null;

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.onBridgeEvent.mockReset();
  bridgeCallback = null;
  apiMock.onBridgeEvent.mockImplementation((cb) => {
    bridgeCallback = cb;
    return () => { bridgeCallback = null; };
  });
  __resetPreferenceStoreForTest();
});

afterEach(() => {
  __resetPreferenceStoreForTest();
});

describe('preferences store', () => {
  it('savePreference applies optimistically and persists via callAPI', async () => {
    const seen = [];
    onPreferenceChange('settings.theme', (v) => seen.push(v));
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });

    await savePreference('settings.theme', 'dark');

    expect(seen).toEqual(['dark']);
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', { key: 'settings.theme', value: 'dark' });
    expect(getPreferenceCached('settings.theme')).toBe('dark');
  });

  it('savePreference passes cwd when scope is provided', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    await savePreference('settings.x', 1, '/tmp/proj');
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', { key: 'settings.x', value: 1, cwd: '/tmp/proj' });
  });

  it('loadPreference fetches via callAPI and notifies listeners on first load', async () => {
    const seen = [];
    onPreferenceChange('settings.foo', (v) => seen.push(v));
    apiMock.callAPI.mockResolvedValueOnce('bar');

    const value = await loadPreference('settings.foo');

    expect(value).toBe('bar');
    expect(seen).toEqual(['bar']);
    expect(getPreferenceCached('settings.foo')).toBe('bar');
  });

  it('bridge-event ui/preferences/changed updates the cache and notifies', () => {
    const seen = [];
    onPreferenceChange('settings.lang', (v) => seen.push(v));
    expect(typeof bridgeCallback).toBe('function');

    bridgeCallback({ type: 'ui/preferences/changed', payload: { key: 'settings.lang', value: 'zh' } });

    expect(seen).toEqual(['zh']);
    expect(getPreferenceCached('settings.lang')).toBe('zh');
  });

  it('bridge-event for unrelated key is ignored', () => {
    const seen = [];
    onPreferenceChange('settings.lang', (v) => seen.push(v));

    bridgeCallback({ type: 'ui/preferences/changed', payload: { key: 'settings.other', value: 'noise' } });
    bridgeCallback({ type: 'cron/job/runStateChanged', payload: { key: 'settings.lang', value: 'noise' } });

    expect(seen).toEqual([]);
  });

  it('bridge-event respects scope (cwd) when filtering subscribers', () => {
    const seenGlobal = [];
    const seenScoped = [];
    onPreferenceChange('settings.k', (v) => seenGlobal.push(v));
    onPreferenceChange('settings.k', (v) => seenScoped.push(v), '/proj/a');

    bridgeCallback({ type: 'ui/preferences/changed', payload: { key: 'settings.k', value: 'global', cwd: '' } });
    bridgeCallback({ type: 'ui/preferences/changed', payload: { key: 'settings.k', value: 'scoped', cwd: '/proj/a' } });

    expect(seenGlobal).toEqual(['global']);
    expect(seenScoped).toEqual(['scoped']);
  });

  it('onPreferenceChange returns unsubscribe', () => {
    const seen = [];
    const off = onPreferenceChange('settings.t', (v) => seen.push(v));

    bridgeCallback({ type: 'ui/preferences/changed', payload: { key: 'settings.t', value: 1 } });
    off();
    bridgeCallback({ type: 'ui/preferences/changed', payload: { key: 'settings.t', value: 2 } });

    expect(seen).toEqual([1]);
  });

  it('savePreference rolls back the optimistic apply when the RPC fails', async () => {
    const seen = [];
    onPreferenceChange('settings.crash', (v) => seen.push(v));
    apiMock.callAPI.mockRejectedValueOnce(new Error('save failed'));

    await expect(savePreference('settings.crash', 'attempted')).rejects.toThrow('save failed');

    // Subscribers see optimistic 'attempted', then a rollback to undefined.
    expect(seen).toEqual(['attempted', undefined]);
    expect(getPreferenceCached('settings.crash')).toBeUndefined();
  });

  it('savePreference rollback restores the previously cached value when one existed', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ ok: true });
    await savePreference('settings.lang', 'en');
    expect(getPreferenceCached('settings.lang')).toBe('en');

    apiMock.callAPI.mockRejectedValueOnce(new Error('boom'));
    await expect(savePreference('settings.lang', 'zh')).rejects.toThrow('boom');

    // Rolled back to the prior 'en', not stuck on the failed 'zh'.
    expect(getPreferenceCached('settings.lang')).toBe('en');
  });
});
