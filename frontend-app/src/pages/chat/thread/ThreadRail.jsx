import React, { useState } from 'react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { archivedStaleReason, displayThreadName, threadSortTimestamp } from '../adapters/threadStateAdapter.js';
import { ThreadCard } from './ThreadCard.jsx';
import { ThreadRailTools } from './ThreadRailTools.jsx';
import { runUIAction } from '../model/chatUiActions.js';
import { firstText, textValue, trimmedText } from '../markdown/markdownMessageModel.js';
import './ThreadRail.css';

function ThreadRail({ copy = APP_COPY.zh.chat, store }) {
  const [showArchivedThreads, setShowArchivedThreads] = useState(false);
  const [confirmCleanMode, setConfirmCleanMode] = useState(false);
  const [deletingThreadId, setDeletingThreadId] = useState('');
  const [hoveredArchiveThreadId, setHoveredArchiveThreadId] = useState('');
  const [hoveredPinThreadId, setHoveredPinThreadId] = useState('');
  const rename = useThreadRenameController(store);
  const activeThreads = store.threads.filter((thread) => !thread.archived);
  const archivedThreads = store.threads.filter((thread) => thread.archived);
  const threads = showArchivedThreads ? archivedThreads : activeThreads;
  const chatListLoading = Boolean(store.chatSurfaceLoadingCwd);
  const visibleThreads = visibleThreadRows(threads, store);
  const staleThreadIds = [];
  if (showArchivedThreads) {
    for (const thread of visibleThreads) {
      if (thread.staleReason) staleThreadIds.push(thread.id);
    }
  }
  const toggleArchiveLabel = showArchivedThreads ? copy.returnThreadList : copy.openArchiveList;
  let emptyThreadText = copy.emptyThreads;
  if (chatListLoading && !showArchivedThreads) {
    emptyThreadText = copy.loadingThreads;
  } else if (showArchivedThreads) {
    emptyThreadText = copy.emptyArchive;
  }
  const toggleArchiveList = () => {
    setShowArchivedThreads((value) => {
      const next = !value;
      if (!next) {
        setConfirmCleanMode(false);
        setDeletingThreadId('');
      }
      return next;
    });
  };
  return (
    <aside className="thread-rail" data-testid="thread-rail" aria-label={showArchivedThreads ? copy.archiveList : copy.threadList}>
      <ThreadRailTools
        copy={copy}
        count={visibleThreads.length}
        confirmCleanMode={confirmCleanMode}
        showArchivedThreads={showArchivedThreads}
        staleThreadIds={staleThreadIds}
        toggleArchiveLabel={toggleArchiveLabel}
        onNewThread={store.newThread}
        onCleanConfirm={() => {
          setConfirmCleanMode(false);
          runUIAction('thread.delete', () => store.deleteStaleThreads(staleThreadIds));
        }}
        onCleanMode={() => setConfirmCleanMode(true)}
        onCancelClean={() => setConfirmCleanMode(false)}
        onToggleArchive={toggleArchiveList}
      />
      <div className="thread-list">
        {visibleThreads.length === 0 ? (
          <p className="thread-empty">
            {emptyThreadText}
          </p>
        ) : null}
        {visibleThreads.map((thread) => (
          <ThreadCard
            copy={copy}
            key={thread.id}
            thread={thread}
            store={store}
            active={store.activeThreadId === thread.id}
            editing={rename.editingThreadId === thread.id}
            editingName={rename.editingName}
            hoveredArchiveThreadId={hoveredArchiveThreadId}
            hoveredPinThreadId={hoveredPinThreadId}
            renaming={rename.renamingThreadId === thread.id}
            onBeginRename={rename.beginRename}
            onCancelRename={rename.cancelRename}
            onRenameBlur={rename.handleRenameBlur}
            onSetEditingName={rename.setEditingName}
            onSetHoveredArchiveThreadId={setHoveredArchiveThreadId}
            onSetHoveredPinThreadId={setHoveredPinThreadId}
            onSubmitRename={rename.submitRename}
            deleting={deletingThreadId === thread.id}
            onBeginDelete={() => setDeletingThreadId(thread.id)}
            onCancelDelete={() => setDeletingThreadId('')}
            onConfirmDelete={() => {
              setDeletingThreadId('');
              runUIAction('thread.delete', () => store.deleteStaleThreads([thread.id]));
            }}
          />
        ))}
      </div>
    </aside>
  );
}

function useThreadRenameController(store) {
  const [editingThreadId, setEditingThreadId] = useState('');
  const [editingName, setEditingName] = useState('');
  const [renamingThreadId, setRenamingThreadId] = useState('');

  const beginRename = (thread) => {
    setEditingThreadId(thread.id);
    setEditingName(displayThreadName(thread, ''));
  };
  const cancelRename = () => {
    if (renamingThreadId) return;
    setEditingThreadId('');
    setEditingName('');
  };
  const submitRename = async (thread) => {
    const nextName = editingName.trim();
    if (!nextName || renamingThreadId) return;
    if (nextName === trimmedText(thread.name)) {
      cancelRename();
      return;
    }
    setRenamingThreadId(thread.id);
    try {
      const saved = await store.renameThread(thread.id, nextName);
      if (saved) {
        setEditingThreadId('');
        setEditingName('');
      }
    }
    finally {
      setRenamingThreadId('');
    }
  };
  const handleRenameBlur = (event, thread) => {
    const saveFor = textValue(event.relatedTarget?.dataset?.renameSaveButtonFor);
    if (saveFor === thread.id) return;
    cancelRename();
  };

  return { beginRename, cancelRename, editingName, editingThreadId, handleRenameBlur, renamingThreadId, setEditingName, submitRename };
}

function visibleThreadRows(threads, store) {
  const rows = threads
    .map((thread, index) => ({
      ...thread,
      staleReason: archivedStaleReason(thread),
      listIndex: index,
      pinnedAt: Number(firstText(store.pinnedThreadAtById?.[thread.id], thread.pinnedAt, 0)),
      activityAt: threadSortTimestamp(firstText(store.activityThreadAtById?.[thread.id], thread.updatedAt)),
    }))
    .sort(sortThreadRows);
  return rows;
}

function sortThreadRows(left, right) {
  const leftPinned = left.pinnedAt > 0;
  const rightPinned = right.pinnedAt > 0;
  if (leftPinned !== rightPinned) return leftPinned ? -1 : 1;
  if (leftPinned && rightPinned && left.pinnedAt !== right.pinnedAt) return right.pinnedAt - left.pinnedAt;
  if (!leftPinned && !rightPinned && left.activityAt !== right.activityAt) return right.activityAt - left.activityAt;
  return left.listIndex - right.listIndex;
}

export { ThreadRail };
