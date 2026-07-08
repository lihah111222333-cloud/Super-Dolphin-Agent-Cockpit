import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { activityStatDetailEntries, activityStatItems } from '../adapters/runtimeActivityAdapter.js';
import { runtimeLogEntries } from '../adapters/runtimeLogAdapter.js';
import { RuntimeLogLines, RuntimeWarningPopover } from './RuntimeActivityLog.jsx';
import { RuntimeStatList, RuntimeStatTooltip } from './RuntimeActivityStats.jsx';
import { requiredMarkdownArray, requiredMarkdownObject } from '../markdown/markdownMessageModel.js';
import { elementViewportRect } from './runtimeActivityGeometry.js';

const NO_ACTIVE_STAT_DETAILS = Object.freeze([]);
const NO_ACTIVITY_STATS = Object.freeze({});

function RuntimeActivityPanel({
  activityStats,
  tokenUsage,
  warnings,
  runtimeResults,
  activityPanelMax,
  activityPanelHeight,
  activityPanelMinHeight,
  formatTime,
  onResizeKeyDown,
  onResizeStart,
}) {
  /*
   * 活动面板只展示传入的 runtime 视图。
   * tooltip、popover 是本地交互状态，不要写回 store。
   */
  const [activeStat, setActiveStat] = useState(null);
  const [activeWarning, setActiveWarning] = useState(null);
  const panelRef = useRef(null);
  const runtimePopupOpenRef = useRef(false);
  const stats = useMemo(() => (activityStats ? requiredMarkdownObject(activityStats, 'activityStats') : NO_ACTIVITY_STATS), [activityStats]);
  const statItems = useMemo(() => activityStatItems(stats), [stats]);
  const detailEntriesByStat = useMemo(() => Object.fromEntries(
    statItems.map((item) => [item.key, activityStatDetailEntries(stats, item.key)]),
  ), [statItems, stats]);
  const logEntries = useMemo(() => runtimeLogEntries(warnings, runtimeResults), [warnings, runtimeResults]);
  const logLinesVisible = activityPanelHeight > activityPanelMinHeight;
  const visibleActiveWarning = logLinesVisible ? activeWarning : null;
  const activeWarningEntry = useMemo(
    () => logEntries.find((entry) => entry.id === visibleActiveWarning?.id) || null,
    [visibleActiveWarning, logEntries],
  );
  const activeStatItem = useMemo(
    () => statItems.find((item) => item.key === activeStat?.key) || null,
    [activeStat, statItems],
  );
  const activeStatDetailEntries = activeStat
    ? requiredMarkdownArray(detailEntriesByStat[activeStat.key], 'activeStat.detailEntries')
    : NO_ACTIVE_STAT_DETAILS;
  const hideStatTooltip = useCallback(() => setActiveStat(null), []);
  const hideWarningPopover = useCallback(() => setActiveWarning(null), []);
  const toggleStatTooltip = (key, element) => {
    setActiveWarning(null);
    setActiveStat((current) => (
      current?.key === key ? null : { key, anchorRect: elementViewportRect(element) }
    ));
  };
  const toggleWarningPopover = (id, element) => {
    setActiveStat(null);
    setActiveWarning((current) => (
      current?.id === id ? null : {
        id,
        anchorRect: elementViewportRect(element),
        panelRect: elementViewportRect(panelRef.current),
      }
    ));
  };
  const handleStatKeyDown = (event, key) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggleStatTooltip(key, event.currentTarget);
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      hideStatTooltip();
    }
  };
  const handleWarningKeyDown = (event, id) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggleWarningPopover(id, event.currentTarget);
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      hideWarningPopover();
    }
  };

  if (!logLinesVisible && activeWarning) setActiveWarning(null);

  useEffect(() => {
    runtimePopupOpenRef.current = Boolean(activeStat || visibleActiveWarning);
  }, [activeStat, visibleActiveWarning]);

  useEffect(() => {
    const keepRuntimePopupOpen = (target) => {
      if (!(target instanceof Element)) return false;
      return Boolean(target.closest('.runtime-stat, .runtime-stat-tooltip, .warning-log-line, .warning-log-popover, .activity-panel-resizer'));
    };
    const handleDocumentDismiss = (event) => {
      if (!runtimePopupOpenRef.current || keepRuntimePopupOpen(event.target)) return;
      setActiveStat(null);
      setActiveWarning(null);
    };
    const handleDocumentKeyDown = (event) => {
      if (!runtimePopupOpenRef.current || event.key !== 'Escape') return;
      setActiveStat(null);
      setActiveWarning(null);
    };
    document.addEventListener('pointerdown', handleDocumentDismiss);
    document.addEventListener('click', handleDocumentDismiss);
    document.addEventListener('keydown', handleDocumentKeyDown);
    return () => {
      document.removeEventListener('pointerdown', handleDocumentDismiss);
      document.removeEventListener('click', handleDocumentDismiss);
      document.removeEventListener('keydown', handleDocumentKeyDown);
    };
  }, []);

  return (
    <section className={`runtime-activity-panel${logLinesVisible ? '' : ' is-log-collapsed'}`} aria-label="工具使用面板" ref={panelRef}>
      <RuntimeActivityResizer
        activityPanelMax={activityPanelMax}
        activityPanelHeight={activityPanelHeight}
        activityPanelMinHeight={activityPanelMinHeight}
        onResizeKeyDown={onResizeKeyDown}
        onResizeStart={onResizeStart}
      />
      <RuntimeStatList
        activeStat={activeStat}
        onStatKeyDown={handleStatKeyDown}
        onToggleStat={toggleStatTooltip}
        statItems={statItems}
        tokenUsage={tokenUsage}
      />
      <RuntimeStatTooltip activeStat={activeStat} detailEntries={activeStatDetailEntries} item={activeStatItem} />
      {logLinesVisible ? (
        <RuntimeLogLines
          activeWarning={visibleActiveWarning}
          entries={logEntries}
          formatTime={formatTime}
          onWarningKeyDown={handleWarningKeyDown}
          onToggleWarning={toggleWarningPopover}
        />
      ) : null}
      <RuntimeWarningPopover entry={activeWarningEntry} formatTime={formatTime} hoverState={visibleActiveWarning} />
    </section>
  );
}

function RuntimeActivityResizer({ activityPanelMax, activityPanelHeight, activityPanelMinHeight, onResizeKeyDown, onResizeStart }) {
  return (
    <button
      type="button"
      className="activity-panel-resizer"
      role="separator"
      aria-label="调整工具使用面板高度"
      aria-orientation="horizontal"
      aria-valuemin={activityPanelMinHeight}
      aria-valuemax={activityPanelMax}
      aria-valuenow={activityPanelHeight}
      title="拖动调整工具使用面板高度，最大为应用高度的 1/2"
      data-testid="activity-panel-resizer"
      onKeyDown={onResizeKeyDown}
      onPointerDown={(event) => onResizeStart(event, 'pointer')}
      onMouseDown={(event) => {
        if (!window.PointerEvent) onResizeStart(event, 'mouse');
      }}
    >
      <span className="sr-only">调整工具使用面板高度，当前 {activityPanelHeight} 像素</span>
    </button>
  );
}

export { RuntimeActivityPanel };
