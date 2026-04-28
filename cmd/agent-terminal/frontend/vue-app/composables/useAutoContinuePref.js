// @ts-nocheck
// Phase 1.2 自动续接偏好：从 ui/preferences 读 taskHandoff.autoContinueOnAlert，缓存在模块级 ref 中。
// SettingsPage 保存时直接更新模块 ref，调度器（Phase 1.3+）立即看到新值。
import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';

const PREF_KEY = 'taskHandoff.autoContinueOnAlert';
const DEFAULT_AUTO_CONTINUE = true;

// 模块单例 ref：所有调用方共享同一个引用，SettingsPage 保存后立即同步。
const autoContinueOnAlert = ref(DEFAULT_AUTO_CONTINUE);
let loadPromise = null;

/**
 * 校验：必须是 boolean。
 * @param {unknown} v
 * @returns {boolean}
 */
export function isValidAutoContinuePref(v) {
  return typeof v === 'boolean';
}

/**
 * 异步加载（首次幂等）。失败保留默认值并 logWarn。
 * @returns {Promise<boolean>}
 */
export async function loadAutoContinuePref() {
  if (loadPromise) return loadPromise;
  loadPromise = (async () => {
    try {
      const res = await callAPI('ui/preferences/get', { key: PREF_KEY });
      if (isValidAutoContinuePref(res)) {
        autoContinueOnAlert.value = res;
      } else if (res != null) {
        logWarn('ui', 'autoContinuePref.invalid', { value: res });
      }
    } catch (err) {
      logWarn('ui', 'autoContinuePref.load_failed', { error: (err && err.message) || String(err) });
    }
    return autoContinueOnAlert.value;
  })();
  return loadPromise;
}

/**
 * 保存到 preferences。校验失败抛错；成功后立即更新模块 ref，所有订阅者同步刷新。
 * @param {boolean} value
 */
export async function saveAutoContinuePref(value) {
  if (!isValidAutoContinuePref(value)) {
    throw new Error('自动续接偏好必须是 boolean');
  }
  await callAPI('ui/preferences/set', { key: PREF_KEY, value });
  autoContinueOnAlert.value = value;
}

/**
 * 给调度器 / SettingsPage 用。返回模块共享 ref；首次访问时懒加载（不阻塞 UI）。
 * @returns {{ value: boolean }}
 */
export function useAutoContinuePref() {
  if (!loadPromise) loadAutoContinuePref();
  return autoContinueOnAlert;
}

// 仅供测试使用：重置内部状态以便不同 case 间隔离。
export function _resetAutoContinuePrefForTest() {
  autoContinueOnAlert.value = DEFAULT_AUTO_CONTINUE;
  loadPromise = null;
}
