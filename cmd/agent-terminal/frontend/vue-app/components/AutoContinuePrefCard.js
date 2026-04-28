// @ts-nocheck
// Phase 1.5：自动续接偏好开关卡片。挂在 DagsPage 顶部。
// 自治：直接 use*Pref + save*Pref，父级无需传递。
// 偏好作用域：仅自动化任务（task thread）。普通对话不受影响。

import { onMounted, ref } from '../../lib/vue.esm-browser.prod.js';
import { logWarn } from '../services/log.js';
import {
  loadAutoContinuePref,
  saveAutoContinuePref,
  useAutoContinuePref,
} from '../composables/useAutoContinuePref.js';

export const AutoContinuePrefCard = {
  name: 'AutoContinuePrefCard',
  setup() {
    const enabledRef = useAutoContinuePref();
    const saving = ref(false);
    const error = ref('');

    onMounted(() => { loadAutoContinuePref().catch(() => {}); });

    async function onToggle(event) {
      const next = Boolean(event && event.target && event.target.checked);
      saving.value = true;
      error.value = '';
      try {
        await saveAutoContinuePref(next);
      } catch (err) {
        error.value = (err && err.message) || String(err);
        logWarn('ui', 'autoContinuePrefCard.save_failed', { value: next, error: error.value });
      } finally {
        saving.value = false;
      }
    }

    return { enabledRef, saving, error, onToggle };
  },
  template: `
    <div class="data-card-vue auto-continue-pref-card" data-testid="auto-continue-pref-card">
      <div class="data-row-vue">
        <strong>自动续接（自动化任务）</strong>
        <span>
          task thread 撞 token critical / 进程崩溃时自动续命：优先压缩上下文 → 失败则起新对话继承摘要 → 进程崩溃尝试恢复。
          失败时在顶部 banner 显示"一键重试"。<strong>不影响普通对话。</strong>
        </span>
      </div>
      <div class="data-row-vue">
        <label class="auto-continue-pref-toggle" style="display:inline-flex; align-items:center; gap:8px; cursor:pointer;">
          <input
            type="checkbox"
            data-testid="auto-continue-pref-checkbox"
            :checked="enabledRef.value"
            :disabled="saving"
            @change="onToggle"
          />
          <span>{{ enabledRef.value ? '已启用' : '已关闭' }}</span>
          <span v-if="saving" style="opacity:0.7;">保存中…</span>
        </label>
        <span v-if="error" data-testid="auto-continue-pref-error" style="color:var(--color-danger,#c33); margin-left:12px;">{{ error }}</span>
      </div>
    </div>
  `,
};
