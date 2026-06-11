import React, { useLayoutEffect, useMemo, useRef, useState } from 'react';
import { archivedStaleReason, displayThreadName, threadSortTimestamp } from '../adapters/threadStateAdapter.js';
import { ThreadCard } from './ThreadCard.jsx';
import { ThreadRailTools } from './ThreadRailTools.jsx';
import {
  THREAD_RAIL_DEFAULT_VIEWPORT_HEIGHT,
  THREAD_RAIL_OVERSCAN,
  THREAD_RAIL_ROW_HEIGHT,
  THREAD_RAIL_WINDOW_THRESHOLD,
  computeThreadWindow,
} from './ThreadRailWindow.js';
import { runUIAction } from './chatUiActions.js';

function ThreadRail({ store }) {
  const [showArchivedThreads, setShowArchivedThreads] = useState(false);
  const [confirmCleanMode, setConfirmCleanMode] = useState(false);
  const [deletingThreadId, setDeletingThreadId] = useState('');
  const [hoveredArchiveThreadId, setHoveredArchiveThreadId] = useState('');
  const [hoveredPinThreadId, setHoveredPinThreadId] = useState('');
  const listRef = useRef(null);
  const anchoredThreadIdRef = useRef('');
  const [windowState, setWindowState] = useState({ scrollTop: 0, viewportHeight: THREAD_RAIL_DEFAULT_VIEWPORT_HEIGHT });
  const rename = useThreadRenameController(store);
  const activeThreads = store.threads.filter((thread) => !thread.archived);
  const archivedThreads = store.threads.filter((thread) => thread.archived);
  const threads = showArchivedThreads ? archivedThreads : activeThreads;
  const chatListLoading = Boolean(store.chatSurfaceLoadingCwd);
  const visibleThreads = visibleThreadRows(threads, store);
  const activeThreadId = store.pendingActiveThreadId || store.activeThreadId;
  const activeThreadIndex = useMemo(
    () => visibleThreads.findIndex((thread) => thread.id === activeThreadId),
    [activeThreadId, visibleThreads],
  );
  const threadWindow = useMemo(
    () => computeThreadWindow(visibleThreads, windowState),
    [visibleThreads, windowState],
  );
  useLayoutEffect(() => {
    if (visibleThreads.length <= THREAD_RAIL_WINDOW_THRESHOLD || activeThreadIndex < 0 || !activeThreadId) {
      anchoredThreadIdRef.current = '';
      return;
    }
    if (anchoredThreadIdRef.current === activeThreadId) return;
    const targetScrollTop = Math.max(0, (activeThreadIndex - THREAD_RAIL_OVERSCAN) * THREAD_RAIL_ROW_HEIGHT);
    anchoredThreadIdRef.current = activeThreadId;
    if (listRef.current) listRef.current.scrollTop = targetScrollTop;
    setWindowState((current) => (
      current.scrollTop === targetScrollTop
        ? current
        : { ...current, scrollTop: targetScrollTop }
    ));
  }, [activeThreadId, activeThreadIndex, visibleThreads.length]);
  const staleThreadIds = [];
  if (showArchivedThreads) {
    for (const thread of visibleThreads) {
      if (thread.staleReason) staleThreadIds.push(thread.id);
    }
  }
  const toggleArchiveLabel = showArchivedThreads ? '返回会话列表' : '打开归档列表';
  let emptyThreadText = '暂无会话，点击「新建对话」开始草稿';
  if (chatListLoading && !showArchivedThreads) {
    emptyThreadText = '正在加载会话列表…';
  } else if (showArchivedThreads) {
    emptyThreadText = '暂无归档会话';
  }
  const toggleArchiveList = () => {
    setShowArchivedThreads((value) => {
      const next = !value;
      if (!next) {
        setConfirmCleanMode(false);
        setDeletingThreadId('');
      }
      anchoredThreadIdRef.current = '';
      setWindowState({ scrollTop: 0, viewportHeight: THREAD_RAIL_DEFAULT_VIEWPORT_HEIGHT });
      if (listRef.current) listRef.current.scrollTop = 0;
      return next;
    });
  };
  const handleThreadListScroll = (event) => {
    const target = event.currentTarget;
    const viewportHeight = Number(target.clientHeight) || THREAD_RAIL_DEFAULT_VIEWPORT_HEIGHT;
    const scrollTop = Math.max(0, Number(target.scrollTop) || 0);
    setWindowState((current) => (
      current.scrollTop === scrollTop && current.viewportHeight === viewportHeight
        ? current
        : { scrollTop, viewportHeight }
    ));
  };
  return (
    <aside className="thread-rail" data-testid="thread-rail" aria-label={showArchivedThreads ? '归档列表' : '会话列表'}>
      <ThreadRailTools
        count={visibleThreads.length}
        confirmCleanMode={confirmCleanMode}
        showArchivedThreads={showArchivedThreads}
        staleThreadIds={staleThreadIds}
        toggleArchiveLabel={toggleArchiveLabel}
        onNewThread={store.newThread}
        onCleanConfirm={() => {
          setConfirmCleanMode(false);
          runUIAction(() => store.deleteStaleThreads(staleThreadIds));
        }}
        onCleanMode={() => setConfirmCleanMode(true)}
        onCancelClean={() => setConfirmCleanMode(false)}
        onToggleArchive={toggleArchiveList}
      />
      <div
        ref={listRef}
        className={`thread-list ${threadWindow.virtualized ? 'thread-list--virtual' : ''}`}
        data-testid="thread-list"
        onScroll={handleThreadListScroll}
      >
        {visibleThreads.length === 0 ? (
          <p className="thread-empty">
            {emptyThreadText}
          </p>
        ) : null}
        {threadWindow.virtualized ? <div className="thread-list-spacer" style={{ height: threadWindow.topSpacer }} /> : null}
        <div className="thread-window-items">
          {threadWindow.rows.map((thread) => (
            <ThreadCard
              key={thread.id}
              thread={thread}
              store={store}
              active={activeThreadId === thread.id}
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
                runUIAction(() => store.deleteStaleThreads([thread.id]));
              }}
            />
          ))}
        </div>
        {threadWindow.virtualized ? <div className="thread-list-spacer" style={{ height: threadWindow.bottomSpacer }} /> : null}
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
    if (nextName === (thread.name || '').toString().trim()) {
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
    const saveFor = event.relatedTarget?.dataset?.renameSaveButtonFor || '';
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
      pinnedAt: Number(store.pinnedThreadAtById?.[thread.id] || thread.pinnedAt || 0),
      activityAt: threadSortTimestamp(store.activityThreadAtById?.[thread.id] || thread.updatedAt),
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
