const THREAD_RAIL_MIN_WIDTH = 240;
const RIGHT_PANEL_CLOSE_THRESHOLD = 0;
const SPLITTER_WIDTH = 6;

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

function chatLayoutWidthBudget(viewportWidth = currentViewportWidth()) {
  return Math.max(0, viewportWidth - NAV_RAIL_WIDTH);
}

function ratioWidth(ratio, viewportWidth = currentViewportWidth()) {
  return Math.floor(chatLayoutWidthBudget(viewportWidth) * ratio);
}

function threadRailTargetWidth(viewportWidth = currentViewportWidth()) {
  return Math.max(THREAD_RAIL_MIN_WIDTH, ratioWidth(THREAD_RAIL_RATIO, viewportWidth));
}

function rightPanelDefaultWidth(viewportWidth = currentViewportWidth()) {
  return Math.max(0, ratioWidth(RIGHT_PANEL_DEFAULT_RATIO, viewportWidth));
}

function rightPanelMaxWidth(viewportWidth, threadRailWidth) {
  const layoutWidth = chatLayoutWidthBudget(viewportWidth);
  const ratioMax = ratioWidth(RIGHT_PANEL_MAX_RATIO, viewportWidth);
  const conversationMin = ratioWidth(CONVERSATION_MIN_RATIO, viewportWidth);
  const remainingAfterConversation = layoutWidth - threadRailWidth - (SPLITTER_WIDTH * 2) - conversationMin;
  return Math.max(0, Math.min(ratioMax, remainingAfterConversation));
}

function resizerNextWidth(event, currentWidth, maxWidth, minWidth, mode) {
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

export {
  RIGHT_PANEL_CLOSE_THRESHOLD,
  SPLITTER_WIDTH,
  THREAD_RAIL_MIN_WIDTH,
  chatLayoutWidthBudget,
  clampWidth,
  currentViewportWidth,
  ratioWidth,
  resizerNextWidth,
  rightPanelDefaultWidth,
  rightPanelMaxWidth,
  threadRailTargetWidth,
};
