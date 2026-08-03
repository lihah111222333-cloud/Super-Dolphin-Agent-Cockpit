import React, { useRef } from 'react';
import { Button as AriaButton, Dialog, DialogTrigger, Popover } from 'react-aria-components';
import { runtimeLogInlineLabel, runtimeLogTimestamp, warningDetailText } from '../adapters/runtimeLogAdapter.js';
import { warningLogPopoverStyle } from './runtimeActivityGeometry.js';

function runtimePopoverShouldCloseOnInteractOutside(element) {
  return !element.closest('.activity-panel-resizer');
}

function RuntimeLogLines({ activeWarning, activeWarningEntry, entries, formatTime, onWarningOpenChange }) {
  return (
    <div className="log-lines" data-testid="warning-log-panel" aria-label="最近活动">
      {entries.length === 0 ? (
        <div className="runtime-log-empty">
          <strong>暂无运行活动</strong>
          <span>任务开始后，事件与结果会显示在这里。</span>
        </div>
      ) : null}
      {entries.map((entry) => (
        <RuntimeLogLine
          key={entry.id}
          activeWarning={activeWarning}
          activeWarningEntry={activeWarningEntry}
          entry={entry}
          formatTime={formatTime}
          onOpenChange={onWarningOpenChange}
        />
      ))}
    </div>
  );
}

function RuntimeLogLine({ activeWarning, activeWarningEntry, entry, formatTime, onOpenChange }) {
  const triggerRef = useRef(null);
  const isOpen = activeWarning?.id === entry.id;
  return (
    <DialogTrigger isOpen={isOpen} onOpenChange={(open) => onOpenChange(entry.id, open, triggerRef.current)}>
      <AriaButton
        ref={triggerRef}
        type="button"
        className={`warning-log-line runtime-log-line--${entry.runtimeKind || 'warning'}`}
        aria-expanded={isOpen}
        aria-haspopup="dialog"
      >
        <time>{formatTime(runtimeLogTimestamp(entry))}</time>
        <span className="runtime-log-kind">{entry.runtimeKind === 'result' ? '结果' : '警告'}</span>
        <b>{runtimeLogInlineLabel(entry)}</b>
        {Number(entry.occurrenceCount) > 1 ? <span> ×{Number(entry.occurrenceCount)}</span> : null}
      </AriaButton>
      {isOpen ? <RuntimeWarningPopover entry={activeWarningEntry} formatTime={formatTime} hoverState={activeWarning} /> : null}
    </DialogTrigger>
  );
}

function RuntimeWarningPopover({ entry, formatTime, hoverState }) {
  if (!entry) return null;
  return (
    <Popover className="warning-log-popover" data-testid="warning-log-popover" style={warningLogPopoverStyle(hoverState.anchorRect, hoverState.panelRect)} shouldCloseOnInteractOutside={runtimePopoverShouldCloseOnInteractOutside}>
      <Dialog aria-label={runtimeLogInlineLabel(entry)} className="warning-log-popover-dialog">
        <span className="warning-log-popover-title">
          <time>{formatTime(runtimeLogTimestamp(entry))}</time>
          <b>{runtimeLogInlineLabel(entry)}</b>
        </span>
        <code>{warningDetailText(entry)}</code>
      </Dialog>
    </Popover>
  );
}

export { RuntimeLogLines, RuntimeWarningPopover };
