// @ts-nocheck
import { logWarn } from '../services/log.js';
import { measureTailHeight, getWidthVersion, getFontVersion, setInvalidateCallback } from '../services/pretext-layout.js';

function buildStreamingMarkdownState(text, emptyState) {
  if (!text) return emptyState;
  const heightPx = measureTailHeight(text);
  return Object.freeze({ text, heightPx });
}

function createFrameScheduler() {
  let lastRenderTime = 0;
  return function scheduleFrame(callback) {
    // Use a balanced strategy: we want to avoid main-thread saturation but also
    // ensure smooth visual updates. requestAnimationFrame syncs natively with display.
    if (typeof requestAnimationFrame === 'function') {
      let handleId = null;
      const loop = (timestamp) => {
        const now = timestamp || performance.now();
        // Throttle to ~30fps (32ms interval) so we don't block every single render frame
        // with heavy Markdown parsing tasks for huge outputs.
        if (!lastRenderTime || now - lastRenderTime >= 32) {
          lastRenderTime = now;
          callback();
        } else {
          handleId = requestAnimationFrame(loop);
        }
      };
      handleId = requestAnimationFrame(loop);
      return { type: 'raf', cancel: () => cancelAnimationFrame(handleId) };
    }
    return { type: 'timeout', id: setTimeout(callback, 32) };
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

export function createStreamingMarkdownStateResolver(onStateFlush = null, onStallDetected = null) {
  const cache = new Map();
  const displayedByItemId = new Map();
  const pendingByItemId = new Map();
  const emptyState = Object.freeze({ text: '', heightPx: 0 });
  let lastWidthVer = 0, lastFontVer = 0;
  const scheduleFrame = createFrameScheduler();
  let scheduledFrame = null;
  let staleGuardTimer = null;
  let disposed = false;

  setInvalidateCallback(() => {
    if (disposed) return;  // v4-fix: disposed 后不再触发
    const wv = getWidthVersion();
    if (wv !== lastWidthVer) {
      cache.clear();
      displayedByItemId.clear();
      lastWidthVer = wv;
      lastFontVer = getFontVersion();
      if (typeof onStateFlush === 'function') onStateFlush();
    }
  });

  function clearStaleGuard() {
    if (staleGuardTimer !== null) {
      clearTimeout(staleGuardTimer);
      staleGuardTimer = null;
    }
  }

  function getStateByText(text) {
    if (!text) return emptyState;
    const wv = getWidthVersion(), fv = getFontVersion();
    if (wv !== lastWidthVer || fv !== lastFontVer) {
      cache.clear();
      lastWidthVer = wv;
      lastFontVer = fv;
    }
    if (cache.has(text)) return cache.get(text) || emptyState;
    const next = buildStreamingMarkdownState(text, emptyState);
    cache.set(text, next);
    if (cache.size > 280) cache.delete(cache.keys().next().value);
    return next;
  }

  function flushPending() {
    scheduledFrame = null;
    if (disposed || pendingByItemId.size === 0) return;
    clearStaleGuard();
    let changed = false;
    const flushStart = performance.now();
    for (const [itemId, entry] of pendingByItemId.entries()) {
      const current = displayedByItemId.get(itemId);
      if (!entry.state) {
        entry.state = getStateByText(entry.text);
      }
      if (!current || current.text !== entry.text || current.state !== entry.state) {
        displayedByItemId.set(itemId, entry);
        changed = true;
      }
    }
    const flushDurationMs = Math.round(performance.now() - flushStart);
    const flushedCount = pendingByItemId.size;
    pendingByItemId.clear();
    if (changed && typeof onStateFlush === 'function') onStateFlush();
    // Warn if flush render took too long — this blocks main thread and causes UI stall
    if (flushDurationMs > 50 && typeof onStallDetected === 'function') {
      onStallDetected({
        reason: 'flush_slow',
        duration_ms: flushDurationMs,
        items_flushed: flushedCount,
        displayed_count: displayedByItemId.size,
      });
    }
    // Safety net: if new pending appeared during the heavy render above, re-schedule
    if (!disposed && pendingByItemId.size > 0) scheduleFlush();
  }

  function scheduleFlush() {
    if (scheduledFrame || disposed) return;
    scheduledFrame = scheduleFrame(flushPending);
  }

  const resolve = (item) => {
    const text = (item?.text || '').toString();
    const itemId = (item?.id || '').toString().trim();

    if (itemId) {
      const prevEntry = displayedByItemId.get(itemId) || pendingByItemId.get(itemId);
      const prevLen = prevEntry?.text?.length || 0;
      if (prevLen > 0 && text.length === 0) {
        logWarn('ui', 'chat.streaming.text_vanished', { item_id: itemId, prev_len: prevLen, done: item?.done });
      } else if (prevLen > 0 && text.length < prevLen && !item?.done) {
        logWarn('ui', 'chat.streaming.text_shrunk', { item_id: itemId, prev_len: prevLen, current_len: text.length, done: item?.done });
      }
    }

    if (!text) return emptyState;

    if (item?.done !== false || !itemId) {
      clearStaleGuard();
      if (itemId) {
        displayedByItemId.delete(itemId);
        pendingByItemId.delete(itemId);
      }
      return getStateByText(text);
    }

    const displayed = displayedByItemId.get(itemId);
    if (!displayed) {
      const nextState = getStateByText(text);
      const initial = { text, state: nextState };
      displayedByItemId.set(itemId, initial);
      return nextState;
    }
    if (displayed.text === text) {
      clearStaleGuard();
      return displayed.state;
    }

    const pending = pendingByItemId.get(itemId);
    if (!pending || pending.text !== text) {
      pendingByItemId.set(itemId, { text, state: null });
      scheduleFlush();
    }
    // Backstop: force flush if normal path hasn't resolved within 200ms
    clearStaleGuard();
    const backstopEnqueuedAt = performance.now();
    staleGuardTimer = setTimeout(() => {
      staleGuardTimer = null;
      if (disposed) return;
      const backstopDelayMs = Math.round(performance.now() - backstopEnqueuedAt);
      if (typeof onStallDetected === 'function') {
        onStallDetected({
          reason: 'stale_guard_fired',
          delay_ms: backstopDelayMs,
          pending_count: pendingByItemId.size,
          displayed_count: displayedByItemId.size,
        });
      }
      flushPending();
    }, 200);
    return displayed.state;
  };

  resolve.dispose = () => {
    disposed = true;
    setInvalidateCallback(null);
    pendingByItemId.clear();
    displayedByItemId.clear();
    cancelFrame(scheduledFrame);
    scheduledFrame = null;
    clearStaleGuard();
  };

  return resolve;
}
