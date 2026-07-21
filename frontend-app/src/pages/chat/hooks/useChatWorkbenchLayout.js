import { useEffect, useRef, useState } from 'react';
import { runUIAction } from '../model/chatUiActions.js';
import { RIGHT_PANEL_CLOSE_THRESHOLD, SPLITTER_WIDTH, THREAD_RAIL_MIN_WIDTH, chatLayoutWidthBudget, clampWidth, currentViewportWidth, ratioWidth, resizerNextWidth, rightPanelDefaultWidth, rightPanelMaxWidth, threadRailTargetWidth } from '../model/chatWorkbenchLayoutModel.js';

function useViewportWidth() {
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

function useThreadRailLayout({ viewportWidth, rightPanelOpen, rightPanelWidth, layoutRef }) {
  const [threadRailWidth, setThreadRailWidth] = useState(() => threadRailTargetWidth());
  const resizedRef = useRef(false);
  const activeDragDisposerRef = useRef(null);
  const maxWidth = threadRailTargetWidth(viewportWidth);
  const width = clampWidth(threadRailWidth, THREAD_RAIL_MIN_WIDTH, maxWidth);

  useEffect(() => {
    setThreadRailWidth((currentWidth) => {
      const targetWidth = threadRailTargetWidth(viewportWidth);
      if (!resizedRef.current) return targetWidth;
      return clampWidth(currentWidth, THREAD_RAIL_MIN_WIDTH, targetWidth);
    });
  }, [viewportWidth]);

  useEffect(() => () => activeDragDisposerRef.current?.(), []);

  const beginResize = (event) => {
    activeDragDisposerRef.current?.();
    event.preventDefault();
    resizedRef.current = true;
    event.currentTarget?.setPointerCapture?.(event.pointerId);

    const startX = event.clientX;
    const startWidth = width;
    let latestWidth = startWidth;
    const layoutElement = layoutRef.current;

    const layoutColumnsForWidth = (nextWidth) => {
      const rightWidth = clampWidth(rightPanelWidth, 0, rightPanelMaxWidth(viewportWidth, nextWidth));
      return rightPanelOpen
        ? `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightWidth}px`
        : 'minmax(0, 1fr)';
    };

    const state = { stopped: false };
    const dispose = ({ commit }) => {
      if (state.stopped) return;
      state.stopped = true;
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', finish);
      window.removeEventListener('pointercancel', cancel);
      window.removeEventListener('blur', cancel);
      event.currentTarget?.releasePointerCapture?.(event.pointerId);
      if (activeDragDisposerRef.current === cancel) activeDragDisposerRef.current = null;
      if (!commit && layoutElement) {
        layoutElement.style.gridTemplateColumns = layoutColumnsForWidth(startWidth);
      }
      if (commit) setThreadRailWidth(latestWidth);
    };
    const cancel = () => dispose({ commit: false });
    const finish = () => dispose({ commit: true });
    const move = (moveEvent) => {
      if (Number(moveEvent.buttons) === 0) {
        finish();
        return;
      }
      const rawNext = startWidth + (moveEvent.clientX - startX);
      latestWidth = clampWidth(rawNext, THREAD_RAIL_MIN_WIDTH, maxWidth);
      if (layoutElement) {
        layoutElement.style.gridTemplateColumns = layoutColumnsForWidth(latestWidth);
      }
    };

    activeDragDisposerRef.current = cancel;
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', finish);
    window.addEventListener('pointercancel', cancel);
    window.addEventListener('blur', cancel);
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

function useRuntimeSidePanelLayout({
  activeThreadId,
  railWidth,
  rightPanelWidth,
  setRightPanelWidth,
  store,
  viewportWidth,
  open,
  setOpen,
  layoutRef,
}) {
  const maxWidth = rightPanelMaxWidth(viewportWidth, railWidth);
  const width = clampWidth(rightPanelWidth, 0, maxWidth);
  const activeDragDisposerRef = useRef(null);
  useEffect(() => () => activeDragDisposerRef.current?.(), []);
  useRuntimePanelWidthSync({ maxWidth, open, rightPanelWidth, setOpen, setRightPanelWidth, viewportWidth });
  useRuntimeDiffSync({ activeThreadId, open, store });
  const beginResize = (event) => {
    beginRightPanelDrag({ activeDragDisposerRef, event, layoutRef, maxWidth, setOpen, setRightPanelWidth, width });
  };
  const handleKeyDown = (event) => {
    const nextWidth = resizerNextWidth(event, width, maxWidth, 0, 'right');
    if (nextWidth === null) return;
    event.preventDefault();
    if (nextWidth <= RIGHT_PANEL_CLOSE_THRESHOLD) {
      setRightPanelWidth(0);
      setOpen(false);
      return;
    }
    setRightPanelWidth(nextWidth);
  };
  const toggle = () => toggleRuntimePanel({ maxWidth, open, rightPanelWidth, setOpen, setRightPanelWidth, viewportWidth });
  return { beginResize, handleKeyDown, maxWidth, open, toggle, width };
}

function useRuntimePanelWidthSync({
  maxWidth,
  open,
  rightPanelWidth,
  setOpen,
  setRightPanelWidth,
  viewportWidth,
}) {
  useEffect(() => {
    if (!open) return;
    const savedWidth = clampWidth(rightPanelWidth, 0, maxWidth);
    const defaultWidth = clampWidth(rightPanelDefaultWidth(viewportWidth), 0, maxWidth);
    const targetWidth = savedWidth === 0 ? defaultWidth : savedWidth;
    if (targetWidth <= 0) {
      if (rightPanelWidth !== 0) setRightPanelWidth(0);
      setOpen(false);
      return;
    }
    if (targetWidth !== rightPanelWidth) setRightPanelWidth(targetWidth);
  }, [maxWidth, open, rightPanelWidth, setOpen, setRightPanelWidth, viewportWidth]);
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
    runUIAction('thread.sync', () => store.syncThreadState?.(activeThreadId, {
      includeArchived: true,
      includeDiff: true,
      loadMessages: false,
      preserveActiveThreadId: true,
    }));
  }, [activeThreadId, open, store]);
}

function toggleRuntimePanel({
  maxWidth,
  open,
  rightPanelWidth,
  setOpen,
  setRightPanelWidth,
  viewportWidth,
}) {
  const next = !open;
  if (next && rightPanelWidth === 0) {
    setRightPanelWidth(clampWidth(rightPanelDefaultWidth(viewportWidth), 0, maxWidth));
  }
  setOpen(next);
}

function beginRightPanelDrag({
  activeDragDisposerRef,
  event,
  layoutRef,
  maxWidth,
  setOpen,
  setRightPanelWidth,
  width,
}) {
  activeDragDisposerRef.current?.();
  event.preventDefault();
  event.currentTarget?.setPointerCapture?.(event.pointerId);
  const drag = rightPanelDragState({ activeDragDisposerRef, event, layoutRef, maxWidth, setOpen, setRightPanelWidth, width });
  window.addEventListener('pointermove', drag.move);
  window.addEventListener('pointerup', drag.finish);
  window.addEventListener('pointercancel', drag.cancel);
  window.addEventListener('blur', drag.cancel);
}

function rightPanelDragState({
  activeDragDisposerRef,
  event,
  layoutRef,
  maxWidth,
  setOpen,
  setRightPanelWidth,
  width,
}) {
  const startX = event.clientX;
  const startWidth = width;
  const layoutElement = layoutRef.current;
  const layoutColumnsForWidth = (nextWidth) => `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${nextWidth}px`;
  const state = { latestWidth: startWidth, stopped: false };
  const applyDragWidth = (nextWidth) => {
    if (layoutElement) layoutElement.style.gridTemplateColumns = layoutColumnsForWidth(nextWidth);
  };
  const finishDrag = (commit) => {
    if (state.stopped) return;
    state.stopped = true;
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', finish);
    window.removeEventListener('pointercancel', cancel);
    window.removeEventListener('blur', cancel);
    event.currentTarget?.releasePointerCapture?.(event.pointerId);
    if (activeDragDisposerRef.current === cancel) activeDragDisposerRef.current = null;
    if (!commit) {
      if (layoutElement) layoutElement.style.gridTemplateColumns = layoutColumnsForWidth(startWidth);
      return;
    }
    if (state.latestWidth <= RIGHT_PANEL_CLOSE_THRESHOLD) {
      setRightPanelWidth(0);
      setOpen(false);
      return;
    }
    setRightPanelWidth(state.latestWidth);
  };
  const cancel = () => finishDrag(false);
  const finish = () => finishDrag(true);
  const move = (moveEvent) => moveRightPanelDrag({ applyDragWidth, finish, maxWidth, moveEvent, startWidth, startX, state });
  const drag = { cancel, finish, move };
  activeDragDisposerRef.current = cancel;
  return drag;
}

function moveRightPanelDrag({
  applyDragWidth,
  finish,
  maxWidth,
  moveEvent,
  startWidth,
  startX,
  state,
}) {
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

export { RIGHT_PANEL_CLOSE_THRESHOLD, SPLITTER_WIDTH, THREAD_RAIL_MIN_WIDTH, chatLayoutWidthBudget, ratioWidth, resizerNextWidth, rightPanelDefaultWidth, rightPanelMaxWidth, threadRailTargetWidth, useRuntimeSidePanelLayout, useThreadRailLayout, useViewportWidth };
