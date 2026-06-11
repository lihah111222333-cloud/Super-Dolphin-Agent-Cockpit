import React from 'react';
import { Archive, ArrowLeft, Bot, Pencil, Trash2 } from 'lucide-react';

function ThreadRailTools({
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
      <button type="button" className="round thread-new-primary" aria-label="新建对话" title="新对话：发送第一条消息时才会创建会话" onClick={onNewThread}>
        <Pencil size={17} />
      </button>
      <output className="count thread-count" aria-label={`${count} 个 Agent`} title={`${count} 个 Agent`}>
        <Bot size={14} />
        <strong>{count}</strong>
      </output>
      {showArchivedThreads && staleThreadIds.length > 0 && !confirmCleanMode ? (
        <button type="button" className="round thread-clean" aria-label="清理无用对话" title="清理无用对话" onClick={onCleanMode}>
          <Trash2 size={15} />
        </button>
      ) : null}
      {showArchivedThreads && confirmCleanMode ? (
        <>
          <button type="button" className="thread-clean-confirm" onClick={onCleanConfirm}>确认</button>
          <button type="button" className="thread-clean-cancel" onClick={onCancelClean}>取消</button>
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
