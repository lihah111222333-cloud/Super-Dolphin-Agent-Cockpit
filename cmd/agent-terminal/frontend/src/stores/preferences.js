// @ts-nocheck
// Reactive preferences store: single source of truth for ui/preferences/*.
//
// Wires together:
//   * `ui/preferences/get` / `ui/preferences/set` RPCs (callAPI)
//   * The `ui/preferences/changed` bridge-event published by the Go backend
//     (internal/module/uistate -> internal/platform/eventsurface ->
//     internal/ui/wails/bridge -> wails 'bridge-event' channel)
//
// Same-process savePreference triggers an optimistic local notify, so
// subscribers update immediately without waiting for the bridge-event
// echo. The bridge-event listener still applies any out-of-band update
// (e.g. another window writing the same scope), keeping cross-window
// state consistent.

import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI, onBridgeEvent } from '../services/api.js';
import { logDebug, logError } from '../services/log.js';

const PREFERENCES_BRIDGE_METHOD = 'ui/preferences/changed';

const cache = new Map(); // cacheKey -> latest value
const listeners = new Map(); // cacheKey -> Set<callback>
const refs = new Map(); // cacheKey -> Vue ref

let bridgeAttached = false;
let bridgeAttempted = false;
let bridgeUnsubscribe = null;

function cacheKey(scope, key) {
  const normalizedScope = (scope || '').toString().trim();
  const normalizedKey = (key || '').toString().trim();
  return `${normalizedScope}\x1f${normalizedKey}`;
}

function notifyListeners(ck, value) {
  const set = listeners.get(ck);
  if (set && set.size > 0) {
    for (const cb of Array.from(set)) {
      try {
        cb(value);
      } catch {
        // ignore listener errors so a faulty subscriber cannot poison others
      }
    }
  }
  const r = refs.get(ck);
  if (r) r.value = value;
}

function applyChange(scope, key, value) {
  const ck = cacheKey(scope, key);
  const prev = cache.get(ck);
  cache.set(ck, value);
  if (prev !== value) {
    notifyListeners(ck, value);
  }
}

function attachBridge() {
  if (bridgeAttached || bridgeAttempted) return;
  bridgeAttempted = true;
  try {
    bridgeUnsubscribe = onBridgeEvent((evt) => {
      const type = (evt?.type || evt?.method || '').toString();
      if (type !== PREFERENCES_BRIDGE_METHOD) return;
      const payload = evt?.payload || evt?.params || {};
      const key = (payload.key || '').toString();
      if (!key) return;
      const scope = (payload.cwd || '').toString();
      applyChange(scope, key, payload.value);
    });
    bridgeAttached = true;
  } catch (error) {
    // bridge unavailable (test environment, runtime not ready, etc.).
    // Mark as attempted so we do not flood logs with retries; callers
    // can still rely on optimistic updates from savePreference.
    // Demoted to debug because this path is hit by test mocks and by
    // pages that import the store before wails runtime is ready.
    logDebug('preferences', 'bridge.attach.failed', {
      error: error?.message || String(error),
    });
  }
}

export function detachPreferenceBridge() {
  if (typeof bridgeUnsubscribe === 'function') {
    try {
      bridgeUnsubscribe();
    } catch {
      // ignore
    }
  }
  bridgeUnsubscribe = null;
  bridgeAttached = false;
  bridgeAttempted = false;
}

export function getPreferenceCached(key, scope = '') {
  const ck = cacheKey(scope, key);
  return cache.has(ck) ? cache.get(ck) : undefined;
}

export function getScopedPreferenceCached(key, scope = '') {
  return getPreferenceCached(key, (scope || '').toString().trim()) ?? getPreferenceCached(key);
}

export function onPreferenceChange(key, callback, scope = '') {
  if (typeof callback !== 'function') return () => {};
  attachBridge();
  const ck = cacheKey(scope, key);
  let set = listeners.get(ck);
  if (!set) {
    set = new Set();
    listeners.set(ck, set);
  }
  set.add(callback);
  return () => {
    const current = listeners.get(ck);
    if (!current) return;
    current.delete(callback);
    if (current.size === 0) listeners.delete(ck);
  };
}

export function preferenceRef(key, options = {}) {
  attachBridge();
  const scope = (options.scope || '').toString();
  const ck = cacheKey(scope, key);
  const existing = refs.get(ck);
  if (existing) return existing;
  const initial = cache.has(ck) ? cache.get(ck) : (options.default ?? null);
  const r = ref(initial);
  refs.set(ck, r);
  return r;
}

export async function loadPreference(key, scope = '') {
  attachBridge();
  const payload = { key };
  if (scope) payload.cwd = scope;
  try {
    const value = await callAPI('ui/preferences/get', payload);
    applyChange(scope, key, value);
    return value;
  } catch (error) {
    logError('preferences', 'load.failed', {
      key,
      scope,
      error: error?.message || String(error),
    });
    throw error;
  }
}

export async function savePreference(key, value, scope = '') {
  attachBridge();
  const payload = { key, value };
  if (scope) payload.cwd = scope;
  const ck = cacheKey(scope, key);
  const hadPrev = cache.has(ck);
  const prev = hadPrev ? cache.get(ck) : undefined;
  // Optimistic local apply so subscribers see the value immediately;
  // the backend bridge-event echo is idempotent (same value -> no-op).
  applyChange(scope, key, value);
  try {
    await callAPI('ui/preferences/set', payload);
    return value;
  } catch (error) {
    // Rollback the optimistic apply so other consumers do not observe a
    // value that was never actually persisted by the backend. Without
    // this rollback a failed save would leave every cross-page
    // subscriber stuck on a phantom value until something else writes.
    if (hadPrev) {
      applyChange(scope, key, prev);
    } else {
      cache.delete(ck);
      notifyListeners(ck, undefined);
    }
    logError('preferences', 'save.failed', {
      key,
      scope,
      error: error?.message || String(error),
    });
    throw error;
  }
}

// Test-only helper for resetting module state between vitest cases.
export function __resetPreferenceStoreForTest() {
  detachPreferenceBridge();
  cache.clear();
  listeners.clear();
  refs.clear();
}

export const PREFERENCES_BRIDGE_EVENT_NAME = PREFERENCES_BRIDGE_METHOD;
