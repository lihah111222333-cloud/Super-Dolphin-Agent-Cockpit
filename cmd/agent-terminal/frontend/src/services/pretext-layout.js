// @ts-nocheck
import { logDebug, logWarn, logError } from './log.js';

// ── 模块级状态 ──
let resolvedFontShorthand = null;
let measuredLineHeight = 0;
let cachedContainerWidth = 0;
let widthVersion = 0;
let fontVersion = 0;
let containerObserver = null;
let observedElement = null;
let rafId = null;
let containerCheckInterval = null;
let pretextModule = null;
let loadFailed = false;
let featureDisabled = false;
let preparedCache = new Map();
let loggedErrors = new Set();
let onInvalidate = null;

const PREPARED_CACHE_LIMIT = 50;
const PADDING_DEDUCTION = 80;
const GENERIC_FAMILIES = /\b(system-ui|ui-monospace|ui-sans-serif|ui-serif|ui-rounded|monospace|sans-serif|serif|cursive|fantasy)\b/i;

// ── 去重日志 ──
function logOnce(key, err) {
  if (loggedErrors.has(key)) return;
  loggedErrors.add(key);
  logError('ui', `pretext.${key}`, { error: String(err) });
}

// ── 字体探测：probe + cs.font + generic family 拦截 ──
// Returns true if named fonts were found, false if only generic families.
function resolveMonoFont() {
  let container = null;
  try {
    container = document.createElement('div');
    container.className = 'chat-item-body';
    container.style.cssText = 'position:absolute;visibility:hidden;pointer-events:none';
    const probe = document.createElement('pre');
    probe.className = 'chat-item-plain';
    probe.textContent = 'X';
    container.appendChild(probe);
    document.body.appendChild(container);

    const cs = getComputedStyle(probe);
    const candidateFont = cs.font;

    const families = cs.fontFamily;
    const namedFonts = families.split(',')
      .map(f => f.trim().replace(/^["']|["']$/g, ''))
      .filter(f => !GENERIC_FAMILIES.test(f));
    if (namedFonts.length === 0) {
      // [FIX] Don't permanently disable — CSS may not be loaded yet.
      // Return false so caller can retry.
      logDebug('ui', 'pretext.font.probe_generic_only', { fontFamily: families });
      return false;
    }

    resolvedFontShorthand = candidateFont;
    measuredLineHeight = parseFloat(cs.lineHeight) || (parseFloat(cs.fontSize) * 1.7);
    fontVersion += 1;
    logDebug('ui', 'pretext.font.resolved', { font: resolvedFontShorthand, lineHeight: measuredLineHeight, namedFonts });

    preparedCache.clear();
    if (pretextModule?.clearCache) {
      try { pretextModule.clearCache(); } catch { /* ignore */ }
    }
    return true;
  } catch (err) {
    logOnce('probe_failed', err);
    return false;
  } finally {
    if (container && container.parentNode) {
      container.parentNode.removeChild(container);
    }
  }
}

// ── pretext 加载：一次性熔断 ──
async function loadPretext() {
  if (loadFailed || pretextModule) return;
  try {
    pretextModule = await import('@chenglou/pretext');
    logDebug('ui', 'pretext.module.loaded', { version: pretextModule?.version || '0.0.4' });
    if (onInvalidate) onInvalidate();
  } catch (err) {
    loadFailed = true;
    logWarn('ui', 'pretext.import_failed', { error: String(err) });
  }
}

// ── 初始化（幂等 + 字体探测重试） ──
let _fontRetryCount = 0;
let _fontRetryTimer = null;
const FONT_RETRY_MAX = 5;
const FONT_RETRY_DELAY_MS = 200;

export function initPretextLayout() {
  if (resolvedFontShorthand || featureDisabled || loadFailed) return;
  const ok = resolveMonoFont();
  if (ok) {
    _fontRetryCount = 0;
    loadPretext();
    return;
  }
  // Font probe failed — CSS may not be loaded yet. Schedule retry.
  _fontRetryCount += 1;
  if (_fontRetryCount >= FONT_RETRY_MAX) {
    featureDisabled = true;
    logWarn('ui', 'pretext.disabled.generic_font_only', { retries: _fontRetryCount });
    return;
  }
  if (_fontRetryTimer === null) {
    _fontRetryTimer = setTimeout(() => {
      _fontRetryTimer = null;
      initPretextLayout();
    }, FONT_RETRY_DELAY_MS);
  }
}

// ── 宽度观测：支持容器重绑 ──
export function observeContainerWidth() {
  const el = document.querySelector('.chat-messages-vue');
  if (!el) {
    if (rafId === null && typeof requestAnimationFrame === 'function') {
      rafId = requestAnimationFrame(() => {
        rafId = null;
        observeContainerWidth();
      });
    }
    return;
  }
  rafId = null;

  if (containerObserver && observedElement !== el) {
    containerObserver.disconnect();
    containerObserver = null;
    observedElement = null;
  }
  if (containerObserver) return;

  containerObserver = new ResizeObserver(entries => {
    const w = entries[0]?.contentRect?.width ?? 0;
    if (w > 0 && w !== cachedContainerWidth) {
      cachedContainerWidth = w;
      widthVersion += 1;
      if (onInvalidate) onInvalidate();
    }
  });
  containerObserver.observe(el);
  observedElement = el;
  cachedContainerWidth = el.clientWidth;
  logDebug('ui', 'pretext.container.observed', { width: cachedContainerWidth });

  if (containerCheckInterval === null) {
    containerCheckInterval = setInterval(() => {
      const current = document.querySelector('.chat-messages-vue');
      if (current && current !== observedElement) {
        if (containerObserver) {
          containerObserver.disconnect();
          containerObserver = null;
        }
        observedElement = null;
        observeContainerWidth();
      }
    }, 2000);
  }
}

export function disconnectContainerObserver() {
  if (containerObserver) {
    containerObserver.disconnect();
    containerObserver = null;
    observedElement = null;
  }
  if (rafId !== null) {
    if (typeof cancelAnimationFrame === 'function') cancelAnimationFrame(rafId);
    rafId = null;
  }
  if (containerCheckInterval !== null) {
    clearInterval(containerCheckInterval);
    containerCheckInterval = null;
  }
  if (_fontRetryTimer !== null) {
    clearTimeout(_fontRetryTimer);
    _fontRetryTimer = null;
  }
  preparedCache.clear();
  if (pretextModule?.clearCache) {
    try { pretextModule.clearCache(); } catch { /* ignore */ }
  }
}

// ── 热路径：prepared 复用 + pre-wrap + 去重日志 ──
export function measureTailHeight(tailText) {
  if (featureDisabled || !pretextModule || !resolvedFontShorthand || !tailText || cachedContainerWidth <= 0) {
    return 0;
  }
  try {
    const maxWidth = Math.max(cachedContainerWidth - PADDING_DEDUCTION, 100);
    const cached = preparedCache.get(tailText);
    let prepared;
    if (cached && cached.fontVer === fontVersion) {
      prepared = cached.prepared;
    } else {
      prepared = pretextModule.prepare(tailText, resolvedFontShorthand, { whiteSpace: 'pre-wrap' });
      if (preparedCache.size >= PREPARED_CACHE_LIMIT) {
        preparedCache.delete(preparedCache.keys().next().value);
      }
      preparedCache.set(tailText, { prepared, fontVer: fontVersion });
    }
    const h = pretextModule.layout(prepared, maxWidth, measuredLineHeight).height;
    if (h > 0 && !loggedErrors.has('first_measure')) {
      loggedErrors.add('first_measure');
      logDebug('ui', 'pretext.measure.first_success', { height: h, maxWidth, lineHeight: measuredLineHeight });
    }
    return h;
  } catch (err) {
    logOnce('measure_failed', err);
    return 0;
  }
}

// ── 外部接口 ──
export function setInvalidateCallback(cb) { onInvalidate = cb; }
/** 就绪检查 — 当前内部由 measureTailHeight 自行守卫；保留供 Phase 2 虚拟滚动外部判断使用 */
export function isPretextReady() {
  return !featureDisabled && !!pretextModule && !!resolvedFontShorthand && cachedContainerWidth > 0;
}
export function getWidthVersion() { return widthVersion; }
export function getFontVersion() { return fontVersion; }
