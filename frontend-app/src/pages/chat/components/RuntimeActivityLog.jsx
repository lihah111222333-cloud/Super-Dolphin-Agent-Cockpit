import React from 'react';
import { runtimeLogInlineLabel, runtimeLogTimestamp, warningDetailText } from '../adapters/runtimeActivityAdapter.js';
import { warningLogPopoverStyle } from './runtimeActivityGeometry.js';

function RuntimeLogLines({ activeWarning, entries, formatTime, onWarningKeyDown, onToggleWarning }) {
  return (
    <div className="log-lines" data-testid="warning-log-panel">
      {entries.length === 0 ? <p><time>--:--</time> runtime log 等待事件</p> : null}
      {entries.map((entry) => (
        <button
          type="button"
          key={entry.id}
          className={`warning-log-line runtime-log-line--${entry.runtimeKind || 'warning'}`}
          aria-expanded={activeWarning?.id === entry.id}
          aria-haspopup="dialog"
          onClick={(event) => {
            event.stopPropagation();
            onToggleWarning(entry.id, event.currentTarget);
          }}
          onKeyDown={(event) => onWarningKeyDown(event, entry.id)}
        >
          <time>{formatTime(runtimeLogTimestamp(entry))}</time> <b>{runtimeLogInlineLabel(entry)}</b>
          {Number(entry.occurrenceCount) > 1 ? <span> ×{Number(entry.occurrenceCount)}</span> : null}
        </button>
      ))}
    </div>
  );
}

function RuntimeWarningPopover({ entry, formatTime, hoverState }) {
  if (!entry) return null;
  return (
    <div className="warning-log-popover" data-testid="warning-log-popover" role="tooltip" style={warningLogPopoverStyle(hoverState.anchorRect, hoverState.panelRect)}>
      <span className="warning-log-popover-title">
        <time>{formatTime(runtimeLogTimestamp(entry))}</time>
        <b>{runtimeLogInlineLabel(entry)}</b>
      </span>
      <code>{warningDetailText(entry)}</code>
    </div>
  );
}

export { RuntimeLogLines, RuntimeWarningPopover };
