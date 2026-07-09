import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { activityStatDetailEntries, activityStatItems } from '../adapters/runtimeActivityAdapter.js';
import { runtimeLogEntries } from '../adapters/runtimeLogAdapter.js';
import { RuntimeLogLines } from './RuntimeActivityLog.jsx';
import { RuntimeStatList } from './RuntimeActivityStats.jsx';
import { requiredMarkdownArray, requiredMarkdownObject } from '../markdown/markdownMessageModel.js';
import { elementViewportRect } from './runtimeActivityGeometry.js';

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
  const [activePopover, setActivePopover] = useState(null);
  const panelRef = useRef(null);
  const stats = useMemo(() => (activityStats ? requiredMarkdownObject(activityStats, 'activityStats') : NO_ACTIVITY_STATS), [activityStats]);
  const statItems = useMemo(() => activityStatItems(stats), [stats]);
  const detailEntriesByStat = useMemo(() => Object.fromEntries(
    statItems.map((item) => [item.key, activityStatDetailEntries(stats, item.key)]),
  ), [statItems, stats]);
  const logEntries = useMemo(() => runtimeLogEntries(warnings, runtimeResults), [warnings, runtimeResults]);
  const logLinesVisible = activityPanelHeight > activityPanelMinHeight;
  const activeStat = activePopover?.type === 'stat' ? activePopover : null;
  const visibleActiveWarning = logLinesVisible && activePopover?.type === 'warning' ? activePopover : null;
  const activeWarningEntry = useMemo(
    () => logEntries.find((entry) => entry.id === visibleActiveWarning?.id) || null,
    [visibleActiveWarning, logEntries],
  );
  const normalizedDetailEntriesByStat = useMemo(() => Object.fromEntries(
    Object.entries(detailEntriesByStat).map(([key, entries]) => [key, requiredMarkdownArray(entries, 'activeStat.detailEntries')]),
  ), [detailEntriesByStat]);
  const handleStatOpenChange = useCallback((key, open, element) => {
    setActivePopover((current) => {
      if (!open) return current?.type === 'stat' && current.key === key ? null : current;
      return { type: 'stat', key, anchorRect: elementViewportRect(element) };
    });
  }, []);
  const handleWarningOpenChange = useCallback((id, open, element) => {
    setActivePopover((current) => {
      if (!open) return current?.type === 'warning' && current.id === id ? null : current;
      return {
        type: 'warning',
        id,
        anchorRect: elementViewportRect(element),
        panelRect: elementViewportRect(panelRef.current),
      };
    });
  }, []);
  useEffect(() => {
    if (!logLinesVisible && activePopover?.type === 'warning') setActivePopover(null);
  }, [activePopover, logLinesVisible]);

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
        detailEntriesByStat={normalizedDetailEntriesByStat}
        onStatOpenChange={handleStatOpenChange}
        statItems={statItems}
        tokenUsage={tokenUsage}
      />
      {logLinesVisible ? (
        <RuntimeLogLines
          activeWarning={visibleActiveWarning}
          activeWarningEntry={activeWarningEntry}
          entries={logEntries}
          formatTime={formatTime}
          onWarningOpenChange={handleWarningOpenChange}
        />
      ) : null}
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
