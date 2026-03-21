// @ts-nocheck
import { logWarn } from '../services/log.js';

const FENCE_DELIMITER_RE = /^(`{3,}|~{3,})/;
const BLOCKISH_TRAILING_LINE_RE = /^\s*(?:#{1,6}\s|>\s|\|.*\|?|(?:-{3,}|\*{3,}|_{3,})\s*$)/;

const INLINE_BALANCED_MARKDOWN_RE = /(`[^`\n]+`|\*\*[^*\n]+\*\*|(^|[^*])\*[^*\n]+\*([^*]|$)|~~[^~\n]+~~|\[[^\]\n]+\]\([^\)\n]+\))/;
const SENTENCE_END_RE = /[。！？!?；;：:.…](?:[)\]"'”’`*_~]+)?$/;

function normalizeStreamingMarkdownText(rawText) {
  return (rawText || '').toString().replace(/\r\n?/g, '\n');
}

function readFenceMarker(line) {
  const trimmed = (line || '').toString().trimStart();
  const match = trimmed.match(FENCE_DELIMITER_RE);
  if (!match) return null;
  return {
    char: match[1][0],
    size: match[1].length,
  };
}

function isFenceClose(line, fenceMarker) {
  if (!fenceMarker) return false;
  const nextMarker = readFenceMarker(line);
  if (!nextMarker) return false;
  return nextMarker.char === fenceMarker.char && nextMarker.size >= fenceMarker.size;
}

function countUnescapedToken(source, token) {
  if (!source || !token) return 0;
  let count = 0;
  for (let index = 0; index <= source.length - token.length; index += 1) {
    if (source.slice(index, index + token.length) !== token) continue;
    let slashCount = 0;
    for (let cursor = index - 1; cursor >= 0 && source[cursor] === '\\'; cursor -= 1) slashCount += 1;
    if (slashCount % 2 === 1) continue;
    count += 1;
    index += token.length - 1;
  }
  return count;
}

function hasBalancedPairs(source, openChar, closeChar) {
  let depth = 0;
  for (let index = 0; index < source.length; index += 1) {
    const char = source[index];
    if (char === '\\') {
      index += 1;
      continue;
    }
    if (char === openChar) depth += 1;
    else if (char === closeChar) {
      depth -= 1;
      if (depth < 0) return false;
    }
  }
  return depth === 0;
}

function hasUnclosedInlineMarkdown(line) {
  const normalized = (line || '').toString().trim();
  if (!normalized) return false;
  if (countUnescapedToken(normalized, '`') % 2 === 1) return true;
  if (countUnescapedToken(normalized, '**') % 2 === 1) return true;
  if (countUnescapedToken(normalized, '~~') % 2 === 1) return true;
  if (!hasBalancedPairs(normalized, '[', ']')) return true;
  if (normalized.includes('](') && !hasBalancedPairs(normalized, '(', ')')) return true;
  return false;
}

function shouldPromoteTrailingLine(line) {
  const normalized = (line || '').toString().trim();
  if (!normalized) return false;
  if (readFenceMarker(normalized)) return false;
  if (hasUnclosedInlineMarkdown(normalized)) return false;
  if (BLOCKISH_TRAILING_LINE_RE.test(normalized)) return true;
  if (SENTENCE_END_RE.test(normalized)) return true;
  return INLINE_BALANCED_MARKDOWN_RE.test(normalized);
}

function buildStreamingMarkdownState(text, renderAssistantBody, emptyState, done = false) {
  if (!text) return emptyState;
  const parts = done
    ? { stableText: text, tailText: '' }
    : splitStreamingMarkdownForDisplay(text);
  const html = parts.stableText ? renderAssistantBody(parts.stableText) : '';
  return Object.freeze({
    html,
    tailText: parts.tailText || '',
  });
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

export function splitStreamingMarkdownForDisplay(rawText) {
  const text = normalizeStreamingMarkdownText(rawText);
  if (!text) return { stableText: '', tailText: '' };

  const lines = text.split('\n');
  let boundary = 0;
  let offset = 0;
  let openFenceMarker = null;
  let openFenceBoundary = 0;

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const lineEnd = offset + line.length;
    const hasLineBreak = index < lines.length - 1;
    const fenceMarker = readFenceMarker(line);

    if (fenceMarker) {
      if (!openFenceMarker) {
        openFenceMarker = fenceMarker;
        openFenceBoundary = boundary;
      } else if (isFenceClose(line, openFenceMarker)) {
        openFenceMarker = null;
        boundary = hasLineBreak ? lineEnd + 1 : lineEnd;
      }
    } else if (!openFenceMarker && hasLineBreak) {
      boundary = lineEnd + 1;
    }

    offset = lineEnd + 1;
  }

  if (openFenceMarker) boundary = openFenceBoundary;
  else {
    const trailingText = text.slice(boundary);
    if (trailingText && shouldPromoteTrailingLine(trailingText)) boundary = text.length;
  }

  if (boundary <= 0) return { stableText: '', tailText: text };
  if (boundary >= text.length) return { stableText: text, tailText: '' };
  return {
    stableText: text.slice(0, boundary),
    tailText: text.slice(boundary),
  };
}

export function createStreamingMarkdownStateResolver(renderAssistantBody, onStateFlush = null, onStallDetected = null) {
  const cache = new Map();
  const displayedByItemId = new Map();
  const pendingByItemId = new Map();
  const emptyState = Object.freeze({ html: '', tailText: '' });
  const scheduleFrame = createFrameScheduler();
  let scheduledFrame = null;
  let staleGuardTimer = null;
  let disposed = false;

  function clearStaleGuard() {
    if (staleGuardTimer !== null) {
      clearTimeout(staleGuardTimer);
      staleGuardTimer = null;
    }
  }

  function getStateByText(text, done = false) {
    if (!text) return emptyState;
    if (done) {
      return buildStreamingMarkdownState(text, renderAssistantBody, emptyState, true);
    }
    if (cache.has(text)) return cache.get(text) || emptyState;
    const next = buildStreamingMarkdownState(text, renderAssistantBody, emptyState, false);
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
      return getStateByText(text, true);
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
    pendingByItemId.clear();
    displayedByItemId.clear();
    cancelFrame(scheduledFrame);
    scheduledFrame = null;
    clearStaleGuard();
  };

  return resolve;
}
