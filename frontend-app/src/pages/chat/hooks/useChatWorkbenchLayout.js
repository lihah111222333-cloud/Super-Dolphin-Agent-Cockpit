import { useEffect, useRef, useState } from 'react';
import { runUIAction } from '../components/chatUiActions.js';

export const THREAD_RAIL_MIN_WIDTH = 240;
export const RIGHT_PANEL_CLOSE_THRESHOLD = 0;
export const SPLITTER_WIDTH = 6;

const THREAD_RAIL_RATIO = 0.2;
const RIGHT_PANEL_DEFAULT_RATIO = 0.2;
const RIGHT_PANEL_MAX_RATIO = 0.4;
const CONVERSATION_MIN_RATIO = 0.4;
const NAV_RAIL_WIDTH = 76;
const RESIZER_KEY_STEP = 16;

function clampWidth(value, min, max) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) return min;
  return Math.max(min, Math.min(max, numeric));
}

function currentViewportWidth() {
  if (typeof window === 'undefined') return 0;
  const width = Number(window.innerWidth);
  return Number.isFinite(width) ? width : 0;
}

export function chatLayoutWidthBudget(viewportWidth = currentViewportWidth()) {
  return Math.max(0, viewportWidth - NAV_RAIL_WIDTH);
}

export function ratioWidth(ratio, viewportWidth = currentViewportWidth()) {
  return Math.floor(chatLayoutWidthBudget(viewportWidth) * ratio);
}

export function threadRailTargetWidth(viewportWidth = currentViewportWidth()) {
  return Math.max(THREAD_RAIL_MIN_WIDTH, ratioWidth(THREAD_RAIL_RATIO, viewportWidth));
}

export function rightPanelDefaultWidth(viewportWidth = currentViewportWidth()) {
  return Math.max(0, ratioWidth(RIGHT_PANEL_DEFAULT_RATIO, viewportWidth));
}

export function rightPanelMaxWidth(viewportWidth, threadRailWidth) {
  const layoutWidth = chatLayoutWidthBudget(viewportWidth);
  const ratioMax = ratioWidth(RIGHT_PANEL_MAX_RATIO, viewportWidth);
  const conversationMin = ratioWidth(CONVERSATION_MIN_RATIO, viewportWidth);
  const remainingAfterConversation = layoutWidth - threadRailWidth - (SPLITTER_WIDTH * 2) - conversationMin;
  return Math.max(0, Math.min(ratioMax, remainingAfterConversation));
}

export function resizerNextWidth(event, currentWidth, maxWidth, minWidth, mode) {
  if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return null;
  if (event.key === 'Home') return minWidth;
  if (event.key === 'End') return maxWidth;
  const direction = mode === 'right' ? 1 : -1;
  const deltaByKey = {
    ArrowLeft: RESIZER_KEY_STEP * direction,
    ArrowRight: -RESIZER_KEY_STEP * direction,
  };
  const delta = deltaByKey[event.key];
  return delta === undefined ? null : clampWidth(currentWidth + delta, minWidth, maxWidth);
}

export function useViewportWidth() {
  const [viewportWidth, setViewportWidth] = useState(currentViewportWidth);
  useEffect(() => {
    let frameId = null;
    const onResize = () => {
      if (frameId) return;
      frameId = window.requestAnimationFrame(() => {
        frameId = null;
        setViewportWidth(currentViewportWidth());
      });
    };
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      if (frameId) window.cancelAnimationFrame(frameId);
    };
  }, []);
  return viewportWidth;
}

export function useThreadRailLayout({ viewportWidth, rightPanelOpen, store, layoutRef }) {
  const [threadRailWidth, setThreadRailWidth] = useState(() => threadRailTargetWidth());
  const resizedRef = useRef(false);
  const maxWidth = threadRailTargetWidth(viewportWidth);
  const width = clampWidth(threadRailWidth, THREAD_RAIL_MIN_WIDTH, maxWidth);

  useEffect(() => {
    setThreadRailWidth((currentWidth) => {
      const targetWidth = threadRailTargetWidth(viewportWidth);
      if (!resizedRef.current) return targetWidth;
      return clampWidth(currentWidth, THREAD_RAIL_MIN_WIDTH, targetWidth);
    });
  }, [viewportWidth]);

  const beginResize = (event) => {
    event.preventDefault();
    resizedRef.current = true;
    event.currentTarget?.setPointerCapture?.(event.pointerId);

    const startX = event.clientX;
    const startWidth = width;
    let latestWidth = startWidth;

    const layoutColumnsForWidth = (nextWidth) => {
      const rightWidth = clampWidth(store.rightPanelWidth, 0, rightPanelMaxWidth(viewportWidth, nextWidth));
      return rightPanelOpen
        ? `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightWidth}px`
        : 'minmax(0, 1fr)';
    };

    const move = (moveEvent) => {
      if (Number(moveEvent.buttons) === 0) {
        stop();
        return;
      }
      const rawNext = startWidth + (moveEvent.clientX - startX);
      latestWidth = clampWidth(rawNext, THREAD_RAIL_MIN_WIDTH, maxWidth);
      if (layoutRef.current) {
        layoutRef.current.style.gridTemplateColumns = layoutColumnsForWidth(latestWidth);
      }
    };

    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
      window.removeEventListener('pointercancel', stop);
      window.removeEventListener('blur', stop);
      event.currentTarget?.releasePointerCapture?.(event.pointerId);

      setThreadRailWidth(latestWidth);
    };

    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
    window.addEventListener('pointercancel', stop);
    window.addEventListener('blur', stop);
  };

  const handleKeyDown = (event) => {
    const nextWidth = resizerNextWidth(event, width, maxWidth, THREAD_RAIL_MIN_WIDTH, 'rail');
    if (nextWidth === null) return;
    event.preventDefault();
    resizedRef.current = true;
    setThreadRailWidth(nextWidth);
  };

  return { beginResize, handleKeyDown, maxWidth, width };
}

