// @ts-nocheck
// Phase 1.7c watchdog 偏好：从 ui/preferences 读 taskHandoff.threadWatchdog，
// 模块单例 ref，与 useAutoContinuePref 同模式但独立单列（不复用 autoContinueOnAlert）。
//
// false 时：handleBridgeEvent 戳点 + useThreadWatchdog scan 全部 skip。
import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';

const PREF_KEY = 'taskHandoff.threadWatchdog';
const DEFAULT_THREAD_WATCHDOG = true;

const threadWatchdogPref = ref(DEFAULT_THREAD_WATCHDOG);
const threadWatchdogPrefReady = ref(false);
let loadPromise = null;

export function isValidThreadWatchdogPref(v) {
  return typeof v === 'boolean';
}

export async function loadThreadWatchdogPref() {
  if (loadPromise) return loadPromise;
  loadPromise = (async () => {
    try {
      const res = await callAPI('ui/preferences/get', { key: PREF_KEY });
      if (isValidThreadWatchdogPref(res)) {
        threadWatchdogPref.value = res;
      } else if (res != null) {
        logWarn('ui', 'threadWatchdogPref.invalid', { value: res });
      }
    } catch (err) {
      logWarn('ui', 'threadWatchdogPref.load_failed', { error: (err && err.message) || String(err) });
    } finally {
      threadWatchdogPrefReady.value = true;
    }
    return threadWatchdogPref.value;
  })();
  return loadPromise;
}

export async function saveThreadWatchdogPref(value) {
  if (!isValidThreadWatchdogPref(value)) {
    throw new Error('watchdog 偏好必须是 boolean');
  }
  await callAPI('ui/preferences/set', { key: PREF_KEY, value });
  threadWatchdogPref.value = value;
}

export function useThreadWatchdogPref() {
  if (!loadPromise) loadThreadWatchdogPref();
  return threadWatchdogPref;
}

export function useThreadWatchdogPrefReady() {
  if (!loadPromise) loadThreadWatchdogPref();
  return threadWatchdogPrefReady;
}

export function _resetThreadWatchdogPrefForTest() {
  threadWatchdogPref.value = DEFAULT_THREAD_WATCHDOG;
  threadWatchdogPrefReady.value = false;
  loadPromise = null;
}
