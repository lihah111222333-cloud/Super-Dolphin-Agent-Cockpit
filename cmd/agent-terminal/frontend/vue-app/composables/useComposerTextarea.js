import { ref, nextTick } from '../../lib/vue.esm-browser.prod.js';
import { logDebug } from '../services/log.js';
import {
  applyComposerTextareaAutoHeight,
  COMPOSER_APP_HEIGHT_MAX_RATIO,
  COMPOSER_CHAT_READABLE_MIN_HEIGHT,
  COMPOSER_INPUT_BOUNDARY_GAP,
  COMPOSER_INPUT_MAX_HEIGHT,
  COMPOSER_INPUT_MIN_HEIGHT,
} from '../utils/composer-textarea-height.js';
/**
 * 管理 ComposerBar 的 textarea 自适应高度 + IME 组合输入拦截。
 */
export function useComposerTextarea() {
  const composerInputRef = ref(null);
  const isComposing = ref(false);

  function resolveComposerInputMaxHeight() {
    const input = composerInputRef.value;
    const viewportHeight = Number(window?.innerHeight || 0);
    const fallbackMaxHeight = viewportHeight
      ? Math.max(COMPOSER_INPUT_MIN_HEIGHT, Math.round(viewportHeight * 0.72) - COMPOSER_INPUT_BOUNDARY_GAP)
      : COMPOSER_INPUT_MAX_HEIGHT;
    if (!input || typeof input.getBoundingClientRect !== 'function') {
      return fallbackMaxHeight;
    }
    const boundary = input.closest?.('.chat-workspace')
      || input.closest?.('.workspace-area')
      || input.closest?.('.unified-chat-page')
      || null;
    const rect = input.getBoundingClientRect();
    const boundaryTop = boundary && typeof boundary.getBoundingClientRect === 'function'
      ? boundary.getBoundingClientRect().top
      : COMPOSER_INPUT_BOUNDARY_GAP;
    const composerShell = input.closest?.('.chat-composer-shell')
      || input.closest?.('.workspace-bottom-row')
      || null;
    const composerRect = composerShell && typeof composerShell.getBoundingClientRect === 'function'
      ? composerShell.getBoundingClientRect()
      : null;
    const composerChromeHeight = composerRect
      ? Math.max(0, Math.floor((Number(composerRect.height) || 0) - (Number(rect.height) || 0)))
      : 0;
    const availableHeight = Math.floor(
      rect.bottom - Math.max(boundaryTop, COMPOSER_INPUT_BOUNDARY_GAP) - COMPOSER_INPUT_BOUNDARY_GAP,
    );
    if (!Number.isFinite(availableHeight) || availableHeight <= 0) {
      return fallbackMaxHeight;
    }
    return Math.max(
      COMPOSER_INPUT_MIN_HEIGHT,
      Math.min(
        availableHeight - COMPOSER_CHAT_READABLE_MIN_HEIGHT - COMPOSER_INPUT_BOUNDARY_GAP - composerChromeHeight,
        Math.round(viewportHeight * COMPOSER_APP_HEIGHT_MAX_RATIO) - composerChromeHeight,
      ),
    );
  }

  function syncComposerInputHeight() {
    applyComposerTextareaAutoHeight(composerInputRef.value, resolveComposerInputMaxHeight());
  }

  function setComposerInputRef(element) {
    composerInputRef.value = element || null;
    if (composerInputRef.value) nextTick(() => syncComposerInputHeight());
  }

  function onInput() {
    syncComposerInputHeight();
  }

  function onCompositionStart() {
    isComposing.value = true;
    logDebug('ui', 'composerBar.composition.start', {});
  }

  function onCompositionEnd() {
    isComposing.value = false;
    logDebug('ui', 'composerBar.composition.end', {});
  }

  return {
    composerInputRef,
    isComposing,
    syncComposerInputHeight,
    setComposerInputRef,
    onInput,
    onCompositionStart,
    onCompositionEnd,
  };
}
