const THREAD_RAIL_WINDOW_THRESHOLD = 80;
const THREAD_RAIL_ROW_HEIGHT = 68;
const THREAD_RAIL_DEFAULT_VIEWPORT_HEIGHT = 640;
const THREAD_RAIL_OVERSCAN = 6;

function computeThreadWindow(threads, windowState) {
  if (threads.length <= THREAD_RAIL_WINDOW_THRESHOLD) {
    return { rows: threads, topSpacer: 0, bottomSpacer: 0, virtualized: false };
  }
  const viewportHeight = Math.max(
    THREAD_RAIL_ROW_HEIGHT,
    Number(windowState.viewportHeight) || THREAD_RAIL_DEFAULT_VIEWPORT_HEIGHT,
  );
  const scrollTop = Math.max(0, Number(windowState.scrollTop) || 0);
  const startIndex = Math.max(0, Math.floor(scrollTop / THREAD_RAIL_ROW_HEIGHT) - THREAD_RAIL_OVERSCAN);
  const visibleCount = Math.ceil(viewportHeight / THREAD_RAIL_ROW_HEIGHT) + THREAD_RAIL_OVERSCAN * 2;
  const endIndex = Math.min(threads.length, startIndex + visibleCount);
  return {
    rows: threads.slice(startIndex, endIndex),
    topSpacer: startIndex * THREAD_RAIL_ROW_HEIGHT,
    bottomSpacer: Math.max(0, (threads.length - endIndex) * THREAD_RAIL_ROW_HEIGHT),
    virtualized: true,
  };
}

export {
  THREAD_RAIL_DEFAULT_VIEWPORT_HEIGHT,
  THREAD_RAIL_OVERSCAN,
  THREAD_RAIL_ROW_HEIGHT,
  THREAD_RAIL_WINDOW_THRESHOLD,
  computeThreadWindow,
};
