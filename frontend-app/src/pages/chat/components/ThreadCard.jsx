import React, { useEffect, useRef } from 'react';
import {
  displayThreadName,
  threadCardStatusLabel,
  threadProviderLabel,
  threadStatusBusy,
  threadStatusDotState,
  threadStatusDotTitle,
} from '../adapters/threadStateAdapter.js';
import { ThreadCardActions } from './ThreadCardActions.jsx';
import { ThreadDisplayCardContent as ThreadDisplayCardContentView } from './ThreadDisplayCardContent.jsx';
import { runUIAction } from './chatUiActions.js';

function ThreadCard({
  thread,
  store,
  active,
  editing,
  editingName,
  hoveredArchiveThreadId,
  hoveredPinThreadId,
  renaming,
  onBeginRename,
  onCancelRename,
  onRenameBlur,
  onSetEditingName,
  onSetHoveredArchiveThreadId,
  onSetHoveredPinThreadId,
  onSubmitRename,
  deleting,
  onBeginDelete,
  onCancelDelete,
  onConfirmDelete,
}) {
  const archiveLabel = thread.archived ? '恢复会话' : '归档会话';
  const threadLabel = displayThreadName(thread);
  if (deleting) {
    return (
      <div className={`thread-card ${active ? 'active' : ''} thread-card--deleting`}>
        <div className="thread-delete-confirm-label">确定删除该会话？</div>
        <div className="thread-delete-confirm-actions">
          <button type="button" className="thread-delete-confirm-btn confirm" onClick={onConfirmDelete}>确认</button>
          <button type="button" className="thread-delete-confirm-btn cancel" onClick={onCancelDelete}>取消</button>
        </div>
      </div>
    );
  }
  return (
    <div className={`thread-card ${active ? 'active' : ''}`}>
      {editing ? (
        <ThreadRenameCardContent
          thread={thread}
          editingName={editingName}
          renaming={renaming}
          onCancelRename={onCancelRename}
          onRenameBlur={onRenameBlur}
          onSetEditingName={onSetEditingName}
          onSubmitRename={onSubmitRename}
        />
      ) : (
        <ThreadDisplayCardContent thread={thread} store={store} />
      )}
      <ThreadCardActions
        thread={thread}
        threadLabel={threadLabel}
        editing={editing}
        archiveLabel={archiveLabel}
        hoveredArchiveThreadId={hoveredArchiveThreadId}
        hoveredPinThreadId={hoveredPinThreadId}
        loading={Boolean(store.threadArchiveLoadingByThread?.[thread.id])}
        onBeginRename={() => onBeginRename(thread)}
        onSetHoveredArchiveThreadId={onSetHoveredArchiveThreadId}
        onSetHoveredPinThreadId={onSetHoveredPinThreadId}
        onToggleArchive={() => runUIAction(() => store.archiveThread(thread.id, !thread.archived))}
        onTogglePin={() => runUIAction(() => store.toggleThreadPin(thread.id))}
        onBeginDelete={onBeginDelete}
      />
    </div>
  );
}

function ThreadRenameCardContent({ thread, editingName, renaming, onCancelRename, onRenameBlur, onSetEditingName, onSubmitRename }) {
  const inputRef = useRef(null);
  useEffect(() => {
    const input = inputRef.current;
    if (!input || renaming) return;
    input.focus({ preventScroll: true });
    input.select();
  }, [renaming]);

  return (
    <div className="thread-main thread-main--editing">
      <input
        ref={inputRef}
        className="thread-name-input"
        aria-label="会话别名"
        value={editingName}
        maxLength={64}
        disabled={renaming}
        onFocus={(event) => event.currentTarget.select()}
        onChange={(event) => onSetEditingName(event.target.value)}
        onClick={(event) => event.stopPropagation()}
        onBlur={(event) => onRenameBlur(event, thread)}
        onKeyDown={(event) => handleThreadRenameKeyDown(event, thread, onSubmitRename, onCancelRename)}
      />
      <button
        type="button"
        className="thread-rename-save"
        aria-label="保存别名"
        data-rename-save-button-for={thread.id}
        disabled={renaming}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => runUIAction(() => onSubmitRename(thread))}
      >
        保存
      </button>
    </div>
  );
}

function handleThreadRenameKeyDown(event, thread, onSubmitRename, onCancelRename) {
  if (event.key === 'Enter') {
    event.preventDefault();
    runUIAction(() => onSubmitRename(thread));
  }
  if (event.key === 'Escape') {
    event.preventDefault();
    onCancelRename();
  }
}

function ThreadDisplayCardContent({ thread, store }) {
  const running = threadStatusBusy(thread.status);
  const threadLabel = displayThreadName(thread);
  const statusLabel = threadCardStatusLabel(thread, running);
  const statusDotState = threadStatusDotState(thread.status);
  const statusDotTitle = threadStatusDotTitle(thread.status, statusLabel);
  return (
    <ThreadDisplayCardContentView
      providerLabel={threadProviderLabel(thread.provider)}
      staleReason={thread.staleReason}
      statusDotState={statusDotState}
      statusDotTitle={statusDotTitle}
      statusLabel={statusLabel}
      threadLabel={threadLabel}
      onSelect={() => runUIAction(() => store.setActiveThread(thread.id))}
    />
  );
}

export { ThreadCard };