export function useRuntimeSidePanelLayout({ activeThreadId, railWidth, store, viewportWidth, open, setOpen, layoutRef }) {
  const resizedRef = useRef(false);
  const maxWidth = rightPanelMaxWidth(viewportWidth, railWidth);
  const width = clampWidth(store.rightPanelWidth, 0, maxWidth);
  useRuntimePanelWidthSync({ maxWidth, open, resizedRef, setOpen, store, viewportWidth });
  useRuntimeDiffSync({ activeThreadId, open, store });
  const beginResize = (event) => {
    resizedRef.current = true;
    beginRightPanelDrag({ event, layoutRef, maxWidth, setOpen, store, width });
  };
  const handleKeyDown = (event) => {
    const nextWidth = resizerNextWidth(event, width, maxWidth, 0, 'right');
    if (nextWidth === null) return;
    event.preventDefault();
    resizedRef.current = true;
    if (nextWidth <= RIGHT_PANEL_CLOSE_THRESHOLD) {
      store.setRightPanelWidth?.(0);
      setOpen(false);
      return;
    }
    store.setRightPanelWidth?.(nextWidth);
  };
  const toggle = () => toggleRuntimePanel({ maxWidth, open, resizedRef, setOpen, store, viewportWidth });
  return { beginResize, handleKeyDown, maxWidth, open, toggle, width };
}

function useRuntimePanelWidthSync({ maxWidth, open, resizedRef, setOpen, store, viewportWidth }) {
  useEffect(() => {
    if (!open) return;
    const targetWidth = resizedRef.current
      ? clampWidth(store.rightPanelWidth, 0, maxWidth)
      : clampWidth(rightPanelDefaultWidth(viewportWidth), 0, maxWidth);
    if (targetWidth <= 0) {
      store.setRightPanelWidth?.(0);
      setOpen(false);
      return;
    }
    if (targetWidth !== store.rightPanelWidth) store.setRightPanelWidth?.(targetWidth);
  }, [maxWidth, open, resizedRef, setOpen, store, viewportWidth]);
}

function useRuntimeDiffSync({ activeThreadId, open, store }) {
  useEffect(() => {
    /*
     * 右侧面板打开时才补齐 diff。
     * loadMessages:false 表示复用当前 timeline，不重新拉历史消息。
     */
    if (!open || !activeThreadId) return;
    if (store.threadDiffReadyByThread?.[activeThreadId]) return;
    if (store.threadStateLoadingByThread?.[activeThreadId]) return;
    runUIAction(() => store.syncThreadState?.(activeThreadId, {
      includeArchived: true,
      includeDiff: true,
      loadMessages: false,
      preserveActiveThreadId: true,
    }));
  }, [activeThreadId, open, store]);
}

function toggleRuntimePanel({ maxWidth, open, resizedRef, setOpen, store, viewportWidth }) {
  const next = !open;
  if (next) {
    resizedRef.current = false;
    store.setRightPanelWidth?.(clampWidth(rightPanelDefaultWidth(viewportWidth), 0, maxWidth));
  }
  setOpen(next);
}

function beginRightPanelDrag({ event, layoutRef, maxWidth, setOpen, store, width }) {
  event.preventDefault();
  event.currentTarget?.setPointerCapture?.(event.pointerId);
  const drag = rightPanelDragState({ event, layoutRef, maxWidth, setOpen, store, width });
  window.addEventListener('pointermove', drag.move);
  window.addEventListener('pointerup', drag.finish);
  window.addEventListener('pointercancel', drag.finish);
  window.addEventListener('blur', drag.finish);
}

function rightPanelDragState({ event, layoutRef, maxWidth, setOpen, store, width }) {
  const startX = event.clientX;
  const startWidth = width;
  const layoutColumnsForWidth = (nextWidth) => `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${nextWidth}px`;
  const state = { latestWidth: startWidth, stopped: false };
  const applyDragWidth = (nextWidth) => {
    if (layoutRef.current) layoutRef.current.style.gridTemplateColumns = layoutColumnsForWidth(nextWidth);
  };
  const finish = () => finishRightPanelDrag({ event, setOpen, state, store, drag });
  const move = (moveEvent) => moveRightPanelDrag({ applyDragWidth, finish, maxWidth, moveEvent, startWidth, startX, state });
  const drag = { finish, move };
  return drag;
}

function moveRightPanelDrag({ applyDragWidth, finish, maxWidth, moveEvent, startWidth, startX, state }) {
  if (Number(moveEvent.buttons) === 0) {
    finish();
    return;
  }
  const rawNext = startWidth - (moveEvent.clientX - startX);
  if (rawNext <= RIGHT_PANEL_CLOSE_THRESHOLD) {
    state.latestWidth = 0;
    applyDragWidth(0);
    finish();
    return;
  }
  state.latestWidth = clampWidth(rawNext, 0, maxWidth);
  applyDragWidth(state.latestWidth);
}

function finishRightPanelDrag({ event, setOpen, state, store, drag }) {
  if (state.stopped) return;
  state.stopped = true;
  window.removeEventListener('pointermove', drag.move);
  window.removeEventListener('pointerup', drag.finish);
  window.removeEventListener('pointercancel', drag.finish);
  window.removeEventListener('blur', drag.finish);
  event.currentTarget?.releasePointerCapture?.(event.pointerId);
  if (state.latestWidth <= RIGHT_PANEL_CLOSE_THRESHOLD) {
    store.setRightPanelWidth?.(0);
    setOpen(false);
    return;
  }
  store.setRightPanelWidth?.(state.latestWidth);
}
