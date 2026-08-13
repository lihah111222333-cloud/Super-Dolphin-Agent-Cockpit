const DESKTOP_RAIL_MIN_WIDTH = 280;
const DESKTOP_RAIL_MAX_WIDTH = 460;
const MOBILE_RAIL_GUTTER = 52;
const MOBILE_RAIL_MAX_WIDTH = 320;
const MOBILE_BREAKPOINT = 920;
const RUNTIME_COMPACT_BREAKPOINT = 720;
const SPLITTER_WIDTH = 6;
const THREAD_RAIL_WIDTH = 240;
const THREAD_RAIL_HIDDEN_MIN_VIEWPORT_WIDTH = 1200;
const THREAD_RAIL_HIDDEN_MAX_VIEWPORT_WIDTH = 1280;
const CONVERSATION_MIN_WIDTH = 440;
const ACTIVITY_PANEL_MIN_HEIGHT = 112;
const DESKTOP_RUNTIME_TOOLBAR_HEIGHT = 67;
const COMPACT_RUNTIME_TOOLBAR_HEIGHT = 112;

function requiredFinite(value, name) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) throw new TypeError(`${name} must be a finite number`);
  return numeric;
}

function requiredBoolean(value, name) {
  if (typeof value !== 'boolean') throw new TypeError(`${name} must be a boolean`);
  return value;
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

function freezeRecord(value) {
  Object.values(value).forEach((entry) => {
    if (entry && typeof entry === 'object' && !Object.isFrozen(entry)) freezeRecord(entry);
  });
  return Object.freeze(value);
}

function solveWorkbenchGeometry(input) {
  if (input === null || typeof input !== 'object') {
    throw new TypeError('GeometrySolver input must be an object');
  }
  const viewportWidth = Math.max(0, requiredFinite(input.viewportWidth, 'viewportWidth'));
  const viewportHeight = Math.max(0, requiredFinite(input.viewportHeight, 'viewportHeight'));
  const railOpen = requiredBoolean(input.railOpen, 'railOpen');
  const rightOpen = requiredBoolean(input.rightOpen, 'rightOpen');
  const requestedRailWidth = requiredFinite(input.railWidth, 'railWidth');
  const rightPreference = requiredFinite(input.rightPreference, 'rightPreference');
  const requestedActivityHeight = requiredFinite(input.activityHeight, 'activityHeight');
  const mobile = viewportWidth <= MOBILE_BREAKPOINT;
  const railMax = mobile
    ? Math.max(0, Math.min(MOBILE_RAIL_MAX_WIDTH, viewportWidth - MOBILE_RAIL_GUTTER))
    : DESKTOP_RAIL_MAX_WIDTH;
  const railMin = mobile ? Math.min(DESKTOP_RAIL_MIN_WIDTH, railMax) : DESKTOP_RAIL_MIN_WIDTH;
  const railExpandedWidth = clamp(requestedRailWidth, railMin, railMax);
  const railDisplayed = railOpen ? railExpandedWidth : 0;
  const railConsumed = mobile ? 0 : railDisplayed;
  const threadRailVisible = viewportWidth < THREAD_RAIL_HIDDEN_MIN_VIEWPORT_WIDTH
    || viewportWidth > THREAD_RAIL_HIDDEN_MAX_VIEWPORT_WIDTH;
  const threadRailConsumed = threadRailVisible ? THREAD_RAIL_WIDTH : 0;
  const mainWidth = Math.max(0, viewportWidth - railConsumed - threadRailConsumed);
  const conversationMin = Math.min(mainWidth, Math.max(CONVERSATION_MIN_WIDTH, Math.floor(mainWidth * 0.4)));
  const rightMax = Math.max(0, Math.min(
    Math.floor(mainWidth * 0.4),
    mainWidth - SPLITTER_WIDTH - conversationMin,
  ));
  const rightDefault = Math.max(0, Math.floor(mainWidth * 0.2));
  const requestedRightWidth = input.rightDisplayWidth === undefined
    ? (rightPreference === 0 ? rightDefault : rightPreference)
    : requiredFinite(input.rightDisplayWidth, 'rightDisplayWidth');
  const rightDisplayed = rightOpen && rightMax > 0
    ? clamp(requestedRightWidth, Math.min(1, rightMax), rightMax)
    : 0;
  const rightSplitter = rightDisplayed > 0 ? SPLITTER_WIDTH : 0;
  const conversationWidth = Math.max(0, mainWidth - rightSplitter - rightDisplayed);
  const toolbarHeight = viewportWidth <= RUNTIME_COMPACT_BREAKPOINT
    ? COMPACT_RUNTIME_TOOLBAR_HEIGHT
    : DESKTOP_RUNTIME_TOOLBAR_HEIGHT;
  const runtimeContentHeight = Math.max(0, Math.floor(viewportHeight) - toolbarHeight);
  const activityMax = Math.max(ACTIVITY_PANEL_MIN_HEIGHT, Math.floor(runtimeContentHeight / 2));
  const activityDisplayed = clamp(
    Math.round(requestedActivityHeight),
    ACTIVITY_PANEL_MIN_HEIGHT,
    activityMax,
  );
  const diffMin = Math.max(0, Math.floor(runtimeContentHeight / 2));
  const diffMax = Math.max(diffMin, runtimeContentHeight - ACTIVITY_PANEL_MIN_HEIGHT);
  const conversationGridTemplateColumns = rightDisplayed > 0
    ? `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightDisplayed}px`
    : 'minmax(0, 1fr)';
  const gridTemplateColumns = threadRailVisible
    ? `${THREAD_RAIL_WIDTH}px ${conversationGridTemplateColumns}`
    : conversationGridTemplateColumns;

  return freezeRecord({
    activity: {
      displayed: activityDisplayed,
      max: activityMax,
      min: ACTIVITY_PANEL_MIN_HEIGHT,
      requested: requestedActivityHeight,
    },
    aria: {
      activityMax,
      activityMin: ACTIVITY_PANEL_MIN_HEIGHT,
      activityNow: activityDisplayed,
      railMax,
      railMin,
      railNow: railDisplayed,
      rightMax,
      rightMin: 0,
      rightNow: rightDisplayed,
    },
    composer: {
      rightOffset: rightDisplayed + rightSplitter,
      width: conversationWidth,
    },
    conversation: {
      min: conversationMin,
      width: conversationWidth,
    },
    cssVars: {
      '--activity-panel-height': `${activityDisplayed}px`,
      '--activity-panel-max-height': `${activityMax}px`,
      '--activity-panel-min-height': `${ACTIVITY_PANEL_MIN_HEIGHT}px`,
      '--diff-panel-max-height': `${diffMax}px`,
      '--diff-panel-min-height': `${diffMin}px`,
      '--composer-right-offset': `${rightDisplayed + rightSplitter}px`,
      '--runtime-toolbar-height': `${toolbarHeight}px`,
      '--thread-rail-column-width': `${threadRailConsumed}px`,
      '--workbench-sidebar-width': `${railExpandedWidth}px`,
    },
    gridTemplateColumns,
    rail: {
      consumed: railConsumed,
      displayed: railDisplayed,
      max: railMax,
      min: railMin,
      open: railOpen,
      requested: requestedRailWidth,
    },
    right: {
      defaultWidth: rightDefault,
      displayed: rightDisplayed,
      max: rightMax,
      open: rightOpen,
      preference: rightPreference,
    },
    splitterWidth: SPLITTER_WIDTH,
    threadRail: {
      consumed: threadRailConsumed,
      visible: threadRailVisible,
    },
    viewport: {
      height: viewportHeight,
      width: viewportWidth,
    },
  });
}

export {
  ACTIVITY_PANEL_MIN_HEIGHT,
  DESKTOP_RAIL_MAX_WIDTH,
  DESKTOP_RAIL_MIN_WIDTH,
  MOBILE_BREAKPOINT,
  SPLITTER_WIDTH,
  THREAD_RAIL_WIDTH,
  solveWorkbenchGeometry,
};
