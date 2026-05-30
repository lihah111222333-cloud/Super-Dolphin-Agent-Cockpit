import {
  ref,
  computed,
} from '../../lib/vue.esm-browser.prod.js';
import { isPlanSuperseded, resolvePlanItemKey } from '../utils/plan-utils.js';

const DISMISS_STORAGE_KEY = '__plan_dismissed_v2__';

function loadDismissedPlanKeys() {
  try {
    const raw = sessionStorage.getItem(DISMISS_STORAGE_KEY);
    return raw ? JSON.parse(raw) : {};
  } catch { return {}; }
}

function saveDismissedPlanKeys(map) {
  try { sessionStorage.setItem(DISMISS_STORAGE_KEY, JSON.stringify(map)); } catch {}
}

export function createPinnedPlanState(isCmd, selectedThreadId, activeTimeline) {
  const latestPlanItem = computed(() => {
    if (isCmd.value) return null;
    const list = activeTimeline.value || [];
    for (let index = list.length - 1; index >= 0; index -= 1) {
      const item = list[index];
      if (item?.kind !== 'plan') continue;
      if (isPlanSuperseded(item, index, list)) continue;
      const text = (item.text || '').toString().trim();
      if (!text) continue;
      return item;
    }
    return null;
  });
  const dismissedPlanKeyByThread = ref(loadDismissedPlanKeys());
  const activePinnedPlan = computed(() => {
    const threadId = (selectedThreadId.value || '').toString().trim();
    if (!threadId) return null;
    const item = latestPlanItem.value;
    if (!item) return null;
    const key = resolvePlanItemKey(item);
    if (!key || dismissedPlanKeyByThread.value?.[threadId] === key) return null;
    const text = (item.text || '').toString().trim();
    if (!text) return null;
    return {
      id: ((item?.id ?? '') || key).toString(),
      key,
      threadId,
      done: Boolean(item.done),
      statusText: item.done ? '完成' : '进行中',
      text,
    };
  });

  function dismissPinnedPlan() {
    const plan = activePinnedPlan.value;
    if (!plan) return;
    const next = { ...dismissedPlanKeyByThread.value, [plan.threadId]: plan.key };
    dismissedPlanKeyByThread.value = next;
    saveDismissedPlanKeys(next);
  }

  return { activePinnedPlan, dismissPinnedPlan };
}
