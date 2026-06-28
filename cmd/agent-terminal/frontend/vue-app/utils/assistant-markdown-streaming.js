// @ts-nocheck
import { logWarn } from '../services/log.js';
import { measureTailHeight, getWidthVersion, getFontVersion, setInvalidateCallback } from '../services/pretext-layout.js';

const FENCE_DELIMITER_RE = /^(`{3,}|~{3,})/;
const BLOCKISH_TRAILING_LINE_RE = new RegExp(
  String.raw`^\s*(#{1,6}\s|>\s|\|.*\|?|(-{3,}|\*{3,}|_{3,})\s*$)`,
);
const INLINE_BALANCED_MARKDOWN_RE = /(`[^`\n]+`|\*\*[^*\n]+\*\*|(^|[^*])\*[^*\n]+\*([^*]|$)|~~[^~\n]+~~|\[[^\]\n]+\]\([^\)\n]+\))/;
const SENTENCE_END_RE = new RegExp(
  String.raw`[?????;.!?]([)\]"'??*_~]+)?$`,
);

function normalizeStreamingMarkdownText(rawText) {
  return (rawText || '').toString().replace(/\r\n?/g, '\n');
}

function readFenceMarker(line) {
  const trimmed = (line || '').toString().trimStart();
  const match = trimmed.match(FENCE_DELIMITER_RE);
  if (!match) return null;
  return { char: match[1][0], size: match[1].length };
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

function buildStreamingMarkdownState(text, renderAssistantBody, emptyState) {
  if (!text) return emptyState;
  const parts = splitStreamingMarkdownForDisplay(text);
  const html = parts.stableText ? renderAssistantBody(parts.stableText) : '';
  const tailText = parts.tailText || '';
  const heightPx = tailText ? measureTailHeight(tailText) : 0;
  return Object.freeze({ text, html, tailText, heightPx });
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

function getStateByText(state, text, renderAssistantBody) {
  if (!text) return state.emptyState;
  const wv = getWidthVersion();
  const fv = getFontVersion();
  if (wv !== state.lastWidthVer || fv !== state.lastFontVer) {
    state.cache.clear();
    state.lastWidthVer = wv;
    state.lastFontVer = fv;
  }
  let cached = state.cache.get(text);
  if (cached) {
    state.cache.delete(text);
    state.cache.set(text, cached);
    return cached;
  }
  const next = buildStreamingMarkdownState(text, renderAssistantBody, state.emptyState);
  state.cache.set(text, next);
  if (state.cache.size > 280) {
    const oldestKey = state.cache.keys().next().value;
    state.cache.delete(oldestKey);
  }
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
    if (!entry.state) entry.state = getStateByText(state, entry.text, state.renderAssistantBody);
    if (!current || current.text !== entry.text || current.state !== entry.state) {
      state.displayedByItemId.set(itemId, entry);
      changed = true;
    }
  }
  trimDisplayedByItemId(state);

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

const DISPLAYED_BY_ITEM_ID_CAP = 64;

function trimDisplayedByItemId(state) {
  while (state.displayedByItemId.size > DISPLAYED_BY_ITEM_ID_CAP) {
    const oldestKey = state.displayedByItemId.keys().next().value;
    if (oldestKey === undefined) break;
    state.displayedByItemId.delete(oldestKey);
  }
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
    return getStateByText(state, text, state.renderAssistantBody);
  }

  const displayed = state.displayedByItemId.get(itemId);
  if (!displayed) {
    const nextState = getStateByText(state, text, state.renderAssistantBody);
    const initial = { text, state: nextState };
    state.displayedByItemId.set(itemId, initial);
    trimDisplayedByItemId(state);
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

export function createStreamingMarkdownStateResolver(renderAssistantBody, onStateFlush = null, onStallDetected = null) {
  const state = {
    cache: new Map(),
    displayedByItemId: new Map(),
    pendingByItemId: new Map(),
    emptyState: Object.freeze({ text: '', html: '', tailText: '', heightPx: 0 }),
    renderAssistantBody,
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
