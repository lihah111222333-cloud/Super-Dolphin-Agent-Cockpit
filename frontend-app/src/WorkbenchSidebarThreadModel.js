import { useCallback, useState } from 'react';
import { runUIAction } from './shared/ui/runUIAction.js';
import { APP_COPY } from './shared/i18n/appI18n.js';
import { currentTimestampMillis, errorMessage, optionalTimestampMillis } from './pages/shared/pageShared.js';
import { projectThreadLabel } from './WorkbenchSidebarModel.js';

function uiActionOptions(store) {
  return {
    onError: (error) => {
      store?.addWarning?.('error', 'ui.action.failed', { error: errorMessage(error) });
    },
  };
}

export function formatRelativeTime(dateString, copy = APP_COPY.zh.workbench.relativeTime) {
  if (!dateString) return '';
  const dateTime = optionalTimestampMillis(dateString, 'relative time timestamp');
  if (!dateTime) return '';
  const diffMs = currentTimestampMillis('relative time clock') - dateTime;
  if (diffMs < 0) return copy.now;

  const diffSecs = Math.floor(diffMs / 1000);
  const diffMins = Math.floor(diffSecs / 60);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);
  const diffWeeks = Math.floor(diffDays / 7);
  const diffMonths = Math.floor(diffDays / 30);

  if (diffMins < 1) return copy.now;
  if (diffMins < 60) return copy.minute.replace('{count}', diffMins);
  if (diffHours < 24) return copy.hour.replace('{count}', diffHours);
  if (diffDays < 7) return copy.day.replace('{count}', diffDays);
  if (diffWeeks < 5) return copy.week.replace('{count}', diffWeeks);
  return copy.month.replace('{count}', diffMonths);
}

export function useSidebarThreadActions(store, options = {}) {
  const { onDeleteThreads, onRenameThread } = options;
  const [editingThreadId, setEditingThreadId] = useState('');
  const [editingName, setEditingName] = useState('');
  const [renamingThreadId, setRenamingThreadId] = useState('');
  const [deletingThreadId, setDeletingThreadId] = useState('');

  const beginRename = useCallback((thread, event) => {
    event?.stopPropagation?.();
    if (!thread?.id) return;
    setDeletingThreadId('');
    setEditingThreadId(thread.id);
    setEditingName(projectThreadLabel(thread));
  }, []);

  const cancelRename = useCallback((event) => {
    event?.stopPropagation?.();
    if (renamingThreadId) return;
    setEditingThreadId('');
    setEditingName('');
  }, [renamingThreadId]);

  const handleRenameBlur = useCallback((event) => {
    if (renamingThreadId) return;
    const nextTarget = event.relatedTarget;
    if (nextTarget && event.currentTarget.contains(nextTarget)) return;
    setEditingThreadId('');
    setEditingName('');
  }, [renamingThreadId]);

  const submitRename = useCallback(async (thread, event) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    const nextName = editingName.trim();
    if (!thread?.id || !nextName || renamingThreadId) return;
    if (nextName === projectThreadLabel(thread).trim()) {
      cancelRename(event);
      return;
    }
    setRenamingThreadId(thread.id);
    try {
      const saved = await store?.renameThread?.(thread.id, nextName);
      if (saved) {
        onRenameThread?.(thread.id, nextName);
        setEditingThreadId('');
        setEditingName('');
      }
    }
    finally {
      setRenamingThreadId('');
    }
  }, [cancelRename, editingName, onRenameThread, renamingThreadId, store]);

  const beginDelete = useCallback((threadId, event) => {
    event?.stopPropagation?.();
    if (!threadId) return;
    setEditingThreadId('');
    setEditingName('');
    setDeletingThreadId(threadId);
  }, []);

  const cancelDelete = useCallback((event) => {
    event?.stopPropagation?.();
    setDeletingThreadId('');
  }, []);

  const confirmDelete = useCallback((threadId, event) => {
    event?.stopPropagation?.();
    if (!threadId) return;
    setDeletingThreadId('');
    runUIAction('thread.delete', async () => {
      const result = await store?.deleteStaleThreads?.([threadId]);
      if (!result || result.deleted > 0) onDeleteThreads?.([threadId]);
    }, uiActionOptions(store));
  }, [onDeleteThreads, store]);

  return {
    beginDelete,
    beginRename,
    cancelDelete,
    cancelRename,
    confirmDelete,
    deletingThreadId,
    editingName,
    editingThreadId,
    handleRenameBlur,
    renamingThreadId,
    setEditingName,
    submitRename,
  };
}
