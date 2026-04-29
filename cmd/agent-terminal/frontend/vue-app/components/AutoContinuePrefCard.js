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
import {
  loadThreadWatchdogPref,
  saveThreadWatchdogPref,
  useThreadWatchdogPref,
} from '../composables/useThreadWatchdogPref.js';

export const AutoContinuePrefCard = {
  name: 'AutoContinuePrefCard',
  setup() {
    const enabledRef = useAutoContinuePref();
    const watchdogRef = useThreadWatchdogPref();
    const saving = ref(false);
    const watchdogSaving = ref(false);
    const error = ref('');
    const watchdogError = ref('');

    onMounted(() => {
      loadAutoContinuePref().catch(() => {});
      loadThreadWatchdogPref().catch(() => {});
    });

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

    async function onToggleWatchdog(event) {
      const next = Boolean(event && event.target && event.target.checked);
      watchdogSaving.value = true;
      watchdogError.value = '';
      try {
        await saveThreadWatchdogPref(next);
      } catch (err) {
        watchdogError.value = (err && err.message) || String(err);
        logWarn('ui', 'threadWatchdogPrefCard.save_failed', { value: next, error: watchdogError.value });
      } finally {
        watchdogSaving.value = false;
      }
    }

    return {
      enabledRef, saving, error, onToggle,
      watchdogRef, watchdogSaving, watchdogError, onToggleWatchdog,
    };
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
        <label class="auto-continue-pref-toggle">
          <input
            type="checkbox"
            class="auto-continue-pref-input"
            data-testid="auto-continue-pref-checkbox"
            :checked="enabledRef"
            :disabled="saving"
            @change="onToggle"
          />
          <span class="auto-continue-pref-track" aria-hidden="true">
            <span class="auto-continue-pref-thumb"></span>
          </span>
          <span class="auto-continue-pref-status">{{ enabledRef ? '开启' : '关闭' }}</span>
          <span v-if="saving" class="auto-continue-pref-saving">保存中…</span>
        </label>
        <span v-if="error" data-testid="auto-continue-pref-error" class="auto-continue-pref-error">{{ error }}</span>
      </div>
      <div class="data-row-vue">
        <strong>thread watchdog（事件停滞检测）</strong>
        <span>
          后端事件流 180s+ 停滞时自动检测：task thread 静悄悄发“继续”；普通对话由 banner 弹按钮由用户主动点。门限：per-thread 60s/1 + 全局 5min/10。
        </span>
      </div>
      <div class="data-row-vue">
        <label class="auto-continue-pref-toggle">
          <input
            type="checkbox"
            class="auto-continue-pref-input"
            data-testid="thread-watchdog-pref-checkbox"
            :checked="watchdogRef"
            :disabled="watchdogSaving"
            @change="onToggleWatchdog"
          />
          <span class="auto-continue-pref-track" aria-hidden="true">
            <span class="auto-continue-pref-thumb"></span>
          </span>
          <span class="auto-continue-pref-status">{{ watchdogRef ? '开启' : '关闭' }}</span>
          <span v-if="watchdogSaving" class="auto-continue-pref-saving">保存中…</span>
        </label>
        <span v-if="watchdogError" data-testid="thread-watchdog-pref-error" class="auto-continue-pref-error">{{ watchdogError }}</span>
      </div>
    </div>
  `,
};
