import React from 'react';
import { Archive, Pencil, Pin, Trash2 } from 'lucide-react';

function ThreadCardActions({
  thread,
  threadLabel,
  editing,
  archiveLabel,
  hoveredArchiveThreadId,
  hoveredPinThreadId,
  loading,
  onBeginRename,
  onSetHoveredArchiveThreadId,
  onSetHoveredPinThreadId,
  onToggleArchive,
  onTogglePin,
  onBeginDelete,
}) {
  return (
    <div className="thread-card-actions" aria-label={`${threadLabel} 操作`}>
      {!editing ? (
        <>
          <button
            type="button"
            className="thread-rename-trigger"
            aria-label="重命名会话"
            title={`重命名 ${threadLabel}`}
            onClick={onBeginRename}
          >
            <Pencil size={13} aria-hidden="true" />
          </button>
          {thread.archived ? (
            <button
              type="button"
              className="thread-delete-trigger"
              aria-label="删除会话"
              title={`删除 ${threadLabel}`}
              onClick={onBeginDelete}
            >
              <Trash2 size={13} aria-hidden="true" />
            </button>
          ) : (
            <ThreadPinButton
              thread={thread}
              hoveredPinThreadId={hoveredPinThreadId}
              onSetHoveredPinThreadId={onSetHoveredPinThreadId}
              onToggle={onTogglePin}
            />
          )}
        </>
      ) : null}
      <ThreadArchiveButton
        thread={thread}
        archiveLabel={archiveLabel}
        hoveredArchiveThreadId={hoveredArchiveThreadId}
        loading={loading}
        onSetHoveredArchiveThreadId={onSetHoveredArchiveThreadId}
        onToggle={onToggleArchive}
      />
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

function ThreadPinButton({ thread, hoveredPinThreadId, onSetHoveredPinThreadId, onToggle }) {
  const pinned = thread.pinnedAt > 0 || thread.pinned;
  const pinLabel = pinned ? '取消置顶对话' : '置顶对话';
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
