import { useEffect, useState } from 'react';

const RESIZER_KEY_STEP = 16;
const RUNTIME_TOOLBAR_HEIGHT = 67;
const ACTIVITY_ICON_ROW_HEIGHT = 64;
const ACTIVITY_PANEL_MIN_HEIGHT = ACTIVITY_ICON_ROW_HEIGHT;
/*
 * 默认高度比图标行高出一档，让「最近活动」日志行默认可见；
 * 用户仍可通过拖拽或键盘收回到 64px 的图标行。
 */
const ACTIVITY_PANEL_DEFAULT_HEIGHT = 96;

function currentViewportHeight() {
  if (typeof window === 'undefined') return 0;
  const height = Number(window.innerHeight);
  return Number.isFinite(height) ? height : 0;
}

function runtimePanelContentHeight(viewportHeight = currentViewportHeight()) {
  return Math.max(0, Math.floor(viewportHeight) - RUNTIME_TOOLBAR_HEIGHT);
}

function activityPanelMaxHeight(viewportHeight = currentViewportHeight()) {
  return Math.max(ACTIVITY_PANEL_MIN_HEIGHT, Math.floor(runtimePanelContentHeight(viewportHeight) / 2));
}

function clampActivityPanelHeight(value, viewportHeight = currentViewportHeight()) {
  const numeric = Number(value);
  const height = Number.isFinite(numeric) ? numeric : ACTIVITY_PANEL_DEFAULT_HEIGHT;
  return Math.max(ACTIVITY_PANEL_MIN_HEIGHT, Math.min(activityPanelMaxHeight(viewportHeight), Math.round(height)));
}

function runtimePanelHeightVars(activityPanelHeight, viewportHeight = currentViewportHeight()) {
  const contentHeight = runtimePanelContentHeight(viewportHeight);
  const activityMaxHeight = activityPanelMaxHeight(viewportHeight);
  const diffMinHeight = Math.max(0, Math.floor(contentHeight / 2));
  const diffMaxHeight = Math.max(diffMinHeight, contentHeight - ACTIVITY_PANEL_MIN_HEIGHT);
  return {
    '--runtime-toolbar-height': `${RUNTIME_TOOLBAR_HEIGHT}px`,
    '--activity-panel-height': `${clampActivityPanelHeight(activityPanelHeight, viewportHeight)}px`,
    '--activity-panel-min-height': `${ACTIVITY_PANEL_MIN_HEIGHT}px`,
    '--activity-panel-max-height': `${activityMaxHeight}px`,
    '--diff-panel-min-height': `${diffMinHeight}px`,
    '--diff-panel-max-height': `${diffMaxHeight}px`,
  };
}

function activityPanelNextKeyboardHeight(event, currentHeight, maxHeight) {
  const keyActions = {
    ArrowUp: currentHeight + RESIZER_KEY_STEP,
    PageUp: currentHeight + RESIZER_KEY_STEP,
    ArrowDown: currentHeight - RESIZER_KEY_STEP,
    PageDown: currentHeight - RESIZER_KEY_STEP,
    Home: ACTIVITY_PANEL_MIN_HEIGHT,
    End: maxHeight,
  };
  return keyActions[event.key] ?? null;
}

function useRuntimePanelLayout() {
  /*
   * 右侧 runtime panel 的高度只跟窗口和活动面板高度有关。
   * 拖拽时先写 CSS 变量，松手后再更新 React state。
   */
  const [viewportHeight, setViewportHeight] = useState(currentViewportHeight);
  const [activityPanelHeight, setActivityPanelHeight] = useState(() => clampActivityPanelHeight(ACTIVITY_PANEL_DEFAULT_HEIGHT));
  const activityPanelMax = activityPanelMaxHeight(viewportHeight);
  useEffect(() => {
    let frameId = null;
    const onResize = () => {
      if (frameId) return;
      frameId = window.requestAnimationFrame(() => {
        frameId = null;
        const nextHeight = currentViewportHeight();
        setViewportHeight(nextHeight);
        setActivityPanelHeight((height) => clampActivityPanelHeight(height, nextHeight));
      });
    };
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      if (frameId) window.cancelAnimationFrame(frameId);
    };
  }, []);
  const beginActivityPanelResize = (event, inputType = 'pointer') => {
    event.preventDefault();
    if (inputType === 'pointer') event.currentTarget?.setPointerCapture?.(event.pointerId);
    const startY = event.clientY;
    const startHeight = activityPanelHeight;
    const moveEventName = inputType === 'mouse' ? 'mousemove' : 'pointermove';
    const stopEventName = inputType === 'mouse' ? 'mouseup' : 'pointerup';
    const panelEl = (event.currentTarget || event.target)?.closest?.('.runtime-panel') || document.querySelector('.runtime-panel');
    let latestHeight = startHeight;
    const move = (moveEvent) => {
      const nextHeight = clampActivityPanelHeight(startHeight + (startY - moveEvent.clientY), viewportHeight);
      latestHeight = nextHeight;
      if (panelEl) panelEl.style.setProperty('--activity-panel-height', `${nextHeight}px`);
    };
    const stop = () => {
      window.removeEventListener(moveEventName, move);
      window.removeEventListener(stopEventName, stop);
      if (inputType === 'pointer') window.removeEventListener('pointercancel', stop);
      setActivityPanelHeight(latestHeight);
    };
    window.addEventListener(moveEventName, move);
    window.addEventListener(stopEventName, stop);
    if (inputType === 'pointer') window.addEventListener('pointercancel', stop);
  };
  const handleActivityPanelResizeKeyDown = (event) => {
    if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    const nextHeight = activityPanelNextKeyboardHeight(event, activityPanelHeight, activityPanelMax);
    if (nextHeight === null) return;
    event.preventDefault();
    setActivityPanelHeight(clampActivityPanelHeight(nextHeight, viewportHeight));
  };
  return {
    activityPanelHeight,
    activityPanelMax,
    beginActivityPanelResize,
    handleActivityPanelResizeKeyDown,
    viewportHeight,
  };
}

export { ACTIVITY_PANEL_MIN_HEIGHT, runtimePanelHeightVars, useRuntimePanelLayout };
