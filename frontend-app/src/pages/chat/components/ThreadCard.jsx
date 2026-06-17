import React, { useEffect, useRef } from 'react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
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
  copy = APP_COPY.zh.chat,
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
  const archiveLabel = thread.archived ? copy.restoreThread : copy.archiveThread;
  const threadLabel = displayThreadName(thread);
  if (deleting) {
    return (
      <div className={`thread-card ${active ? 'active' : ''} thread-card--deleting`}>
        <div className="thread-delete-confirm-label">{copy.deleteThreadQuestion}</div>
        <div className="thread-delete-confirm-actions">
          <button type="button" className="thread-delete-confirm-btn confirm" onClick={onConfirmDelete}>{copy.confirm}</button>
          <button type="button" className="thread-delete-confirm-btn cancel" onClick={onCancelDelete}>{copy.cancel}</button>
        </div>
      </div>
    );
  }
  const running = threadStatusBusy(thread.status);
  return (
    <div className={`thread-card ${active ? 'active' : ''}`}>
      {editing ? (
        <ThreadRenameCardContent
          copy={copy}
          thread={thread}
          editingName={editingName}
          renaming={renaming}
          onCancelRename={onCancelRename}
          onRenameBlur={onRenameBlur}
          onSetEditingName={onSetEditingName}
          onSubmitRename={onSubmitRename}
        />
      ) : (
        <ThreadDisplayCardContent thread={thread} store={store} onBeginRename={() => onBeginRename(thread)} />
      )}
      <ThreadCardActions
        thread={thread}
        threadLabel={threadLabel}
        copy={copy}
        editing={editing}
        running={running}
        runningLabel={copy.threadRunning || '会话运行中'}
        archiveLabel={archiveLabel}
        hoveredArchiveThreadId={hoveredArchiveThreadId}
        hoveredPinThreadId={hoveredPinThreadId}
        loading={Boolean(store.threadArchiveLoadingByThread?.[thread.id])}
        onSetHoveredArchiveThreadId={onSetHoveredArchiveThreadId}
        onSetHoveredPinThreadId={onSetHoveredPinThreadId}
        onToggleArchive={() => runUIAction(() => store.archiveThread(thread.id, !thread.archived))}
        onTogglePin={() => runUIAction(() => store.toggleThreadPin(thread.id))}
        onBeginDelete={onBeginDelete}
      />
    </div>
  );
}

function ThreadRenameCardContent({ copy = APP_COPY.zh.chat, thread, editingName, renaming, onCancelRename, onRenameBlur, onSetEditingName, onSubmitRename }) {
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
        aria-label={copy.threadAlias}
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
        aria-label={copy.saveAlias}
        data-rename-save-button-for={thread.id}
        disabled={renaming}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => runUIAction(() => onSubmitRename(thread))}
      >
        {copy.saveAlias}
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

function ThreadDisplayCardContent({ thread, store, onBeginRename }) {
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
      onBeginRename={onBeginRename}
      onSelect={() => runUIAction(() => store.setActiveThread(thread.id))}
    />
  );
}

export { ThreadCard };
