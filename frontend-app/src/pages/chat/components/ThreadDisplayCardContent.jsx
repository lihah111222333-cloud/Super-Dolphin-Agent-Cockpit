import React from 'react';

function ThreadDisplayCardContent({
  threadLabel,
  providerLabel,
  statusDotState,
  statusDotTitle,
  statusLabel,
  staleReason,
  onSelect,
}) {
  return (
    <button type="button" className="thread-main" onClick={onSelect}>
      <span className="thread-name" title={threadLabel}>
        {threadLabel}
      </span>
      <b>{providerLabel}</b>
      <ThreadStatusLine
        staleReason={staleReason}
        statusDotState={statusDotState}
        statusDotTitle={statusDotTitle}
        statusLabel={statusLabel}
      />
    </button>
  );
}

function ThreadStatusLine({ staleReason, statusDotState, statusDotTitle, statusLabel }) {
  return (
    <span className="thread-status-row" data-thread-status={statusDotState}>
      <span
        className={`thread-status-dot thread-status-dot--${statusDotState}`}
        title={statusDotTitle}
        aria-hidden="true"
      />
      {statusLabel ? <span className="thread-status-label">{statusLabel}</span> : null}
      {staleReason ? (
        <span className="thread-stale-badge" data-stale-reason={staleReason}>
          {staleReason === 'expired' ? '超7天' : '空对话'}
        </span>
      ) : null}
    </span>
  );
}

export { ThreadDisplayCardContent };
