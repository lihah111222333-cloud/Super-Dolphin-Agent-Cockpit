import React from 'react';
import { Check, RefreshCw, Trash2, X } from 'lucide-react';
import { threadStatusBusy } from './app/appShellModel.js';
import { APP_COPY } from './shared/i18n/appI18n.js';
import { formatRelativeTime } from './WorkbenchSidebarThreadModel.js';

export function SidebarThreadRow({
  active,
  copy = APP_COPY.zh.workbench,
  label,
  onSelect,
  openLabel,
  thread,
  threadActions,
}) {
  const editing = threadActions.editingThreadId === thread.id;
  const deleting = threadActions.deletingThreadId === thread.id;
  const renaming = threadActions.renamingThreadId === thread.id;
  const running = threadStatusBusy(thread.status);
  const runningLabel = copy.threadRunning || '会话运行中';

  if (deleting) {
    return (
      <li className="sidebar-thread-row sidebar-thread-row--confirm">
        <span>{copy.deleteQuestion}</span>
        <div className="sidebar-thread-confirm-actions">
          <button type="button" onClick={(event) => threadActions.confirmDelete(thread.id, event)}>
            {copy.delete}
          </button>
          <button type="button" onClick={threadActions.cancelDelete}>
            {copy.cancel}
          </button>
        </div>
      </li>
    );
  }

  if (editing) {
    return (
      <li className="sidebar-thread-row sidebar-thread-row--editing">
        <form className="sidebar-thread-rename" onBlur={threadActions.handleRenameBlur} onSubmit={(event) => threadActions.submitRename(thread, event)}>
          <input
            aria-label={copy.conversationName}
            autoFocus
            disabled={renaming}
            maxLength={64}
            value={threadActions.editingName}
            onChange={(event) => threadActions.setEditingName(event.target.value)}
            onClick={(event) => event.stopPropagation()}
            onFocus={(event) => event.currentTarget.select()}
            onKeyDown={(event) => {
              if (event.key === 'Escape') threadActions.cancelRename(event);
            }}
          />
          <button
            type="submit"
            aria-label={copy.saveConversationName}
            disabled={renaming}
            onMouseDown={(event) => event.preventDefault()}
          >
            <Check size={13} aria-hidden="true" />
          </button>
          <button
            type="button"
            aria-label={copy.cancelRename}
            disabled={renaming}
            onClick={threadActions.cancelRename}
            onMouseDown={(event) => event.preventDefault()}
          >
            <X size={13} aria-hidden="true" />
          </button>
        </form>
      </li>
    );
  }

  return (
    <li className="sidebar-thread-row">
      <button
        type="button"
        className={`sidebar-thread-item${active ? ' active' : ''}`}
        onClick={onSelect}
        onDoubleClick={(event) => threadActions.beginRename(thread, event)}
        aria-label={openLabel}
        title={label}
      >
        <span className="sidebar-thread-title">{label}</span>
        {thread.updatedAt && (
          <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt, copy.relativeTime)}</span>
        )}
      </button>
      <div className={`thread-inline-actions${running ? ' is-running' : ''}`} aria-label={copy.conversationActions}>
        {running ? (
          <span className="thread-inline-spinner" aria-label={runningLabel} title={runningLabel}>
            <RefreshCw size={13} aria-hidden="true" />
          </span>
        ) : null}
        <button
          type="button"
          className="thread-inline-action-btn danger"
          onClick={(event) => threadActions.beginDelete(thread.id, event)}
          aria-label={`${copy.delete}：${label}`}
          title={copy.delete}
        >
          <Trash2 size={13} aria-hidden="true" />
        </button>
      </div>
    </li>
  );
}
