import React from 'react';
import { Archive, Pin, RefreshCw, Trash2 } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';

function ThreadCardActions({
  thread,
  threadLabel,
  copy = APP_COPY.zh.chat,
  editing,
  archiveLabel,
  hoveredArchiveThreadId,
  hoveredPinThreadId,
  loading,
  running = false,
  runningLabel = '会话运行中',
  onSetHoveredArchiveThreadId,
  onSetHoveredPinThreadId,
  onToggleArchive,
  onTogglePin,
  onBeginDelete,
}) {
  return (
    <div className="thread-card-actions" aria-label={`${threadLabel} ${copy.threadActionsSuffix}`}>
      {!editing ? (
        <>
          {!thread.archived ? (
            <ThreadPinButton
              copy={copy}
              thread={thread}
              hoveredPinThreadId={hoveredPinThreadId}
              onSetHoveredPinThreadId={onSetHoveredPinThreadId}
              onToggle={onTogglePin}
            />
          ) : null}
          {running ? (
            <span className="thread-running-spinner" aria-label={runningLabel} title={runningLabel}>
              <RefreshCw size={13} aria-hidden="true" />
            </span>
          ) : null}
          <button
            type="button"
            className="thread-delete-trigger"
            aria-label={copy.deleteThread}
            title={`${copy.deleteThread} ${threadLabel}`}
            onClick={onBeginDelete}
          >
            <Trash2 size={13} aria-hidden="true" />
          </button>
          {thread.archived ? (
            <ThreadArchiveButton
              thread={thread}
              archiveLabel={archiveLabel}
              hoveredArchiveThreadId={hoveredArchiveThreadId}
              loading={loading}
              onSetHoveredArchiveThreadId={onSetHoveredArchiveThreadId}
              onToggle={onToggleArchive}
            />
          ) : null}
        </>
      ) : null}
    </div>
  );
}

function ThreadArchiveButton({ thread, archiveLabel, hoveredArchiveThreadId, loading, onSetHoveredArchiveThreadId, onToggle }) {
  const clearHover = () => onSetHoveredArchiveThreadId((current) => (current === thread.id ? '' : current));
  return (
    <button
      type="button"
      className={`thread-archive ${thread.archived ? 'active' : ''}`}
      aria-label={archiveLabel}
      disabled={loading}
      onClick={onToggle}
      onMouseEnter={() => onSetHoveredArchiveThreadId(thread.id)}
      onMouseLeave={clearHover}
      onFocus={() => onSetHoveredArchiveThreadId(thread.id)}
      onBlur={clearHover}
    >
      <Archive size={15} />
      {hoveredArchiveThreadId === thread.id ? (
        <span className="thread-action-tooltip" data-testid="thread-archive-tooltip" role="tooltip">
          {archiveLabel}
        </span>
      ) : null}
    </button>
  );
}

function ThreadPinButton({ copy = APP_COPY.zh.chat, thread, hoveredPinThreadId, onSetHoveredPinThreadId, onToggle }) {
  const pinned = thread.pinnedAt > 0 || thread.pinned;
  const pinLabel = pinned ? copy.unpinThread : copy.pinThread;
  const clearHover = () => onSetHoveredPinThreadId((current) => (current === thread.id ? '' : current));
  return (
    <button
      type="button"
      className={`thread-pin ${pinned ? 'active' : ''}`}
      aria-label={pinLabel}
      aria-pressed={pinned}
      onClick={onToggle}
      onMouseEnter={() => onSetHoveredPinThreadId(thread.id)}
      onMouseLeave={clearHover}
      onFocus={() => onSetHoveredPinThreadId(thread.id)}
      onBlur={clearHover}
    >
      <Pin size={14} strokeWidth={2.2} />
      {hoveredPinThreadId === thread.id ? (
        <span className="thread-pin-tooltip" data-testid="thread-pin-tooltip" role="tooltip">
          {pinLabel}
        </span>
      ) : null}
    </button>
  );
}

export { ThreadCardActions };
