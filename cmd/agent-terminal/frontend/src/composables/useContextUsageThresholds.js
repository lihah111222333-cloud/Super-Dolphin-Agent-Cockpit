// @ts-nocheck
// Phase 1 阈值配置：从 ui/preferences 读 contextUsageAlerts.thresholds，缓存在模块级 ref 中。
// SettingsPage 保存时直接更新模块 ref，UnifiedChatPage 的 useThreadStatus 立即看到新值。
import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';
import { DEFAULT_CONTEXT_USAGE_THRESHOLDS } from '../utils/format-utils.js';

const PREF_KEY = 'contextUsageAlerts.thresholds';

// 模块单例 ref：所有调用方共享同一个引用，SettingsPage 保存后立即同步。
const tokenThresholds = ref([...DEFAULT_CONTEXT_USAGE_THRESHOLDS]);
let loadPromise = null;

/**
 * 校验：必须是长度 3 的数组，每项是 (0, 100) 之间的有限数，且严格升序。
 * @param {unknown} arr
 * @returns {boolean}
 */
export function isValidThresholds(arr) {
  if (!Array.isArray(arr) || arr.length !== 3) return false;
  const nums = arr.map((v) => Number(v));
  if (!nums.every((n) => Number.isFinite(n) && n > 0 && n < 100)) return false;
  return nums[0] < nums[1] && nums[1] < nums[2];
}

/**
 * 异步加载（首次幂等）。失败保留默认值并 logWarn。
 * @returns {Promise<number[]>}
 */
export async function loadContextUsageThresholds() {
  if (loadPromise) return loadPromise;
  loadPromise = (async () => {
    try {
      const res = await callAPI('ui/preferences/get', { key: PREF_KEY });
      if (isValidThresholds(res)) {
        tokenThresholds.value = res.map(Number);
      } else if (res != null) {
        logWarn('ui', 'contextUsageThresholds.invalid', { value: res });
      }
    } catch (err) {
      logWarn('ui', 'contextUsageThresholds.load_failed', { error: (err && err.message) || String(err) });
      loadPromise = null; // 允许后续重试，避免冷启动失败后永远锁定默认值
    }
    return tokenThresholds.value;
  })();
  return loadPromise;
}

/**
 * 保存到 preferences。校验失败抛错；成功后立即更新模块 ref，所有订阅者同步刷新。
 * @param {number[]} values
 */
export async function saveContextUsageThresholds(values) {
  if (!isValidThresholds(values)) {
    throw new Error('阈值必须是 3 个 (0, 100) 之间的数字，且严格升序');
  }
  const nums = values.map(Number);
  await callAPI('ui/preferences/set', { key: PREF_KEY, value: nums });
  tokenThresholds.value = nums;
}

/**
 * 给 useThreadStatus 用。返回模块共享 ref；首次访问时懒加载（不阻塞 UI）。
 * @returns {{ value: number[] }}
 */
export function useContextUsageThresholds() {
  if (!loadPromise) loadContextUsageThresholds();
  return tokenThresholds;
}

// 仅供测试使用：重置内部状态以便不同 case 间隔离。
export function _resetContextUsageThresholdsForTest() {
  tokenThresholds.value = [...DEFAULT_CONTEXT_USAGE_THRESHOLDS];
  loadPromise = null;
}
