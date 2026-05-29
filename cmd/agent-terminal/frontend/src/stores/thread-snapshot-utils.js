// @ts-nocheck

export function mergeStringMapAtomic(current, source) {
  if (!source || typeof source !== 'object') return { next: current, changed: false };
  let next = current;
  let changed = false;
  for (const [key, value] of Object.entries(source)) {
    const str = (value || '').toString();
    if (next[key] === str) continue;
    if (!changed) {
      next = { ...(next || {}) };
      changed = true;
    }
    next[key] = str;
  }
  return { next, changed };
}

export function mergeObjectMapAtomic(current, source) {
  if (!source || typeof source !== 'object') return { next: current, changed: false };
  let next = current;
  let changed = false;
  for (const [key, value] of Object.entries(source)) {
    const normalized = value && typeof value === 'object' ? value : {};
    if (JSON.stringify(next[key]) === JSON.stringify(normalized)) continue;
    if (!changed) {
      next = { ...(next || {}) };
      changed = true;
    }
    Object.freeze(normalized);
    next[key] = normalized;
  }
  return { next, changed };
}

export function isShallowObjectEqual(left, right) {
  if (left === right) return true;
  if (!left || !right || typeof left !== 'object' || typeof right !== 'object') return left === right;
  const leftKeys = Object.keys(left);
  if (leftKeys.length !== Object.keys(right).length) return false;
  for (const key of leftKeys) {
    if (!Object.prototype.hasOwnProperty.call(right, key)) return false;
    const lv = left[key], rv = right[key];
    if (lv === rv) continue;
    if (Array.isArray(lv) && Array.isArray(rv)) {
      if (lv.length !== rv.length) return false;
      for (let i = 0; i < lv.length; i += 1) {
        if (!isShallowObjectEqual(lv[i], rv[i])) return false;
      }
      continue;
    }
    return false;
  }
  return true;
}

export function freezeTimelineItemsAtomic(source, current) {
  const prevItems = Array.isArray(current) ? current : null;
  const newItems = Array.isArray(source) ? source : [];
  let changed = !prevItems || prevItems.length !== newItems.length;
  const nextItems = new Array(newItems.length);
  for (let i = 0; i < newItems.length; i += 1) {
    const prevItem = prevItems?.[i];
    const nextItem = newItems[i];
    if (prevItem && isShallowObjectEqual(prevItem, nextItem)) {
      nextItems[i] = prevItem;
      continue;
    }
    if (nextItem && typeof nextItem === 'object') Object.freeze(nextItem);
    nextItems[i] = nextItem;
    changed = true;
  }
  return changed ? { items: Object.freeze(nextItems), changed } : { items: prevItems, changed };
}

export function normalizeProviderThreadID(value) {
  return (value || '').toString().trim();
}

export function normalizeAgentRuntimeEntry(value) {
  const normalized = value && typeof value === 'object' ? { ...value } : {};
  const providerThreadID = normalizeProviderThreadID(normalized.providerThreadId || normalized.provider_thread_id);
  if (providerThreadID) normalized.providerThreadId = providerThreadID;
  else delete normalized.providerThreadId;
  delete normalized.codexThreadId;
  delete normalized.codex_thread_id;
  return normalized;
}

export function normalizeAgentRuntimeMap(source) {
  if (!source || typeof source !== 'object') return {};
  const next = {};
  for (const [key, value] of Object.entries(source)) next[key] = normalizeAgentRuntimeEntry(value);
  return next;
}
