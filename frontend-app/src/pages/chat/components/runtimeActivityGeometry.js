const FLOATING_POPOVER_MARGIN = 12;
const RUNTIME_STAT_TOOLTIP_WIDTH = 360;
const RUNTIME_STAT_TOOLTIP_MIN_HEIGHT = 96;
const WARNING_POPOVER_MIN_WIDTH = 280;

function currentViewportWidth() {
  if (typeof window === 'undefined') return 0;
  const width = Number(window.innerWidth);
  return Number.isFinite(width) ? width : 0;
}

function currentViewportHeight() {
  if (typeof window === 'undefined') return 0;
  const height = Number(window.innerHeight);
  return Number.isFinite(height) ? height : 0;
}

function elementViewportRect(element) {
  if (!element?.getBoundingClientRect) return null;
  const rect = element.getBoundingClientRect();
  return {
    left: rect.left,
    right: rect.right,
    top: rect.top,
    bottom: rect.bottom,
    width: rect.width,
    height: rect.height,
  };
}

function runtimeStatTooltipStyle(anchorRect) {
  if (!anchorRect) return {};
  const viewportWidth = currentViewportWidth();
  const viewportHeight = currentViewportHeight();
  const maxLeft = Math.max(FLOATING_POPOVER_MARGIN, viewportWidth - RUNTIME_STAT_TOOLTIP_WIDTH - FLOATING_POPOVER_MARGIN);
  const left = Math.max(FLOATING_POPOVER_MARGIN, Math.min(maxLeft, Math.round(anchorRect.left)));
  const preferredBottom = Math.max(FLOATING_POPOVER_MARGIN, Math.round(viewportHeight - anchorRect.top + 10));
  const maxBottom = Math.max(FLOATING_POPOVER_MARGIN, viewportHeight - FLOATING_POPOVER_MARGIN - RUNTIME_STAT_TOOLTIP_MIN_HEIGHT);
  const bottom = Math.min(preferredBottom, maxBottom);
  const maxHeight = Math.max(
    RUNTIME_STAT_TOOLTIP_MIN_HEIGHT,
    Math.round(viewportHeight - bottom - FLOATING_POPOVER_MARGIN),
  );
  return {
    '--runtime-stat-tooltip-left': `${left}px`,
    '--runtime-stat-tooltip-bottom': `${bottom}px`,
    '--runtime-stat-tooltip-max-height': `${maxHeight}px`,
  };
}

function warningLogPopoverStyle(anchorRect, panelRect) {
  if (!anchorRect || !panelRect) return {};
  const viewportWidth = currentViewportWidth();
  const viewportHeight = currentViewportHeight();
  const preferredLeft = Math.round(panelRect.left + 18);
  const preferredRight = Math.round(viewportWidth - panelRect.right + 18);
  const leftLimit = Math.max(FLOATING_POPOVER_MARGIN, viewportWidth - WARNING_POPOVER_MIN_WIDTH - FLOATING_POPOVER_MARGIN);
  const left = Math.max(FLOATING_POPOVER_MARGIN, Math.min(leftLimit, preferredLeft));
  const right = Math.max(FLOATING_POPOVER_MARGIN, preferredRight);
  const bottom = Math.max(FLOATING_POPOVER_MARGIN, Math.round(viewportHeight - anchorRect.top + 10));
  return {
    '--warning-log-popover-left': `${left}px`,
    '--warning-log-popover-right': `${right}px`,
    '--warning-log-popover-bottom': `${bottom}px`,
  };
}

export { elementViewportRect, runtimeStatTooltipStyle, warningLogPopoverStyle };
