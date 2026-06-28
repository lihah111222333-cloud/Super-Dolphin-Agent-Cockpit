import { computed, ref } from '../../../lib/vue.esm-browser.prod.js';
import { isPlanSuperseded } from '../../utils/plan-utils.js';

const VISIBLE_WINDOW = 100;
const BLOCK_MD_RE = new RegExp([
  '(^|\\n)\\s{0,3}([#>*\\-]|\\d+\\.)\\s',
  '```',
  '\\n\\s*\\n',
].join('|'));

function isDialogItem(item) {
  const kind = (item?.kind || '').toString().trim();
  return kind === 'assistant' || kind === 'user';
}

function isBottomOnlyStatusItem(item) {
  const kind = (item?.kind || '').toString().trim();
  return kind === 'thinking' || kind === 'command' || kind === 'tool';
}

function trailingProcessItems(source) {
  const all = Array.isArray(source) ? source : [];
  if (all.length === 0) return [];
  const bucket = [];
  for (let index = all.length - 1; index >= 0; index -= 1) {
    const item = all[index];
    if (!item || typeof item !== 'object') continue;
    const kind = (item.kind || '').toString().trim();
    if (!kind) continue;
    if (isDialogItem(item)) break;
    bucket.push(item);
  }
  return bucket.reverse();
}

function latestPresenceItems(source) {
  const all = Array.isArray(source) ? source : [];
  if (all.length === 0) return [];
  const bucket = [];
  let started = false;
  let seenDialogBoundary = false;
  for (let index = all.length - 1; index >= 0; index -= 1) {
    const item = all[index];
    if (!item || typeof item !== 'object') continue;
    const kind = (item.kind || '').toString().trim();
    if (!kind) continue;
    const dialog = isDialogItem(item);
    if (!started) {
      bucket.push(item);
      started = true;
      if (dialog) seenDialogBoundary = true;
      continue;
    }
    if (dialog && seenDialogBoundary) break;
    bucket.push(item);
    if (dialog) seenDialogBoundary = true;
  }
  return bucket.reverse();
}

function resolvePlanTimelineKey(item) {
  if (!item || typeof item !== 'object') return '';
  const id = (item.id || '').toString().trim();
  if (id) return `id:${id}`;
  const timestamp = (item.ts || '').toString().trim();
  const text = (item.text || '').toString().trim();
  if (!text) return '';
  if (timestamp) return `ts:${timestamp}`;
  return text.length > 32 ? text.substring(0, 32) : text;
}

import { logDebug, logWarn } from '../../services/log.js';

const TIMELINE_RENDER_DEBUG_MS = 4;
const TIMELINE_RENDER_WARN_MS = 16;

function getItemKey(item, index) {
  if (!item) return `idx-${index}`;
  const itemId = (item.id || '').toString().trim();
  if (itemId) return itemId;
  if (item.kind === 'plan') {
    const planKey = resolvePlanTimelineKey(item);
    if (planKey) return planKey;
  }
  const fallbackKey = `idx-${index}-${item.ts || ''}`;
  logWarn('ui', 'timeline.key.fallback', { index, fallbackKey, kind: item.kind, text: (item.text || '').substring(0, 20) });
  return fallbackKey;
}

function isShortReasoningItem(item) {
  if (!item || item.kind !== 'assistant' || !item.done) return false;
  const text = (item.text || '').toString();
  if (text.length === 0) return false;
  if (BLOCK_MD_RE.test(text)) return false;
  if (!text.includes(' ')) return false;
  const lines = text.split('\n').filter(Boolean);
  if (lines.length > 4 || text.length > 800) return false;
  return true;
}

export function useTimelineItems(props) {
  const visibleCount = ref(VISIBLE_WINDOW);
  const timelineItems = computed(() => {
    const all = Array.isArray(props.items) ? props.items : [];
    const filtered = all.filter((item, index) => !isBottomOnlyStatusItem(item) && !isPlanSuperseded(item, index, all));
    const pinnedId = props.pinnedPlanItemId;
    if (!props.pinnedPlanVisible || pinnedId === null || pinnedId === undefined || pinnedId === '') {
      return filtered;
    }
    return filtered;
  });
  const mergedTimelineItems = computed(() => {
    const start = performance.now();
    const all = timelineItems.value;
    if (all.length === 0) return all;
    const result = [];
    let index = 0;
    while (index < all.length) {
      const item = all[index];
      if (isShortReasoningItem(item)) {
        const group = [item];
        let cursor = index + 1;
        while (cursor < all.length && isShortReasoningItem(all[cursor])) {
          group.push(all[cursor]);
          cursor += 1;
        }
        if (group.length >= 2) {
          const lastItem = group[group.length - 1];
          result.push({
            ...lastItem,
            id: lastItem.id || `merged-${group[0].id || index}`,
            text: group.map((entry) => (entry.text || '').toString().trim()).join('\n\n'),
          });
          index = cursor;
          continue;
        }
      }
      result.push(item);
      index += 1;
    }
    const elapsed = performance.now() - start;
    if (elapsed > TIMELINE_RENDER_DEBUG_MS) {
      const log = elapsed > TIMELINE_RENDER_WARN_MS ? logWarn : logDebug;
      log('ui', 'chat.render.timeline.perf', {
        duration_ms: Math.round(elapsed * 100) / 100,
        debug_threshold_ms: TIMELINE_RENDER_DEBUG_MS,
        warn_threshold_ms: TIMELINE_RENDER_WARN_MS,
        source_items: all.length,
        merged_items: result.length
      });
    }
    return result;
  });
  const visibleOffset = computed(() => {
    const all = mergedTimelineItems.value;
    if (all.length <= visibleCount.value) return 0;
    return all.length - visibleCount.value;
  });

  const visibleItems = computed(() => {
    const all = mergedTimelineItems.value;
    const offset = visibleOffset.value;
    if (offset === 0) return all;
    return all.slice(offset);
  });

  const hasMore = computed(() => {
    return mergedTimelineItems.value.length > visibleCount.value;
  });

  function showMore() {
    visibleCount.value = Math.min(mergedTimelineItems.value.length, visibleCount.value + VISIBLE_WINDOW);
  }

  return {
    timelineItems,
    mergedTimelineItems,
    visibleItems,
    visibleOffset,
    hasMore,
    showMore,
    getItemKey,
    isDialogItem,
    isBottomOnlyStatusItem,
    trailingProcessItems,
    latestPresenceItems,
  };
}
