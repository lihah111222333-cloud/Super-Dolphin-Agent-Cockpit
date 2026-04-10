// @ts-nocheck
import { logWarn } from '../services/log.js';
import { measureTailHeight, getWidthVersion, getFontVersion, setInvalidateCallback } from '../services/pretext-layout.js';

function buildStreamingMarkdownState(text, emptyState) {
  if (!text) return emptyState;
  const heightPx = measureTailHeight(text);
  return Object.freeze({ text, heightPx });
}

function createFrameScheduler() {
  return function scheduleFrame(callback) {
    // Rely natively on requestAnimationFrame to synchronize with display hardware (60Hz / 120Hz).
    // Artificial 32ms gating causes frame pacing misalignment (jitter) and dropping frames during stream interpolation.
    if (typeof requestAnimationFrame === 'function') {
      const handleId = requestAnimationFrame(callback);
      return { type: 'raf', cancel: () => cancelAnimationFrame(handleId) };
    }
    return { type: 'timeout', id: setTimeout(callback, 16) };
  };
}

function cancelFrame(handle) {
  if (!handle) return;
  if (handle.type === 'raf' && typeof handle.cancel === 'function') {
    handle.cancel();
  } else if (handle.id) {
    clearTimeout(handle.id);
  }
}

function clearStaleGuard(state) {
  if (state.staleGuardTimer !== null) {
    clearTimeout(state.staleGuardTimer);
    state.staleGuardTimer = null;
  }
}

function getStateByText(state, text) {
  if (!text) return state.emptyState;
  const wv = getWidthVersion();
  const fv = getFontVersion();
  if (wv !== state.lastWidthVer || fv !== state.lastFontVer) {
    state.cache.clear();
    state.lastWidthVer = wv;
    state.lastFontVer = fv;
  }
  if (state.cache.has(text)) return state.cache.get(text) || state.emptyState;
  const next = buildStreamingMarkdownState(text, state.emptyState);
  state.cache.set(text, next);
  if (state.cache.size > 280) state.cache.delete(state.cache.keys().next().value);
  return next;
}

function handleLayoutInvalidation(state) {
  if (state.disposed) return;
  const wv = getWidthVersion();
  if (wv !== state.lastWidthVer) {
    state.cache.clear();
    state.displayedByItemId.clear();
    state.lastWidthVer = wv;
    state.lastFontVer = getFontVersion();
    if (typeof state.onStateFlush === 'function') state.onStateFlush();
  }
}

function flushPending(state) {
  state.scheduledFrame = null;
  if (state.disposed || state.pendingByItemId.size === 0) return;
  clearStaleGuard(state);
  let changed = false;
  const flushStart = performance.now();
  
  for (const [itemId, entry] of state.pendingByItemId.entries()) {
    const current = state.displayedByItemId.get(itemId);
    if (!entry.state) entry.state = getStateByText(state, entry.text);
    if (!current || current.text !== entry.text || current.state !== entry.state) {
      state.displayedByItemId.set(itemId, entry);
      changed = true;
    }
  }

  const flushDurationMs = Math.round(performance.now() - flushStart);
  const flushedCount = state.pendingByItemId.size;
  state.pendingByItemId.clear();
  
  if (changed && typeof state.onStateFlush === 'function') state.onStateFlush();
  
  if (flushDurationMs > 50 && typeof state.onStallDetected === 'function') {
    state.onStallDetected({
      reason: 'flush_slow',
      duration_ms: flushDurationMs,
      items_flushed: flushedCount,
      displayed_count: state.displayedByItemId.size,
    });
  }
  
  if (!state.disposed && state.pendingByItemId.size > 0) scheduleFlush(state);
}

function scheduleFlush(state) {
  if (state.scheduledFrame || state.disposed) return;
  state.scheduledFrame = state.scheduleFrame(() => flushPending(state));
}

function resolveStreamingState(state, item) {
  const text = (item?.text || '').toString();
  const itemId = (item?.id || '').toString().trim();

  if (itemId) {
    const prevEntry = state.displayedByItemId.get(itemId) || state.pendingByItemId.get(itemId);
    const prevLen = prevEntry?.text?.length || 0;
    if (prevLen > 0 && text.length === 0) {
      logWarn('ui', 'chat.streaming.text_vanished', { item_id: itemId, prev_len: prevLen, done: item?.done });
    } else if (prevLen > 0 && text.length < prevLen && !item?.done) {
      logWarn('ui', 'chat.streaming.text_shrunk', { item_id: itemId, prev_len: prevLen, current_len: text.length, done: item?.done });
    }
  }

  if (!text) return state.emptyState;
  if (item?.done !== false || !itemId) {
    clearStaleGuard(state);
    if (itemId) {
      state.displayedByItemId.delete(itemId);
      state.pendingByItemId.delete(itemId);
    }
    return getStateByText(state, text);
  }

  const displayed = state.displayedByItemId.get(itemId);
  if (!displayed) {
    const nextState = getStateByText(state, text);
    const initial = { text, state: nextState };
    state.displayedByItemId.set(itemId, initial);
    return nextState;
  }
  if (displayed.text === text) {
    clearStaleGuard(state);
    return displayed.state;
  }

  const pending = state.pendingByItemId.get(itemId);
  if (!pending || pending.text !== text) {
    state.pendingByItemId.set(itemId, { text, state: null });
    scheduleFlush(state);
  }
  clearStaleGuard(state);
  const backstopEnqueuedAt = performance.now();
  state.staleGuardTimer = setTimeout(() => {
    state.staleGuardTimer = null;
    if (state.disposed) return;
    const backstopDelayMs = Math.round(performance.now() - backstopEnqueuedAt);
    if (typeof state.onStallDetected === 'function') {
      state.onStallDetected({
        reason: 'stale_guard_fired',
        delay_ms: backstopDelayMs,
        pending_count: state.pendingByItemId.size,
        displayed_count: state.displayedByItemId.size,
      });
    }
    flushPending(state);
  }, 200);
  return displayed.state;
}

function disposeStreamingMarkdownStateResolver(state) {
  state.disposed = true;
  setInvalidateCallback(null);
  state.pendingByItemId.clear();
  state.displayedByItemId.clear();
  cancelFrame(state.scheduledFrame);
  state.scheduledFrame = null;
  clearStaleGuard(state);
}

export function createStreamingMarkdownStateResolver(onStateFlush = null, onStallDetected = null) {
  const state = {
    cache: new Map(),
    displayedByItemId: new Map(),
    pendingByItemId: new Map(),
    emptyState: Object.freeze({ text: '', heightPx: 0 }),
    lastWidthVer: 0,
    lastFontVer: 0,
    scheduleFrame: createFrameScheduler(),
    scheduledFrame: null,
    staleGuardTimer: null,
    disposed: false,
    onStateFlush,
    onStallDetected,
  };
  setInvalidateCallback(() => handleLayoutInvalidation(state));
  const resolve = (item) => resolveStreamingState(state, item);
  resolve.dispose = () => disposeStreamingMarkdownStateResolver(state);
  return resolve;
}
