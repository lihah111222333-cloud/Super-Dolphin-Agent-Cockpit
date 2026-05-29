import {
  ref,
  computed,
  watch,
  onBeforeUnmount,
} from '../../lib/vue.esm-browser.prod.js';

export function useResizePanels(opts) {
  const {
    isCmd,
    modeKey,
    showWorkspace,
    threadStore,
    workspaceRef,
  } = opts;

  const dragging = ref(false);
  const threadRailDragging = ref(false);
  const activityPanelDragging = ref(false);
  const splitRatio = ref(threadStore.getSplitRatio(modeKey.value));
  const THREAD_RAIL_DEFAULT_WIDTH = 232;
  const threadRailWidth = ref(
    typeof threadStore.getThreadRailWidth === 'function'
      ? threadStore.getThreadRailWidth()
      : THREAD_RAIL_DEFAULT_WIDTH,
  );
  const ACTIVITY_PANEL_DEFAULT_HEIGHT = 124;
  const ACTIVITY_PANEL_MIN_HEIGHT = 124;
  const ACTIVITY_PANEL_MAX_HEIGHT = 460;
  const activityPanelHeight = ref(ACTIVITY_PANEL_DEFAULT_HEIGHT);

  let clearThreadRailResizeListeners = () => { };
  let clearActivityPanelResizeListeners = () => { };

  function clampThreadRailWidth(value) {
    const number = Number(value);
    const fallback = THREAD_RAIL_DEFAULT_WIDTH;
    const normalized = Number.isFinite(number) ? Math.round(number) : fallback;
    return Math.max(188, Math.min(420, normalized));
  }

  const threadRailStyle = computed(() => {
    if (isCmd.value) return {};
    const width = clampThreadRailWidth(threadRailWidth.value);
    return {
      width: `${width}px`,
      flex: `0 0 ${width}px`,
    };
  });

  const chatComposerShellStyle = computed(() => {
    if (isCmd.value) return {};
    const ratio = Math.max(30, Math.min(75, Math.round(Number(splitRatio.value) || 60)));
    return {
      width: `calc(${ratio}% - 6px)`,
    };
  });

  function clampActivityPanelHeight(value, maxHeight = ACTIVITY_PANEL_MAX_HEIGHT) {
    const number = Number(value);
    const fallback = ACTIVITY_PANEL_DEFAULT_HEIGHT;
    const normalized = Number.isFinite(number) ? Math.round(number) : fallback;
    const cappedMax = Math.max(
      ACTIVITY_PANEL_MIN_HEIGHT,
      Math.floor(Number(maxHeight) || ACTIVITY_PANEL_MAX_HEIGHT),
    );
    return Math.max(ACTIVITY_PANEL_MIN_HEIGHT, Math.min(cappedMax, normalized));
  }

  const activityPanelRowStyle = computed(() => {
    if (isCmd.value) return {};
    return {
      '--activity-panel-base-height': `${ACTIVITY_PANEL_MIN_HEIGHT}px`,
      '--activity-panel-overlay-height': `${clampActivityPanelHeight(activityPanelHeight.value)}px`,
      '--activity-panel-fixed-height': `${ACTIVITY_PANEL_MIN_HEIGHT}px`,
    };
  });

  function onThreadRailResizeStart(event) {
    if (isCmd.value) return;
    if (event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    document.body.classList.add('is-col-resizing');
    threadRailDragging.value = true;
    clearThreadRailResizeListeners();

    const startX = event.clientX;
    const startWidth = clampThreadRailWidth(threadRailWidth.value);

    const onMove = (e) => {
      const delta = e.clientX - startX;
      threadRailWidth.value = clampThreadRailWidth(startWidth + delta);
    };

    const onUp = () => {
      threadRailDragging.value = false;
      document.body.classList.remove('is-col-resizing');
      clearThreadRailResizeListeners();
    };

    clearThreadRailResizeListeners = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      window.removeEventListener('blur', onUp);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    window.addEventListener('blur', onUp);
  }

  function onResizeStart(event) {
    if (showWorkspace?.value === false) return;
    if (event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    document.body.classList.add('is-col-resizing');
    dragging.value = true;

    const onMove = (e) => {
      const root = workspaceRef.value;
      if (!root) return;
      const rect = root.getBoundingClientRect();
      if (!rect.width) return;
      const next = ((e.clientX - rect.left) / rect.width) * 100;
      splitRatio.value = Math.max(30, Math.min(75, Math.round(next)));
    };

    const onUp = () => {
      dragging.value = false;
      document.body.classList.remove('is-col-resizing');
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      window.removeEventListener('blur', onUp);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    window.addEventListener('blur', onUp);
  }

  function onActivityResizeStart(event) {
    if (isCmd.value) return;
    if (event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    document.body.classList.add('is-row-resizing');
    activityPanelDragging.value = true;
    clearActivityPanelResizeListeners();

    const startY = event.clientY;
    const startHeight = clampActivityPanelHeight(activityPanelHeight.value);
    const viewportMaxHeight = Math.max(
      ACTIVITY_PANEL_MIN_HEIGHT,
      Math.floor(window.innerHeight * 0.72),
    );
    const maxHeight = Math.max(ACTIVITY_PANEL_MAX_HEIGHT, viewportMaxHeight);

    const onMove = (e) => {
      const nextHeight = startHeight + (startY - e.clientY);
      activityPanelHeight.value = clampActivityPanelHeight(nextHeight, maxHeight);
    };

    const onUp = () => {
      activityPanelDragging.value = false;
      document.body.classList.remove('is-row-resizing');
      clearActivityPanelResizeListeners();
    };

    clearActivityPanelResizeListeners = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      window.removeEventListener('blur', onUp);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    window.addEventListener('blur', onUp);
  }

  watch(
    () => modeKey.value,
    () => {
      splitRatio.value = threadStore.getSplitRatio(modeKey.value);
      if (typeof threadStore.getThreadRailWidth === 'function') {
        threadRailWidth.value = threadStore.getThreadRailWidth();
      }
    },
    { immediate: true },
  );

  watch(
    () => splitRatio.value,
    (value) => {
      threadStore.setSplitRatio(modeKey.value, value);
    },
  );

  watch(
    () => threadRailWidth.value,
    (value) => {
      const next = clampThreadRailWidth(value);
      if (next !== value) {
        threadRailWidth.value = next;
        return;
      }
      if (typeof threadStore.setThreadRailWidth === 'function') {
        threadStore.setThreadRailWidth(next);
      }
    },
  );

  onBeforeUnmount(() => {
    dragging.value = false;
    threadRailDragging.value = false;
    activityPanelDragging.value = false;
    document.body.classList.remove('is-col-resizing');
    document.body.classList.remove('is-row-resizing');
    clearThreadRailResizeListeners();
    clearThreadRailResizeListeners = () => { };
    clearActivityPanelResizeListeners();
    clearActivityPanelResizeListeners = () => { };
  });

  return {
    dragging,
    threadRailDragging,
    activityPanelDragging,
    splitRatio,
    threadRailWidth,
    activityPanelHeight,
    threadRailStyle,
    chatComposerShellStyle,
    activityPanelRowStyle,
    onResizeStart,
    onThreadRailResizeStart,
    onActivityResizeStart,
  };
}
