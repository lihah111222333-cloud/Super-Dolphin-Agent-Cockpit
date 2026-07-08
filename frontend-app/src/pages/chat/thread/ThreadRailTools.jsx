import React from 'react';
import { Archive, ArrowLeft, Bot, Pencil, Trash2 } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';

function ThreadRailTools({
  copy = APP_COPY.zh.chat,
  count,
  confirmCleanMode,
  showArchivedThreads,
  staleThreadIds,
  toggleArchiveLabel,
  onNewThread,
  onCleanConfirm,
  onCleanMode,
  onCancelClean,
  onToggleArchive,
}) {
  return (
    <div className="thread-tools">
      <button type="button" className="round thread-new-primary" aria-label={copy.newThread} title={copy.newThreadTitle} onClick={onNewThread}>
        <Pencil size={17} />
      </button>
      <output className="count thread-count" aria-label={`${count} ${copy.agentCountSuffix}`} title={`${count} ${copy.agentCountSuffix}`}>
        <Bot size={14} />
        <strong>{count}</strong>
      </output>
      {showArchivedThreads && staleThreadIds.length > 0 && !confirmCleanMode ? (
        <button type="button" className="round thread-clean" aria-label={copy.cleanStale} title={copy.cleanStale} onClick={onCleanMode}>
          <Trash2 size={15} />
        </button>
      ) : null}
      {showArchivedThreads && confirmCleanMode ? (
        <>
          <button type="button" className="thread-clean-confirm" onClick={onCleanConfirm}>{copy.confirm}</button>
          <button type="button" className="thread-clean-cancel" onClick={onCancelClean}>{copy.cancel}</button>
        </>
      ) : null}
      <button
        type="button"
        className={`round thread-archive-toggle ${showArchivedThreads ? 'active' : ''}`}
        aria-label={toggleArchiveLabel}
        title={toggleArchiveLabel}
        onClick={onToggleArchive}
      >
        {showArchivedThreads ? <ArrowLeft size={15} /> : <Archive size={15} />}
      </button>
    </div>
  );
}

export { ThreadRailTools };
